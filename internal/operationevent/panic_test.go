package operationevent

import (
	"bytes"
	"context"
	"errors"
	"net/http"
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

func TestRecoveredPanicErrorPreservesDetailsAndSuppressesConnectionAbort(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	event := New("job.test", WithEmitter(func(string, Level, Snapshot) {}))
	err := RecoveredPanicError(event, "", "private panic value", []byte("panic-site stack"))
	if !errors.Is(err, ErrRecoveredPanic) {
		t.Fatalf("recovered panic classification lost: %v", err)
	}
	event.EnrichError(err)
	snapshot := event.Snapshot()
	if snapshot.ErrorType != "panic" || snapshot.ErrorCode != "internal_panic" || snapshot.Reason != "panic" {
		t.Fatalf("unexpected recovered panic details: %#v", snapshot)
	}
	if !strings.Contains(output.String(), "panic-site stack") || strings.Contains(output.String(), "private panic value") {
		t.Fatalf("unexpected panic diagnostic:\n%s", output.String())
	}

	output.Reset()
	err = RecoveredPanicError(event, "", http.ErrAbortHandler, []byte("suppressed stack"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("connection abort was not canceled: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("connection abort emitted a diagnostic:\n%s", output.String())
	}
}
