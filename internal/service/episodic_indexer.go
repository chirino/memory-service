package service

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
)

// EpisodicIndexer polls for memories with indexed_at IS NULL and:
//   - Active rows (deleted_at IS NULL): generates embeddings and upserts them into the vector store.
//   - Soft-deleted rows (deleted_at IS NOT NULL): removes the corresponding vector entries.
type EpisodicIndexer struct {
	store     registryepisodic.EpisodicStore
	embedder  registryembed.Embedder
	interval  time.Duration
	batchSize int
	mu        sync.Mutex
}

// EpisodicIndexRunStats summarizes a single indexer cycle.
type EpisodicIndexRunStats struct {
	Pending            int `json:"pending"`
	Processed          int `json:"processed"`
	SkippedNoEmbedding int `json:"skipped_no_embedding"`
	Embedded           int `json:"embedded"`
	VectorUpserts      int `json:"vector_upserts"`
	VectorDeletes      int `json:"vector_deletes"`
	Failures           int `json:"failures"`
}

// NewEpisodicIndexer creates a new EpisodicIndexer. If embedder is nil, indexing is skipped
// for active rows but soft-deleted cleanup still runs.
func NewEpisodicIndexer(store registryepisodic.EpisodicStore, embedder registryembed.Embedder, interval time.Duration, batchSize int) *EpisodicIndexer {
	return &EpisodicIndexer{
		store:     store,
		embedder:  embedder,
		interval:  interval,
		batchSize: batchSize,
	}
}

// Start runs the indexer until ctx is cancelled.
func (idx *EpisodicIndexer) Start(ctx context.Context) {
	if idx == nil || idx.store == nil || idx.interval <= 0 {
		return
	}
	ticker := time.NewTicker(idx.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = idx.Trigger(ctx)
		}
	}
}

// Trigger runs one indexing cycle synchronously.
func (idx *EpisodicIndexer) Trigger(ctx context.Context) (EpisodicIndexRunStats, error) {
	if idx == nil || idx.store == nil {
		return EpisodicIndexRunStats{}, nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.runOnce(ctx), nil
}

func (idx *EpisodicIndexer) runOnce(ctx context.Context) EpisodicIndexRunStats {
	stats := EpisodicIndexRunStats{}
	var pending []registryepisodic.PendingMemory
	err := idx.store.InReadTx(ctx, func(readCtx context.Context) error {
		var err error
		pending, err = idx.store.FindMemoriesPendingIndexing(readCtx, idx.batchSize)
		return err
	})
	if err != nil {
		log.Error("Episodic indexer: find pending failed", "err", err)
		stats.Failures++
		return stats
	}
	stats.Pending = len(pending)
	for _, m := range pending {
		stats.Processed++
		if m.DeletedAt != nil {
			// Soft-deleted: remove vector entries.
			if err := idx.store.InWriteTx(ctx, func(writeCtx context.Context) error {
				if err := idx.store.DeleteMemoryVectors(writeCtx, m.ID); err != nil {
					return err
				}
				return idx.store.SetMemoryIndexedAt(writeCtx, m.ID, time.Now())
			}); err != nil {
				log.Warn("Episodic indexer: delete vectors failed", "id", m.ID, "err", err)
				stats.Failures++
				continue
			}
			stats.VectorDeletes++
			continue
		}

		// Active row: embed and upsert.
		if idx.embedder == nil || len(m.IndexedContent) == 0 {
			// No embedder or no value — mark as indexed with no vector.
			stats.SkippedNoEmbedding++
			if err := idx.store.InWriteTx(ctx, func(writeCtx context.Context) error {
				return idx.store.SetMemoryIndexedAt(writeCtx, m.ID, time.Now())
			}); err != nil {
				log.Error("Episodic indexer: set indexed_at failed", "id", m.ID, "err", err)
				stats.Failures++
			}
			continue
		}

		// Batch-embed all non-empty string fields in a single call.
		type fieldEntry struct{ name, text string }
		var entries []fieldEntry
		names := make([]string, 0, len(m.IndexedContent))
		for name := range m.IndexedContent {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			text := m.IndexedContent[name]
			if text != "" {
				entries = append(entries, fieldEntry{name, text})
			}
		}

		var upserts []registryepisodic.MemoryVectorUpsert
		if len(entries) > 0 {
			texts := make([]string, len(entries))
			for i, fe := range entries {
				texts[i] = fe.text
			}
			embeddings, err := idx.embedder.EmbedTexts(ctx, texts)
			if err != nil {
				log.Warn("Episodic indexer: embed failed", "id", m.ID, "err", err)
				stats.Failures++
			} else {
				stats.Embedded += len(embeddings)
				for i, fe := range entries {
					if i < len(embeddings) {
						upserts = append(upserts, registryepisodic.MemoryVectorUpsert{
							MemoryID:         m.ID,
							FieldName:        fe.name,
							Namespace:        m.Namespace,
							PolicyAttributes: m.PolicyAttributes,
							Embedding:        embeddings[i],
						})
					}
				}
			}
		}

		if len(upserts) > 0 {
			if err := idx.store.InWriteTx(ctx, func(writeCtx context.Context) error {
				if err := idx.store.UpsertMemoryVectors(writeCtx, upserts); err != nil {
					return err
				}
				return idx.store.SetMemoryIndexedAt(writeCtx, m.ID, time.Now())
			}); err != nil {
				log.Warn("Episodic indexer: upsert vectors failed", "id", m.ID, "err", err)
				stats.Failures++
				continue
			}
			stats.VectorUpserts += len(upserts)
		} else if err := idx.store.InWriteTx(ctx, func(writeCtx context.Context) error {
			return idx.store.SetMemoryIndexedAt(writeCtx, m.ID, time.Now())
		}); err != nil {
			log.Error("Episodic indexer: set indexed_at failed", "id", m.ID, "err", err)
			stats.Failures++
		}
	}
	return stats
}
