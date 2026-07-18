package service

import (
	"context"
	"errors"

	"github.com/chirino/memory-service/internal/operationevent"
)

func jobContextResult(ctx context.Context, err error) (operationevent.Result, bool) {
	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return operationevent.ResultTimedOut, true
	}
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return operationevent.ResultCanceled, true
	}
	return "", false
}

func markJobInterrupted(event *operationevent.Event, ctx context.Context, err error) bool {
	result, interrupted := jobContextResult(ctx, err)
	if !interrupted {
		return false
	}
	if result == operationevent.ResultTimedOut {
		event.SetReason("deadline")
	} else {
		event.SetReason("shutdown")
	}
	return true
}

func emitJobTerminal(event *operationevent.Event, ctx context.Context, failures int64) {
	// Cancellation errors are filtered before callers increment failures. Once a
	// real failure has been recorded, keep the run failed even if shutdown races
	// with a later pass.
	if failures > 0 {
		event.EmitTerminal(operationevent.ResultFailed)
		return
	}
	switch event.Snapshot().Reason {
	case "deadline":
		event.EmitTerminal(operationevent.ResultTimedOut)
		return
	case "shutdown":
		event.EmitTerminal(operationevent.ResultCanceled)
		return
	}
	if result, ok := jobContextResult(ctx, nil); ok {
		if result == operationevent.ResultTimedOut {
			event.SetReason("deadline")
		} else {
			event.SetReason("shutdown")
		}
		event.EmitTerminal(result)
		return
	}
	event.EmitTerminal(operationevent.ResultSuccess)
}

func emitJobError(event *operationevent.Event, ctx context.Context, err error, reason string) {
	if result, ok := jobContextResult(ctx, err); ok {
		if result == operationevent.ResultTimedOut {
			event.SetReason("deadline")
		} else {
			event.SetReason("shutdown")
		}
		event.EmitTerminal(result)
		return
	}
	event.SetReason(reason)
	event.EnrichError(err)
	event.EmitTerminal(operationevent.ResultFailed)
}
