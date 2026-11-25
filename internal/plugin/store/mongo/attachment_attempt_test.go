//go:build !nomongo

package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMongoAttachmentAttemptIDRoundTripsThroughStoreReads(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)

	id, attempt := uuid.New(), uuid.New()
	attemptStr := attempt.String()
	_, err := mongoStore.attachments().InsertOne(ctx, bson.M{
		"_id":          id.String(),
		"content_type": "text/plain",
		"user_id":      "user1",
		"status":       "uploading",
		"created_at":   time.Now(),
		"attempt_id":   attemptStr,
	})
	require.NoError(t, err)

	var attachment *model.Attachment
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		attachment, err = store.GetAttachment(txCtx, "user1", "", id)
		return err
	}))
	require.NotNil(t, attachment.AttemptID, "the stored attempt_id must map onto the model")
	require.Equal(t, attempt, *attachment.AttemptID)

	admin, err := store.AdminGetAttachment(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, admin.AttemptID)
	require.Equal(t, attempt, *admin.AttemptID)
}

func TestMongoAttachmentWithoutAttemptIDReadsNull(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)

	// A document written before the field existed has no attempt_id at all.
	legacyID := uuid.New()
	_, err := mongoStore.attachments().InsertOne(ctx, bson.M{
		"_id":          legacyID.String(),
		"content_type": "text/plain",
		"user_id":      "user1",
		"status":       "ready",
		"created_at":   time.Now(),
	})
	require.NoError(t, err)

	var legacy, created *model.Attachment
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		legacy, err = store.GetAttachment(txCtx, "user1", "", legacyID)
		if err != nil {
			return err
		}
		created, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{ContentType: "text/plain"})
		return err
	}))
	require.Nil(t, legacy.AttemptID)
	require.Nil(t, created.AttemptID, "a create without a caller-supplied ID must not get an attempt ID")

	// Both documents must match an equality filter on null, which is how the
	// conditional operations compare a legacy row's attempt.
	count, err := mongoStore.attachments().CountDocuments(ctx, bson.M{
		"_id":        bson.M{"$in": bson.A{legacyID.String(), created.ID.String()}},
		"attempt_id": nil,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
}

func TestMongoCreateAttachmentIgnoresCallerSuppliedAttemptID(t *testing.T) {
	store, mongoStore, ctx := setupTestMongoStore(t, false)

	forged := uuid.New()
	var created, read *model.Attachment
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		created, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{ContentType: "text/plain", AttemptID: &forged})
		if err != nil {
			return err
		}
		read, err = store.GetAttachment(txCtx, "user1", "", created.ID)
		return err
	}))
	require.Nil(t, created.AttemptID, "the returned attachment must not carry a caller-supplied attempt ID")
	require.Nil(t, read.AttemptID)

	count, err := mongoStore.attachments().CountDocuments(ctx, bson.M{"_id": created.ID.String(), "attempt_id": nil})
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "the stored document must have no attempt_id")
}
