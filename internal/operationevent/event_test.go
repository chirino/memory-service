package operationevent

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestEventLifecycleAndDeepSnapshot(t *testing.T) {
	var mu sync.Mutex
	var records []Snapshot
	event := New("http GET /v1/conversations/{conversationId}", WithEmitter(func(_ string, _ Level, snapshot Snapshot) {
		mu.Lock()
		records = append(records, snapshot)
		mu.Unlock()
	}))
	event.SetRequestID(" request\nidentifier ")
	event.SetConversationID("conversation-1")
	event.EnrichError(WithErrorDetails(context.Canceled, ErrorDetails{ErrorType: "client", ErrorCode: "closed"}))

	if !event.EmitStart() || event.EmitStart() {
		t.Fatal("start must emit exactly once")
	}
	if !event.EmitTerminal(ResultCanceled) || event.EmitTerminal(ResultFailed) {
		t.Fatal("terminal must emit exactly once")
	}

	mu.Lock()
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].Phase != "start" || records[1].Phase != "complete" {
		t.Fatalf("unexpected phases: %#v", records)
	}
	if records[1].RequestID != "request identifier" {
		t.Fatalf("request ID was not sanitized: %q", records[1].RequestID)
	}
	records[1].ErrorDetails[0].ErrorCode = "mutated"
	mu.Unlock()
	if got := event.Snapshot().ErrorDetails[0].ErrorCode; got != "closed" {
		t.Fatalf("snapshot shared mutable details: %q", got)
	}
}

func TestSnapshotDeepCopiesProviderDetails(t *testing.T) {
	event := New("job.test", WithEmitter(func(string, Level, Snapshot) {}))
	event.EnrichError(WithErrorDetails(context.Canceled, ErrorDetails{
		ErrorType: "provider",
		Provider:  &ErrorDetailsProvider{Name: "test", TransactionID: "transaction-1"},
	}))
	snapshot := event.Snapshot()
	snapshot.ErrorDetails[0].Provider.TransactionID = "mutated"
	if got := event.Snapshot().ErrorDetails[0].Provider.TransactionID; got != "transaction-1" {
		t.Fatalf("provider details shared mutable state: %q", got)
	}
}

func TestConcurrentMutationAndSnapshot(t *testing.T) {
	event := New("job.entry_index", WithEmitter(func(string, Level, Snapshot) {}))
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				event.SetUserID(strings.Repeat("u", 200))
				event.SetWorkCount(int64(j))
				_ = event.Snapshot()
			}
		}()
	}
	wg.Wait()
	if len([]rune(event.Snapshot().UserID)) != 128 {
		t.Fatal("field was not bounded")
	}
}

func TestContextAndResultLevels(t *testing.T) {
	event := New("job.test", WithEmitter(func(string, Level, Snapshot) {}))
	ctx := WithContext(context.Background(), event)
	if FromContext(ctx) != event {
		t.Fatal("event not recovered from context")
	}
	if LevelForResult(ResultCanceled) != LevelInfo || LevelForResult(ResultInvalid) != LevelWarn || LevelForResult(ResultFailed) != LevelError {
		t.Fatal("unexpected result level mapping")
	}
}

func TestSnapshotLogArgsUseStableCanonicalOrder(t *testing.T) {
	args := snapshotLogArgs(Snapshot{
		Phase:          "complete",
		RequestID:      "request-1",
		Status:         200,
		Result:         ResultSuccess,
		ConversationID: "conversation-1",
		WorkCount:      2,
	})
	want := []any{
		"phase", "complete",
		"requestID", "request-1",
		"status", 200,
		"result", ResultSuccess,
		"conversationID", "conversation-1",
		"workCount", int64(2),
	}
	if len(args) != len(want) {
		t.Fatalf("args = %#v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %#v, want %#v; all=%#v", i, args[i], want[i], args)
		}
	}
}
