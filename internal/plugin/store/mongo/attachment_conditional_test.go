//go:build !nomongo

package mongo

import (
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/testutil/attachmentstoretest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Real MongoDB replica set (Testcontainers), not a mock.
func TestMongoConditionalAttachmentOperations(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)
	attachmentstoretest.Run(t, store, ctx, func(t *testing.T, id uuid.UUID, archived bool) {
		t.Helper()
		update := bson.M{"$unset": bson.M{"archived_at": ""}}
		if archived {
			update = bson.M{"$set": bson.M{"archived_at": time.Now()}}
		}
		_, err := mongoStore.attachments().UpdateOne(ctx, bson.M{"_id": uuidToStr(id)}, update)
		require.NoError(t, err)
	})
}
