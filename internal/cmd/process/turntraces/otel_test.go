package turntraces

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestOTELAttributesMapTurnTraceFieldsToLangfuse(t *testing.T) {
	sink := &otelSink{cfg: Config{
		RuntimeVersion: "test-release",
		Environment:    "test-env",
	}}
	attrs := sink.attributes(SpanData{
		Name:           defaultSpanName,
		TurnID:         "turn-1",
		ConversationID: "conv-1",
		SessionID:      "group-1",
		UserID:         "alice",
		AgentID:        "assistant",
		ClientID:       "client-1",
		UserEntryID:    "user-entry-1",
		AgentEntryID:   "agent-entry-1",
		Input:          "What should I remember?",
		Output:         "I will remember that.",
		ContextEntries: []ContextEntryData{
			{
				ID:          "context-entry-1",
				Cursor:      "cursor-context-1",
				ContentType: "application/vnd.memory-service.memory+json",
				Text:        "The user likes precise BDD tests.",
				CreatedAt:   time.Date(2026, 5, 19, 12, 0, 0, 500, time.UTC),
			},
			{
				ID:          "context-entry-2",
				Cursor:      "cursor-context-2",
				ContentType: "application/vnd.memory-service.memory+json",
				Text:        "The user expects Langfuse spans.",
				CreatedAt:   time.Date(2026, 5, 19, 12, 0, 0, 750, time.UTC),
			},
		},
		StartCursor:  "cursor-1",
		EndCursor:    "cursor-2",
		StartTime:    time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		EndTime:      time.Date(2026, 5, 19, 12, 0, 1, 0, time.UTC),
		EndReason:    "agent_history_entry",
		ContextCount: 2,
		Level:        "DEFAULT",
		Tags:         []string{"memory-service", "turn-trace", "end:agent_history_entry"},
		Metadata:     map[string]string{"conversation_group_id": "group-1"},
	})

	values := attrMap(attrs)
	require.Equal(t, defaultSpanName, values["langfuse.trace.name"])
	require.Equal(t, "group-1", values["langfuse.session.id"])
	require.Equal(t, "group-1", values["session.id"])
	require.Equal(t, "alice", values["langfuse.user.id"])
	require.Equal(t, "alice", values["user.id"])
	require.Equal(t, "test-release", values["langfuse.release"])
	require.Equal(t, "test-env", values["langfuse.environment"])
	require.Equal(t, "span", values["langfuse.observation.type"])
	require.Equal(t, "DEFAULT", values["langfuse.observation.level"])
	require.Equal(t, "conv-1", values["langfuse.trace.metadata.conversation_id"])
	require.Equal(t, "turn-1", values["langfuse.trace.metadata.turn_id"])
	require.Equal(t, "agent_history_entry", values["langfuse.trace.metadata.turn_end_reason"])
	require.Equal(t, "cursor-1", values["langfuse.trace.metadata.start_cursor"])
	require.Equal(t, "cursor-2", values["langfuse.trace.metadata.end_cursor"])
	require.Equal(t, "user-entry-1", values["langfuse.trace.metadata.user_entry_id"])
	require.Equal(t, "agent-entry-1", values["langfuse.trace.metadata.agent_entry_id"])
	require.Equal(t, int64(2), values["langfuse.trace.metadata.context_entry_count"])
	require.Equal(t, "assistant", values["langfuse.trace.metadata.agent_id"])
	require.Equal(t, "client-1", values["langfuse.trace.metadata.client_id"])
	require.Equal(t, "What should I remember?", values["langfuse.trace.input"])
	require.Equal(t, "What should I remember?", values["langfuse.observation.input"])
	require.Equal(t, "What should I remember?", values["input.value"])
	require.Equal(t, "I will remember that.", values["langfuse.trace.output"])
	require.Equal(t, "I will remember that.", values["langfuse.observation.output"])
	require.Equal(t, "I will remember that.", values["output.value"])
	require.Equal(t, "group-1", values["langfuse.trace.metadata.conversation_group_id"])
	require.Equal(t, "conv-1", values["langfuse.observation.metadata.conversation_id"])
	require.Equal(t, "turn-1", values["langfuse.observation.metadata.turn_id"])
	require.Equal(t, "agent_history_entry", values["langfuse.observation.metadata.turn_end_reason"])
	require.Equal(t, int64(2), values["langfuse.observation.metadata.context_entry_count"])
	require.ElementsMatch(t, []string{"memory-service", "turn-trace", "end:agent_history_entry"}, values["langfuse.trace.tags"])
}

func TestOTELLLMAttributesMapContextEntriesToGenerationObservation(t *testing.T) {
	sink := &otelSink{}
	attrs := sink.llmAttributes(SpanData{
		TurnID:         "turn-1",
		ConversationID: "conv-1",
		UserEntryID:    "user-entry-1",
		AgentEntryID:   "agent-entry-1",
		Output:         "I will remember that.",
		ContextEntries: []ContextEntryData{
			{
				ID:   "context-entry-1",
				Text: "The user likes precise BDD tests.",
			},
			{
				ID:   "context-entry-2",
				Text: "The user expects Langfuse spans.",
			},
		},
		Level: "DEFAULT",
	})

	values := attrMap(attrs)
	expectedInput := "context-entry-1: The user likes precise BDD tests.\ncontext-entry-2: The user expects Langfuse spans."
	require.Equal(t, "generation", values["langfuse.observation.type"])
	require.Equal(t, "DEFAULT", values["langfuse.observation.level"])
	require.Equal(t, "conv-1", values["langfuse.observation.metadata.conversation_id"])
	require.Equal(t, "turn-1", values["langfuse.observation.metadata.turn_id"])
	require.Equal(t, int64(2), values["langfuse.observation.metadata.context_entry_count"])
	require.ElementsMatch(t, []string{"context-entry-1", "context-entry-2"}, values["langfuse.observation.metadata.context_entry_ids"])
	require.Equal(t, "user-entry-1", values["langfuse.observation.metadata.user_entry_id"])
	require.Equal(t, "agent-entry-1", values["langfuse.observation.metadata.agent_entry_id"])
	require.Equal(t, expectedInput, values["langfuse.observation.input"])
	require.Equal(t, expectedInput, values["input.value"])
	require.Equal(t, expectedInput, values["gen_ai.prompt"])
	require.Equal(t, "I will remember that.", values["langfuse.observation.output"])
	require.Equal(t, "I will remember that.", values["output.value"])
	require.Equal(t, "I will remember that.", values["gen_ai.completion"])
}

func attrMap(attrs []attribute.KeyValue) map[string]any {
	values := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value.AsInterface()
	}
	return values
}
