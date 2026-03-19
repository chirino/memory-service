//go:build !nosqlite && sqlite_fts5

package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSQLiteMigratorCreatesCoreTables(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "memory.db"),
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
		"entries_fts",
	} {
		var count int64
		err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE (type = 'table' OR type = 'view') AND name = ?", table).Scan(&count).Error
		require.NoError(t, err, table)
		require.Equal(t, int64(1), count, table)
	}
}
