//go:build !nosqlite && windows

package sqlitevec

import (
	"context"

	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
)

func init() {
	registryvector.Register(registryvector.Plugin{
		Name:   "sqlite",
		Loader: load,
	})
}

func load(context.Context) (registryvector.VectorStore, error) {
	return &Store{}, nil
}

type Store struct{}

func (s *Store) IsEnabled() bool { return false }
func (s *Store) Name() string    { return "sqlite" }

func (s *Store) Search(context.Context, []float32, []uuid.UUID, int) ([]registryvector.VectorSearchResult, error) {
	return []registryvector.VectorSearchResult{}, nil
}

func (s *Store) Upsert(context.Context, []registryvector.UpsertRequest) error {
	return nil
}

func (s *Store) DeleteByConversationGroupID(context.Context, uuid.UUID) error {
	return nil
}
