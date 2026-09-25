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
	Detail             string
	CheckpointInterval time.Duration
	ReconnectMin       time.Duration
	ReconnectMax       time.Duration
	LeaseTTL           time.Duration
	OnReady            func(bool)
}

// Runtime runs an EventProcessor against a checkpointed event stream.
type Runtime struct {
	Events      EventClient
	Checkpoints CheckpointClient
	Processor   EventProcessor
	Config      Config
	lastSaved   string
	revision    string
	lease       *CheckpointLease
	leased      LeasedCheckpointClient
}

// Run loads checkpoint state, subscribes to events, dispatches them to the
// processor, and persists checkpoint snapshots after safe progress.
func (r *Runtime) Run(ctx context.Context) error {
	if r.Config.OnReady != nil {
		r.Config.OnReady(false)
		defer r.Config.OnReady(false)
	}
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
		r.revision = checkpoint.Revision
		if cursor := lastEventCursor(checkpoint.Value); cursor != "" {
			afterCursor = cursor
		}
		log.Info("loaded processor checkpoint", "clientID", r.Config.ClientID, "afterCursor", afterCursor, "updatedAt", checkpoint.UpdatedAt)
	} else {
		log.Info("starting without processor checkpoint", "clientID", r.Config.ClientID, "afterCursor", afterCursor)
		if r.Config.LeaseTTL > 0 {
			snapshot, snapshotErr := r.Processor.Snapshot()
			if snapshotErr != nil {
				return snapshotErr
			}
			checkpoint, snapshotErr = r.Checkpoints.Put(ctx, r.Config.ClientID, r.Processor.ContentType(), snapshot)
			if snapshotErr != nil {
				return snapshotErr
			}
			r.lastSaved = string(snapshot)
			r.revision = checkpoint.Revision
		}
	}
	if r.Config.LeaseTTL > 0 {
		leased, ok := r.Checkpoints.(LeasedCheckpointClient)
		if !ok {
			return errors.New("checkpoint client does not support leases")
		}
		lease, err := leased.AcquireLease(ctx, r.Config.ClientID, r.Config.LeaseTTL)
		if err != nil {
			return fmt.Errorf("acquire checkpoint lease: %w", err)
		}
		r.leased, r.lease = leased, &lease
		defer r.releaseLease()
		// Reload after fencing out other owners. A competing initializer may
		// have updated the checkpoint between the first read and acquisition.
		checkpoint, err = r.Checkpoints.Get(ctx, r.Config.ClientID)
		if err != nil {
			return fmt.Errorf("reload leased checkpoint: %w", err)
		}
		if checkpoint.ContentType != "" && checkpoint.ContentType != r.Processor.ContentType() {
			return fmt.Errorf("checkpoint content type mismatch: got %q, want %q", checkpoint.ContentType, r.Processor.ContentType())
		}
		if err := r.Processor.Load(checkpoint.Value); err != nil {
			return fmt.Errorf("reload leased checkpoint: %w", err)
		}
		r.lastSaved = string(checkpoint.Value)
		r.revision = checkpoint.Revision
		if cursor := lastEventCursor(checkpoint.Value); cursor != "" {
			afterCursor = cursor
		}
		if consumer, ok := r.Processor.(LeaseGenerationConsumer); ok {
			if err := consumer.SetLeaseGeneration(lease.Generation); err != nil {
				return err
			}
		}
		log.Info("acquired processor checkpoint lease", "clientID", r.Config.ClientID, "generation", lease.Generation, "expiresAt", lease.ExpiresAt)
	}

	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	leaseErrors := r.startRuntimeErrors(workCtx, cancelWork)
	if bootstrapper, ok := r.Processor.(BootstrapProcessor); ok {
		cursor, bootstrapErr := bootstrapper.Bootstrap(workCtx, r.Events, func(saveCtx context.Context) error {
			select {
			case leaseErr := <-leaseErrors:
				return leaseErr
			default:
			}
			_, saveErr := r.save(saveCtx)
			return saveErr
		})
		if bootstrapErr != nil {
			if leaseErr := pollError(leaseErrors); leaseErr != nil {
				return leaseErr
			}
			return fmt.Errorf("processor bootstrap: %w", bootstrapErr)
		}
		if cursor != "" {
			afterCursor = cursor
		}
	}

	backoff := defaultDuration(r.Config.ReconnectMin, time.Second)
	maxBackoff := defaultDuration(r.Config.ReconnectMax, 30*time.Second)

	for {
		select {
		case err := <-leaseErrors:
			return err
		default:
		}
		if err := workCtx.Err(); err != nil {
			if leaseErr := pollError(leaseErrors); leaseErr != nil {
				return leaseErr
			}
			return nil
		}
		streamCtx, cancelStream := context.WithCancel(workCtx)
		stream, err := r.Events.Subscribe(streamCtx, SubscribeRequest{
			Kinds:             r.Config.Kinds,
			Detail:            defaultStringValue(r.Config.Detail, "full"),
			AfterCursor:       afterCursor,
			Scope:             r.Config.Scope,
			Justification:     r.Config.Justification,
			EntryChannels:     r.Config.EntryChannels,
			EntryContentTypes: r.Config.EntryContentTypes,
			EntryRoles:        r.Config.EntryRoles,
		})
		if err != nil {
			cancelStream()
			if leaseErr := pollError(leaseErrors); leaseErr != nil {
				return leaseErr
			}
			log.Warn("processor event subscription failed", "clientID", r.Config.ClientID, "scope", r.Config.Scope, "afterCursor", afterCursor, "err", err, "retryIn", backoff)
			if waitErr := sleepContextOrError(workCtx, backoff, leaseErrors); waitErr != nil {
				if ctx.Err() == nil {
					return waitErr
				}
				return nil
			}
			backoff = minDuration(backoff*2, maxBackoff)
			continue
		}
		log.Info("processor event stream subscribed", "clientID", r.Config.ClientID, "scope", r.Config.Scope, "afterCursor", afterCursor, "kinds", r.Config.Kinds, "entryChannels", r.Config.EntryChannels, "entryContentTypes", r.Config.EntryContentTypes, "entryRoles", r.Config.EntryRoles)
		if r.Config.OnReady != nil {
			r.Config.OnReady(true)
		}
		backoff = defaultDuration(r.Config.ReconnectMin, time.Second)

		ticker := time.NewTicker(defaultDuration(r.Config.CheckpointInterval, 5*time.Second))
		err = r.consume(ctx, streamCtx, stream, ticker, leaseErrors, &afterCursor)
		ticker.Stop()
		cancelStream()
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if leaseErr := pollError(leaseErrors); leaseErr != nil {
				return leaseErr
			}
			return nil
		}
		if errors.Is(err, io.EOF) {
			if r.Config.OnReady != nil {
				r.Config.OnReady(false)
			}
			log.Warn("processor event stream ended", "clientID", r.Config.ClientID, "afterCursor", afterCursor, "retryIn", backoff)
			if waitErr := sleepContextOrError(ctx, backoff, leaseErrors); waitErr != nil {
				if ctx.Err() == nil {
					return waitErr
				}
				return nil
			}
			backoff = minDuration(backoff*2, maxBackoff)
			continue
		}
		return err
	}
}

func defaultStringValue(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func (r *Runtime) consume(parentCtx, workCtx context.Context, stream EventStream, ticker *time.Ticker, leaseErrors <-chan error, afterCursor *string) error {
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
			case <-workCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-workCtx.Done():
			if leaseErr := pollError(leaseErrors); leaseErr != nil {
				return leaseErr
			}
			if parentCtx.Err() == nil {
				return workCtx.Err()
			}
			saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.Processor.Flush(saveCtx)
			_, _ = r.save(saveCtx)
			cancel()
			return nil
		case <-ticker.C:
			if err := r.Processor.Flush(workCtx); err != nil {
				if leaseErr := pollError(leaseErrors); leaseErr != nil {
					return leaseErr
				}
				return err
			}
			if cursor, err := r.save(workCtx); err != nil {
				return err
			} else if cursor != "" {
				*afterCursor = cursor
			}
		case err := <-leaseErrors:
			return err
		case result := <-recvCh:
			if result.err != nil {
				if workCtx.Err() != nil {
					if leaseErr := pollError(leaseErrors); leaseErr != nil {
						return leaseErr
					}
					return nil
				}
				return result.err
			}
			event := result.event
			log.Debug("processor event received",
				"clientID", r.Config.ClientID,
				"kind", event.Kind,
				"event", event.Event,
				"cursor", event.Cursor,
				"time", event.Time,
				"dataBytes", len(event.Data),
			)
			if err := r.Processor.Handle(workCtx, event); err != nil {
				if leaseErr := pollError(leaseErrors); leaseErr != nil {
					return leaseErr
				}
				return err
			}
			log.Debug("processor event handled", "clientID", r.Config.ClientID, "kind", event.Kind, "event", event.Event, "cursor", event.Cursor)
			cursor, err := r.save(workCtx)
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
	var checkpoint Checkpoint
	if r.lease != nil {
		checkpoint, err = r.leased.PutCAS(ctx, r.Config.ClientID, r.Processor.ContentType(), snapshot, r.revision, r.lease.Token)
	} else {
		checkpoint, err = r.Checkpoints.Put(ctx, r.Config.ClientID, r.Processor.ContentType(), snapshot)
	}
	if err != nil {
		return "", err
	}
	r.revision = checkpoint.Revision
	r.lastSaved = string(snapshot)
	cursor := lastEventCursor(snapshot)
	log.Debug("processor checkpoint saved", "clientID", r.Config.ClientID, "cursor", cursor, "bytes", len(snapshot))
	return cursor, nil
}

func (r *Runtime) releaseLease() {
	if r.leased == nil || r.lease == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.leased.ReleaseLease(ctx, r.Config.ClientID, r.lease.Token); err != nil {
		log.Warn("release processor checkpoint lease failed", "clientID", r.Config.ClientID, "err", err)
	}
	r.lease = nil
}

func (r *Runtime) startRuntimeErrors(ctx context.Context, cancelWork context.CancelFunc) <-chan error {
	leaseErrors := r.startLeaseRenewal(ctx)
	var processorErrors <-chan error
	if source, ok := r.Processor.(BackgroundErrorSource); ok {
		processorErrors = source.BackgroundErrors()
	}
	if leaseErrors == nil && processorErrors == nil {
		return nil
	}
	errorsCh := make(chan error, 1)
	go func() {
		for leaseErrors != nil || processorErrors != nil {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-leaseErrors:
				if !ok {
					leaseErrors = nil
					continue
				}
				if err != nil {
					errorsCh <- err
					cancelWork()
					return
				}
			case err, ok := <-processorErrors:
				if !ok {
					processorErrors = nil
					continue
				}
				if err != nil {
					errorsCh <- err
					cancelWork()
					return
				}
			}
		}
	}()
	return errorsCh
}

func (r *Runtime) startLeaseRenewal(ctx context.Context) <-chan error {
	if r.leased == nil || r.lease == nil {
		return nil
	}
	errorsCh := make(chan error, 1)
	token, generation := r.lease.Token, r.lease.Generation
	go func() {
		ticker := time.NewTicker(r.Config.LeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(ctx, r.Config.LeaseTTL/3)
				lease, err := r.leased.RenewLease(renewCtx, r.Config.ClientID, token, r.Config.LeaseTTL)
				cancel()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					errorsCh <- fmt.Errorf("renew checkpoint lease: %w", err)
					return
				}
				if lease.Generation != generation {
					errorsCh <- errors.New("checkpoint lease generation changed during renewal")
					return
				}
			}
		}
	}()
	return errorsCh
}

func pollError(errorsCh <-chan error) error {
	select {
	case err := <-errorsCh:
		return err
	default:
		return nil
	}
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

func sleepContextOrError(ctx context.Context, d time.Duration, errCh <-chan error) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-timer.C:
		return nil
	}
}
