package service

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/operationevent"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventstream"
)

// EpisodicTTLService runs background passes on a configurable interval:
//  1. Expiry pass — archives memories whose TTL has elapsed (deleted_reason=2).
//  2. Eviction pass A — hard-deletes superseded-update rows (deleted_reason=0) once re-indexed.
//  3. Eviction pass B — tombstones delete/expired rows (deleted_reason IN (1,2)) once re-indexed,
//     clearing encrypted data while keeping the row for event history.
//  4. Tombstone cleanup — hard-deletes tombstones older than tombstoneRetention.
type EpisodicTTLService struct {
	store              registryepisodic.EpisodicStore
	memoryStore        registrystore.MemoryStore
	eventBus           registryeventbus.EventBus
	interval           time.Duration
	evictionBatch      int
	tombstoneRetention time.Duration
}

// NewEpisodicTTLService creates a new EpisodicTTLService.
func NewEpisodicTTLService(store registryepisodic.EpisodicStore, memoryStore registrystore.MemoryStore, eventBus registryeventbus.EventBus, interval time.Duration, evictionBatch int, tombstoneRetention time.Duration) *EpisodicTTLService {
	return &EpisodicTTLService{
		store:              store,
		memoryStore:        memoryStore,
		eventBus:           eventBus,
		interval:           interval,
		evictionBatch:      evictionBatch,
		tombstoneRetention: tombstoneRetention,
	}
}

// Start runs the TTL service until ctx is cancelled.
func (s *EpisodicTTLService) Start(ctx context.Context) {
	if s == nil || s.store == nil || s.interval <= 0 {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *EpisodicTTLService) runOnce(ctx context.Context) {
	event := operationevent.New("job.episodic_maintenance")
	var work, failures int64
	started := false
	recordWork := func(count int64) {
		if count <= 0 {
			return
		}
		work += count
		if !started {
			event.SetWorkCount(work)
			event.EmitStart()
			started = true
		}
	}
	defer func() {
		event.SetWorkCount(work)
		event.SetFailureCount(failures)
		emitJobTerminal(event, ctx, failures)
	}()
	defer recoverJobPanic(event, func() { failures++ })
	// Pass 1: expire memories whose TTL has elapsed.
	var (
		n      int64
		err    error
		events []registryeventbus.Event
	)
	err = s.store.InWriteTx(ctx, func(writeCtx context.Context) error {
		if lifecycle, ok := s.store.(registryepisodic.BackgroundMemoryLifecycleStore); ok {
			var changes []registryepisodic.MemoryLifecycleChange
			changes, err = lifecycle.ExpireMemoriesWithChanges(writeCtx)
			n = int64(len(changes))
			if err == nil {
				events, err = s.appendMemoryLifecycleEvents(writeCtx, changes, "updated", "expired")
			}
		} else {
			n, err = s.store.ExpireMemories(writeCtx)
		}
		return err
	})
	if err != nil {
		if markJobInterrupted(event, ctx, err) {
			return
		}
		log.Error("Episodic TTL expiry failed", "err", err)
		event.SetReason("expiry_failed")
		event.EnrichError(err)
		failures++
	} else if n > 0 {
		recordWork(n)
		s.publishMemoryLifecycleEvents(ctx, events)
	}

	// Pass 2A: hard-delete superseded update rows once vector cleanup is confirmed.
	err = s.store.InWriteTx(ctx, func(writeCtx context.Context) error {
		n, err = s.store.HardDeleteEvictableUpdates(writeCtx, s.evictionBatch)
		return err
	})
	if err != nil {
		if markJobInterrupted(event, ctx, err) {
			return
		}
		log.Error("Episodic eviction (updates) failed", "err", err)
		event.SetReason("update_eviction_failed")
		event.EnrichError(err)
		failures++
	} else if n > 0 {
		recordWork(n)
	}

	// Pass 2B: tombstone delete/expired rows once vector cleanup is confirmed.
	err = s.store.InWriteTx(ctx, func(writeCtx context.Context) error {
		events = nil
		if lifecycle, ok := s.store.(registryepisodic.BackgroundMemoryLifecycleStore); ok {
			var changes []registryepisodic.MemoryLifecycleChange
			changes, err = lifecycle.TombstoneDeletedMemoriesWithChanges(writeCtx, s.evictionBatch)
			n = int64(len(changes))
			if err == nil {
				events, err = s.appendMemoryLifecycleEvents(writeCtx, changes, "deleted", "evicted")
			}
		} else {
			n, err = s.store.TombstoneDeletedMemories(writeCtx, s.evictionBatch)
		}
		return err
	})
	if err != nil {
		if markJobInterrupted(event, ctx, err) {
			return
		}
		log.Error("Episodic tombstone pass failed", "err", err)
		event.SetReason("tombstone_failed")
		event.EnrichError(err)
		failures++
	} else if n > 0 {
		recordWork(n)
		s.publishMemoryLifecycleEvents(ctx, events)
	}

	// Pass 3: hard-delete tombstones older than the retention period.
	if s.tombstoneRetention > 0 {
		olderThan := time.Now().Add(-s.tombstoneRetention)
		err = s.store.InWriteTx(ctx, func(writeCtx context.Context) error {
			events = nil
			if lifecycle, ok := s.store.(registryepisodic.BackgroundMemoryLifecycleStore); ok {
				var changes []registryepisodic.MemoryLifecycleChange
				changes, err = lifecycle.HardDeleteExpiredTombstonesWithChanges(writeCtx, olderThan, s.evictionBatch)
				n = int64(len(changes))
				if err == nil {
					events, err = s.appendMemoryLifecycleEvents(writeCtx, changes, "deleted", "hard_deleted")
				}
			} else {
				n, err = s.store.HardDeleteExpiredTombstones(writeCtx, olderThan, s.evictionBatch)
			}
			return err
		})
		if err != nil {
			if markJobInterrupted(event, ctx, err) {
				return
			}
			log.Error("Episodic tombstone cleanup failed", "err", err)
			event.SetReason("tombstone_cleanup_failed")
			event.EnrichError(err)
			failures++
		} else if n > 0 {
			recordWork(n)
			s.publishMemoryLifecycleEvents(ctx, events)
		}
	}
}

func (s *EpisodicTTLService) appendMemoryLifecycleEvents(ctx context.Context, changes []registryepisodic.MemoryLifecycleChange, action, change string) ([]registryeventbus.Event, error) {
	events := make([]registryeventbus.Event, 0, len(changes))
	for _, changed := range changes {
		events = append(events, eventstream.MemoryChangedEvent(action, change, &registryepisodic.MemoryItem{
			ID: changed.ID, MemoryKind: changed.MemoryKind, Revision: changed.Revision,
			CreatedAt: changed.CreatedAt, ExpiresAt: changed.ExpiresAt, ArchivedAt: changed.ArchivedAt,
		}))
	}
	if s.memoryStore == nil {
		return events, nil
	}
	appended, used, err := eventstream.AppendOutboxEvents(ctx, s.memoryStore, events...)
	if err != nil {
		return nil, err
	}
	if used {
		return appended, nil
	}
	return events, nil
}

func (s *EpisodicTTLService) publishMemoryLifecycleEvents(ctx context.Context, events []registryeventbus.Event) {
	if len(events) == 0 || s.memoryStore == nil || s.eventBus == nil {
		return
	}
	if err := eventstream.PublishEvents(ctx, s.memoryStore, s.eventBus, events...); err != nil {
		log.Warn("publish episodic maintenance event failed", "err", err)
	}
}
