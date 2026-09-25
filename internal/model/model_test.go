package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Entry caches (local and Redis) store entries as JSON, so MarshalJSON and
// UnmarshalJSON must round-trip content losslessly.
func TestEntryJSONRoundTripPreservesContent(t *testing.T) {
	userID := "alice"
	clientID := "app"
	agentID := "agent"
	epoch := int64(3)
	seq := uint32(7)
	indexed := "indexed text"
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	cases := map[string][]byte{
		"json array":     []byte(`[{"role":"USER","text":"hi"}]`),
		"json object":    []byte(`{"text":"hello","n":1}`),
		"binary content": {0x4d, 0x53, 0x45, 0x48, 0x00, 0xff, 0x10},
		"empty content":  nil,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			want := Entry{
				ID:             uuid.New(),
				ConversationID: "conv-1",
				UserID:         &userID,
				ClientID:       &clientID,
				AgentID:        &agentID,
				Channel:        ChannelHistory,
				Epoch:          &epoch,
				Seq:            &seq,
				ContentType:    "history",
				Content:        content,
				IndexedContent: &indexed,
				CreatedAt:      createdAt,
			}

			data, err := json.Marshal(want)
			require.NoError(t, err)

			var got Entry
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, want, got)
		})
	}
}
