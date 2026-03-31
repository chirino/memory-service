package service

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/knowledge"
	"github.com/chirino/memory-service/internal/model"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
)

// BackgroundIndexer polls for unindexed entries, generates embeddings, stores them,
// and triggers knowledge clustering for affected users.
type BackgroundIndexer struct {
	store     registrystore.MemoryStore
	embedder  registryembed.Embedder
	vector    registryvector.VectorStore
	clusterer *knowledge.Clusterer
	interval  time.Duration
	batch     int
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

// SetClusterer attaches a knowledge clusterer to the indexer.
// When set, clustering runs automatically after each indexing batch.
func (b *BackgroundIndexer) SetClusterer(c *knowledge.Clusterer) {
	b.clusterer = c
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
	var entries []model.Entry
	err := b.store.InReadTx(ctx, func(readCtx context.Context) error {
		var err error
		entries, err = b.store.FindEntriesPendingVectorIndexing(readCtx, b.batch)
		return err
	})
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

	// Mark each entry as indexed and collect affected conversation group IDs.
	now := time.Now()
	count := 0
	affectedGroups := map[uuid.UUID]bool{}
	for _, c := range candidates {
		if err := b.store.InWriteTx(ctx, func(writeCtx context.Context) error {
			return b.store.SetIndexedAt(writeCtx, c.entry.ID, c.entry.ConversationGroupID, now)
		}); err != nil {
			log.Error("Indexer: set indexed_at failed", "entryId", c.entry.ID, "err", err)
			continue
		}
		count++
		affectedGroups[c.entry.ConversationGroupID] = true
	}

	if count > 0 {
		log.Info("Indexer: indexed entries", "count", count)
	}

	// Trigger clustering for owners of affected conversations.
	if b.clusterer != nil && len(affectedGroups) > 0 {
		groupIDs := make([]uuid.UUID, 0, len(affectedGroups))
		for gid := range affectedGroups {
			groupIDs = append(groupIDs, gid)
		}
		b.clusterer.ClusterByConversationGroups(ctx, groupIDs)
	}
}
