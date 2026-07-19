package operationevent

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
)

func TestLogRecoveredPanicIncludesSafeOperationContext(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	event := New("job.vector_store_delete", WithEmitter(func(string, Level, Snapshot) {}))
	event.SetRequestID("request-1")
	event.SetTaskID("task-1")
	event.SetRetryAttempt(3)
	LogRecoveredPanic(event, "", "private panic value", []byte("goroutine 1 [running]:\npanic frame"))

	text := output.String()
	for _, expected := range []string{
		"operation panic",
		"operation=job.vector_store_delete",
		"requestID=request-1",
		"taskID=task-1",
		"retryAttempt=3",
		"panicType=string",
		"panic frame",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("panic diagnostic missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "private panic value") {
		t.Fatalf("raw panic value leaked into diagnostic:\n%s", text)
	}
	if snapshot := event.Snapshot(); snapshot.ErrorDetails != nil || snapshot.ErrorType != "" || snapshot.ErrorCode != "" {
		t.Fatalf("panic diagnostic mutated canonical event: %#v", snapshot)
	}
}
