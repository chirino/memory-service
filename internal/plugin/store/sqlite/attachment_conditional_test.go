//go:build !nosqlite

package sqlite

import (
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/testutil/attachmentstoretest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Real SQLite database (file backed), not a mock.
func TestSQLiteConditionalAttachmentOperations(t *testing.T) {
	store, ctx := newAttachmentTaskSQLiteStore(t)
	sqliteStore := store.(*SQLiteStore)
	attachmentstoretest.Run(t, store, ctx, func(t *testing.T, id uuid.UUID, archived bool) {
		t.Helper()
		if !archived {
			require.NoError(t, sqliteStore.db.WithContext(ctx).Exec(
				"UPDATE attachments SET archived_at = NULL, archived_at_ns = NULL WHERE id = ?", id).Error)
			return
		}
		now := time.Now().UTC()
		require.NoError(t, sqliteStore.db.WithContext(ctx).Exec(
			"UPDATE attachments SET archived_at = ?, archived_at_ns = ? WHERE id = ?", now.Unix(), now.Nanosecond(), id).Error)
	})
}
