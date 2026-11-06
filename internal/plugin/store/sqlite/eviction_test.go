//go:build !nosqlite

package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/evictiontest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type sqliteEvictionClock struct{ store *SQLiteStore }

func (c sqliteEvictionClock) exec(t *testing.T, ctx context.Context, statements ...string) func(args ...any) {
	return func(args ...any) {
		require.NoError(t, c.store.InWriteTx(ctx, func(ctx context.Context) error {
			db := c.store.writeDBFor(ctx, "test eviction clock")
			for _, statement := range statements {
				if err := db.Exec(statement, args...).Error; err != nil {
					return err
				}
			}
			return nil
		}))
	}
}

func (c sqliteEvictionClock) SetGroupActivity(t *testing.T, ctx context.Context, groupID uuid.UUID, at time.Time) {
	c.exec(t, ctx,
		`UPDATE conversation_groups SET created_at = ?, created_at_ns = ? WHERE id = ?`,
		`UPDATE entries SET created_at = ?, created_at_ns = ? WHERE conversation_group_id = ?`,
	)(at, groupID)
	c.exec(t, ctx, `UPDATE conversations SET created_at = ?, created_at_ns = ?, updated_at = ?, updated_at_ns = ? WHERE conversation_group_id = ?`)(at, at, groupID)
}

func (c sqliteEvictionClock) SetGroupArchivedAt(t *testing.T, ctx context.Context, groupID uuid.UUID, at time.Time) {
	c.exec(t, ctx,
		`UPDATE conversation_groups SET archived_at = ?, archived_at_ns = ? WHERE id = ?`,
		`UPDATE conversations SET archived_at = ?, archived_at_ns = ? WHERE conversation_group_id = ?`,
	)(at, groupID)
}

func TestSQLiteConversationEvictionFilters(t *testing.T) {
	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "eviction.db"),
		DatastoreMigrateAtStart: true,
		EncryptionDBDisabled:    true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	sqliteStore, ok := store.(*SQLiteStore)
	require.True(t, ok)
	evictiontest.Filters(t, ctx, store, sqliteEvictionClock{sqliteStore})
}
