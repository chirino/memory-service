package service

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/model"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
)

// BackgroundIndexer polls for unindexed entries, generates embeddings, and stores them.
type BackgroundIndexer struct {
	store    registrystore.MemoryStore
	embedder registryembed.Embedder
	vector   registryvector.VectorStore
	interval time.Duration
	batch    int
}

// NewBackgroundIndexer creates a new indexer.
func NewBackgroundIndexer(store registrystore.MemoryStore, embedder registryembed.Embedder, vector registryvector.VectorStore, batchSize int) *BackgroundIndexer {
	return &BackgroundIndexer{
		store:    store,
		embedder: embedder,
		vector:   vector,
		interval: 30 * time.Second,
		batch:    batchSize,
	}
}

// Start begins the background indexing loop. Returns when ctx is cancelled.
func (b *BackgroundIndexer) Start(ctx context.Context) {
	if b.embedder == nil || b.vector == nil || !b.vector.IsEnabled() {
		log.Info("Background indexer disabled (no embedder or vector store)")
		return
	}

	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.indexBatch(ctx)
		}
	}
}

func (b *BackgroundIndexer) indexBatch(ctx context.Context) {
	entries, err := b.store.FindEntriesPendingVectorIndexing(ctx, b.batch)
	if err != nil {
		log.Error("Indexer: list unindexed entries failed", "err", err)
		return
	}

	// Filter to entries that have content to embed.
	type candidate struct {
		entry model.Entry
		text  string
	}
	var candidates []candidate
	for _, e := range entries {
		if e.IndexedContent != nil && *e.IndexedContent != "" {
			candidates = append(candidates, candidate{entry: e, text: *e.IndexedContent})
		}
	}
	if len(candidates) == 0 {
		return
	}

	// Batch embed all texts in one request.
	texts := make([]string, len(candidates))
	for i, c := range candidates {
		texts[i] = c.text
	}
	embeddings, err := b.embedder.EmbedTexts(ctx, texts)
	if err != nil {
		log.Error("Indexer: batch embed failed", "err", err)
		return
	}

	// Batch upsert all embeddings to the vector store.
	upserts := make([]registryvector.UpsertRequest, len(candidates))
	for i, c := range candidates {
		upserts[i] = registryvector.UpsertRequest{
			ConversationGroupID: c.entry.ConversationGroupID,
			ConversationID:      c.entry.ConversationID,
			EntryID:             c.entry.ID,
			Embedding:           embeddings[i],
			ModelName:           b.embedder.ModelName(),
		}
	}
	if err := b.vector.Upsert(ctx, upserts); err != nil {
		log.Error("Indexer: batch vector upsert failed", "err", err)
		return
	}

	// Mark each entry as indexed.
	now := time.Now()
	count := 0
	for _, c := range candidates {
		if err := b.store.SetIndexedAt(ctx, c.entry.ID, c.entry.ConversationGroupID, now); err != nil {
			log.Error("Indexer: set indexed_at failed", "entryId", c.entry.ID, "err", err)
			continue
		}
		count++
	}

	if count > 0 {
		log.Info("Indexer: indexed entries", "count", count)
	}
}
