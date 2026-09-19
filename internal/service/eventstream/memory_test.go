package eventstream

import (
	"encoding/json"
	"testing"
	"time"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMemoryChangedEventOmitsPayloadAndSourceKey(t *testing.T) {
	event := MemoryChangedEvent("created", "created", &registryepisodic.MemoryItem{
		ID: uuid.MustParse("65c8f6aa-f66b-4c29-8dca-f0c845201948"), Namespace: []string{"user", "alice"}, Key: "private-key",
		Value: map[string]any{"secret": "value"}, Attributes: map[string]any{"customer": "private"}, MemoryKind: "support/v1", Revision: 1,
		CreatedAt: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC),
	})
	raw, err := json.Marshal(event.Data)
	require.NoError(t, err)
	require.JSONEq(t, `{"change":"created","createdAt":"2026-09-16T12:00:00Z","memory":"65c8f6aa-f66b-4c29-8dca-f0c845201948","memoryKind":"support/v1","revision":1}`, string(raw))
	require.NotContains(t, string(raw), "alice")
	require.NotContains(t, string(raw), "private")
	require.NotContains(t, string(raw), "secret")
}
