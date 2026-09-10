//go:build !nopostgresql

package postgres_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresMetadataFilterLatestMatchingFork(t *testing.T) {
	store, ctx := setupTestStore(t)
	root, err := store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000001", "Root", map[string]interface{}{"match": "yes"}, nil, nil, nil)
	require.NoError(t, err)
	entries, err := store.AppendEntries(ctx, "user1", root.ID, []registrystore.CreateEntryRequest{{
		Content: json.RawMessage(`"history entry"`), ContentType: "text/plain", Channel: "history",
	}}, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "ffffffff-ffff-4fff-bfff-ffffffffffff", "Fork", map[string]interface{}{"match": "no"}, nil, &root.ID, &entries[0].ID)
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{{Key: "match", Operator: registrystore.ConversationMetadataEqual, Value: "yes"}}
	public, _, err := store.ListConversations(ctx, "user1", nil, nil, 10, model.ListModeLatestFork, model.ConversationAncestryRoots, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, root.ID, public[0].ID)

	admin, _, err := store.AdminListConversations(ctx, registrystore.AdminConversationQuery{
		Mode: model.ListModeLatestFork, Ancestry: model.ConversationAncestryRoots,
		Archived: registrystore.ArchiveFilterExclude, MetadataFilters: filters, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, admin, 1)
	assert.Equal(t, root.ID, admin[0].ID)
}

func TestPostgresMetadataFilterMatchesOnlyStrings(t *testing.T) {
	store, ctx := setupTestStore(t)
	stringConv, err := store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000001", "String", map[string]interface{}{"kind": "1"}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000002", "Numeric", map[string]interface{}{"kind": 1}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000003", "Boolean", map[string]interface{}{"kind": true}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000004", "Array", map[string]interface{}{"kind": []interface{}{"1"}}, nil, nil, nil)
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{{Key: "kind", Operator: registrystore.ConversationMetadataEqual, Value: "1"}}
	public, _, err := store.ListConversations(ctx, "user1", nil, nil, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, stringConv.ID, public[0].ID)

	admin, _, err := store.AdminListConversations(ctx, registrystore.AdminConversationQuery{
		Mode: model.ListModeAll, Ancestry: model.ConversationAncestryAll,
		Archived: registrystore.ArchiveFilterExclude, MetadataFilters: filters, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, admin, 1)
	assert.Equal(t, stringConv.ID, admin[0].ID)

	filters = []registrystore.ConversationMetadataPredicate{{Key: "kind", Operator: registrystore.ConversationMetadataEqual, Value: "true"}}
	public, _, err = store.ListConversations(ctx, "user1", nil, nil, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	assert.Empty(t, public)
}

func TestPostgresMetadataFilterNotEqual(t *testing.T) {
	store, ctx := setupTestStore(t)
	conv1, err := store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000001", "Conv1", map[string]interface{}{"status": "running"}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000002", "Conv2", map[string]interface{}{"status": "waiting"}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000003", "Conv3", map[string]interface{}{"other": "waiting"}, nil, nil, nil)
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{{Key: "status", Operator: registrystore.ConversationMetadataNotEqual, Value: "waiting"}}
	public, _, err := store.ListConversations(ctx, "user1", nil, nil, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, conv1.ID, public[0].ID)
}

func TestPostgresMetadataFilterInvalidCursor(t *testing.T) {
	store, ctx := setupTestStore(t)
	badCursor := "non-existent-cursor-id"
	_, _, err := store.ListConversations(ctx, "user1", nil, &badCursor, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, nil)
	assert.Error(t, err)
	var badReq *registrystore.BadRequestError
	assert.ErrorAs(t, err, &badReq)

	// Admin unknown cursor returns BadRequestError
	_, _, err = store.AdminListConversations(ctx, registrystore.AdminConversationQuery{
		AfterCursor: &badCursor,
		Limit:       10,
	})
	assert.Error(t, err)
	assert.ErrorAs(t, err, &badReq)

	// Public inaccessible cursor (owned by user2, user1 queries) returns BadRequestError
	convUser2, err := store.CreateConversationWithID(ctx, "user2", "", "00000000-0000-4000-8000-000000000099", "User2 Conv", nil, nil, nil, nil)
	require.NoError(t, err)
	inaccessibleCursor := convUser2.ID
	_, _, err = store.ListConversations(ctx, "user1", nil, &inaccessibleCursor, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, nil)
	assert.Error(t, err)
	assert.ErrorAs(t, err, &badReq)
}

func TestPostgresMetadataFilterPaginationSameCreatedAt(t *testing.T) {
	store, ctx := setupTestStore(t)
	cfg := config.FromContext(ctx)
	rawDB, err := gorm.Open(postgres.Open(cfg.DBURL), &gorm.Config{})
	require.NoError(t, err)

	// Create 4 conversations with identical metadata status=active
	ids := []string{
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000003",
		"00000000-0000-4000-8000-000000000004",
	}
	for _, id := range ids {
		_, err := store.CreateConversationWithID(ctx, "user1", "", id, "Title "+id, map[string]interface{}{"status": "active"}, nil, nil, nil)
		require.NoError(t, err)
	}

	// Force exact same created_at timestamp on all 4 conversations
	fixedTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	err = rawDB.Table("conversations").Where("id IN ?", ids).Update("created_at", fixedTime).Error
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{
		{Key: "status", Operator: registrystore.ConversationMetadataEqual, Value: "active"},
	}

	// Public pagination with page size 2: must return IDs sorted descending (0004, 0003 then 0002, 0001)
	p1, cursor1, err := store.ListConversations(ctx, "user1", nil, nil, 2, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, p1, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000004", p1[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000003", p1[1].ID)
	require.NotNil(t, cursor1)
	assert.Equal(t, "00000000-0000-4000-8000-000000000003", *cursor1)

	// Mutate cursor anchor metadata so it no longer matches list filter
	err = rawDB.Table("conversations").Where("id = ?", *cursor1).Update("metadata", `{"status":"mutated_inactive"}`).Error
	require.NoError(t, err)

	p2, _, err := store.ListConversations(ctx, "user1", nil, cursor1, 2, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, p2, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000002", p2[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000001", p2[1].ID)

	// Restore anchor metadata for admin test
	err = rawDB.Table("conversations").Where("id = ?", *cursor1).Update("metadata", `{"status":"active"}`).Error
	require.NoError(t, err)

	// Admin pagination with page size 2
	adminP1, adminCur1, err := store.AdminListConversations(ctx, registrystore.AdminConversationQuery{
		MetadataFilters: filters,
		Limit:           2,
		Mode:            model.ListModeAll,
		Ancestry:        model.ConversationAncestryAll,
	})
	require.NoError(t, err)
	require.Len(t, adminP1, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000004", adminP1[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000003", adminP1[1].ID)
	require.NotNil(t, adminCur1)

	// Mutate anchor metadata for admin cursor test
	err = rawDB.Table("conversations").Where("id = ?", *adminCur1).Update("metadata", `{"status":"mutated_inactive"}`).Error
	require.NoError(t, err)

	adminP2, _, err := store.AdminListConversations(ctx, registrystore.AdminConversationQuery{
		MetadataFilters: filters,
		AfterCursor:     adminCur1,
		Limit:           2,
		Mode:            model.ListModeAll,
		Ancestry:        model.ConversationAncestryAll,
	})
	require.NoError(t, err)
	require.Len(t, adminP2, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000002", adminP2[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000001", adminP2[1].ID)
}

func TestPostgresMetadataFilterSameKeyDuplicateAND(t *testing.T) {
	store, ctx := setupTestStore(t)
	convRunning, err := store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000001", "Running", map[string]interface{}{"status": "running"}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000002", "Waiting", map[string]interface{}{"status": "waiting"}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000003", "Other", map[string]interface{}{"status": "other"}, nil, nil, nil)
	require.NoError(t, err)

	filters := []registrystore.ConversationMetadataPredicate{
		{Key: "status", Operator: registrystore.ConversationMetadataEqual, Value: "running"},
		{Key: "status", Operator: registrystore.ConversationMetadataNotEqual, Value: "waiting"},
	}

	public, _, err := store.ListConversations(ctx, "user1", nil, nil, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, convRunning.ID, public[0].ID)

	admin, _, err := store.AdminListConversations(ctx, registrystore.AdminConversationQuery{
		Mode: model.ListModeAll, Ancestry: model.ConversationAncestryAll,
		Archived: registrystore.ArchiveFilterExclude, MetadataFilters: filters, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, admin, 1)
	assert.Equal(t, convRunning.ID, admin[0].ID)
}
