package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/log"
)

// Config controls the shared checkpointed stream loop.
type Config struct {
	ClientID           string
	Kinds              []string
	EntryChannels      []string
	EntryContentTypes  []string
	EntryRoles         []string
	Scope              string
	AfterCursor        string
	Justification      string
	CheckpointInterval time.Duration
	ReconnectMin       time.Duration
	ReconnectMax       time.Duration
}

// Runtime runs an EventProcessor against a checkpointed event stream.
type Runtime struct {
	Events      EventClient
	Checkpoints CheckpointClient
	Processor   EventProcessor
	Config      Config
	lastSaved   string
}

// Run loads checkpoint state, subscribes to events, dispatches them to the
// processor, and persists checkpoint snapshots after safe progress.
func (r *Runtime) Run(ctx context.Context) error {
	if r.Events == nil {
		return errors.New("event client is required")
	}
	if r.Checkpoints == nil {
		return errors.New("checkpoint client is required")
	}
	if r.Processor == nil {
		return errors.New("processor is required")
	}
	if r.Config.ClientID == "" {
		return errors.New("client ID is required")
	}

	checkpoint, err := r.Checkpoints.Get(ctx, r.Config.ClientID)
	if err != nil && !errors.Is(err, ErrCheckpointNotFound) {
		return err
	}
	afterCursor := r.Config.AfterCursor
	if err == nil {
		if checkpoint.ContentType != "" && checkpoint.ContentType != r.Processor.ContentType() {
			return fmt.Errorf("checkpoint content type mismatch: got %q, want %q", checkpoint.ContentType, r.Processor.ContentType())
		}
		if err := r.Processor.Load(checkpoint.Value); err != nil {
			return fmt.Errorf("load checkpoint: %w", err)
		}
		r.lastSaved = string(checkpoint.Value)
		if cursor := lastEventCursor(checkpoint.Value); cursor != "" {
			afterCursor = cursor
		}
		log.Info("loaded processor checkpoint", "clientID", r.Config.ClientID, "afterCursor", afterCursor, "updatedAt", checkpoint.UpdatedAt)
	} else {
		log.Info("starting without processor checkpoint", "clientID", r.Config.ClientID, "afterCursor", afterCursor)
	}

	backoff := defaultDuration(r.Config.ReconnectMin, time.Second)
	maxBackoff := defaultDuration(r.Config.ReconnectMax, 30*time.Second)

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		stream, err := r.Events.Subscribe(ctx, SubscribeRequest{
			Kinds:             r.Config.Kinds,
			Detail:            "full",
			AfterCursor:       afterCursor,
			Scope:             r.Config.Scope,
			Justification:     r.Config.Justification,
			EntryChannels:     r.Config.EntryChannels,
			EntryContentTypes: r.Config.EntryContentTypes,
			EntryRoles:        r.Config.EntryRoles,
		})
		if err != nil {
			log.Warn("processor event subscription failed", "clientID", r.Config.ClientID, "scope", r.Config.Scope, "afterCursor", afterCursor, "err", err, "retryIn", backoff)
			if waitErr := sleepContext(ctx, backoff); waitErr != nil {
				return nil
			}
			backoff = minDuration(backoff*2, maxBackoff)
			continue
		}
		log.Info("processor event stream subscribed", "clientID", r.Config.ClientID, "scope", r.Config.Scope, "afterCursor", afterCursor, "kinds", r.Config.Kinds, "entryChannels", r.Config.EntryChannels, "entryContentTypes", r.Config.EntryContentTypes, "entryRoles", r.Config.EntryRoles)
		backoff = defaultDuration(r.Config.ReconnectMin, time.Second)

		ticker := time.NewTicker(defaultDuration(r.Config.CheckpointInterval, 5*time.Second))
		err = r.consume(ctx, stream, ticker, &afterCursor)
		ticker.Stop()
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		if errors.Is(err, io.EOF) {
			log.Warn("processor event stream ended", "clientID", r.Config.ClientID, "afterCursor", afterCursor, "retryIn", backoff)
			if waitErr := sleepContext(ctx, backoff); waitErr != nil {
				return nil
			}
			backoff = minDuration(backoff*2, maxBackoff)
			continue
		}
		return err
	}
}

func (r *Runtime) consume(ctx context.Context, stream EventStream, ticker *time.Ticker, afterCursor *string) error {
	type recvResult struct {
		event EventEnvelope
		err   error
	}
	recvCh := make(chan recvResult, 1)
	go func() {
		for {
			event, err := stream.Recv()
			select {
			case recvCh <- recvResult{event: event, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.Processor.Flush(saveCtx)
			_, _ = r.save(saveCtx)
			cancel()
			return nil
		case <-ticker.C:
			if err := r.Processor.Flush(ctx); err != nil {
				return err
			}
			if cursor, err := r.save(ctx); err != nil {
				return err
			} else if cursor != "" {
				*afterCursor = cursor
			}
		case result := <-recvCh:
			if result.err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return result.err
			}
			event := result.event
			if event.Kind == "stream" {
				continue
			}
			log.Debug("processor event received",
				"clientID", r.Config.ClientID,
				"kind", event.Kind,
				"event", event.Event,
				"cursor", event.Cursor,
				"time", event.Time,
				"dataBytes", len(event.Data),
			)
			if err := r.Processor.Handle(ctx, event); err != nil {
				return err
			}
			log.Debug("processor event handled", "clientID", r.Config.ClientID, "kind", event.Kind, "event", event.Event, "cursor", event.Cursor)
			cursor, err := r.save(ctx)
			if err != nil {
				return err
			}
			if cursor != "" {
				*afterCursor = cursor
			}
		}
	}
}

func (r *Runtime) save(ctx context.Context) (string, error) {
	snapshot, err := r.Processor.Snapshot()
	if err != nil {
		return "", err
	}
	if string(snapshot) == r.lastSaved {
		return lastEventCursor(snapshot), nil
	}
	if _, err := r.Checkpoints.Put(ctx, r.Config.ClientID, r.Processor.ContentType(), snapshot); err != nil {
		return "", err
	}
	r.lastSaved = string(snapshot)
	cursor := lastEventCursor(snapshot)
	log.Debug("processor checkpoint saved", "clientID", r.Config.ClientID, "cursor", cursor, "bytes", len(snapshot))
	return cursor, nil
}

func defaultDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
