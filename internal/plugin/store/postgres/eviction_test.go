//go:build !nopostgresql

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/testutil/evictiontest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type postgresEvictionClock struct{ store *PostgresStore }

func (c postgresEvictionClock) SetGroupActivity(t *testing.T, _ context.Context, groupID uuid.UUID, at time.Time) {
	require.NoError(t, c.store.db.Exec(`UPDATE conversation_groups SET created_at = ? WHERE id = ?`, at, groupID).Error)
	require.NoError(t, c.store.db.Exec(`UPDATE conversations SET created_at = ?, updated_at = ? WHERE conversation_group_id = ?`, at, at, groupID).Error)
	require.NoError(t, c.store.db.Exec(`UPDATE entries SET created_at = ? WHERE conversation_group_id = ?`, at, groupID).Error)
}

func (c postgresEvictionClock) SetGroupArchivedAt(t *testing.T, _ context.Context, groupID uuid.UUID, at time.Time) {
	require.NoError(t, c.store.db.Exec(`UPDATE conversation_groups SET archived_at = ? WHERE id = ?`, at, groupID).Error)
	require.NoError(t, c.store.db.Exec(`UPDATE conversations SET archived_at = ? WHERE conversation_group_id = ?`, at, groupID).Error)
}

func TestPostgresConversationEvictionFilters(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	evictiontest.Filters(t, ctx, store, postgresEvictionClock{store})
}

func TestPostgresConversationEvictionConcurrentActivity(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	evictiontest.ConcurrentActivity(t, ctx, store, postgresEvictionClock{store})
}
