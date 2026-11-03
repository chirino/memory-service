package service

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/operationevent"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventstream"
)

// EvictionService periodically cleans up archived records past retention.
type EvictionService struct {
	store     registrystore.MemoryStore
	eventBus  registryeventbus.EventBus
	interval  time.Duration
	retention time.Duration
	batchSize int
	delay     time.Duration
}

// NewEvictionService creates a new eviction service.
func NewEvictionService(store registrystore.MemoryStore, eventBus registryeventbus.EventBus, batchSize int, delayMs int) *EvictionService {
	return &EvictionService{
		store:     store,
		eventBus:  eventBus,
		interval:  1 * time.Hour,
		retention: 30 * 24 * time.Hour, // 30 days default
		batchSize: batchSize,
		delay:     time.Duration(delayMs) * time.Millisecond,
	}
}

// Start begins the periodic eviction loop. Returns when ctx is cancelled.
func (e *EvictionService) Start(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runEviction(ctx)
		}
	}
}

func (e *EvictionService) runEviction(ctx context.Context) {
	event := operationevent.New("job.eviction")
	var evicted, failures int
	defer func() {
		event.SetWorkCount(int64(evicted))
		event.SetFailureCount(int64(failures))
		emitJobTerminal(event, ctx, int64(failures))
	}()
	defer recoverJobPanic(event, func() { failures++ })
	filter := registrystore.DefaultConversationEvictionFilter(time.Now().Add(-e.retention))
	var total int64
	err := e.store.InReadTx(ctx, func(readCtx context.Context) error {
		var err error
		total, err = e.store.CountEvictableGroups(readCtx, filter)
		return err
	})
	if err != nil {
		if markJobInterrupted(event, ctx, err) {
			return
		}
		log.Error("Eviction: count failed", "err", err)
		event.SetReason("count_failed")
		event.EnrichError(err)
		failures++
		return
	}
	if total == 0 {
		return
	}

	event.SetWorkCount(total)
	event.EmitStart()
	for {
		var batch eventstream.ConversationEvictionBatch
		if err := e.store.InWriteTx(ctx, func(writeCtx context.Context) error {
			var err error
			batch, err = eventstream.EvictConversationGroups(writeCtx, e.store, filter, e.batchSize)
			return err
		}); err != nil {
			if markJobInterrupted(event, ctx, err) {
				return
			}
			log.Error("Eviction: hard delete failed", "err", err)
			event.SetReason("delete_failed")
			event.EnrichError(err)
			failures++
			return
		}
		if batch.Found == 0 {
			break
		}
		filter = batch.Next
		if err := eventstream.PublishEvents(ctx, e.store, e.eventBus, batch.Events...); err != nil {
			if markJobInterrupted(event, ctx, err) {
				return
			}
			log.Error("Eviction: publish delete events failed", "err", err)
			event.SetReason("event_publish_failed")
			event.EnrichError(err)
			failures++
		}
		evicted += batch.Deleted

		if e.delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(e.delay):
			}
		}
	}
}
