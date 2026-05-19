package turntraces

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	"github.com/stretchr/testify/require"
)

type captureSink struct {
	mu    sync.Mutex
	spans []SpanData
	err   error
}

func (s *captureSink) EmitTurnSpan(_ context.Context, span SpanData) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, span)
	return nil
}

func (s *captureSink) snapshot() []SpanData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SpanData(nil), s.spans...)
}

func TestDetectorCompletedTurn(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "entry-user", "conv-1", "history", "history", "USER", base)))
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-2", "entry-context", "conv-1", "context", "LC4J", "", base.Add(time.Second))))
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-3", "entry-ai", "conv-1", "history", "history", "AI", base.Add(2*time.Second))))

	require.Len(t, sink.spans, 1)
	span := sink.spans[0]
	require.Equal(t, "agent_history_entry", span.EndReason)
	require.Equal(t, "entry-user", span.UserEntryID)
	require.Equal(t, "entry-ai", span.AgentEntryID)
	require.Equal(t, "redacted", span.Input)
	require.Equal(t, "redacted", span.Output)
	require.Equal(t, 1, span.ContextCount)
	require.Len(t, span.ContextEntries, 1)
	require.Equal(t, "entry-context", span.ContextEntries[0].ID)
	require.Equal(t, "redacted", span.ContextEntries[0].Text)
	require.Equal(t, "cursor-3", span.EndCursor)

	raw, err := processor.Snapshot()
	require.NoError(t, err)
	var state checkpointState
	require.NoError(t, json.Unmarshal(raw, &state))
	require.Equal(t, "cursor-3", state.LastEventCursor)
	require.Empty(t, state.OpenTurns)
}

func TestDetectorConversationGroupSessionMapping(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{SessionIDMode: "conversation-group"}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelopeWithGroup("cursor-1", "u1", "conv-1", "group-1", "history", "history", "USER", base)))
	require.NoError(t, processor.Handle(context.Background(), entryEnvelopeWithGroup("cursor-2", "a1", "conv-1", "group-1", "history", "history", "AI", base.Add(time.Second))))

	require.Len(t, sink.spans, 1)
	require.Equal(t, "group-1", sink.spans[0].SessionID)
	require.Equal(t, "group-1", sink.spans[0].Metadata["conversation_group_id"])
}

func TestDetectorUsesConfiguredLangfuseName(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{LangfuseName: "custom.turn.name"}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-2", "a1", "conv-1", "history", "history", "AI", base.Add(time.Second))))

	require.Len(t, sink.spans, 1)
	require.Equal(t, "custom.turn.name", sink.spans[0].Name)
}

func TestDetectorClosesTurnFromLC4JHistoryAIEntry(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))
	require.NoError(t, processor.Handle(context.Background(), lc4jHistoryEnvelope("cursor-2", "a1", "conv-1", base.Add(time.Second))))

	require.Len(t, sink.spans, 1)
	require.Equal(t, "agent_history_entry", sink.spans[0].EndReason)
	require.Equal(t, "a1", sink.spans[0].AgentEntryID)
	require.Equal(t, "LC4J answer.", sink.spans[0].Output)
}

func TestDetectorNewUserCutsShortPreviousTurn(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-2", "u2", "conv-1", "history", "history", "USER", base.Add(time.Second))))

	require.Len(t, sink.spans, 1)
	require.Equal(t, "new_user_input", sink.spans[0].EndReason)
	require.Equal(t, "u1", sink.spans[0].UserEntryID)
	require.Equal(t, "cursor-2", sink.spans[0].EndCursor)
	require.NotNil(t, processor.state.OpenTurns["conv-1"])
	require.Equal(t, "u2", processor.state.OpenTurns["conv-1"].UserEntryID)
}

func TestDetectorDuplicateEventsDoNotDuplicateContext(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))
	contextEvent := entryEnvelope("cursor-2", "ctx1", "conv-1", "context", "LC4J", "", base.Add(time.Second))
	require.NoError(t, processor.Handle(context.Background(), contextEvent))
	require.NoError(t, processor.Handle(context.Background(), contextEvent))
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-3", "a1", "conv-1", "history", "history", "AI", base.Add(2*time.Second))))

	require.Len(t, sink.spans, 1)
	require.Equal(t, 1, sink.spans[0].ContextCount)
}

func TestDetectorCheckpointRestore(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))
	raw, err := processor.Snapshot()
	require.NoError(t, err)

	restored := NewProcessor(Config{}, sink)
	require.NoError(t, restored.Load(raw))
	require.NotNil(t, restored.state.OpenTurns["conv-1"])
	require.Equal(t, "cursor-1", restored.state.LastEventCursor)
	require.NoError(t, restored.Handle(context.Background(), entryEnvelope("cursor-2", "a1", "conv-1", "history", "history", "AI", base.Add(time.Second))))

	require.Len(t, sink.spans, 1)
	require.Equal(t, "u1", sink.spans[0].UserEntryID)
}

func TestDetectorSnapshotDoesNotMutateState(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	processor.nowFn = func() time.Time { return base }
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))

	first, err := processor.Snapshot()
	require.NoError(t, err)
	processor.nowFn = func() time.Time { return base.Add(time.Hour) }
	second, err := processor.Snapshot()
	require.NoError(t, err)
	require.JSONEq(t, string(first), string(second))
}

func TestDetectorIdleTimeout(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{IdleTimeout: time.Minute}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	processor.nowFn = func() time.Time { return base }
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))

	processor.nowFn = func() time.Time { return base.Add(time.Minute) }
	require.NoError(t, processor.Flush(context.Background()))

	require.Len(t, sink.spans, 1)
	require.Equal(t, "idle_timeout", sink.spans[0].EndReason)
	require.Equal(t, "cursor-1", sink.spans[0].EndCursor)
}

func TestDetectorCheckpointWindowLimitClosesOldestTurn(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{MaxOpenTurns: 1}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-2", "u2", "conv-2", "history", "history", "USER", base.Add(time.Second))))

	require.Len(t, sink.spans, 1)
	require.Equal(t, "checkpoint_window_limit", sink.spans[0].EndReason)
	require.Equal(t, "conv-1", sink.spans[0].ConversationID)
	require.Nil(t, processor.state.OpenTurns["conv-1"])
	require.NotNil(t, processor.state.OpenTurns["conv-2"])
}

func TestDetectorConversationArchiveClosesTurn(t *testing.T) {
	sink := &captureSink{}
	processor := NewProcessor(Config{}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))

	data, err := json.Marshal(map[string]any{
		"id":          "conv-1",
		"ownerUserId": "alice",
		"archivedAt":  base.Add(time.Second).Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	require.NoError(t, processor.Handle(context.Background(), processruntime.EventEnvelope{
		Event:  "updated",
		Kind:   "conversation",
		Data:   data,
		Cursor: "cursor-2",
		Time:   base.Add(time.Second),
	}))

	require.Len(t, sink.spans, 1)
	require.Equal(t, "conversation_archived", sink.spans[0].EndReason)
	require.Equal(t, "alice", sink.spans[0].UserID)
}

func TestDetectorExportFailureKeepsTurnOpen(t *testing.T) {
	sink := &captureSink{err: errors.New("export failed")}
	processor := NewProcessor(Config{}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))
	err := processor.Handle(context.Background(), entryEnvelope("cursor-2", "a1", "conv-1", "history", "history", "AI", base.Add(time.Second)))
	require.ErrorContains(t, err, "export failed")
	require.NotNil(t, processor.state.OpenTurns["conv-1"])
	require.Equal(t, "cursor-1", processor.state.LastEventCursor)
}

func TestDetectorDropOnExportFailureClosesTurn(t *testing.T) {
	sink := &captureSink{err: errors.New("export failed")}
	processor := NewProcessor(Config{DropOnExportFailure: true}, sink)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base)))
	require.NoError(t, processor.Handle(context.Background(), entryEnvelope("cursor-2", "a1", "conv-1", "history", "history", "AI", base.Add(time.Second))))
	require.Nil(t, processor.state.OpenTurns["conv-1"])
	require.Equal(t, "cursor-2", processor.state.LastEventCursor)
}

func entryEnvelope(cursor, entryID, conversationID, channel, contentType, role string, createdAt time.Time) processruntime.EventEnvelope {
	return entryEnvelopeWithGroup(cursor, entryID, conversationID, "", channel, contentType, role, createdAt)
}

func entryEnvelopeWithGroup(cursor, entryID, conversationID, conversationGroupID, channel, contentType, role string, createdAt time.Time) processruntime.EventEnvelope {
	content := []map[string]string{{"text": "redacted"}}
	if role != "" {
		content[0]["role"] = role
	}
	body := map[string]any{
		"id":             entryID,
		"conversationId": conversationID,
		"userId":         "alice",
		"clientId":       "agent-a",
		"agentId":        "assistant",
		"channel":        channel,
		"contentType":    contentType,
		"content":        content,
		"createdAt":      createdAt.Format(time.RFC3339Nano),
	}
	if conversationGroupID != "" {
		body["conversationGroupId"] = conversationGroupID
	}
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return processruntime.EventEnvelope{
		Event:  "created",
		Kind:   "entry",
		Data:   data,
		Cursor: cursor,
		Time:   createdAt,
	}
}

func lc4jHistoryEnvelope(cursor, entryID, conversationID string, createdAt time.Time) processruntime.EventEnvelope {
	body := map[string]any{
		"id":             entryID,
		"conversationId": conversationID,
		"userId":         "alice",
		"clientId":       "agent-a",
		"agentId":        "assistant",
		"channel":        "history",
		"contentType":    "history/lc4j",
		"content": []map[string]any{
			{
				"role": "AI",
				"events": []map[string]any{
					{"eventType": "PartialResponse", "chunk": "LC4J answer."},
					{"eventType": "Completed", "aiMessage": map[string]any{"text": "LC4J answer."}},
				},
			},
		},
		"createdAt": createdAt.Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return processruntime.EventEnvelope{
		Event:  "created",
		Kind:   "entry",
		Data:   data,
		Cursor: cursor,
		Time:   createdAt,
	}
}
