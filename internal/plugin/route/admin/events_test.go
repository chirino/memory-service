package admin

import (
	"context"
	"encoding/json"
	"testing"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/stretchr/testify/require"
)

func TestEnrichAdminEventResponseFullKeepsSummaryPayload(t *testing.T) {
	raw := json.RawMessage(`{"conversation":"00000000-0000-0000-0000-000000000001","conversation_group":"00000000-0000-0000-0000-000000000002","recording":"rec-1","status":"completed"}`)
	event := registryeventbus.Event{
		Event: "deleted",
		Kind:  "response",
		Data:  raw,
	}

	enriched, ok := enrichAdminEvent(context.Background(), nil, "full", event)
	require.True(t, ok)
	require.Equal(t, raw, enriched.Data)
}
