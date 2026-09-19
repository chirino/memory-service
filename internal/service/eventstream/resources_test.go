package eventstream

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestEntryResourcesSeparateAgentAndAdminFields(t *testing.T) {
	clientID := "client-1"
	entry := model.Entry{
		ID: uuid.MustParse("00000000-0000-4000-8000-000000000001"), ConversationID: "conversation-1",
		ConversationGroupID: uuid.MustParse("00000000-0000-4000-8000-000000000002"), ClientID: &clientID,
		Channel: model.ChannelHistory, ContentType: "history", Content: json.RawMessage(`[{"role":"USER","text":"hello"}]`), CreatedAt: time.Unix(1, 0).UTC(),
	}

	agent := AgentEntryResource(&entry)
	require.NotContains(t, agent, "clientId")
	require.NotContains(t, agent, "conversationGroupId")
	require.Equal(t, "conversation-1", agent["conversationId"])

	admin := AdminEntryResource(&entry)
	require.Equal(t, clientID, admin["clientId"])
	require.Equal(t, entry.ConversationGroupID.String(), admin["conversationGroupId"])
}

func TestConversationResourcesSeparateAgentAndAdminFields(t *testing.T) {
	groupID := uuid.MustParse("00000000-0000-4000-8000-000000000003")
	conversation := registrystore.ConversationDetail{ConversationSummary: registrystore.ConversationSummary{
		ID: "conversation-1", ClientID: "client-1", ConversationGroupID: groupID,
		OwnerUserID: "user-1", Metadata: map[string]any{"tenant": "acme"}, AccessLevel: model.AccessLevelOwner,
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
	}, HasResponseInProgress: true}

	agent := AgentConversationResource(&conversation)
	require.NotContains(t, agent, "clientId")
	require.NotContains(t, agent, "conversationGroupId")
	require.Equal(t, map[string]any{"tenant": "acme"}, agent["metadata"])
	require.Equal(t, true, agent["hasResponseInProgress"])

	admin := AdminConversationResource(&conversation)
	require.Equal(t, "client-1", admin["clientId"])
	require.Equal(t, groupID.String(), admin["conversationGroupId"])
	require.Equal(t, true, admin["hasResponseInProgress"])
}
