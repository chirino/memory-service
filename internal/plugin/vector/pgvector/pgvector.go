package pgvector

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
	pgvec "github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

//go:embed db/pgvector-schema.sql
var pgvectorSchemaSQL string

// pgvectorMigrator implements migrate.Migrator for the pgvector schema.
type pgvectorMigrator struct{}

func (m *pgvectorMigrator) Name() string { return "pgvector" }
func (m *pgvectorMigrator) Migrate(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	if cfg == nil || !cfg.VectorMigrateAtStart || cfg.VectorType != "pgvector" || cfg.DBURL == "" || (cfg.DatastoreType != "" && cfg.DatastoreType != "postgres") {
		return nil
	}
	log.Info("Running migration", "name", m.Name())
	db, err := openDB(cfg.DBURL)
	if err != nil {
		return fmt.Errorf("pgvector migrate: %w", err)
	}
	return db.Exec(pgvectorSchemaSQL).Error
}

func init() {
	registryvector.Register(registryvector.Plugin{
		Name:   "pgvector",
		Loader: load,
	})
	registrymigrate.Register(registrymigrate.Plugin{Order: 200, Migrator: &pgvectorMigrator{}})
}

func load(ctx context.Context) (registryvector.VectorStore, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("pgvector: missing config in context")
	}
	db, err := openDB(cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("pgvector: %w", err)
	}
	return &PgvectorStore{db: db}, nil
}

func openDB(dbURL string) (*gorm.DB, error) {
	return openGormDB(dbURL)
}

// PgvectorStore implements VectorStore using pgvector extension.
type PgvectorStore struct {
	db *gorm.DB
}

func (s *PgvectorStore) IsEnabled() bool { return true }
func (s *PgvectorStore) Name() string    { return "pgvector" }

func (s *PgvectorStore) Search(ctx context.Context, embedding []float32, conversationGroupIDs []uuid.UUID, limit int) ([]registryvector.VectorSearchResult, error) {
	if len(conversationGroupIDs) == 0 {
		return nil, nil
	}

	vec := pgvec.NewVector(embedding)
	rows, err := s.db.WithContext(ctx).Raw(`
		SELECT entry_id, conversation_id, conversation_group_id,
		       1 - (embedding <=> ?::vector) AS score
		FROM entry_embeddings
		WHERE conversation_group_id = ANY(?)
		ORDER BY embedding <=> ?::vector
		LIMIT ?`,
		vec, conversationGroupIDs, vec, limit,
	).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []registryvector.VectorSearchResult
	for rows.Next() {
		var r registryvector.VectorSearchResult
		var groupID uuid.UUID
		if err := rows.Scan(&r.EntryID, &r.ConversationID, &groupID, &r.Score); err != nil {
			log.Error("pgvector scan error", "err", err)
			continue
		}
		results = append(results, r)
	}
	return results, nil
}

func (s *PgvectorStore) Upsert(ctx context.Context, entries []registryvector.UpsertRequest) error {
	for _, e := range entries {
		vec := pgvec.NewVector(e.Embedding)
		if err := s.db.WithContext(ctx).Exec(`
			INSERT INTO entry_embeddings (entry_id, conversation_id, conversation_group_id, embedding, model)
			VALUES (?, ?, ?, ?::vector, ?)
			ON CONFLICT (entry_id, conversation_group_id)
			DO UPDATE SET embedding = EXCLUDED.embedding, model = EXCLUDED.model`,
			e.EntryID, e.ConversationID, e.ConversationGroupID, vec, e.ModelName,
		).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *PgvectorStore) DeleteByConversationGroupID(ctx context.Context, conversationGroupID uuid.UUID) error {
	return s.db.WithContext(ctx).Exec(
		"DELETE FROM entry_embeddings WHERE conversation_group_id = ?",
		conversationGroupID,
	).Error
}
