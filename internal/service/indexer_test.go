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

func TestBackgroundIndexerRecoversPanicWithOperationContext(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	indexedContent := "content"
	store := &backgroundIndexerTestStore{entries: []model.Entry{{
		ID:                  uuid.New(),
		ConversationID:      "conversation-1",
		ConversationGroupID: uuid.New(),
		IndexedContent:      &indexedContent,
	}}}
	indexer := NewBackgroundIndexer(store, backgroundIndexerTestEmbedder{panicValue: "private panic"}, &taskProcessorTestVector{}, 10)
	indexer.indexBatch(context.Background())

	text := output.String()
	for _, expected := range []string{
		"operation panic",
		"operation=job.entry_index",
		"indexer_test.go",
		"job.entry_index",
		"phase=complete",
		"result=failed",
		"reason=panic",
		"failureCount=1",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("panic recovery missing %q:\n%s", expected, text)
		}
	}
	if strings.Count(text, "phase=complete") != 1 {
		t.Fatalf("panic emitted more than one terminal event:\n%s", text)
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

type backgroundIndexerTestEmbedder struct {
	panicValue any
}

func (e backgroundIndexerTestEmbedder) EmbedTexts(context.Context, []string) ([][]float32, error) {
	if e.panicValue != nil {
		panic(e.panicValue)
	}
	return [][]float32{{1}}, nil
}

func (backgroundIndexerTestEmbedder) ModelName() string { return "test" }
func (backgroundIndexerTestEmbedder) Dimension() int    { return 1 }
