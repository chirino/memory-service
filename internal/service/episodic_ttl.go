package service

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
)

// EpisodicTTLService runs background passes on a configurable interval:
//  1. Expiry pass — soft-deletes memories whose TTL has elapsed (deleted_reason=2).
//  2. Eviction pass A — hard-deletes superseded-update rows (deleted_reason=0) once re-indexed.
//  3. Eviction pass B — tombstones delete/expired rows (deleted_reason IN (1,2)) once re-indexed,
//     clearing encrypted data while keeping the row for event history.
//  4. Tombstone cleanup — hard-deletes tombstones older than tombstoneRetention.
type EpisodicTTLService struct {
	store              registryepisodic.EpisodicStore
	interval           time.Duration
	evictionBatch      int
	tombstoneRetention time.Duration
}

// NewEpisodicTTLService creates a new EpisodicTTLService.
func NewEpisodicTTLService(store registryepisodic.EpisodicStore, interval time.Duration, evictionBatch int, tombstoneRetention time.Duration) *EpisodicTTLService {
	return &EpisodicTTLService{
		store:              store,
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
	// Pass 1: expire memories whose TTL has elapsed.
	n, err := s.store.ExpireMemories(ctx)
	if err != nil {
		log.Error("Episodic TTL expiry failed", "err", err)
	} else if n > 0 {
		log.Info("Episodic TTL expiry", "expired", n)
	}

	// Pass 2A: hard-delete superseded update rows once vector cleanup is confirmed.
	n, err = s.store.HardDeleteEvictableUpdates(ctx, s.evictionBatch)
	if err != nil {
		log.Error("Episodic eviction (updates) failed", "err", err)
	} else if n > 0 {
		log.Info("Episodic eviction (updates)", "deleted", n)
	}

	// Pass 2B: tombstone delete/expired rows once vector cleanup is confirmed.
	n, err = s.store.TombstoneDeletedMemories(ctx, s.evictionBatch)
	if err != nil {
		log.Error("Episodic tombstone pass failed", "err", err)
	} else if n > 0 {
		log.Info("Episodic tombstone pass", "tombstoned", n)
	}

	// Pass 3: hard-delete tombstones older than the retention period.
	if s.tombstoneRetention > 0 {
		olderThan := time.Now().Add(-s.tombstoneRetention)
		n, err = s.store.HardDeleteExpiredTombstones(ctx, olderThan, s.evictionBatch)
		if err != nil {
			log.Error("Episodic tombstone cleanup failed", "err", err)
		} else if n > 0 {
			log.Info("Episodic tombstone cleanup", "deleted", n)
		}
	}
}
