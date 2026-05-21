//go:build integration

package turntraces

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/testutil/testlangfuse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLangfuseOTLPExport(t *testing.T) {
	stack := testlangfuse.Start(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", stack.OTLPEndpoint())
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", stack.OTLPHeaders())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sink, err := newOTELSink(ctx, Config{
		ServiceName:    "memory-service-turn-traces-test",
		Environment:    "integration",
		RuntimeVersion: "test",
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	err = sink.EmitTurnSpan(ctx, SpanData{
		Name:           defaultSpanName,
		TurnID:         "turn-langfuse-test",
		ConversationID: "conversation-langfuse-test",
		SessionID:      "session-langfuse-test",
		UserID:         "alice-langfuse-test",
		ClientID:       "client-langfuse-test",
		UserEntryID:    "user-entry-langfuse-test",
		AgentEntryID:   "agent-entry-langfuse-test",
		StartCursor:    "cursor-start",
		EndCursor:      "cursor-end",
		StartTime:      now.Add(-time.Second),
		EndTime:        now,
		EndReason:      "agent_history_entry",
		ContextCount:   1,
		Level:          "DEFAULT",
		Tags:           []string{"memory-service", "turn-trace", "end:agent_history_entry"},
		Metadata:       map[string]string{"conversation_group_id": "group-langfuse-test"},
	})
	require.NoError(t, err)
	require.NoError(t, sink.Shutdown(ctx))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		traces, err := stack.FetchTraces(context.Background())
		require.NoError(c, err)
		raw, err := json.Marshal(traces)
		require.NoError(c, err)
		body := string(raw)
		require.True(c, strings.Contains(body, defaultSpanName), "trace response did not include %q: %s", defaultSpanName, body)
		require.True(c, strings.Contains(body, "conversation-langfuse-test"), "trace response did not include conversation metadata: %s", body)
	}, 2*time.Minute, 2*time.Second)
}
