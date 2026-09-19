package eventstream

import (
	"context"
	"encoding/json"
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

type currentStateMemoryStore struct {
	registrystore.MemoryStore
	conversations []registrystore.ConversationSummary
	entries       []model.Entry
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
