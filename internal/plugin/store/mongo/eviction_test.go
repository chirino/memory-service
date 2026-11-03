//go:build !nomongo

package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/testutil/evictiontest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type mongoEvictionClock struct{ store *MongoStore }

func (c mongoEvictionClock) SetGroupActivity(t *testing.T, ctx context.Context, groupID uuid.UUID, at time.Time) {
	id := uuidToStr(groupID)
	inGroup := bson.M{"conversation_group_id": id}
	_, err := c.store.groups().UpdateByID(ctx, id, bson.M{"$set": bson.M{"created_at": at}})
	require.NoError(t, err)
	_, err = c.store.conversations().UpdateMany(ctx, inGroup, bson.M{"$set": bson.M{"created_at": at, "updated_at": at}})
	require.NoError(t, err)
	_, err = c.store.entries().UpdateMany(ctx, inGroup, bson.M{"$set": bson.M{"created_at": at}})
	require.NoError(t, err)
}

func (c mongoEvictionClock) SetGroupArchivedAt(t *testing.T, ctx context.Context, groupID uuid.UUID, at time.Time) {
	id := uuidToStr(groupID)
	_, err := c.store.groups().UpdateByID(ctx, id, bson.M{"$set": bson.M{"archived_at": at}})
	require.NoError(t, err)
	_, err = c.store.conversations().UpdateMany(ctx, bson.M{"conversation_group_id": id}, bson.M{"$set": bson.M{"archived_at": at}})
	require.NoError(t, err)
}

func TestMongoConversationEvictionFilters(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)
	evictiontest.Filters(t, ctx, store, mongoEvictionClock{mongoStore})
}

func TestMongoConversationEvictionConcurrentActivity(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)
	evictiontest.ConcurrentActivity(t, ctx, store, mongoEvictionClock{mongoStore})
}
