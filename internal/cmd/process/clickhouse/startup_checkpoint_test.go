package clickhouse

import (
	"context"
	"encoding/json"
	"testing"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	"github.com/stretchr/testify/require"
)

type storedCheckpoint struct {
	checkpoint processruntime.Checkpoint
}

func (s storedCheckpoint) Get(context.Context, string) (processruntime.Checkpoint, error) {
	return s.checkpoint, nil
}
func (storedCheckpoint) Put(context.Context, string, string, json.RawMessage) (processruntime.Checkpoint, error) {
	panic("startup precheck must not write a checkpoint")
}

// An incompatible checkpoint must fail before OpenSink claims an empty
// database. The unreachable address proves no ClickHouse connection is tried.
func TestStartRejectsIncompatibleCheckpointBeforeOpeningClickHouse(t *testing.T) {
	cfg := testConfig()
	cfg.Addresses = []string{"127.0.0.1:1"}
	for name, tc := range map[string]struct {
		checkpoint processruntime.Checkpoint
		message    string
	}{
		"previous exporter": {
			checkpoint: processruntime.Checkpoint{ContentType: "application/vnd.memory-service.clickhouse-checkpoint+json;v=1", Value: json.RawMessage(`{"version":1}`)},
			message:    "use a new client ID and a new empty analytics database",
		},
		"invalid value": {
			checkpoint: processruntime.Checkpoint{ContentType: checkpointContentType, Value: json.RawMessage(`{"exporterId":"test"}`)},
			message:    `load checkpoint for client ID "test"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			running, err := StartProcessor(context.Background(), StartOptions{ClientID: "test", Config: cfg, Events: &markerEventClient{}, Checkpoints: storedCheckpoint{tc.checkpoint}})
			require.Nil(t, running)
			require.ErrorContains(t, err, tc.message)
			require.NotContains(t, err.Error(), "ClickHouse:")
		})
	}
}
