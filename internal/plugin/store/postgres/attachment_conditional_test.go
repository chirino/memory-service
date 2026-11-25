//go:build !nopostgresql

package postgres

import (
	"testing"

	"github.com/chirino/memory-service/internal/testutil/attachmentstoretest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Real PostgreSQL (Testcontainers), not a mock.
func TestPostgresConditionalAttachmentOperations(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	attachmentstoretest.Run(t, store, ctx, func(t *testing.T, id uuid.UUID, archived bool) {
		t.Helper()
		sql := "UPDATE attachments SET archived_at = NULL WHERE id = ?"
		if archived {
			sql = "UPDATE attachments SET archived_at = NOW() WHERE id = ?"
		}
		require.NoError(t, store.db.WithContext(ctx).Exec(sql, id).Error)
	})
}
