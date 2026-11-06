//go:build !nomongo

package mongo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// setupMongoIdempotencyStore creates a fresh replica-set-backed store and returns the
// concrete *mongoEpisodicStore for white-box access alongside a background context.
func setupMongoIdempotencyStore(t *testing.T) (*mongoEpisodicStore, context.Context) {
	t.Helper()

	mongoURL := testmongo.StartMongo(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = mongoURL
	cfg.DatastoreType = "mongo"
	cfg.EncryptionDBDisabled = true

	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))

	loader, err := registryepisodic.Select("mongo")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	return store.(*mongoEpisodicStore), ctx
}

// baseMongoIdempotencyReq builds a minimal valid PutMemoryRequest with a caller-supplied RevisionID.
func baseMongoIdempotencyReq(ns []string, key string, id uuid.UUID) registryepisodic.PutMemoryRequest {
	return registryepisodic.PutMemoryRequest{
		Namespace:  ns,
		Key:        key,
		Value:      map[string]interface{}{"data": "hello"},
		Index:      map[string]string{"text": "hello"},
		TTLSeconds: 0,
		MemoryKind: "default/v1",
		RevisionID: &id,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// LookupMemoryByID — basic outcomes
// ──────────────────────────────────────────────────────────────────────────────

// TestMongoLookupMemoryByID_NotFound verifies (nil, nil) when no row exists.
func TestMongoLookupMemoryByID_NotFound(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	id := uuid.New()
	req := baseMongoIdempotencyReq([]string{"user", "alice", "notfound"}, "k1", id)

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	assert.Nil(t, result, "must return nil when no row exists")
}

// TestMongoLookupMemoryByID_ExactRetry verifies ExactRetry=true for a matching stored row.
func TestMongoLookupMemoryByID_ExactRetry(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "exactretry"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	var first *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		first, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, first)
	assert.False(t, first.ExactRetry)

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry)
	assert.Equal(t, id, result.ID)
	assert.Equal(t, first.Revision, result.Revision)
	assert.Equal(t,
		first.CreatedAt.UTC().Truncate(time.Millisecond),
		result.CreatedAt.UTC().Truncate(time.Millisecond),
		"CreatedAt must be stable across retries")
}

// TestMongoLookupMemoryByID_NamespaceMismatch verifies MemoryRevisionIDConflictError
// when the caller supplies an ID belonging to a different namespace.
func TestMongoLookupMemoryByID_NamespaceMismatch(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	id := uuid.New()
	req := baseMongoIdempotencyReq([]string{"user", "alice", "ns-mismatch"}, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqBob := baseMongoIdempotencyReq([]string{"user", "bob", "ns-mismatch"}, "k1", id)
	result, err := store.LookupMemoryByID(ctx, id, reqBob)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestMongoLookupMemoryByID_KeyMismatch verifies conflict when key differs.
func TestMongoLookupMemoryByID_KeyMismatch(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "key-mismatch"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqOtherKey := baseMongoIdempotencyReq(ns, "different-key", id)
	result, err := store.LookupMemoryByID(ctx, id, reqOtherKey)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestMongoLookupMemoryByID_ValueMismatch verifies conflict when value differs.
func TestMongoLookupMemoryByID_ValueMismatch(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "value-mismatch"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqDiff := baseMongoIdempotencyReq(ns, "k1", id)
	reqDiff.Value = map[string]interface{}{"data": "different"}
	result, err := store.LookupMemoryByID(ctx, id, reqDiff)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestMongoLookupMemoryByID_KindMismatch verifies conflict when memory_kind differs.
func TestMongoLookupMemoryByID_KindMismatch(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "kind-mismatch"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqDiff := baseMongoIdempotencyReq(ns, "k1", id)
	reqDiff.MemoryKind = "profile/v1"
	result, err := store.LookupMemoryByID(ctx, id, reqDiff)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestMongoLookupMemoryByID_ArchivedRow verifies NotFoundError for deleted_reason=1.
func TestMongoLookupMemoryByID_ArchivedRow(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "archived-row"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		return store.ArchiveMemory(wctx, ns, "k1", nil)
	}))

	result, err := store.LookupMemoryByID(ctx, id, req)
	assert.Nil(t, result)
	var notFound *registrystore.NotFoundError
	require.ErrorAs(t, err, &notFound, "archived row must return NotFoundError")
}

// TestMongoLookupMemoryByID_ExpiredRow verifies NotFoundError for deleted_reason=2.
func TestMongoLookupMemoryByID_ExpiredRow(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "expired-row"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 1

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	// Force-expire via direct collection update.
	deletedReason2 := int32(2)
	_, err := store.col.UpdateOne(ctx,
		bson.M{"_id": id.String()},
		bson.M{"$set": bson.M{"archived_at": time.Now(), "deleted_reason": deletedReason2}},
	)
	require.NoError(t, err)

	result, err := store.LookupMemoryByID(ctx, id, req)
	assert.Nil(t, result)
	var notFound *registrystore.NotFoundError
	require.ErrorAs(t, err, &notFound, "expired row (deleted_reason=2) must return NotFoundError")
}

// TestMongoLookupMemoryByID_ExpiredButUnsweptRow verifies the spec decision D6: a row whose
// TTL has passed but which the sweeper has not archived (deleted_reason absent) is still
// an exact retry. Only deleted_reason 1 or 2 returns NotFoundError. No sweeper runs here.
func TestMongoLookupMemoryByID_ExpiredButUnsweptRow(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "expired-unswept"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 1

	var first *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		first, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, first.ExpiresAt)
	time.Sleep(time.Until(first.ExpiresAt.Add(100 * time.Millisecond)))
	require.True(t, time.Now().After(*first.ExpiresAt), "test precondition: TTL must have elapsed")

	n, err := store.col.CountDocuments(ctx, bson.M{"_id": id.String(), "deleted_reason": bson.M{"$exists": true, "$ne": nil}})
	require.NoError(t, err)
	require.Zero(t, n, "test precondition: row must not have been swept")

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry)
	assert.Equal(t, first.ID, result.ID)
	assert.True(t, first.CreatedAt.Equal(result.CreatedAt))
	require.NotNil(t, result.ExpiresAt)
	assert.True(t, first.ExpiresAt.Equal(*result.ExpiresAt))
}

// TestMongoLookupMemoryByID_SupersededNotEvicted verifies ExactRetry=true for
// a superseded row (deleted_reason=0) that has not yet been evicted.
func TestMongoLookupMemoryByID_SupersededNotEvicted(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "superseded-notevicted"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))
	// Supersede with a second write (no RevisionID — generates new UUID).
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1",
			Value: map[string]interface{}{"rev": 2}, MemoryKind: "default/v1",
		})
		return err
	}))

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result, "superseded but not evicted must still match")
	assert.True(t, result.ExactRetry)
}

// TestMongoLookupMemoryByID_TTLMismatch verifies conflict when TTL differs.
func TestMongoLookupMemoryByID_TTLMismatch(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "ttl-mismatch"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 3600

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqDiff := baseMongoIdempotencyReq(ns, "k1", id)
	reqDiff.TTLSeconds = 7200
	result, err := store.LookupMemoryByID(ctx, id, reqDiff)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestMongoLookupMemoryByID_TTLMatch verifies ExactRetry=true when TTL matches.
func TestMongoLookupMemoryByID_TTLMatch(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "ttl-match"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 3600

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry, "same TTL must be recognized as exact retry")
}

// TestMongoLookupMemoryByID_NilIndexEqualsEmpty verifies nil index matches stored {}.
func TestMongoLookupMemoryByID_NilIndexEqualsEmpty(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "nil-index"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	req.Index = nil

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result, "nil index must match stored {}")
	assert.True(t, result.ExactRetry)
}

// ──────────────────────────────────────────────────────────────────────────────
// PutMemory — caller-supplied ID and timestamp
// ──────────────────────────────────────────────────────────────────────────────

// TestMongoPutMemory_CallerSuppliedID verifies the stored _id equals RevisionID.
func TestMongoPutMemory_CallerSuppliedID(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	id := uuid.New()
	req := baseMongoIdempotencyReq([]string{"user", "alice", "caller-id"}, "k1", id)

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		result, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, result)
	assert.Equal(t, id, result.ID)
	assert.False(t, result.ExactRetry)

	// Confirm the stored document's _id matches.
	var doc struct {
		ID string `bson:"_id"`
	}
	require.NoError(t, store.col.FindOne(ctx, bson.M{"_id": id.String()}).Decode(&doc))
	assert.Equal(t, id.String(), doc.ID)
}

// TestMongoPutMemory_UTCTimestamp verifies that CreatedAt is UTC-truncated to millisecond
// precision and is stable across retries.
func TestMongoPutMemory_UTCTimestamp(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "utc-ts"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	var writeResult *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		writeResult, err = store.PutMemory(wctx, req)
		return err
	}))

	lookup, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, lookup)

	// MongoDB stores timestamps at millisecond precision.
	assert.Equal(t, time.UTC, lookup.CreatedAt.Location(), "stored CreatedAt must be UTC")
	assert.Equal(t,
		writeResult.CreatedAt.UTC().Truncate(time.Millisecond),
		lookup.CreatedAt.UTC().Truncate(time.Millisecond),
		"PutMemory result and stored row must agree on CreatedAt")
}

// TestMongoPutMemory_ExactRetryNoNewRow verifies N retries leave exactly 1 document
// with that ID, 1 active document, and indexed_at absent (not re-triggered).
func TestMongoPutMemory_ExactRetryNoNewRow(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "no-new-row"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	nsEncoded, err := encodeNS(ns)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		result, err := store.LookupMemoryByID(ctx, id, req)
		require.NoError(t, err, "retry %d must not error", i)
		require.NotNil(t, result)
		assert.True(t, result.ExactRetry)
	}

	count, err := store.col.CountDocuments(ctx, bson.M{"_id": id.String()})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "exactly one document must exist after retries")

	activeCount, err := store.col.CountDocuments(ctx, bson.M{
		"namespace":   nsEncoded,
		"key":         "k1",
		"archived_at": bson.M{"$exists": false},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), activeCount, "exactly one active document must exist")

	var doc struct {
		IndexedAt *time.Time `bson:"indexed_at"`
	}
	require.NoError(t, store.col.FindOne(ctx, bson.M{"_id": id.String()}).Decode(&doc))
	assert.Nil(t, doc.IndexedAt, "indexed_at must remain absent after retries")
}

// TestMongoPutMemory_ExactRetryPredecessorArchivedOnce verifies the predecessor
// is archived exactly once across retries.
func TestMongoPutMemory_ExactRetryPredecessorArchivedOnce(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "archive-once"}

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"rev": 1}, MemoryKind: "default/v1",
		})
		return err
	}))

	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	for i := 0; i < 3; i++ {
		result, err := store.LookupMemoryByID(ctx, id, req)
		require.NoError(t, err)
		assert.True(t, result.ExactRetry)
	}

	nsEncoded, err := encodeNS(ns)
	require.NoError(t, err)
	archivedCount, err := store.col.CountDocuments(ctx, bson.M{
		"namespace":      nsEncoded,
		"key":            "k1",
		"archived_at":    bson.M{"$exists": true},
		"deleted_reason": int32(0),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), archivedCount, "predecessor must be archived exactly once")
}

// TestMongoPutMemory_ExactRetryExpiresAtStable verifies ExpiresAt stability across retries.
func TestMongoPutMemory_ExactRetryExpiresAtStable(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "expires-stable"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 7200

	var first *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		first, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, first)
	require.NotNil(t, first.ExpiresAt)

	retry, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.True(t, retry.ExactRetry)
	require.NotNil(t, retry.ExpiresAt)
	assert.Equal(t,
		first.ExpiresAt.UTC().Truncate(time.Millisecond),
		retry.ExpiresAt.UTC().Truncate(time.Millisecond),
		"ExpiresAt must be stable across retries")
}

// ──────────────────────────────────────────────────────────────────────────────
// Create-only mode
// ──────────────────────────────────────────────────────────────────────────────

// TestMongoPutMemory_CreateOnly_NoActiveRow verifies create-only succeeds with revision=1.
func TestMongoPutMemory_CreateOnly_NoActiveRow(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	id := uuid.New()
	req := baseMongoIdempotencyReq([]string{"user", "alice", "create-only-new"}, "k1", id)
	req.CreateOnly = true

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		result, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, result)
	assert.Equal(t, id, result.ID)
	assert.False(t, result.ExactRetry)
	assert.Equal(t, int64(1), result.Revision)
}

// TestMongoPutMemory_CreateOnly_ExactRetry verifies ExactRetry=true on exact retry.
func TestMongoPutMemory_CreateOnly_ExactRetry(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	id := uuid.New()
	req := baseMongoIdempotencyReq([]string{"user", "alice", "create-only-retry"}, "k1", id)
	req.CreateOnly = true

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))
	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry)
}

// TestMongoPutMemory_CreateOnly_ActiveRowConflict verifies ErrMemoryRevisionConflict
// when an active row already exists.
func TestMongoPutMemory_CreateOnly_ActiveRowConflict(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-conflict"}

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"existing": true}, MemoryKind: "default/v1",
		})
		return err
	}))

	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	req.CreateOnly = true

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict)
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.ErrorAs(t, err, &createOnly, "a create-only conflict must be distinguishable from the numeric conflicts")

	// The rejected write has no side effects: no document for the new ID, and the existing
	// active document is the only one for the key, unarchived.
	byID, cerr := store.col.CountDocuments(ctx, bson.M{"_id": id.String()})
	require.NoError(t, cerr)
	assert.Equal(t, int64(0), byID, "no document may be created for the rejected revision_id")
	total, cerr := store.col.CountDocuments(ctx, bson.M{"key": "k1"})
	require.NoError(t, cerr)
	assert.Equal(t, int64(1), total, "exactly the original document must exist")
	active, cerr := store.col.CountDocuments(ctx, bson.M{"key": "k1", "archived_at": bson.M{"$exists": false}})
	require.NoError(t, cerr)
	assert.Equal(t, int64(1), active, "the existing active document must not be archived")
}

// TestMongoPutMemory_CreateOnly_NumericExpectedRevisionStillRuns verifies that when
// create-only mode passes (no active row), the numeric ExpectedRevision check still fires.
func TestMongoPutMemory_CreateOnly_NumericExpectedRevisionStillRuns(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-numeric"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)
	req.CreateOnly = true
	wrongRevision := int64(99)
	req.ExpectedRevision = &wrongRevision

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict,
		"numeric ExpectedRevision must fire after create-only check passes")
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.False(t, errors.As(err, &createOnly), "a numeric conflict must not be classified as create-only")

	count, err := store.col.CountDocuments(ctx, bson.M{"_id": id.String()})
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "no document must be inserted when numeric check fails")
}

// ──────────────────────────────────────────────────────────────────────────────
// ExpectedRevisionID mode
// ──────────────────────────────────────────────────────────────────────────────

// TestMongoPutMemory_ExpectedRevisionID_Match verifies update succeeds when UUID matches.
func TestMongoPutMemory_ExpectedRevisionID_Match(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-match"}

	var activeID uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		result, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		if err == nil {
			activeID = result.ID
		}
		return err
	}))

	newID := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &activeID

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		result, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, result)
	assert.Equal(t, newID, result.ID)
	assert.False(t, result.ExactRetry)
}

// TestMongoPutMemory_ExpectedRevisionID_Mismatch verifies ExpectedActiveRevisionIDMismatchError.
func TestMongoPutMemory_ExpectedRevisionID_Mismatch(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-mismatch"}

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		return err
	}))

	newID := uuid.New()
	wrongExpected := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &wrongExpected

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	require.ErrorAs(t, err, &mismatchErr)
	assert.Equal(t, wrongExpected.String(), mismatchErr.ExpectedID)
}

// TestMongoPutMemory_ExpectedRevisionID_NoActiveRow verifies mismatch error when no active row.
func TestMongoPutMemory_ExpectedRevisionID_NoActiveRow(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-norow"}
	newID := uuid.New()
	someExpected := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &someExpected

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	require.ErrorAs(t, err, &mismatchErr)
}

// TestMongoPutMemory_ExpectedRevisionID_ExactRetry verifies exact retry short-circuits
// before the ExpectedRevisionID check.
func TestMongoPutMemory_ExpectedRevisionID_ExactRetry(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-exactretry"}

	var activeID uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		result, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		if err == nil {
			activeID = result.ID
		}
		return err
	}))

	newID := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &activeID

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	result, err := store.LookupMemoryByID(ctx, newID, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry)
}

// TestMongoPutMemory_ExpectedRevisionID_NumericMismatch verifies numeric check fires
// after UUID check passes.
func TestMongoPutMemory_ExpectedRevisionID_NumericMismatch(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-numeric-mismatch"}

	var activeID uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		result, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		if err == nil {
			activeID = result.ID
		}
		return err
	}))

	newID := uuid.New()
	wrongNumeric := int64(99)
	req := baseMongoIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &activeID
	req.ExpectedRevision = &wrongNumeric

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict)
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	assert.False(t, errors.As(err, &mismatchErr), "must not be ExpectedActiveRevisionIDMismatchError")
}

// ──────────────────────────────────────────────────────────────────────────────
// Eviction and concurrency
// ──────────────────────────────────────────────────────────────────────────────

// TestMongoPutMemory_EvictedRowTreatedAsNewWrite verifies that after eviction,
// the same UUID is treated as a new write.
func TestMongoPutMemory_EvictedRowTreatedAsNewWrite(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "eviction"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))
	// Supersede.
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"rev": 2}, MemoryKind: "default/v1",
		})
		return err
	}))
	// Mark indexed so eligible for eviction.
	_, err := store.col.UpdateOne(ctx,
		bson.M{"_id": id.String()},
		bson.M{"$set": bson.M{"indexed_at": time.Now()}},
	)
	require.NoError(t, err)
	// Evict.
	n, err := store.HardDeleteEvictableUpdates(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	// LookupMemoryByID must return (nil, nil) after eviction.
	lookup, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	assert.Nil(t, lookup, "evicted document must appear as not found")

	// PutMemory must succeed as a new write.
	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		result, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, result)
	assert.Equal(t, id, result.ID)
	assert.False(t, result.ExactRetry)
}

// TestMongoPutMemory_ConcurrentSameID verifies that N goroutines writing the
// same RevisionID produce exactly one ExactRetry=false and N-1 ExactRetry=true,
// zero errors, and exactly one document.
// WithTransaction replays the losing callback after WriteConflict so it sees
// the winner's committed document and returns ExactRetry=true.
func TestMongoPutMemory_ConcurrentSameID(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "concurrent"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	const n = 5
	type outcome struct {
		result *registryepisodic.MemoryWriteResult
		err    error
	}
	results := make([]outcome, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			var r *registryepisodic.MemoryWriteResult
			err := store.InWriteTx(ctx, func(wctx context.Context) error {
				var e error
				r, e = store.PutMemory(wctx, req)
				return e
			})
			results[i] = outcome{r, err}
		}()
	}
	wg.Wait()

	var creators, retries, failures int
	for _, o := range results {
		if o.err != nil {
			t.Logf("goroutine error: %v", o.err)
			failures++
			continue
		}
		if o.result.ExactRetry {
			retries++
		} else {
			creators++
		}
	}
	assert.Equal(t, 0, failures, "no goroutine must error (WithTransaction replays on WriteConflict)")
	assert.Equal(t, 1, creators, "exactly one goroutine must be the creator")
	assert.Equal(t, n-1, retries, "all others must detect ExactRetry=true")

	count, err := store.col.CountDocuments(ctx, bson.M{"_id": id.String()})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "exactly one document must exist")
}

// ──────────────────────────────────────────────────────────────────────────────
// Fault-injection and transaction-replay tests
// ──────────────────────────────────────────────────────────────────────────────

// TestMongoPutMemory_FaultBetweenArchiveAndInsert verifies rollback atomicity:
// injecting a failure after UpdateMany (archive) and before InsertOne leaves the
// predecessor active, and a subsequent call with the same revision_id succeeds.
func TestMongoPutMemory_FaultBetweenArchiveAndInsert(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "fault-arch-insert"}

	// Write a predecessor.
	var predecessorID uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		result, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		if err == nil {
			predecessorID = result.ID
		}
		return err
	}))

	faultErr := errors.New("injected fault between archive and insert")
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	// Inject fault after archive step.
	store.afterMemoryArchive = func() error { return faultErr }
	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	store.afterMemoryArchive = nil
	require.ErrorIs(t, err, faultErr, "fault must propagate")

	// Predecessor must still be active (transaction rolled back).
	count, countErr := store.col.CountDocuments(ctx, bson.M{
		"_id": predecessorID.String(), "archived_at": bson.M{"$exists": false},
	})
	require.NoError(t, countErr)
	assert.Equal(t, int64(1), count, "predecessor must remain active after rollback")

	// New ID must not exist.
	newCount, countErr := store.col.CountDocuments(ctx, bson.M{"_id": id.String()})
	require.NoError(t, countErr)
	assert.Equal(t, int64(0), newCount, "no new document must exist after rollback")

	// Retry without fault must succeed.
	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var e error
		result, e = store.PutMemory(wctx, req)
		return e
	}))
	require.NotNil(t, result)
	assert.Equal(t, id, result.ID)
	assert.False(t, result.ExactRetry)

	// Predecessor archived exactly once.
	nsEncoded, encErr := encodeNS(ns)
	require.NoError(t, encErr)
	archivedCount, countErr := store.col.CountDocuments(ctx, bson.M{
		"namespace": nsEncoded, "key": "k1",
		"archived_at": bson.M{"$exists": true}, "deleted_reason": int32(0),
	})
	require.NoError(t, countErr)
	assert.Equal(t, int64(1), archivedCount, "predecessor must be archived exactly once")
}

// TestMongoPutMemory_TransientErrorCausesCallbackReplay verifies that a
// TransientTransactionError on the first callback attempt causes WithTransaction
// to replay the callback, and the final result is correct.
// Spec §666-670: "Assert: WithTransaction replays the callback; the final result
// is correct; assert the callback ran exactly twice."
func TestMongoPutMemory_TransientErrorCausesCallbackReplay(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "transient-replay"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	attempts := 0
	store.beforeMemoryInsert = func() error {
		attempts++
		if attempts == 1 {
			// Return a transient error only on the first attempt.
			return mongo.CommandError{
				Message: "simulated write conflict",
				Labels:  []string{"TransientTransactionError"},
			}
		}
		return nil // allow the second attempt to succeed
	}
	t.Cleanup(func() { store.beforeMemoryInsert = nil })

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		result, err = store.PutMemory(wctx, req)
		return err
	}))
	assert.Equal(t, 2, attempts, "callback must have been called exactly twice")
	require.NotNil(t, result)
	assert.Equal(t, id, result.ID)
	assert.False(t, result.ExactRetry)

	count, err := store.col.CountDocuments(ctx, bson.M{"_id": id.String()})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "exactly one document must exist after replay")
}

// TestMongoPutMemory_CallbackStateResetOnReplay verifies that InWriteTx's WithTransaction
// replays the callback on TransientTransactionError and that the final result is correct.
// Uses an attempt counter so the transient error fires exactly once.
func TestMongoPutMemory_CallbackStateResetOnReplay(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "state-reset"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	// insertAttempts counts how many times beforeMemoryInsert is reached.
	// On the first call we return TransientTransactionError; on subsequent calls we allow the insert.
	insertAttempts := 0
	store.beforeMemoryInsert = func() error {
		insertAttempts++
		if insertAttempts == 1 {
			return mongo.CommandError{
				Message: "simulated transient error for state-reset test",
				Labels:  []string{"TransientTransactionError"},
			}
		}
		return nil
	}
	t.Cleanup(func() { store.beforeMemoryInsert = nil })

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		result, err = store.PutMemory(wctx, req)
		return err
	}))

	// The callback was replayed: insertAttempts must be 2 (one aborted, one committed).
	assert.Equal(t, 2, insertAttempts, "beforeMemoryInsert must have been reached twice (one abort, one commit)")
	require.NotNil(t, result)
	assert.Equal(t, id, result.ID, "result ID must match the committed insert")
	assert.False(t, result.ExactRetry)

	count, err := store.col.CountDocuments(ctx, bson.M{"_id": id.String()})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "exactly one document must exist")
}

// TestMongoPutMemory_ExpectedRevisionIgnoredOnExactRetry verifies that when
// an exact retry is detected (row already exists), replay detection short-circuits
// before the numeric ExpectedRevision check — even if a stale ExpectedRevision
// is supplied. Spec §647: "ExpectedRevision on the retry is ignored while the
// row exists (replay detection short-circuits)."
func TestMongoPutMemory_ExpectedRevisionIgnoredOnExactRetry(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "exprev-ignored-retry"}
	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	// Retry with a wrong ExpectedRevision — must still be an exact retry.
	wrongRevision := int64(99)
	reqRetry := baseMongoIdempotencyReq(ns, "k1", id)
	reqRetry.ExpectedRevision = &wrongRevision

	result, err := store.LookupMemoryByID(ctx, id, reqRetry)
	require.NoError(t, err, "exact retry must short-circuit before numeric ExpectedRevision check")
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry,
		"wrong ExpectedRevision must be ignored when row is found by RevisionID — replay detection wins")
}

// TestMongoPutMemory_ConcurrentSameID_PredecessorArchivedOnce extends the concurrent
// test to verify the predecessor is archived exactly once after all N writers finish.
// Spec §653: "one row, one archive of the predecessor."
func TestMongoPutMemory_ConcurrentSameID_PredecessorArchivedOnce(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "concurrent-archive-once"}

	// Write a predecessor first.
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"rev": 0}, MemoryKind: "default/v1",
		})
		return err
	}))

	id := uuid.New()
	req := baseMongoIdempotencyReq(ns, "k1", id)

	const n = 4
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = store.InWriteTx(ctx, func(wctx context.Context) error {
				_, err := store.PutMemory(wctx, req)
				return err
			})
		}()
	}
	wg.Wait()

	nsEncoded, err := encodeNS(ns)
	require.NoError(t, err)

	// Exactly one document with that ID must exist.
	count, err := store.col.CountDocuments(ctx, bson.M{"_id": id.String()})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "exactly one document must exist")

	// The predecessor must be archived exactly once (deleted_reason=0).
	archivedCount, err := store.col.CountDocuments(ctx, bson.M{
		"namespace":      nsEncoded,
		"key":            "k1",
		"archived_at":    bson.M{"$exists": true},
		"deleted_reason": int32(0),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), archivedCount, "predecessor must be archived exactly once across all concurrent writers")
}

// TestMongoPutMemory_AuthorizedPredecessorConflict_IsNotCreateOnly verifies that an
// AuthorizedPredecessor conflict stays the bare sentinel and is not classified as create-only,
// so REST can tell the two apart. CreateOnly is left false: this is the non-idempotent path.
func TestMongoPutMemory_AuthorizedPredecessorConflict_IsNotCreateOnly(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "predecessor-conflict"}

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"existing": true}, MemoryKind: "default/v1",
		})
		return err
	}))

	req := baseMongoIdempotencyReq(ns, "k1", uuid.New())
	// The caller believed no active row existed, but one does.
	req.AuthorizedPredecessor = &registryepisodic.MemoryPredecessorExpectation{Exists: false}
	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict)
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.False(t, errors.As(err, &createOnly), "an AuthorizedPredecessor conflict must not be classified as create-only")
}
