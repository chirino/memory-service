package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/chirino/memory-service/internal/operationevent"
)

func TestEmitJobTerminalPreservesFailuresRecordedBeforeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var gotLevel operationevent.Level
	var got operationevent.Snapshot
	event := operationevent.New("job.test", operationevent.WithEmitter(func(_ string, level operationevent.Level, snapshot operationevent.Snapshot) {
		gotLevel = level
		got = snapshot
	}))
	emitJobTerminal(event, ctx, 3)

	if got.Result != operationevent.ResultFailed || gotLevel != operationevent.LevelError {
		t.Fatalf("failed job emitted %#v at %s", got, gotLevel)
	}
}

func TestEmitJobTerminalReportsCancellationWithoutFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var gotLevel operationevent.Level
	var got operationevent.Snapshot
	event := operationevent.New("job.test", operationevent.WithEmitter(func(_ string, level operationevent.Level, snapshot operationevent.Snapshot) {
		gotLevel = level
		got = snapshot
	}))
	emitJobTerminal(event, ctx, 0)

	if got.Result != operationevent.ResultCanceled || got.Reason != "shutdown" || gotLevel != operationevent.LevelInfo {
		t.Fatalf("canceled job emitted %#v at %s", got, gotLevel)
	}
}

func TestMarkedWrappedCancellationRemainsCanceled(t *testing.T) {
	var got operationevent.Snapshot
	event := operationevent.New("job.test", operationevent.WithEmitter(func(_ string, _ operationevent.Level, snapshot operationevent.Snapshot) {
		got = snapshot
	}))
	if !markJobInterrupted(event, context.Background(), fmt.Errorf("worker stopped: %w", context.Canceled)) {
		t.Fatal("wrapped cancellation was not recognized")
	}
	emitJobTerminal(event, context.Background(), 0)
	if got.Result != operationevent.ResultCanceled || got.Reason != "shutdown" {
		t.Fatalf("wrapped cancellation emitted %#v", got)
	}
}
