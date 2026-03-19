//go:build !nosqlite

package sqlitevec

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	sqlitestore "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

//go:embed db/schema.sql
var schemaSQL string

type migrator struct{}

func (m *migrator) Name() string { return "sqlite-vector" }

func (m *migrator) Migrate(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	if cfg == nil || !cfg.VectorMigrateAtStart {
		return nil
	}
	if cfg.DatastoreType != "sqlite" || strings.TrimSpace(cfg.VectorType) != "sqlite" {
		return nil
	}
	_, sqlDB, err := sqlitestore.SharedDB(ctx)
	if err != nil {
		return fmt.Errorf("sqlite vector migrate: %w", err)
	}
	caps, err := sqlitestore.SharedCapabilities(ctx)
	if err != nil {
		return fmt.Errorf("sqlite vector migrate: %w", err)
	}
	if !caps.VecEnabled {
		log.Warn("Skipping sqlite vector migration because the sqlite vector extension is unavailable")
		return nil
	}
	log.Info("Running migration", "name", m.Name())
	if _, err := sqlDB.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("sqlite vector migrate: %w", err)
	}
	return nil
}

func init() {
	registryvector.Register(registryvector.Plugin{
		Name:   "sqlite",
		Loader: load,
	})
	registrymigrate.Register(registrymigrate.Plugin{Order: 200, Migrator: &migrator{}})
}

func load(ctx context.Context) (registryvector.VectorStore, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("sqlite vector: missing config")
	}
	db, _, err := sqlitestore.SharedDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite vector: %w", err)
	}
	caps, err := sqlitestore.SharedCapabilities(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite vector: %w", err)
	}
	if !caps.VecEnabled {
		log.Warn("SQLite vector store unavailable; semantic search disabled")
	}
	return &Store{db: db, enabled: caps.VecEnabled}, nil
}

type Store struct {
	db      *gorm.DB
	enabled bool
}

func (s *Store) IsEnabled() bool { return s != nil && s.enabled }
func (s *Store) Name() string    { return "sqlite" }

func (s *Store) Search(ctx context.Context, embedding []float32, conversationGroupIDs []uuid.UUID, limit int) ([]registryvector.VectorSearchResult, error) {
	if !s.IsEnabled() {
		return []registryvector.VectorSearchResult{}, nil
	}
	if len(conversationGroupIDs) == 0 || limit <= 0 {
		return []registryvector.VectorSearchResult{}, nil
	}
	queryVector, err := vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("serialize query vector: %w", err)
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(conversationGroupIDs)), ",")
	args := make([]any, 0, len(conversationGroupIDs)+3)
	args = append(args, queryVector)
	for _, id := range conversationGroupIDs {
		args = append(args, id.String())
	}
	args = append(args, queryVector)
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT
			entry_id,
			conversation_id,
			1.0 - vec_distance_cosine(embedding, ?) AS score
		FROM entry_embeddings
		WHERE conversation_group_id IN (%s)
		ORDER BY vec_distance_cosine(embedding, ?) ASC, entry_id ASC
		LIMIT ?`, placeholders)

	rows, err := s.db.WithContext(ctx).Raw(query, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]registryvector.VectorSearchResult, 0, limit)
	for rows.Next() {
		var (
			entryID        string
			conversationID string
			score          float64
		)
		if err := rows.Scan(&entryID, &conversationID, &score); err != nil {
			return nil, err
		}
		entryUUID, err := uuid.Parse(entryID)
		if err != nil {
			return nil, err
		}
		conversationUUID, err := uuid.Parse(conversationID)
		if err != nil {
			return nil, err
		}
		results = append(results, registryvector.VectorSearchResult{
			EntryID:        entryUUID,
			ConversationID: conversationUUID,
			Score:          score,
		})
	}
	return results, nil
}

func (s *Store) Upsert(ctx context.Context, entries []registryvector.UpsertRequest) error {
	if !s.IsEnabled() || len(entries) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, entry := range entries {
			vectorBlob, err := vec.SerializeFloat32(entry.Embedding)
			if err != nil {
				return fmt.Errorf("serialize entry vector: %w", err)
			}
			if err := tx.Exec(`
				INSERT INTO entry_embeddings (entry_id, conversation_id, conversation_group_id, embedding, model)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(entry_id) DO UPDATE SET
					conversation_id = excluded.conversation_id,
					conversation_group_id = excluded.conversation_group_id,
					embedding = excluded.embedding,
					model = excluded.model
			`, entry.EntryID.String(), entry.ConversationID.String(), entry.ConversationGroupID.String(), vectorBlob, entry.ModelName).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) DeleteByConversationGroupID(ctx context.Context, conversationGroupID uuid.UUID) error {
	if !s.IsEnabled() {
		return nil
	}
	return s.db.WithContext(ctx).
		Exec("DELETE FROM entry_embeddings WHERE conversation_group_id = ?", conversationGroupID.String()).
		Error
}
