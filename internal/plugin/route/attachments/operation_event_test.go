package attachments

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/operationevent"
)

func TestSourceURLAttachmentOperationRecoversPanic(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	var snapshots []operationevent.Snapshot
	event := operationevent.New("job.attachment_download", operationevent.WithEmitter(func(_ string, _ operationevent.Level, snapshot operationevent.Snapshot) {
		snapshots = append(snapshots, snapshot)
	}))
	event.SetAttachmentID("attachment-1")
	runSourceURLAttachmentOperation(context.Background(), event, func(context.Context) error {
		panic("private download panic")
	})

	if len(snapshots) != 2 {
		t.Fatalf("got %d events, want start and terminal", len(snapshots))
	}
	terminal := snapshots[1]
	if terminal.Result != operationevent.ResultFailed || terminal.Reason != "panic" || terminal.FailureCount != 1 || terminal.AttachmentID != "attachment-1" {
		t.Fatalf("unexpected panic terminal event: %#v", terminal)
	}
	for _, expected := range []string{"operation panic", "operation=job.attachment_download", "attachmentID=attachment-1", "operation_event_test.go"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("panic diagnostic missing %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "private download panic") {
		t.Fatalf("raw panic value leaked:\n%s", output.String())
	}
}

func TestSourceURLAttachmentOperationSuppressesConnectionAbortPanic(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	var terminal operationevent.Snapshot
	event := operationevent.New("job.attachment_download", operationevent.WithEmitter(func(_ string, _ operationevent.Level, snapshot operationevent.Snapshot) {
		if snapshot.Phase == "complete" {
			terminal = snapshot
		}
	}))
	runSourceURLAttachmentOperation(context.Background(), event, func(context.Context) error {
		panic(http.ErrAbortHandler)
	})

	if terminal.Result != operationevent.ResultCanceled || terminal.Reason != "client_disconnect" {
		t.Fatalf("unexpected connection-abort event: %#v", terminal)
	}
	if output.Len() != 0 {
		t.Fatalf("connection abort emitted a stack diagnostic:\n%s", output.String())
	}
}

func TestSourceURLAttachmentOperationPreservesPropagatedPanicReason(t *testing.T) {
	var terminal operationevent.Snapshot
	event := operationevent.New("job.attachment_download", operationevent.WithEmitter(func(_ string, _ operationevent.Level, snapshot operationevent.Snapshot) {
		if snapshot.Phase == "complete" {
			terminal = snapshot
		}
	}))
	runSourceURLAttachmentOperation(context.Background(), event, func(context.Context) error {
		return fmt.Errorf("worker stopped: %w", operationevent.ErrRecoveredPanic)
	})

	if terminal.Result != operationevent.ResultFailed || terminal.Reason != "panic" || terminal.FailureCount != 1 {
		t.Fatalf("unexpected propagated-panic event: %#v", terminal)
	}
}
