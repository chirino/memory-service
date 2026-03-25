package grpc

import (
	"context"
	"encoding/json"
	"testing"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/stretchr/testify/require"
)

func TestEnrichGRPCEventResponseFullKeepsSummaryPayload(t *testing.T) {
	raw := json.RawMessage(`{"conversation":"00000000-0000-0000-0000-000000000001","conversation_group":"00000000-0000-0000-0000-000000000002","recording":"rec-1","status":"failed"}`)
	event := registryeventbus.Event{
		Event: "deleted",
		Kind:  "response",
		Data:  raw,
	}

	enriched, ok, err := (&EventStreamServer{}).enrichGRPCEvent(context.Background(), "alice", "full", event)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, raw, enriched.Data)
}
