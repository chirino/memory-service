package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/operationevent"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestEnrichAdminEventResponseFullKeepsSummaryPayload(t *testing.T) {
	raw := json.RawMessage(`{"conversation":"00000000-0000-0000-0000-000000000001","conversation_group":"00000000-0000-0000-0000-000000000002","recording":"rec-1","status":"completed"}`)
	event := registryeventbus.Event{
		Event: "deleted",
		Kind:  "response",
		Data:  raw,
	}

	enriched, ok, err := enrichAdminEvent(context.Background(), nil, nil, "full", event)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, raw, enriched.Data)
}

func TestEnrichAdminMemoryEventFullLoadsMemory(t *testing.T) {
	memoryID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	store := &adminEventEpisodicStore{item: &registryepisodic.MemoryItem{
		ID: memoryID, Namespace: []string{"tenant", "alice"}, Key: "profile", Value: map[string]any{"secret": "hydrated"}, MemoryKind: "profile/v1", CreatedAt: time.Unix(10, 0).UTC(), Revision: 2,
	}}
	event := registryeventbus.Event{Event: "updated", Kind: "memory", Data: json.RawMessage(`{"memory":"00000000-0000-4000-8000-000000000001","change":"updated"}`)}

	enriched, ok, err := enrichAdminEvent(context.Background(), nil, store, "full", event)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, store.getCalls)
	raw, err := json.Marshal(enriched.Data)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"00000000-0000-4000-8000-000000000001","namespace":["tenant","alice"],"key":"profile","value":{"secret":"hydrated"},"kind":"profile/v1","createdAt":"1970-01-01T00:00:10Z","archived":false,"revision":2}`, string(raw))
	require.Equal(t, "updated", enriched.Change)
}

func TestEnrichAdminMemoryEventSummaryDoesNotLoadMemory(t *testing.T) {
	store := &adminEventEpisodicStore{}
	raw := json.RawMessage(`{"memory":"00000000-0000-4000-8000-000000000001","change":"updated"}`)
	event := registryeventbus.Event{Event: "updated", Kind: "memory", Data: raw}

	enriched, ok, err := enrichAdminEvent(context.Background(), nil, store, "summary", event)
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, store.getCalls)
	require.Equal(t, raw, enriched.Data)
}

func TestEnrichAdminMemoryEventMissingPreservesSummary(t *testing.T) {
	store := &adminEventEpisodicStore{}
	raw := json.RawMessage(`{"memory":"00000000-0000-4000-8000-000000000001","change":"hard_deleted"}`)
	event := registryeventbus.Event{Event: "deleted", Kind: "memory", Data: raw}

	enriched, ok, err := enrichAdminEvent(context.Background(), nil, store, "full", event)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, store.getCalls)
	require.Equal(t, raw, enriched.Data)
}

func TestEnrichAdminMemoryEventReturnsStoreFailure(t *testing.T) {
	storeErr := errors.New("database unavailable")
	store := &adminEventEpisodicStore{err: storeErr}
	event := registryeventbus.Event{Event: "updated", Kind: "memory", Data: json.RawMessage(`{"memory":"00000000-0000-4000-8000-000000000001","change":"updated"}`)}

	_, ok, err := enrichAdminEvent(context.Background(), nil, store, "full", event)
	require.False(t, ok)
	require.ErrorIs(t, err, storeErr)
}

func TestHandleAdminSSEEventsMarksSubscribeFailureAfterCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	streamErr := operationevent.WithErrorDetails(errors.New("private event bus failure"), operationevent.ErrorDetails{
		ErrorType: "event_bus",
		ErrorCode: "subscribe_failed",
	})
	bus := &failingSubscribeEventBus{err: streamErr}
	router := gin.New()
	router.Use(security.OperationEventMiddleware())
	var event *operationevent.Event
	router.GET("/v1/admin/events", func(c *gin.Context) {
		c.Set(security.ContextKeyUserID, "admin-1")
		event = security.OperationEventFromGin(c)
		HandleAdminSSEEvents(c, nil, nil, bus, &cfg)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil))

	require.Equal(t, http.StatusOK, response.Code)
	require.NotNil(t, event)
	snapshot := event.Snapshot()
	require.Equal(t, operationevent.ResultFailed, snapshot.Result)
	require.Equal(t, "subscribe_failed", snapshot.Reason)
	require.Equal(t, "event_bus", snapshot.ErrorType)
	require.Equal(t, "subscribe_failed", snapshot.ErrorCode)
}

type failingSubscribeEventBus struct {
	err error
}

type adminEventEpisodicStore struct {
	registryepisodic.EpisodicStore
	item     *registryepisodic.MemoryItem
	err      error
	getCalls int
}

func (s *adminEventEpisodicStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s *adminEventEpisodicStore) AdminGetMemoryByID(context.Context, uuid.UUID) (*registryepisodic.MemoryItem, error) {
	s.getCalls++
	return s.item, s.err
}

func (b *failingSubscribeEventBus) Publish(context.Context, registryeventbus.Event) error {
	return nil
}

func (b *failingSubscribeEventBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return nil, b.err
}

func (b *failingSubscribeEventBus) Close() error { return nil }
