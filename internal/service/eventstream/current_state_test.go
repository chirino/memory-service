package eventstream

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStreamAdminCurrentStateEmitsAdminResources(t *testing.T) {
	groupID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	entryID := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	clientID := "client-1"
	store := &currentStateMemoryStore{
		conversations: []registrystore.ConversationSummary{{
			ID: "conversation-1", ClientID: clientID, ConversationGroupID: groupID,
			OwnerUserID: "user-1", CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(), AccessLevel: model.AccessLevelOwner,
		}},
		entries: []model.Entry{{
			ID: entryID, ConversationID: "conversation-1", ConversationGroupID: groupID, ClientID: &clientID,
			Channel: model.ChannelContext, ContentType: "SpringAI", Content: json.RawMessage(`[{"role":"user","text":"hello"}]`), CreatedAt: time.Unix(3, 0).UTC(),
		}},
	}
	episodic := &currentStateEpisodicStore{items: []registryepisodic.MemoryItem{{
		ID: uuid.MustParse("00000000-0000-4000-8000-000000000003"), Namespace: []string{"users", "alice"}, Key: "profile",
		Value: map[string]any{"name": "Alice"}, MemoryKind: "profile/v1", CreatedAt: time.Unix(4, 0).UTC(), Revision: 1,
	}}}
	var events []registryeventbus.Event
	err := StreamAdminCurrentState(context.Background(), store, episodic, nil, nil, NewEntryEventFilter([]string{"history", "context", "journal"}, nil, nil), func(event registryeventbus.Event) error {
		events = append(events, event)
		return nil
	})
	require.NoError(t, err)
	require.Len(t, events, 3)
	require.Equal(t, []string{"conversation", "entry", "memory"}, []string{events[0].Kind, events[1].Kind, events[2].Kind})
	require.Equal(t, groupID.String(), events[0].Data.(map[string]any)["conversationGroupId"])
	require.Equal(t, clientID, events[1].Data.(map[string]any)["clientId"])
	require.Equal(t, "profile/v1", events[2].Data.(map[string]any)["kind"])
}

type pagedMembershipStore struct {
	currentStateMemoryStore
	calls int
}

func (s *pagedMembershipStore) AdminListEventSnapshotMemberships(_ context.Context, after *registrystore.EventSnapshotMembershipCursor, limit int) ([]model.ConversationMembership, *registrystore.EventSnapshotMembershipCursor, error) {
	s.calls++
	start := 0
	if after != nil {
		for start < len(s.memberships) && s.memberships[start].UserID <= after.UserID {
			start++
		}
	}
	end := min(start+limit, len(s.memberships))
	if end == len(s.memberships) {
		return s.memberships[start:end], nil, nil
	}
	last := s.memberships[end-1]
	return s.memberships[start:end], &registrystore.EventSnapshotMembershipCursor{GroupID: last.ConversationGroupID.String(), UserID: last.UserID}, nil
}

func TestMembershipCurrentStateTraversesMultiplePages(t *testing.T) {
	s := &pagedMembershipStore{}
	group := uuid.New()
	for i := range currentStatePageSize*2 + 1 {
		s.memberships = append(s.memberships, model.ConversationMembership{ConversationGroupID: group, UserID: fmt.Sprintf("member-%04d", i), AccessLevel: model.AccessLevelReader, CreatedAt: time.Now().UTC()})
	}
	seen := map[string]bool{}
	err := StreamAdminCurrentState(context.Background(), s, nil, map[string]bool{"membership": true}, nil, NewEntryEventFilter(nil, nil, nil), func(e registryeventbus.Event) error {
		id := e.Data.(map[string]any)["id"].(string)
		require.False(t, seen[id])
		seen[id] = true
		return nil
	})
	require.NoError(t, err)
	require.Len(t, seen, currentStatePageSize*2+1)
	require.Equal(t, 3, s.calls)
}

type currentStateMemoryStore struct {
	registrystore.MemoryStore
	conversations []registrystore.ConversationSummary
	entries       []model.Entry
	memberships   []model.ConversationMembership
}

func (s *currentStateMemoryStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s *currentStateMemoryStore) AdminListEventSnapshotConversations(context.Context, *registrystore.EventSnapshotConversationCursor, int) ([]registrystore.ConversationSummary, *registrystore.EventSnapshotConversationCursor, error) {
	return s.conversations, nil, nil
}

func (s *currentStateMemoryStore) AdminListEventSnapshotEntries(context.Context, *registrystore.EventSnapshotEntryCursor, int) ([]model.Entry, *registrystore.EventSnapshotEntryCursor, error) {
	return s.entries, nil, nil
}

type currentStateEpisodicStore struct {
	registryepisodic.EpisodicStore
	items []registryepisodic.MemoryItem
}

func (s *currentStateEpisodicStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s *currentStateEpisodicStore) AdminListEventSnapshotMemories(context.Context, registryepisodic.AdminMemoryQuery) (registryepisodic.AdminMemoryPage, error) {
	return registryepisodic.AdminMemoryPage{Items: s.items}, nil
}

func (s *currentStateMemoryStore) SupportsAnalyticsMembershipSnapshots() bool { return true }
func (s *currentStateMemoryStore) AdminListEventSnapshotMemberships(context.Context, *registrystore.EventSnapshotMembershipCursor, int) ([]model.ConversationMembership, *registrystore.EventSnapshotMembershipCursor, error) {
	return s.memberships, nil, nil
}
func (s *currentStateMemoryStore) AdminGetEventSnapshotMembership(context.Context, string, string) (*model.ConversationMembership, error) {
	return nil, nil
}

func TestMembershipCurrentStateAndConversationFiltering(t *testing.T) {
	group := uuid.New()
	store := &currentStateMemoryStore{memberships: []model.ConversationMembership{{ConversationGroupID: group, UserID: "owner/é", AccessLevel: model.AccessLevelOwner, CreatedAt: time.Now().UTC()}}}
	for _, filtered := range []bool{false, true} {
		var conversationIDs map[string]bool
		if filtered {
			conversationIDs = map[string]bool{"conversation": true}
		}
		var events []registryeventbus.Event
		err := StreamAdminCurrentState(context.Background(), store, nil, map[string]bool{"membership": true}, conversationIDs, NewEntryEventFilter(nil, nil, nil), func(event registryeventbus.Event) error { events = append(events, event); return nil })
		require.NoError(t, err)
		if filtered {
			require.Empty(t, events)
		} else {
			require.Len(t, events, 1)
			record := events[0].Data.(map[string]any)
			require.Equal(t, model.MembershipResourceID(group, "owner/é"), record["id"])
			require.Equal(t, group.String(), record["conversationGroupId"])
			require.NotContains(t, record, "conversationId")
		}
	}
}
