package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAdminEvictPublishesCompletedBatchesBeforeLaterFailure(t *testing.T) {
	for _, mode := range []string{"json", "sse", "async"} {
		for _, atomic := range []bool{false, true} {
			for _, failure := range []string{"lookup", "outbox"} {
				name := "non_atomic/"
				if atomic {
					name = "atomic/"
				}
				t.Run(name+mode+"/"+failure, func(t *testing.T) {
					store := &evictionTestStore{groupID: uuid.New(), atomic: atomic, failure: failure}
					bus := &evictionTestBus{t: t, store: store}
					response := runEvictionRequest(store, bus, mode, failure == "outbox")
					assertEvictionFailure(t, response, mode)
					require.True(t, store.deleted, "completed batch must stay committed")
					require.Len(t, bus.events, 1, "committed deletion must publish despite later failure")
					event := bus.events[0]
					require.Equal(t, "conversation", event.Kind)
					require.Equal(t, "deleted", event.Event)
					require.Equal(t, store.groupID, event.ConversationGroupID)
					require.Equal(t, []string{"alice", "bob"}, event.UserIDs)
					require.Equal(t, "conversation-1", event.Data.(map[string]any)["conversation"])

					// A retry cannot load the deleted membership snapshot and must not
					// duplicate the event that was already delivered.
					store.failure = ""
					response = runEvictionRequest(store, bus, mode, false)
					if mode == "json" {
						require.Equal(t, http.StatusNoContent, response.Code)
					} else {
						require.Contains(t, response.Body.String(), `"progress":100`)
					}
					require.Len(t, bus.events, 1)
				})
			}
		}
	}
}

func TestAdminEvictDoesNotPublishRolledBackBatch(t *testing.T) {
	for _, mode := range []string{"json", "sse", "async"} {
		t.Run(mode, func(t *testing.T) {
			store := &evictionTestStore{groupID: uuid.New(), atomic: true, failure: "commit"}
			bus := &evictionTestBus{t: t, store: store}
			response := runEvictionRequest(store, bus, mode, false)
			assertEvictionFailure(t, response, mode)
			require.False(t, store.deleted)
			require.Empty(t, bus.events, "rolled-back deletion must not publish")
		})
	}
}

func TestAdminEvictConversationFilter(t *testing.T) {
	only, exclude, include := registrystore.ConversationArchiveOnly, registrystore.ConversationArchiveExclude, registrystore.ConversationArchiveInclude
	archivedAt, lastActivity, createdAt := registrystore.ConversationAgeArchivedAt, registrystore.ConversationAgeLastActivity, registrystore.ConversationAgeCreatedAt
	valid := []struct {
		name     string
		filter   string
		archived registrystore.ConversationArchiveFilter
		age      registrystore.ConversationAgeBasis
	}{
		{"omitted", ``, only, archivedAt},
		{"null", `,"conversationFilter":null`, only, archivedAt},
		{"empty", `,"conversationFilter":{}`, only, archivedAt},
		{"archived only", `,"conversationFilter":{"archived":"only"}`, only, archivedAt},
		{"age basis only", `,"conversationFilter":{"ageBasis":"lastActivity"}`, only, lastActivity},
		{"null property", `,"conversationFilter":{"archived":null,"ageBasis":"createdAt"}`, only, createdAt},
		{"exclude last activity", `,"conversationFilter":{"archived":"exclude","ageBasis":"lastActivity"}`, exclude, lastActivity},
		{"exclude created", `,"conversationFilter":{"archived":"exclude","ageBasis":"createdAt"}`, exclude, createdAt},
		{"include last activity", `,"conversationFilter":{"archived":"include","ageBasis":"lastActivity"}`, include, lastActivity},
		{"include created", `,"conversationFilter":{"archived":"include","ageBasis":"createdAt"}`, include, createdAt},
		{"only created", `,"conversationFilter":{"archived":"only","ageBasis":"createdAt"}`, only, createdAt},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			store := &evictionTestStore{groupID: uuid.New()}
			before := time.Now()
			response := runEvictionBody(store, &evictionTestBus{t: t, store: store}, `{"retentionPeriod":"P30D","resourceTypes":["conversations"]`+tc.filter+`}`)
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			require.NotEmpty(t, store.filters)
			filter := store.filters[0]
			require.Equal(t, tc.archived, filter.Archived)
			require.Equal(t, tc.age, filter.AgeBasis)
			require.Empty(t, filter.GroupIDs)
			require.WithinDuration(t, before.Add(-30*24*time.Hour), filter.Cutoff, time.Minute)
			// The deletion recheck reuses the filter, restricted to the selected groups.
			require.Len(t, store.filters, 3)
			require.Equal(t, []uuid.UUID{store.groupID}, store.filters[1].GroupIDs)
			require.Equal(t, filter.AgeBasis, store.filters[1].AgeBasis)
		})
	}

	invalid := map[string]string{
		"default age with exclude":      `{"retentionPeriod":"P30D","resourceTypes":["conversations"],"conversationFilter":{"archived":"exclude"}}`,
		"archivedAt with exclude":       `{"retentionPeriod":"P30D","resourceTypes":["conversations"],"conversationFilter":{"archived":"exclude","ageBasis":"archivedAt"}}`,
		"archivedAt with include":       `{"retentionPeriod":"P30D","resourceTypes":["conversations"],"conversationFilter":{"archived":"include","ageBasis":"archivedAt"}}`,
		"unknown archived value":        `{"retentionPeriod":"P30D","resourceTypes":["conversations"],"conversationFilter":{"archived":"all"}}`,
		"unknown age basis":             `{"retentionPeriod":"P30D","resourceTypes":["conversations"],"conversationFilter":{"ageBasis":"updatedAt"}}`,
		"case mismatch":                 `{"retentionPeriod":"P30D","resourceTypes":["conversations"],"conversationFilter":{"ageBasis":"lastactivity"}}`,
		"unknown property":              `{"retentionPeriod":"P30D","resourceTypes":["conversations"],"conversationFilter":{"archive":"exclude"}}`,
		"not an object":                 `{"retentionPeriod":"P30D","resourceTypes":["conversations"],"conversationFilter":"exclude"}`,
		"without conversations":         `{"retentionPeriod":"P30D","resourceTypes":["outbox_events"],"conversationFilter":{"archived":"only"}}`,
		"empty filter on outbox events": `{"retentionPeriod":"P30D","resourceTypes":["outbox_events"],"conversationFilter":{}}`,
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			store := &evictionTestStore{groupID: uuid.New()}
			response := runEvictionBody(store, &evictionTestBus{t: t, store: store}, body)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Empty(t, store.filters, "invalid requests must not evict")
		})
	}
}

func runEvictionBody(store *evictionTestStore, bus *evictionTestBus, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/evict", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = req
	adminEvict(c, store, bus)
	c.Writer.WriteHeaderNow()
	return response
}

func runEvictionRequest(store *evictionTestStore, bus *evictionTestBus, mode string, outbox bool) *httptest.ResponseRecorder {
	resources := `"conversations"`
	if outbox {
		resources += `,"outbox_events"`
	}
	path := "/v1/admin/evict"
	if mode == "async" {
		path += "?async=true"
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"retentionPeriod":"P30D","resourceTypes":[`+resources+`]}`))
	req.Header.Set("Content-Type", "application/json")
	if mode == "sse" {
		req.Header.Set("Accept", "text/event-stream")
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = req
	adminEvict(c, store, bus)
	c.Writer.WriteHeaderNow()
	return response
}

func assertEvictionFailure(t *testing.T, response *httptest.ResponseRecorder, mode string) {
	t.Helper()
	if mode == "json" {
		require.Equal(t, http.StatusInternalServerError, response.Code)
	} else {
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), "event: error")
		require.NotContains(t, response.Body.String(), `"progress":100`)
	}
}

// Embedding the interfaces keeps this fault-injection store limited to the
// calls made by eviction. Unimplemented calls fail immediately.
type evictionTestStore struct {
	registrystore.MemoryStore
	registrystore.EventOutboxStore
	groupID uuid.UUID
	atomic  bool
	failure string
	deleted bool
	inWrite bool
	filters []registrystore.ConversationEvictionFilter
}

func (s *evictionTestStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s *evictionTestStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	before := s.deleted
	s.inWrite = true
	err := fn(ctx)
	s.inWrite = false
	if err == nil && s.failure == "commit" {
		err = errors.New("injected commit failure")
	}
	if err != nil && s.atomic {
		s.deleted = before
	}
	return err
}

func (s *evictionTestStore) OutboxEnabled() bool { return false }

func (s *evictionTestStore) CountEvictableGroups(context.Context, registrystore.ConversationEvictionFilter) (int64, error) {
	if s.deleted {
		return 0, nil
	}
	return 1, nil
}

func (s *evictionTestStore) FindEvictableGroupIDs(_ context.Context, filter registrystore.ConversationEvictionFilter, _ int) ([]uuid.UUID, error) {
	s.filters = append(s.filters, filter)
	if s.deleted {
		if s.failure == "lookup" {
			return nil, errors.New("injected later lookup failure")
		}
		return nil, nil
	}
	return []uuid.UUID{s.groupID}, nil
}

func (s *evictionTestStore) LoadDeletedConversationGroups(context.Context, []uuid.UUID) ([]registrystore.DeletedConversationGroup, error) {
	return []registrystore.DeletedConversationGroup{{
		ConversationGroupID: s.groupID,
		ConversationIDs:     []string{"conversation-1"},
		MemberUserIDs:       []string{"alice", "bob"},
	}}, nil
}

func (s *evictionTestStore) CreateTask(context.Context, string, map[string]interface{}) error {
	return nil
}

func (s *evictionTestStore) HardDeleteConversationGroups(context.Context, []uuid.UUID) error {
	s.deleted = true
	return nil
}

func (s *evictionTestStore) EvictOutboxEventsBefore(context.Context, time.Time, int) (int64, error) {
	return 0, errors.New("injected outbox cleanup failure")
}

type evictionTestBus struct {
	registryeventbus.EventBus
	t      *testing.T
	store  *evictionTestStore
	events []registryeventbus.Event
}

func (b *evictionTestBus) Publish(_ context.Context, event registryeventbus.Event) error {
	require.False(b.t, b.store.inWrite, "publication must follow commit")
	require.True(b.t, b.store.deleted, "publication requires a committed deletion")
	b.events = append(b.events, event)
	return nil
}
