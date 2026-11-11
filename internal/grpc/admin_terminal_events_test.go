package grpc

import (
	"context"
	"encoding/json"
	"testing"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/stretchr/testify/require"
)

func TestAdminTerminalEventsPreserveChangeWithoutReload(t *testing.T) {
	server := &EventStreamServer{}
	for _, change := range []string{"hard_deleted", "evicted"} {
		raw := json.RawMessage(`{"conversation":"deleted","change":"` + change + `"}`)
		event := registryeventbus.Event{Kind: "conversation", Event: "deleted", Data: raw}
		enriched, ok, err := server.enrichGRPCAdminEvent(context.Background(), "full", event)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, raw, enriched.Data)
		require.Equal(t, change, enriched.Change)
	}
}
