package agent

import (
	"context"
	"encoding/json"
	"testing"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/stretchr/testify/require"
)

func TestEnrichUserEventResponseFullKeepsSummaryPayload(t *testing.T) {
	raw := json.RawMessage(`{"conversation":"00000000-0000-0000-0000-000000000001","conversation_group":"00000000-0000-0000-0000-000000000002","recording":"rec-1","status":"started"}`)
	event := registryeventbus.Event{
		Event: "created",
		Kind:  "response",
		Data:  raw,
	}

	enriched, ok := enrichUserEvent(context.Background(), nil, "alice", "full", event)
	require.True(t, ok)
	require.Equal(t, raw, enriched.Data)
}
