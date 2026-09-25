//go:build !nopostgresql

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventstream"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestDescendantDeletionAtomicityAndClosure(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	var parent, child, fork, grandchild, unrelated *registrystore.ConversationDetail
	require.NoError(t, store.InWriteTx(ctx, func(ctx context.Context) error {
		var err error
		parent, err = store.CreateConversation(ctx, "owner", "app", "parent", nil, nil, nil, nil)
		require.NoError(t, err)
		child, err = store.createConversationWithID(ctx, "owner", "app", "child", "child", nil, nil, nil, nil, &parent.ID, nil)
		require.NoError(t, err)
		_, err = store.ShareConversation(ctx, "owner", child.ID, "child-reader", model.AccessLevelReader)
		require.NoError(t, err)
		fork, err = store.CreateConversation(ctx, "owner", "app", "child fork", nil, nil, &child.ID, nil)
		require.NoError(t, err)
		grandchild, err = store.createConversationWithID(ctx, "owner", "app", "grandchild", "grandchild", nil, nil, nil, nil, &fork.ID, nil)
		require.NoError(t, err)
		unrelated, err = store.CreateConversation(ctx, "owner", "app", "unrelated", nil, nil, nil, nil)
		return err
	}))

	// Overlapping roots and duplicate inputs must not duplicate deletion events.
	roots := []uuid.UUID{parent.ConversationGroupID, grandchild.ConversationGroupID, parent.ConversationGroupID}
	abort := errors.New("abort deletion")
	require.ErrorIs(t, store.InWriteTx(ctx, func(ctx context.Context) error {
		events, err := eventstream.DeleteConversationGroups(ctx, store, roots)
		require.NoError(t, err)
		require.Len(t, events, 4)
		return abort
	}), abort)

	var count int64
	for table, expected := range map[string]int64{"conversations": 5, "conversation_groups": 4, "tasks": 0, "outbox_events": 0} {
		require.NoError(t, store.db.Table(table).Count(&count).Error)
		require.Equal(t, expected, count, table)
	}

	require.NoError(t, store.InWriteTx(ctx, func(ctx context.Context) error {
		events, err := eventstream.DeleteConversationGroups(ctx, store, roots)
		require.NoError(t, err)
		require.Len(t, events, 4)
		seen := make(map[string]bool)
		for _, event := range events {
			id := event.Data.(map[string]any)["conversation"].(string)
			require.False(t, seen[id])
			seen[id] = true
			if id == parent.ID {
				require.ElementsMatch(t, []string{"owner"}, event.UserIDs)
			} else {
				require.ElementsMatch(t, []string{"owner", "child-reader"}, event.UserIDs)
			}
		}
		return nil
	}))
	for table, expected := range map[string]int64{"conversations": 1, "conversation_groups": 1, "conversation_memberships": 1, "tasks": 3, "outbox_events": 4} {
		require.NoError(t, store.db.Table(table).Count(&count).Error)
		require.Equal(t, expected, count, table)
	}
	_, err := store.GetConversation(ctx, "owner", unrelated.ID)
	require.NoError(t, err)
}

func TestDescendantDeletionBlocksConcurrentChildAndFork(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	parent, err := store.CreateConversation(ctx, "owner", "app", "parent", nil, nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, store.InWriteTx(ctx, func(deleteCtx context.Context) error {
		_, err := store.LoadDeletedConversationGroups(deleteCtx, []uuid.UUID{parent.ConversationGroupID})
		require.NoError(t, err)
		for _, isFork := range []bool{false, true} {
			err := store.InWriteTx(ctx, func(createCtx context.Context) error {
				require.NoError(t, store.dbFor(createCtx).Exec("SET LOCAL lock_timeout = '100ms'").Error)
				if isFork {
					_, err := store.CreateConversation(createCtx, "owner", "app", "fork", nil, nil, &parent.ID, nil)
					return err
				}
				_, err := store.createConversationWithID(createCtx, "owner", "app", "late-child", "late child", nil, nil, nil, nil, &parent.ID, nil)
				return err
			})
			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			require.Equal(t, "55P03", pgErr.Code)
		}
		return nil
	}))
}

func TestNamedTaskRetryInsideTransaction(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	require.NoError(t, store.InWriteTx(ctx, func(ctx context.Context) error {
		body := map[string]any{"taskName": "same-task"}
		require.NoError(t, store.CreateTask(ctx, "test", body))
		require.NoError(t, store.CreateTask(ctx, "test", body))
		var count int64
		require.NoError(t, store.dbFor(ctx).Table("tasks").Count(&count).Error)
		require.EqualValues(t, 1, count)
		return nil
	}))
}
