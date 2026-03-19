//go:build !nosqlite

package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteMigratorCreatesCoreTables(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "missing", "parent", "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)

	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))

	db, _, err := SharedDB(ctx)
	require.NoError(t, err)

	for _, table := range []string{
		"conversation_groups",
		"conversations",
		"conversation_memberships",
		"entries",
		"conversation_ownership_transfers",
		"tasks",
		"attachments",
		"memories",
		"memory_usage_stats",
		"memory_vectors",
	} {
		var count int64
		err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE (type = 'table' OR type = 'view') AND name = ?", table).Scan(&count).Error
		require.NoError(t, err, table)
		require.Equal(t, int64(1), count, table)
	}
}

func TestSQLiteSearchReturnsEmptyWhenFTS5IsUnavailable(t *testing.T) {
	t.Parallel()

	store := &SQLiteStore{handle: &sharedHandle{fts5Enabled: false}}

	results, err := store.SearchEntries(context.Background(), "user-1", "hello world", nil, 10, false, false)
	require.NoError(t, err)
	require.Empty(t, results.Data)
	require.Nil(t, results.AfterCursor)

	adminResults, err := store.AdminSearchEntries(context.Background(), registrystore.AdminSearchQuery{
		Query: "hello world",
		Limit: 10,
	})
	require.NoError(t, err)
	require.Empty(t, adminResults.Data)
	require.Nil(t, adminResults.AfterCursor)
}

func TestSQLiteEpisodicVectorsNoOpWhenExtensionUnavailable(t *testing.T) {
	t.Parallel()

	store := &sqliteEpisodicStore{handle: &sharedHandle{vecEnabled: false}}

	require.NoError(t, store.UpsertMemoryVectors(context.Background(), []registryepisodic.MemoryVectorUpsert{{
		MemoryID:  uuid.New(),
		FieldName: "body",
		Namespace: "users\x1e123",
		Embedding: []float32{1, 0},
	}}))
	require.NoError(t, store.DeleteMemoryVectors(context.Background(), uuid.New()))

	results, err := store.SearchMemoryVectors(context.Background(), "users\x1e123", []float32{1, 0}, nil, 10)
	require.NoError(t, err)
	require.Empty(t, results)
}
