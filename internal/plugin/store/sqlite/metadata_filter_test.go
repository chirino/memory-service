//go:build !nosqlite

package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteMetadataFilterLatestMatchingFork(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	var root *registrystore.ConversationDetail
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var createErr error
		root, createErr = store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000001", "Root", map[string]interface{}{"match": "yes"}, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)

	var entries []model.Entry
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var appendErr error
		entries, appendErr = store.AppendEntries(txCtx, "user1", root.ID, []registrystore.CreateEntryRequest{{
			Content: json.RawMessage(`"history entry"`), ContentType: "text/plain", Channel: "history",
		}}, nil, nil, nil)
		return appendErr
	})
	require.NoError(t, err)
	require.Len(t, entries, 1)

	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, createErr := store.CreateConversationWithID(txCtx, "user1", "", "ffffffff-ffff-4fff-bfff-ffffffffffff", "Fork", map[string]interface{}{"match": "no"}, nil, &root.ID, &entries[0].ID)
		return createErr
	})
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{{Key: "match", Operator: registrystore.ConversationMetadataEqual, Value: "yes"}}
	var public []registrystore.ConversationSummary
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		public, _, listErr = store.ListConversations(txCtx, "user1", nil, nil, 10, model.ListModeLatestFork, model.ConversationAncestryRoots, registrystore.ArchiveFilterExclude, filters)
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, root.ID, public[0].ID)

	var admin []registrystore.ConversationSummary
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		admin, _, listErr = store.AdminListConversations(txCtx, registrystore.AdminConversationQuery{
			Mode: model.ListModeLatestFork, Ancestry: model.ConversationAncestryRoots,
			Archived: registrystore.ArchiveFilterExclude, MetadataFilters: filters, Limit: 10,
		})
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, admin, 1)
	assert.Equal(t, root.ID, admin[0].ID)
}

func TestSQLiteMetadataFilterMatchesOnlyScalarStrings(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	var stringConv *registrystore.ConversationDetail
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var createErr error
		stringConv, createErr = store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000001", "String", map[string]interface{}{"kind": "1"}, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, createErr := store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000002", "Array", map[string]interface{}{"kind": []interface{}{"1"}}, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{{Key: "kind", Operator: registrystore.ConversationMetadataEqual, Value: "1"}}
	var public []registrystore.ConversationSummary
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		public, _, listErr = store.ListConversations(txCtx, "user1", nil, nil, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, stringConv.ID, public[0].ID)

	var admin []registrystore.ConversationSummary
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		admin, _, listErr = store.AdminListConversations(txCtx, registrystore.AdminConversationQuery{
			Mode: model.ListModeAll, Ancestry: model.ConversationAncestryAll,
			Archived: registrystore.ArchiveFilterExclude, MetadataFilters: filters, Limit: 10,
		})
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, admin, 1)
	assert.Equal(t, stringConv.ID, admin[0].ID)
}

func TestSQLiteMetadataFilterSameKeyDuplicateAND(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	var convRunning *registrystore.ConversationDetail
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var createErr error
		convRunning, createErr = store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000001", "Running", map[string]interface{}{"status": "running"}, nil, nil, nil)
		if createErr != nil {
			return createErr
		}
		_, createErr = store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000002", "Waiting", map[string]interface{}{"status": "waiting"}, nil, nil, nil)
		if createErr != nil {
			return createErr
		}
		_, createErr = store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000003", "Other", map[string]interface{}{"status": "other"}, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{
		{Key: "status", Operator: registrystore.ConversationMetadataEqual, Value: "running"},
		{Key: "status", Operator: registrystore.ConversationMetadataNotEqual, Value: "waiting"},
	}

	var public []registrystore.ConversationSummary
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		public, _, listErr = store.ListConversations(txCtx, "user1", nil, nil, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, convRunning.ID, public[0].ID)

	var admin []registrystore.ConversationSummary
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		admin, _, listErr = store.AdminListConversations(txCtx, registrystore.AdminConversationQuery{
			Mode: model.ListModeAll, Ancestry: model.ConversationAncestryAll,
			Archived: registrystore.ArchiveFilterExclude, MetadataFilters: filters, Limit: 10,
		})
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, admin, 1)
	assert.Equal(t, convRunning.ID, admin[0].ID)
}

func TestSQLiteMetadataFilterInvalidCursor(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	badCursor := "non-existent-cursor-id"
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		_, _, listErr := store.ListConversations(txCtx, "user1", nil, &badCursor, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, nil)
		return listErr
	})
	assert.Error(t, err)
	var badReq *registrystore.BadRequestError
	assert.ErrorAs(t, err, &badReq)

	// Admin unknown cursor returns BadRequestError
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		_, _, listErr := store.AdminListConversations(txCtx, registrystore.AdminConversationQuery{
			AfterCursor: &badCursor,
			Limit:       10,
		})
		return listErr
	})
	assert.Error(t, err)
	assert.ErrorAs(t, err, &badReq)

	// Public inaccessible cursor returns BadRequestError
	var convUser2 *registrystore.ConversationDetail
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var createErr error
		convUser2, createErr = store.CreateConversationWithID(txCtx, "user2", "", "00000000-0000-4000-8000-000000000099", "User2 Conv", nil, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)
	inaccessibleCursor := convUser2.ID
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		_, _, listErr := store.ListConversations(txCtx, "user1", nil, &inaccessibleCursor, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, nil)
		return listErr
	})
	assert.Error(t, err)
	assert.ErrorAs(t, err, &badReq)
}

func TestSQLiteMetadataFilterPaginationSameCreatedAt(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	sqliteStore := store.(*SQLiteStore)

	ids := []string{
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000003",
		"00000000-0000-4000-8000-000000000004",
	}
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		for _, id := range ids {
			_, createErr := store.CreateConversationWithID(txCtx, "user1", "", id, "Title "+id, map[string]interface{}{"status": "active"}, nil, nil, nil)
			if createErr != nil {
				return createErr
			}
		}
		// Force exact same created_at timestamp on all 4 conversations
		fixedTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		return sqliteStore.dbFor(txCtx).Table("conversations").Where("id IN ?", ids).Update("created_at", fixedTime).Error
	})
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{
		{Key: "status", Operator: registrystore.ConversationMetadataEqual, Value: "active"},
	}

	// Public pagination with page size 2: must return IDs sorted descending (0004, 0003 then 0002, 0001)
	var p1, p2 []registrystore.ConversationSummary
	var cursor1 *string
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		p1, cursor1, listErr = store.ListConversations(txCtx, "user1", nil, nil, 2, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, p1, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000004", p1[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000003", p1[1].ID)
	require.NotNil(t, cursor1)
	assert.Equal(t, "00000000-0000-4000-8000-000000000003", *cursor1)

	// Mutate cursor anchor metadata so it no longer matches list filter
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		return sqliteStore.dbFor(txCtx).Table("conversations").Where("id = ?", *cursor1).Update("metadata", `{"status":"mutated_inactive"}`).Error
	})
	require.NoError(t, err)

	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		p2, _, listErr = store.ListConversations(txCtx, "user1", nil, cursor1, 2, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, p2, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000002", p2[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000001", p2[1].ID)

	// Restore anchor metadata for admin test
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		return sqliteStore.dbFor(txCtx).Table("conversations").Where("id = ?", *cursor1).Update("metadata", `{"status":"active"}`).Error
	})
	require.NoError(t, err)

	// Admin pagination
	var adminP1, adminP2 []registrystore.ConversationSummary
	var adminCur1 *string
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		adminP1, adminCur1, listErr = store.AdminListConversations(txCtx, registrystore.AdminConversationQuery{
			MetadataFilters: filters,
			Limit:           2,
			Mode:            model.ListModeAll,
			Ancestry:        model.ConversationAncestryAll,
		})
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, adminP1, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000004", adminP1[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000003", adminP1[1].ID)
	require.NotNil(t, adminCur1)

	// Mutate anchor metadata for admin cursor test
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		return sqliteStore.dbFor(txCtx).Table("conversations").Where("id = ?", *adminCur1).Update("metadata", `{"status":"mutated_inactive"}`).Error
	})
	require.NoError(t, err)

	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		adminP2, _, listErr = store.AdminListConversations(txCtx, registrystore.AdminConversationQuery{
			MetadataFilters: filters,
			AfterCursor:     adminCur1,
			Limit:           2,
			Mode:            model.ListModeAll,
			Ancestry:        model.ConversationAncestryAll,
		})
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, adminP2, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000002", adminP2[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000001", adminP2[1].ID)
}

func TestSQLiteMetadataFilterNotEqual(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	var conv1 *registrystore.ConversationDetail
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var createErr error
		conv1, createErr = store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000001", "Conv1", map[string]interface{}{"status": "running"}, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, createErr := store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000002", "Conv2", map[string]interface{}{"status": "waiting"}, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, createErr := store.CreateConversationWithID(txCtx, "user1", "", "00000000-0000-4000-8000-000000000003", "Conv3", map[string]interface{}{"other": "waiting"}, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{{Key: "status", Operator: registrystore.ConversationMetadataNotEqual, Value: "waiting"}}
	var public []registrystore.ConversationSummary
	err = store.InReadTx(ctx, func(txCtx context.Context) error {
		var listErr error
		public, _, listErr = store.ListConversations(txCtx, "user1", nil, nil, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
		return listErr
	})
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, conv1.ID, public[0].ID)
}
