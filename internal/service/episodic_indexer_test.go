package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/chirino/memory-service/internal/txscope"
	"github.com/google/uuid"
)

type fakeEpisodicStore struct {
	registryepisodic.EpisodicStore

	pending    []registryepisodic.PendingMemory
	pendingErr error

	upserts          [][]registryepisodic.MemoryVectorUpsert
	deletedVectorIDs []uuid.UUID
	indexedAtByID    map[uuid.UUID]time.Time
}

func (f *fakeEpisodicStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(txscope.WithIntent(ctx, txscope.IntentRead))
}

func (f *fakeEpisodicStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(txscope.WithIntent(ctx, txscope.IntentWrite))
}

func (f *fakeEpisodicStore) FindMemoriesPendingIndexing(_ context.Context, _ int) ([]registryepisodic.PendingMemory, error) {
	if f.pendingErr != nil {
		return nil, f.pendingErr
	}
	return f.pending, nil
}

func (f *fakeEpisodicStore) UpsertMemoryVectors(_ context.Context, items []registryepisodic.MemoryVectorUpsert) error {
	cp := make([]registryepisodic.MemoryVectorUpsert, len(items))
	copy(cp, items)
	f.upserts = append(f.upserts, cp)
	return nil
}

func (f *fakeEpisodicStore) DeleteMemoryVectors(_ context.Context, memoryID uuid.UUID) error {
	f.deletedVectorIDs = append(f.deletedVectorIDs, memoryID)
	return nil
}

func (f *fakeEpisodicStore) SetMemoryIndexedAt(_ context.Context, memoryID uuid.UUID, indexedAt time.Time) error {
	if f.indexedAtByID == nil {
		f.indexedAtByID = make(map[uuid.UUID]time.Time)
	}
	f.indexedAtByID[memoryID] = indexedAt
	return nil
}

type fakeEmbedder struct {
	registryembed.Embedder

	embeddings [][]float32
	calls      [][]string
}

func (f *fakeEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	cp := make([]string, len(texts))
	copy(cp, texts)
	f.calls = append(f.calls, cp)
	return f.embeddings, nil
}

func TestEpisodicIndexer_EmbedsIndexedContentOnly(t *testing.T) {
	memoryID := uuid.New()
	store := &fakeEpisodicStore{
		pending: []registryepisodic.PendingMemory{
			{
				ID:               memoryID,
				Namespace:        "user\u001falice\u001fnotes",
				PolicyAttributes: map[string]interface{}{"namespace": "user", "sub": "alice"},
				IndexedContent: map[string]string{
					"title":   "safe title",
					"summary": "safe summary",
					"blank":   "",
				},
			},
		},
	}
	embedder := &fakeEmbedder{
		embeddings: [][]float32{
			{1, 2},
			{3, 4},
		},
	}
	indexer := NewEpisodicIndexer(store, embedder, time.Second, 10)

	stats, err := indexer.Trigger(context.Background())
	if err != nil {
		t.Fatalf("Trigger returned unexpected error: %v", err)
	}
	if stats.Pending != 1 || stats.Processed != 1 || stats.Embedded != 2 || stats.VectorUpserts != 2 || stats.SkippedNoEmbedding != 0 || stats.Failures != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	if len(embedder.calls) != 1 {
		t.Fatalf("expected one embed call, got %d", len(embedder.calls))
	}
	if !reflect.DeepEqual(embedder.calls[0], []string{"safe summary", "safe title"}) {
		t.Fatalf("unexpected embed payload: %#v", embedder.calls[0])
	}

	if len(store.upserts) != 1 {
		t.Fatalf("expected one upsert batch, got %d", len(store.upserts))
	}
	if len(store.upserts[0]) != 2 {
		t.Fatalf("expected two vector upserts, got %d", len(store.upserts[0]))
	}
	if store.upserts[0][0].MemoryID != memoryID || store.upserts[0][0].FieldName != "summary" || !reflect.DeepEqual(store.upserts[0][0].Embedding, []float32{1, 2}) {
		t.Fatalf("unexpected first upsert: %#v", store.upserts[0][0])
	}
	if store.upserts[0][1].MemoryID != memoryID || store.upserts[0][1].FieldName != "title" || !reflect.DeepEqual(store.upserts[0][1].Embedding, []float32{3, 4}) {
		t.Fatalf("unexpected second upsert: %#v", store.upserts[0][1])
	}

	if _, ok := store.indexedAtByID[memoryID]; !ok {
		t.Fatalf("memory %s was not marked indexed", memoryID)
	}
}

func TestEpisodicIndexer_SkipsWhenIndexedContentMissing(t *testing.T) {
	memoryID := uuid.New()
	store := &fakeEpisodicStore{
		pending: []registryepisodic.PendingMemory{
			{
				ID:             memoryID,
				Namespace:      "user\u001falice",
				IndexedContent: map[string]string{},
			},
		},
	}
	embedder := &fakeEmbedder{}
	indexer := NewEpisodicIndexer(store, embedder, time.Second, 10)

	stats, err := indexer.Trigger(context.Background())
	if err != nil {
		t.Fatalf("Trigger returned unexpected error: %v", err)
	}
	if stats.SkippedNoEmbedding != 1 || stats.VectorUpserts != 0 || stats.Embedded != 0 || stats.Failures != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if len(embedder.calls) != 0 {
		t.Fatalf("expected no embed calls, got %d", len(embedder.calls))
	}
	if _, ok := store.indexedAtByID[memoryID]; !ok {
		t.Fatalf("memory %s was not marked indexed", memoryID)
	}
}

func TestEpisodicIndexer_DeletesVectorsForDeletedMemory(t *testing.T) {
	memoryID := uuid.New()
	archivedAt := time.Now().Add(-time.Minute)
	store := &fakeEpisodicStore{
		pending: []registryepisodic.PendingMemory{
			{
				ID:             memoryID,
				ArchivedAt:     &archivedAt,
				Namespace:      "user\u001falice",
				IndexedContent: map[string]string{"title": "should not embed"},
			},
		},
	}
	embedder := &fakeEmbedder{}
	indexer := NewEpisodicIndexer(store, embedder, time.Second, 10)

	stats, err := indexer.Trigger(context.Background())
	if err != nil {
		t.Fatalf("Trigger returned unexpected error: %v", err)
	}
	if stats.VectorDeletes != 1 || stats.VectorUpserts != 0 || stats.SkippedNoEmbedding != 0 || stats.Failures != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if len(store.deletedVectorIDs) != 1 || store.deletedVectorIDs[0] != memoryID {
		t.Fatalf("unexpected deleted vector IDs: %#v", store.deletedVectorIDs)
	}
	if len(embedder.calls) != 0 {
		t.Fatalf("expected no embed calls for deleted memory, got %d", len(embedder.calls))
	}
	if _, ok := store.indexedAtByID[memoryID]; !ok {
		t.Fatalf("memory %s was not marked indexed", memoryID)
	}
}
