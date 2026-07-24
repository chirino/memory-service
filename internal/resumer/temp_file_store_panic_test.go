package resumer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/operationevent"
)

func TestReplayWorkerRecoversPanicWithOperationContext(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	event := operationevent.New("grpc /memory.v1.ResponseRecorderService/Replay", operationevent.WithEmitter(func(string, operationevent.Level, operationevent.Snapshot) {}))
	event.SetRequestID("request-1")
	event.SetConversationID("conversation-1")
	ctx := operationevent.WithContext(context.Background(), event)
	out := make(chan ReplayResult, 1)
	runReplayWorker(ctx, out, func(chan<- ReplayResult) {
		panic("private replay panic")
	})

	result, ok := <-out
	if !ok || !errors.Is(result.Err, operationevent.ErrRecoveredPanic) {
		t.Fatalf("replay panic was not propagated: ok=%v err=%v", ok, result.Err)
	}
	if _, ok := <-out; ok {
		t.Fatal("replay result channel was not closed")
	}
	for _, expected := range []string{
		"operation panic",
		`operation="grpc /memory.v1.ResponseRecorderService/Replay"`,
		"requestID=request-1",
		"conversationID=conversation-1",
		"temp_file_store_panic_test.go",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("panic diagnostic missing %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "private replay panic") {
		t.Fatalf("raw panic value leaked:\n%s", output.String())
	}
}
