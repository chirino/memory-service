//go:build !nomongo

package mongo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestMongoMetadataFilterLatestMatchingFork(t *testing.T) {
	dbURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "mongo"
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

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
	assert.Equal(t, model.AccessLevelOwner, public[0].AccessLevel)

	admin, _, err := store.AdminListConversations(ctx, registrystore.AdminConversationQuery{
		Mode: model.ListModeLatestFork, Ancestry: model.ConversationAncestryRoots,
		Archived: registrystore.ArchiveFilterExclude, MetadataFilters: filters, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, admin, 1)
	assert.Equal(t, root.ID, admin[0].ID)
}

func TestMongoMetadataFilterMatchesOnlyScalarStrings(t *testing.T) {
	dbURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "mongo"
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	stringConv, err := store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000001", "String", map[string]interface{}{"kind": "1"}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000002", "Array", map[string]interface{}{"kind": []interface{}{"1"}}, nil, nil, nil)
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
}

func TestMongoMetadataFilterNotEqual(t *testing.T) {
	dbURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "mongo"
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

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

func TestMongoMetadataFilterInvalidCursor(t *testing.T) {
	dbURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "mongo"
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	badCursor := "non-existent-cursor-id"
	_, _, err = store.ListConversations(ctx, "user1", nil, &badCursor, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, nil)
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

	// Public inaccessible cursor returns BadRequestError
	convUser2, err := store.CreateConversationWithID(ctx, "user2", "", "00000000-0000-4000-8000-000000000099", "User2 Conv", nil, nil, nil, nil)
	require.NoError(t, err)
	inaccessibleCursor := convUser2.ID
	_, _, err = store.ListConversations(ctx, "user1", nil, &inaccessibleCursor, 10, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, nil)
	assert.Error(t, err)
	assert.ErrorAs(t, err, &badReq)
}

func TestMongoMetadataFilterPaginationSameCreatedAt(t *testing.T) {
	dbURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "mongo"
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	mStore := store.(*MongoStore)

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

	// Force exact same created_at timestamp on all 4 conversation documents
	fixedTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	_, err = mStore.conversations().UpdateMany(ctx, bson.M{"_id": bson.M{"$in": ids}}, bson.M{"$set": bson.M{"created_at": fixedTime}})
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

	// Mutate cursor anchor's metadata so it no longer matches the list filter
	_, err = mStore.conversations().UpdateByID(ctx, *cursor1, bson.M{"$set": bson.M{"metadata.status": "mutated_inactive"}})
	require.NoError(t, err)

	p2, _, err := store.ListConversations(ctx, "user1", nil, cursor1, 2, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, p2, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000002", p2[0].ID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000001", p2[1].ID)

	// Restore anchor metadata for admin test
	_, err = mStore.conversations().UpdateByID(ctx, *cursor1, bson.M{"$set": bson.M{"metadata.status": "active"}})
	require.NoError(t, err)

	// Admin pagination
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

	// Mutate anchor metadata for admin cursor test as well
	_, err = mStore.conversations().UpdateByID(ctx, *adminCur1, bson.M{"$set": bson.M{"metadata.status": "mutated_inactive"}})
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

func TestMongoMetadataFilterDuplicateKeysAndLimit(t *testing.T) {
	dbURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "mongo"
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	// Create 10 conversations matching status=waiting and env!=prod
	for i := 1; i <= 10; i++ {
		id := "00000000-0000-4000-8000-0000000000" + string([]byte{byte('0' + i/10), byte('0' + i%10)})
		_, err := store.CreateConversationWithID(ctx, "user1", "", id, "Title", map[string]interface{}{
			"status": "waiting",
			"env":    "dev",
		}, nil, nil, nil)
		require.NoError(t, err)
	}

	filters := []registrystore.ConversationMetadataPredicate{
		{Key: "status", Operator: registrystore.ConversationMetadataEqual, Value: "waiting"},
		{Key: "env", Operator: registrystore.ConversationMetadataNotEqual, Value: "prod"},
	}

	// Request page size of 3
	public, cursor, err := store.ListConversations(ctx, "user1", nil, nil, 3, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, public, 3)
	require.NotNil(t, cursor)

	// Fetch next page using cursor
	page2, cursor2, err := store.ListConversations(ctx, "user1", nil, cursor, 3, model.ListModeAll, model.ConversationAncestryAll, registrystore.ArchiveFilterExclude, filters)
	require.NoError(t, err)
	require.Len(t, page2, 3)
	require.NotNil(t, cursor2)
}

func TestMongoMetadataFilterSameKeyDuplicateAND(t *testing.T) {
	dbURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "mongo"
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	convRunning, err := store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000001", "Running", map[string]interface{}{"status": "running"}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000002", "Waiting", map[string]interface{}{"status": "waiting"}, nil, nil, nil)
	require.NoError(t, err)
	_, err = store.CreateConversationWithID(ctx, "user1", "", "00000000-0000-4000-8000-000000000003", "Other", map[string]interface{}{"status": "other"}, nil, nil, nil)
	require.NoError(t, err)

	// Predicates: status=running AND status!=waiting. Must return ONLY convRunning.
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

func TestMongoMetadataFilterPipelineStructure(t *testing.T) {
	filters := []registrystore.ConversationMetadataPredicate{
		{Key: "status", Operator: registrystore.ConversationMetadataEqual, Value: "running"},
		{Key: "status", Operator: registrystore.ConversationMetadataNotEqual, Value: "waiting"},
	}

	metaMatch := buildMongoMetadataFilterMatch(filters)
	require.NotNil(t, metaMatch)
	andList, ok := metaMatch["$and"].(bson.A)
	require.True(t, ok)
	require.Len(t, andList, 2)

	// Public pipeline test
	limit := 10
	publicPipeline := buildPublicConversationListPipeline("user1", nil, nil, limit, model.ListModeLatestFork, model.ConversationAncestryRoots, registrystore.ArchiveFilterExclude, filters)
	optsBuilder := buildConversationAggregateOptions(filters)

	// Verify allowDiskUse is enabled and simple collation is set when filters are present
	require.NotNil(t, optsBuilder)
	actualOpts := &options.AggregateOptions{}
	for _, fn := range optsBuilder.List() {
		require.NoError(t, fn(actualOpts))
	}
	require.NotNil(t, actualOpts.AllowDiskUse)
	assert.True(t, *actualOpts.AllowDiskUse)
	require.NotNil(t, actualOpts.Collation)
	assert.Equal(t, "simple", actualOpts.Collation.Locale)

	// The public pipeline must start from the authenticated user's memberships.
	// Starting from conversations makes each request examine unrelated tenants'
	// conversations before authorization can discard them.
	require.NotEmpty(t, publicPipeline)
	firstStage := publicPipeline[0]
	require.Len(t, firstStage, 1)
	assert.Equal(t, "$match", firstStage[0].Key)
	assert.Equal(t, bson.M{"user_id": "user1"}, firstStage[0].Value)

	// Check stage ordering, exact sort keys, and presence in public pipeline.
	var lookupIdx, groupIdx, sortIdx, limitIdx int = -1, -1, -1, -1
	var finalSortDoc bson.D
	for i, stage := range publicPipeline {
		for _, elem := range stage {
			switch elem.Key {
			case "$lookup":
				lookup, ok := elem.Value.(bson.M)
				if ok && lookup["from"] == "conversations" && lookupIdx == -1 {
					lookupIdx = i
					lookupPipeline, ok := lookup["pipeline"].(mongodriver.Pipeline)
					require.True(t, ok)
					require.Len(t, lookupPipeline, 1)
					require.Len(t, lookupPipeline[0], 1)
					assert.Equal(t, "$match", lookupPipeline[0][0].Key)
					conversationMatch, ok := lookupPipeline[0][0].Value.(bson.M)
					require.True(t, ok)
					andClauses, ok := conversationMatch["$and"].(bson.A)
					require.True(t, ok)
					assert.Len(t, andClauses, 2)
				}
			case "$group":
				if groupIdx == -1 {
					groupIdx = i
				}
			case "$sort":
				sortIdx = i
				if d, ok := elem.Value.(bson.D); ok {
					finalSortDoc = d
				}
			case "$limit":
				limitIdx = i
				assert.Equal(t, int64(limit+1), elem.Value)
			}
		}
	}

	assert.NotEqual(t, -1, lookupIdx, "conversation $lookup should be present for public list")
	assert.NotEqual(t, -1, groupIdx, "latest-fork $group should be present")
	assert.NotEqual(t, -1, sortIdx, "final $sort should be present")
	assert.NotEqual(t, -1, limitIdx, "final $limit should be present")

	// Verify exact sort structure: created_at: -1, _id: -1
	expectedSort := bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}
	assert.Equal(t, expectedSort, finalSortDoc)

	// Membership restriction precedes the conversation join and representative selection.
	assert.True(t, lookupIdx < groupIdx, "conversation lookup must precede group")
	// final stable sort precedes final $limit
	assert.True(t, sortIdx < limitIdx, "sort must precede limit")

	// The migration supplies an index whose prefix supports the initial user match.
	foundUserMembershipIndex := false
	for _, index := range conversationMembershipIndexes() {
		if assert.ObjectsAreEqual(index.Keys, bson.D{{Key: "user_id", Value: 1}, {Key: "conversation_group_id", Value: 1}}) {
			foundUserMembershipIndex = true
		}
	}
	assert.True(t, foundUserMembershipIndex, "membership indexes must support lookup by user_id")

	// Admin pipeline test (should not contain membership lookup)
	adminQuery := registrystore.AdminConversationQuery{
		Limit:           limit,
		Mode:            model.ListModeLatestFork,
		Ancestry:        model.ConversationAncestryRoots,
		Archived:        registrystore.ArchiveFilterExclude,
		MetadataFilters: filters,
	}
	adminPipeline := buildAdminConversationListPipeline(adminQuery, nil, nil)
	hasMembershipLookup := false
	for _, stage := range adminPipeline {
		for _, elem := range stage {
			if elem.Key == "$lookup" {
				if m, ok := elem.Value.(bson.M); ok && m["from"] == "conversation_memberships" {
					hasMembershipLookup = true
				}
			}
		}
	}
	assert.False(t, hasMembershipLookup, "admin pipeline should not contain membership lookup")
}
