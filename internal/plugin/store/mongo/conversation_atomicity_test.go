//go:build !nomongo

package mongo

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/testbarrier"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func setupTestMongoStore(t *testing.T, outboxEnabled bool) (registrystore.MemoryStore, *MongoStore, context.Context) {
	t.Helper()
	dbURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.OutboxEnabled = outboxEnabled
	ctx := config.WithContext(context.Background(), &cfg)

	err := registrymigrate.RunAll(ctx)
	require.NoError(t, err)

	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	mongoStore, ok := store.(*MongoStore)
	require.True(t, ok)

	return store, mongoStore, ctx
}

func TestMongoConversationAtomicityFaultBeforeConversationInsert_Root(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)

	initialGroups, err := mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)

	convID := "root-fault-conv-" + uuid.NewString()
	faultErr := errors.New("injected fault before conversation insert")

	mongoStore.beforeConversationInsert = func() error {
		return faultErr
	}

	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := mongoStore.CreateConversationWithID(txCtx, "user1", "client1", convID, "Test Root", nil, nil, nil, nil)
		return err
	})
	require.ErrorIs(t, err, faultErr)

	// Verify rollback: no group, no ancestry, no conversation in DB
	convCount, err := mongoStore.conversations().CountDocuments(ctx, bson.M{"_id": convID})
	require.NoError(t, err)
	assert.Equal(t, int64(0), convCount, "conversation doc must be rolled back")

	ancestryCount, err := mongoStore.conversationAncestry().CountDocuments(ctx, bson.M{"_id": convID})
	require.NoError(t, err)
	assert.Equal(t, int64(0), ancestryCount, "ancestry doc must be rolled back")

	groupCount, err := mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, initialGroups, groupCount, "conversation group must be rolled back")

	// Retry without fault
	mongoStore.beforeConversationInsert = nil
	var result *registrystore.CreateConversationResult
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var createErr error
		result, createErr = mongoStore.CreateConversationWithID(txCtx, "user1", "client1", convID, "Test Root", nil, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, convID, result.Conversation.ID)

	convCount, err = mongoStore.conversations().CountDocuments(ctx, bson.M{"_id": convID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), convCount)

	ancestryCount, err = mongoStore.conversationAncestry().CountDocuments(ctx, bson.M{"_id": convID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), ancestryCount)

	groupCount, err = mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, initialGroups+1, groupCount, "conversation group must be created on retry")
}

func TestMongoConversationAtomicityFaultBeforeConversationInsert_StartedBy(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)

	// Create parent conversation first
	parentID := "parent-conv-" + uuid.NewString()
	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := mongoStore.CreateConversationWithID(txCtx, "user1", "client1", parentID, "Parent", nil, nil, nil, nil)
		return err
	})
	require.NoError(t, err)

	initialGroups, err := mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), initialGroups)

	startedByID := "started-by-fault-conv-" + uuid.NewString()
	faultErr := errors.New("injected fault before conversation insert")

	mongoStore.beforeConversationInsert = func() error {
		return faultErr
	}

	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := mongoStore.createConversation(txCtx, "user1", "client1", nil, startedByID, "Started By", nil, nil, nil, &parentID, nil)
		return err
	})
	require.ErrorIs(t, err, faultErr)

	// Verify rollback
	convCount, err := mongoStore.conversations().CountDocuments(ctx, bson.M{"_id": startedByID})
	require.NoError(t, err)
	assert.Equal(t, int64(0), convCount)

	ancestryCount, err := mongoStore.conversationAncestry().CountDocuments(ctx, bson.M{"_id": startedByID})
	require.NoError(t, err)
	assert.Equal(t, int64(0), ancestryCount)

	groupCount, err := mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, initialGroups, groupCount, "started-by conversation group must be rolled back")

	// Retry without fault
	mongoStore.beforeConversationInsert = nil
	var result *registrystore.CreateConversationResult
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var createErr error
		result, createErr = mongoStore.createConversation(txCtx, "user1", "client1", nil, startedByID, "Started By", nil, nil, nil, &parentID, nil)
		return createErr
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, startedByID, result.Conversation.ID)

	groupCount, err = mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, initialGroups+1, groupCount, "started-by conversation group must be created on retry")
}

func TestMongoConversationAtomicityFaultBeforeMembershipInsert(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)

	initialGroups, err := mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)

	convID := "membership-fault-conv-" + uuid.NewString()
	faultErr := errors.New("injected fault before membership insert")

	mongoStore.beforeMembershipInsert = func() error {
		return faultErr
	}

	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := mongoStore.CreateConversationWithID(txCtx, "user1", "client1", convID, "Test Membership Fault", nil, nil, nil, nil)
		return err
	})
	require.ErrorIs(t, err, faultErr)

	// Verify full rollback: conversation doc rolled back so no headless conversation exists
	convCount, err := mongoStore.conversations().CountDocuments(ctx, bson.M{"_id": convID})
	require.NoError(t, err)
	assert.Equal(t, int64(0), convCount)

	ancestryCount, err := mongoStore.conversationAncestry().CountDocuments(ctx, bson.M{"_id": convID})
	require.NoError(t, err)
	assert.Equal(t, int64(0), ancestryCount)

	groupCount, err := mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, initialGroups, groupCount, "conversation group must be rolled back")

	membershipCount, err := mongoStore.memberships().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, int64(0), membershipCount, "membership doc must not be created")

	// Retry without fault
	mongoStore.beforeMembershipInsert = nil
	var result *registrystore.CreateConversationResult
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		var createErr error
		result, createErr = mongoStore.CreateConversationWithID(txCtx, "user1", "client1", convID, "Test Membership Fault", nil, nil, nil, nil)
		return createErr
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, convID, result.Conversation.ID)

	// Verify membership exists
	detail, err := store.GetConversation(ctx, "user1", convID)
	require.NoError(t, err)
	assert.Equal(t, convID, detail.ID)
}

func TestMongoConversationConcurrentLiveCreator(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)

	initialGroups, err := mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)

	convID := "concurrent-live-conv-" + uuid.NewString()

	barrier := testbarrier.New(2)
	var once sync.Once
	mongoStore.beforeConversationInsert = func() error {
		once.Do(func() {
			barrier.Wait()
		})
		return nil
	}
	t.Cleanup(func() { mongoStore.beforeConversationInsert = nil })

	var wg sync.WaitGroup
	var res1, res2 *registrystore.CreateConversationResult
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		err1 = store.InWriteTx(ctx, func(txCtx context.Context) error {
			var err error
			res1, err = mongoStore.CreateConversationWithID(txCtx, "user1", "client1", convID, "Concurrent 1", nil, nil, nil, nil)
			return err
		})
	}()

	go func() {
		defer wg.Done()
		barrier.Wait()
		err2 = store.InWriteTx(ctx, func(txCtx context.Context) error {
			var err error
			res2, err = mongoStore.CreateConversationWithID(txCtx, "user1", "client1", convID, "Concurrent 1", nil, nil, nil, nil)
			return err
		})
	}()

	barrier.WaitForParties(t, 2)
	wg.Wait()

	require.NoError(t, err1)
	require.NoError(t, err2)

	require.NotNil(t, res1)
	require.NotNil(t, res2)

	// Exactly one must be new (not ExactRetry) and one must be ExactRetry (or both succeed cleanly)
	assert.Equal(t, convID, res1.Conversation.ID)
	assert.Equal(t, convID, res2.Conversation.ID)
	assert.True(t, res1.ExactRetry || res2.ExactRetry, "one concurrent creator should be exact retry")

	convCount, err := mongoStore.conversations().CountDocuments(ctx, bson.M{"_id": convID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), convCount)

	ancestryCount, err := mongoStore.conversationAncestry().CountDocuments(ctx, bson.M{"_id": convID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), ancestryCount)

	// Verify transaction rollback under write contention: the losing concurrent creator's
	// transaction is retried by WithTransaction and its first-attempt group insert is rolled
	// back, leaving exactly one group document.
	groupCount, err := mongoStore.groups().CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, initialGroups+1, groupCount, "losing concurrent creator's group must be rolled back by transaction retry")
}

func TestMongoStandaloneRejectedWithoutOutbox(t *testing.T) {
	dbURL := testmongo.StartStandaloneMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.OutboxEnabled = false
	ctx := config.WithContext(context.Background(), &cfg)

	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	_, err = loader(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MongoDB requires a replica set or mongos")
}
