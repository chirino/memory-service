package service

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

// EvictionService periodically cleans up soft-deleted records past retention.
type EvictionService struct {
	store     registrystore.MemoryStore
	interval  time.Duration
	retention time.Duration
	batchSize int
	delay     time.Duration
}

// NewEvictionService creates a new eviction service.
func NewEvictionService(store registrystore.MemoryStore, batchSize int, delayMs int) *EvictionService {
	return &EvictionService{
		store:     store,
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
	cutoff := time.Now().Add(-e.retention)
	total, err := e.store.CountEvictableGroups(ctx, cutoff)
	if err != nil {
		log.Error("Eviction: count failed", "err", err)
		return
	}
	if total == 0 {
		return
	}

	log.Info("Eviction: starting", "total", total, "cutoff", cutoff)
	evicted := 0
	for {
		ids, err := e.store.FindEvictableGroupIDs(ctx, cutoff, e.batchSize)
		if err != nil {
			log.Error("Eviction: find IDs failed", "err", err)
			return
		}
		if len(ids) == 0 {
			break
		}
		// Create vector delete tasks before hard-deleting so orphaned
		// embeddings are cleaned up asynchronously by the task processor.
		for _, id := range ids {
			body := map[string]interface{}{"conversationGroupId": id.String()}
			if err := e.store.CreateTask(ctx, "vector_store_delete", body); err != nil {
				log.Error("Eviction: create vector delete task failed", "groupId", id, "err", err)
			}
		}
		if err := e.store.HardDeleteConversationGroups(ctx, ids); err != nil {
			log.Error("Eviction: hard delete failed", "err", err)
		}
		evicted += len(ids)

		if e.delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(e.delay):
			}
		}
	}
	log.Info("Eviction: completed", "evicted", evicted)
}
