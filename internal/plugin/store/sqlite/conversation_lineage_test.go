//go:build !nosqlite

package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventstream"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteForkedChildLineageSurvivesReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lineage.db")
	cfg := &config.Config{
		DatastoreType:                 "sqlite",
		DBURL:                         dbPath,
		DatastoreMigrateAtStart:       true,
		EncryptionDBDisabled:          true,
		EncryptionAttachmentsDisabled: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	loadStore := func() registrystore.MemoryStore {
		require.NoError(t, registrymigrate.RunAll(ctx))
		loader, err := registrystore.Select("sqlite")
		require.NoError(t, err)
		store, err := loader(ctx)
		require.NoError(t, err)
		return store
	}
	closeHandle := func() {
		sharedHandles.Lock()
		handle := sharedHandles.byDSN[dbPath]
		delete(sharedHandles.byDSN, dbPath)
		sharedHandles.Unlock()
		if handle != nil {
			require.NoError(t, handle.sqlDB.Close())
		}
	}
	t.Cleanup(closeHandle)

	store := loadStore()
	concrete := store.(*SQLiteStore)
	var parent *registrystore.ConversationDetail
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		result, err := store.CreateConversationWithID(txCtx, "user1", "client1", "parent", "Parent", nil, nil, nil, nil)
		if err != nil {
			return err
		}
		parent = result.Conversation
		return nil
	}))

	var parentEntries []model.Entry
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		parentEntries, err = store.AppendEntries(txCtx, "user1", parent.ID, []registrystore.CreateEntryRequest{{
			Channel: "history", ContentType: "history", Content: json.RawMessage(`{"role":"USER","text":"delegate"}`),
		}}, nil, nil, nil)
		return err
	}))
	require.Len(t, parentEntries, 1)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.CreateConversationWithID(txCtx, "user1", "client1", "parent-fork", "Parent fork", nil, nil, &parent.ID, &parentEntries[0].ID)
		return err
	}))

	var child *registrystore.ConversationDetail
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		result, err := concrete.createConversationWithID(txCtx, "user1", "client1", "child", "Child", nil, nil, nil, nil, &parent.ID, &parentEntries[0].ID)
		if err != nil {
			return err
		}
		child = result.Conversation
		return nil
	}))
	var childEntries []model.Entry
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		childEntries, err = store.AppendEntries(txCtx, "user1", child.ID, []registrystore.CreateEntryRequest{{
			Channel: "history", ContentType: "history", Content: json.RawMessage(`{"role":"USER","text":"child work"}`),
		}}, nil, nil, nil)
		return err
	}))
	require.Len(t, childEntries, 1)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.CreateConversationWithID(txCtx, "user1", "client1", "child-fork", "Child fork", nil, nil, &child.ID, &childEntries[0].ID)
		return err
	}))
	var storedFork model.Conversation
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		return concrete.dbFor(txCtx).Where("id = ?", "child-fork").First(&storedFork).Error
	}))
	require.Nil(t, storedFork.StartedByConversationID)
	require.Nil(t, storedFork.StartedByEntryID)

	closeHandle()
	store = loadStore()
	var fork *registrystore.ConversationDetail
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		fork, err = store.GetConversation(txCtx, "user1", "child-fork")
		return err
	}))
	require.Equal(t, &parent.ID, fork.StartedByConversationID)
	require.Equal(t, &parentEntries[0].ID, fork.StartedByEntryID)

	var children []registrystore.ConversationSummary
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		children, _, err = store.ListChildConversations(txCtx, "user1", parent.ID, nil, 20)
		return err
	}))
	require.Len(t, children, 1)
	require.Equal(t, child.ID, children[0].ID)

	assertArchived := func(conversationIDs []string, expected bool) {
		t.Helper()
		for _, conversationID := range conversationIDs {
			var detail *registrystore.ConversationDetail
			require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
				var err error
				detail, err = store.GetConversation(txCtx, "user1", conversationID)
				return err
			}))
			require.Equal(t, expected, detail.ArchivedAt != nil, conversationID)
		}
	}
	parentTree := []string{parent.ID, "parent-fork"}
	childTree := []string{child.ID, "child-fork"}

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.ArchiveConversation(txCtx, "user1", parent.ID)
	}))
	assertArchived(parentTree, true)
	assertArchived(childTree, false)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.UnarchiveConversation(txCtx, "user1", parent.ID)
	}))
	assertArchived(parentTree, false)
	assertArchived(childTree, false)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		result, err := store.ArchiveConversationIfNeeded(txCtx, "user1", parent.ID)
		if err != nil {
			return err
		}
		require.True(t, result.Changed)
		return nil
	}))
	assertArchived(parentTree, true)
	assertArchived(childTree, false)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		result, err := store.UnarchiveConversationIfNeeded(txCtx, "user1", parent.ID)
		if err != nil {
			return err
		}
		require.True(t, result.Changed)
		return nil
	}))
	assertArchived(parentTree, false)
	assertArchived(childTree, false)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.AdminSetConversationArchived(txCtx, parent.ID, true)
	}))
	assertArchived(parentTree, true)
	assertArchived(childTree, false)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.AdminSetConversationArchived(txCtx, parent.ID, false)
	}))
	assertArchived(parentTree, false)
	assertArchived(childTree, false)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		events, err := eventstream.DeleteConversationGroups(txCtx, store, []uuid.UUID{parent.ConversationGroupID})
		require.Len(t, events, 4)
		return err
	}))
	for _, conversationID := range append(parentTree, childTree...) {
		err := store.InReadTx(ctx, func(txCtx context.Context) error {
			_, err := store.GetConversation(txCtx, "user1", conversationID)
			return err
		})
		var notFound *registrystore.NotFoundError
		require.ErrorAs(t, err, &notFound, conversationID)
	}
}
