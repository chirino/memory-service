package service

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
)

func TestBackgroundIndexerReportsMetadataWriteCancellation(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	indexedContent := "content"
	store := &backgroundIndexerTestStore{
		entries: []model.Entry{{
			ID:                  uuid.New(),
			ConversationID:      "conversation-1",
			ConversationGroupID: uuid.New(),
			IndexedContent:      &indexedContent,
		}},
		setIndexedErr: context.Canceled,
	}
	indexer := NewBackgroundIndexer(store, backgroundIndexerTestEmbedder{}, &taskProcessorTestVector{}, 10)
	indexer.indexBatch(context.Background())

	text := output.String()
	if !strings.Contains(text, "job.entry_index") || !strings.Contains(text, "result=canceled") || !strings.Contains(text, "reason=shutdown") {
		t.Fatalf("canceled index run was not classified correctly:\n%s", text)
	}
	if strings.Contains(text, "Indexer: set indexed_at failed") || strings.Contains(text, "result=failed") {
		t.Fatalf("cancellation produced failure diagnostics:\n%s", text)
	}
}

type backgroundIndexerTestStore struct {
	registrystore.MemoryStore
	entries       []model.Entry
	setIndexedErr error
}

func (s *backgroundIndexerTestStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s *backgroundIndexerTestStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s *backgroundIndexerTestStore) FindEntriesPendingVectorIndexing(context.Context, int) ([]model.Entry, error) {
	return s.entries, nil
}

func (s *backgroundIndexerTestStore) SetIndexedAt(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return s.setIndexedErr
}

type backgroundIndexerTestEmbedder struct{}

func (backgroundIndexerTestEmbedder) EmbedTexts(context.Context, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}

func (backgroundIndexerTestEmbedder) ModelName() string { return "test" }
func (backgroundIndexerTestEmbedder) Dimension() int    { return 1 }
