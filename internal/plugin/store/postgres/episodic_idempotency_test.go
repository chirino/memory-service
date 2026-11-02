//go:build !nopostgresql

package postgres

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
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPgIdempotencyStore spins up a fresh Postgres database, runs migrations,
// and returns a raw *postgresEpisodicStore for white-box access plus a context.
func setupPgIdempotencyStore(t *testing.T) (*postgresEpisodicStore, context.Context) {
	t.Helper()
	dbURL := testpg.StartPostgres(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "postgres"
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registryepisodic.Select("postgres")
	require.NoError(t, err)
	loaded, err := loader(ctx)
	require.NoError(t, err)
	return loaded.(*postgresEpisodicStore), ctx
}

// baseReq builds a minimal valid PutMemoryRequest with caller-supplied RevisionID.
func baseIdempotencyReq(ns []string, key string, id uuid.UUID) registryepisodic.PutMemoryRequest {
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

// TestPgLookupMemoryByID_NotFound verifies that LookupMemoryByID returns (nil, nil)
// when no row with the given ID exists.
func TestPgLookupMemoryByID_NotFound(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "lookup-notfound"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	assert.Nil(t, result, "must return nil result when row absent")
}

// TestPgLookupMemoryByID_ExactRetry verifies that an identical second write is
// detected and returned with ExactRetry=true, with all stored fields preserved.
func TestPgLookupMemoryByID_ExactRetry(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "lookup-exactretry"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	// First write.
	var first *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		first, err = store.PutMemory(txCtx, req)
		return err
	}))
	require.NotNil(t, first)
	assert.False(t, first.ExactRetry, "first write must not be an exact retry")

	// Retry via LookupMemoryByID (called outside transaction, like the handler).
	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result, "retry must return non-nil result")
	assert.True(t, result.ExactRetry, "retry must be flagged as ExactRetry")
	assert.Equal(t, id, result.ID, "ID must match caller-supplied UUID")
	assert.Equal(t, first.Revision, result.Revision, "revision must match first write")
	assert.Equal(t, first.CreatedAt.UTC().Truncate(time.Microsecond),
		result.CreatedAt.UTC().Truncate(time.Microsecond),
		"CreatedAt must be stable on retry")
}

// TestPgLookupMemoryByID_NamespaceMismatch verifies that a cross-namespace collision
// returns MemoryRevisionIDConflictError without decrypting the stored value.
func TestPgLookupMemoryByID_NamespaceMismatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "ns-mismatch"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	// Write under namespace alice.
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Retry claim from namespace bob — different namespace, same ID.
	reqBob := baseIdempotencyReq([]string{"user", "bob", "ns-mismatch"}, "k1", id)
	result, err := store.LookupMemoryByID(ctx, id, reqBob)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr,
		"cross-namespace collision must return MemoryRevisionIDConflictError")
	// Response must not leak stored data — only caller-supplied revisionID.
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestPgLookupMemoryByID_KeyMismatch verifies that same namespace + different key
// returns MemoryRevisionIDConflictError.
func TestPgLookupMemoryByID_KeyMismatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "key-mismatch"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Same namespace, different key.
	reqOtherKey := baseIdempotencyReq(ns, "different-key", id)
	result, err := store.LookupMemoryByID(ctx, id, reqOtherKey)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestPgLookupMemoryByID_ValueMismatch verifies that same namespace/key but
// different value content returns MemoryRevisionIDConflictError.
func TestPgLookupMemoryByID_ValueMismatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "value-mismatch"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	reqDiff := baseIdempotencyReq(ns, "k1", id)
	reqDiff.Value = map[string]interface{}{"data": "different"}

	result, err := store.LookupMemoryByID(ctx, id, reqDiff)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestPgLookupMemoryByID_KindMismatch verifies that memory_kind mismatch
// (e.g. after kind migration) returns MemoryRevisionIDConflictError.
func TestPgLookupMemoryByID_KindMismatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "kind-mismatch"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	reqDiff := baseIdempotencyReq(ns, "k1", id)
	reqDiff.MemoryKind = "profile/v1" // kind migrated between attempts

	result, err := store.LookupMemoryByID(ctx, id, reqDiff)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestPgLookupMemoryByID_ArchivedRow verifies that an archived row (deleted_reason=1)
// returns NotFoundError.
func TestPgLookupMemoryByID_ArchivedRow(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "archived-row"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Archive the memory (deleted_reason = 1).
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.ArchiveMemory(txCtx, ns, "k1", nil)
	}))

	result, err := store.LookupMemoryByID(ctx, id, req)
	assert.Nil(t, result)
	var notFound *registrystore.NotFoundError
	require.ErrorAs(t, err, &notFound,
		"archived row (deleted_reason=1) must return NotFoundError")
}

// TestPgLookupMemoryByID_ExpiredRow verifies that a row with deleted_reason=2 (expired
// by the TTL sweeper) returns NotFoundError, distinct from deleted_reason=1 (archived).
func TestPgLookupMemoryByID_ExpiredRow(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "expired-row"}
	id := uuid.New()
	// 1 second TTL so the sweeper will pick it up immediately.
	req := baseIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 1

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Force-expire via raw SQL (bypass the 1s wait) so the test is deterministic.
	require.NoError(t,
		store.s.db.Exec(`UPDATE memories SET archived_at = NOW(), deleted_reason = 2 WHERE id = ?`, id).Error)

	result, err := store.LookupMemoryByID(ctx, id, req)
	assert.Nil(t, result)
	var notFound *registrystore.NotFoundError
	require.ErrorAs(t, err, &notFound,
		"expired row (deleted_reason=2) must return NotFoundError")
}

// TestPgLookupMemoryByID_SupersededNotEvicted verifies that a superseded row
// (deleted_reason=0, not yet evicted by HardDeleteEvictableUpdates) is still
// recognized as an exact retry and returns ExactRetry=true. This is the spec
// invariant: "exact retry of a superseded revision returns 200 until eviction".
func TestPgLookupMemoryByID_SupersededNotEvicted(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "superseded-notevicted"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	// First write with caller-supplied ID.
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Second write supersedes first (id becomes superseded, deleted_reason=0).
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace:  ns,
			Key:        "k1",
			Value:      map[string]interface{}{"rev": 2},
			MemoryKind: "default/v1",
		})
		return err
	}))

	// Verify the first row is superseded (deleted_reason=0) but NOT yet evicted.
	var dr int16
	require.NoError(t,
		store.s.db.Raw(`SELECT deleted_reason FROM memories WHERE id = ?`, id).Scan(&dr).Error)
	require.Equal(t, int16(0), dr, "first row must be superseded (deleted_reason=0)")

	// Retry with original request — superseded but not evicted → ExactRetry=true.
	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result, "superseded (not evicted) row must return non-nil result")
	assert.True(t, result.ExactRetry, "superseded (not evicted) row must be ExactRetry=true")
}

// TestPgLookupMemoryByID_TTLMismatch verifies that a retry with a different TTL
// (producing a different ExpiresAt) returns MemoryRevisionIDConflictError.
func TestPgLookupMemoryByID_TTLMismatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "ttl-mismatch"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 3600 // 1 hour

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Retry with a different TTL.
	reqDiff := baseIdempotencyReq(ns, "k1", id)
	reqDiff.TTLSeconds = 7200 // 2 hours

	result, err := store.LookupMemoryByID(ctx, id, reqDiff)
	assert.Nil(t, result)
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &conflictErr,
		"TTL mismatch must return MemoryRevisionIDConflictError")
	assert.Equal(t, id.String(), conflictErr.RevisionID)
}

// TestPgLookupMemoryByID_TTLMatch verifies that an exact retry with the same TTL
// returns ExactRetry=true.
func TestPgLookupMemoryByID_TTLMatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "ttl-match"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 3600

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry, "same TTL must be recognized as exact retry")
}

// TestPgLookupMemoryByID_NilIndexEqualsEmpty verifies that a nil index on the retry
// request matches a stored {} index, per the spec ("normalize nil to {} on both sides").
func TestPgLookupMemoryByID_NilIndexEqualsEmpty(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "nil-index"}
	id := uuid.New()
	// First write: nil index (store writes {} for nil).
	req := baseIdempotencyReq(ns, "k1", id)
	req.Index = nil

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Retry with nil index — must match stored {}.
	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result, "nil index must match stored {} index")
	assert.True(t, result.ExactRetry)
}

// ──────────────────────────────────────────────────────────────────────────────
// PutMemory — caller-supplied ID behaviour
// ──────────────────────────────────────────────────────────────────────────────

// TestPgPutMemory_CallerSuppliedID verifies that PutMemory uses the caller-supplied
// RevisionID as the primary key instead of generating a new UUID.
func TestPgPutMemory_CallerSuppliedID(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "caller-id"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.PutMemory(txCtx, req)
		return err
	}))
	require.NotNil(t, result)
	assert.Equal(t, id, result.ID, "stored ID must equal caller-supplied RevisionID")
	assert.False(t, result.ExactRetry)
}

// TestPgPutMemory_UTCTruncatedTimestamp verifies that the stored CreatedAt is UTC and
// truncated to microsecond precision (the spec requirement for Postgres).
func TestPgPutMemory_UTCTruncatedTimestamp(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "utc-ts"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.PutMemory(txCtx, req)
		return err
	}))
	require.NotNil(t, result)

	// Lookup the row and compare CreatedAt directly from the stored row.
	lookup, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, lookup)

	// The PutMemory result must have no sub-microsecond precision (UTC, truncated).
	writeAt := result.CreatedAt.UTC()
	assert.Equal(t, writeAt, writeAt.Truncate(time.Microsecond),
		"PutMemory result CreatedAt must be truncated to microsecond")

	// GORM may attach the local timezone to the scanned value; normalise both sides.
	storedAt := lookup.CreatedAt.UTC().Truncate(time.Microsecond)
	assert.Equal(t, writeAt.Truncate(time.Microsecond), storedAt,
		"PutMemory result and stored row must agree on CreatedAt")
}

// TestPgPutMemory_ExactRetryNoNewRow verifies that after first write + N retries via
// LookupMemoryByID there is still exactly one row with that ID and one active row.
func TestPgPutMemory_ExactRetryNoNewRow(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "no-new-row"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Three retries.
	for i := 0; i < 3; i++ {
		result, err := store.LookupMemoryByID(ctx, id, req)
		require.NoError(t, err, "retry %d must not error", i)
		require.NotNil(t, result)
		assert.True(t, result.ExactRetry)
	}

	// Verify counts directly in the database.
	var totalCount int64
	require.NoError(t,
		store.s.db.Raw("SELECT COUNT(*) FROM memories WHERE id = ?", id).
			Scan(&totalCount).Error)
	assert.Equal(t, int64(1), totalCount, "exactly one row must exist after retries")

	var activeCount int64
	require.NoError(t,
		store.s.db.Raw("SELECT COUNT(*) FROM memories WHERE namespace = ? AND key = ? AND archived_at IS NULL",
			"user\x1ealice\x1eno-new-row", "k1").Scan(&activeCount).Error)
	assert.Equal(t, int64(1), activeCount, "exactly one active row must exist")

	// indexed_at must still be NULL — retries must not reset it (spec: zero side effects).
	var indexedAtIsNull bool
	require.NoError(t,
		store.s.db.Raw("SELECT indexed_at IS NULL FROM memories WHERE id = ?", id).
			Scan(&indexedAtIsNull).Error)
	assert.True(t, indexedAtIsNull, "indexed_at must remain NULL after retries (not re-triggered)")
}

// TestPgPutMemory_ExactRetryPredecessorArchivedOnce verifies that the predecessor row is
// archived exactly once, and not additionally on retries.
func TestPgPutMemory_ExactRetryPredecessorArchivedOnce(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "archive-once"}
	nsEncoded := "user\x1ealice\x1earchive-once"

	// First write (server-generated ID for the predecessor).
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace:  ns,
			Key:        "k1",
			Value:      map[string]interface{}{"rev": 1},
			MemoryKind: "default/v1",
		})
		return err
	}))

	// Second write with caller-supplied ID (creates, archives predecessor).
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Three retries — must not archive anything additional.
	for i := 0; i < 3; i++ {
		result, err := store.LookupMemoryByID(ctx, id, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.ExactRetry)
	}

	// Predecessor must be archived exactly once (deleted_reason=0 = superseded by update).
	var archivedCount int64
	require.NoError(t,
		store.s.db.Raw(`SELECT COUNT(*) FROM memories WHERE namespace = ? AND key = ? AND archived_at IS NOT NULL AND deleted_reason = 0`,
			nsEncoded, "k1").Scan(&archivedCount).Error)
	assert.Equal(t, int64(1), archivedCount, "predecessor must be archived exactly once")
}

// ──────────────────────────────────────────────────────────────────────────────
// Create-only mode
// ──────────────────────────────────────────────────────────────────────────────

// TestPgPutMemory_CreateOnly_NoActiveRow verifies create-only succeeds when there
// is no active row for (namespace, key).
func TestPgPutMemory_CreateOnly_NoActiveRow(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-new"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)
	req.CreateOnly = true

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.PutMemory(txCtx, req)
		return err
	}))
	require.NotNil(t, result)
	assert.Equal(t, id, result.ID)
	assert.False(t, result.ExactRetry)
	assert.Equal(t, int64(1), result.Revision, "create-only must produce revision 1")
}

// TestPgPutMemory_CreateOnly_ExactRetry verifies that an exact retry of a
// create-only write returns ExactRetry=true and does not conflict.
func TestPgPutMemory_CreateOnly_ExactRetry(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-retry"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)
	req.CreateOnly = true

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	result, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry)
}

// TestPgPutMemory_CreateOnly_ActiveRowConflict verifies that create-only returns
// ErrMemoryRevisionConflict when an active row already exists.
func TestPgPutMemory_CreateOnly_ActiveRowConflict(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-conflict"}

	// Pre-existing active row.
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"existing": true}, MemoryKind: "default/v1",
		})
		return err
	}))

	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)
	req.CreateOnly = true

	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict,
		"create-only with active row must return ErrMemoryRevisionConflict")
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.ErrorAs(t, err, &createOnly, "a create-only conflict must be distinguishable from the numeric conflicts")

	// Active row must be untouched.
	var activeCount int64
	require.NoError(t,
		store.s.db.Raw(`SELECT COUNT(*) FROM memories WHERE namespace = ? AND key = ? AND archived_at IS NULL`,
			"user\x1ealice\x1ecreate-only-conflict", "k1").Scan(&activeCount).Error)
	assert.Equal(t, int64(1), activeCount, "active row must be untouched")

	var byID, total int64
	require.NoError(t, store.s.db.Raw("SELECT COUNT(*) FROM memories WHERE id = ?", id).Scan(&byID).Error)
	assert.Equal(t, int64(0), byID, "no row may be created for the rejected revision_id")
	require.NoError(t, store.s.db.Raw("SELECT COUNT(*) FROM memories WHERE key = 'k1'").Scan(&total).Error)
	assert.Equal(t, int64(1), total, "exactly the original row must exist (nothing archived or inserted)")
}

// ──────────────────────────────────────────────────────────────────────────────
// Expected-active-revision-ID mode
// ──────────────────────────────────────────────────────────────────────────────

// TestPgPutMemory_ExpectedRevisionID_Match verifies the update succeeds when
// ExpectedRevisionID matches the active row's ID.
func TestPgPutMemory_ExpectedRevisionID_Match(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-match"}

	// First write to obtain the active row ID.
	var activeID uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		result, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		if err == nil {
			activeID = result.ID
		}
		return err
	}))

	// Update with caller-supplied ID and ExpectedRevisionID.
	newID := uuid.New()
	req := baseIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &activeID

	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.PutMemory(txCtx, req)
		return err
	}))
	require.NotNil(t, result)
	assert.Equal(t, newID, result.ID)
	assert.False(t, result.ExactRetry)
}

// TestPgPutMemory_ExpectedRevisionID_Mismatch verifies that a mismatched
// expected_revision_id returns ExpectedActiveRevisionIDMismatchError.
func TestPgPutMemory_ExpectedRevisionID_Mismatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-mismatch"}

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		return err
	}))

	newID := uuid.New()
	wrongExpected := uuid.New() // different from actual active row ID
	req := baseIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &wrongExpected

	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	})
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	require.ErrorAs(t, err, &mismatchErr,
		"mismatched expected_revision_id must return ExpectedActiveRevisionIDMismatchError")
	assert.Equal(t, wrongExpected.String(), mismatchErr.ExpectedID)
}

// TestPgPutMemory_ExpectedRevisionID_NoActiveRow verifies that ExpectedRevisionID
// when no active row exists returns ExpectedActiveRevisionIDMismatchError.
func TestPgPutMemory_ExpectedRevisionID_NoActiveRow(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-norow"}
	newID := uuid.New()
	someExpected := uuid.New()
	req := baseIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &someExpected

	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	})
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	require.ErrorAs(t, err, &mismatchErr,
		"no active row with expected_revision_id must return ExpectedActiveRevisionIDMismatchError")
}

// TestPgPutMemory_ExpectedRevisionID_ExactRetry verifies that an exact retry in
// ExpectedRevisionID mode returns ExactRetry=true and ignores the expected_revision_id.
func TestPgPutMemory_ExpectedRevisionID_ExactRetry(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-exactretry"}

	var activeID uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		result, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		if err == nil {
			activeID = result.ID
		}
		return err
	}))

	newID := uuid.New()
	req := baseIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &activeID

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Retry: same RevisionID, fields unchanged — ExactRetry must be detected
	// before any expected_revision_id check.
	result, err := store.LookupMemoryByID(ctx, newID, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ExactRetry, "LookupMemoryByID must detect exact retry")
}

// TestPgPutMemory_CreateOnly_NumericExpectedRevisionStillRuns verifies that
// when create-only mode passes (no active row), the numeric ExpectedRevision
// check still runs and returns ErrMemoryRevisionConflict when it fails.
// Spec: "if both are present and the numeric check fails, ErrMemoryRevisionConflict
// is returned, not the create-only error."
func TestPgPutMemory_CreateOnly_NumericExpectedRevisionStillRuns(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-numeric"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)
	req.CreateOnly = true
	wrongRevision := int64(99) // no active row exists, so any numeric check fails
	req.ExpectedRevision = &wrongRevision

	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	})
	// Create-only check passed (no active row), but numeric check fires and fails.
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict,
		"numeric ExpectedRevision check must fire after create-only check passes")
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.False(t, errors.As(err, &createOnly), "a numeric conflict must not be classified as create-only")

	// No row must have been created.
	var count int64
	require.NoError(t,
		store.s.db.Raw(`SELECT COUNT(*) FROM memories WHERE id = ?`, id).Scan(&count).Error)
	assert.Equal(t, int64(0), count, "no row must exist when numeric check fails")
}

// TestPgPutMemory_ExpectedRevisionID_NumericMismatch verifies that when the UUID
// check passes but the numeric ExpectedRevision does not, ErrMemoryRevisionConflict
// is returned — not ExpectedActiveRevisionIDMismatchError.
// Spec: "expected_revision_id and expected_revision are independent checks that both
// run after the replay check; if the numeric check fails, ErrMemoryRevisionConflict
// is returned."
func TestPgPutMemory_ExpectedRevisionID_NumericMismatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-numeric-mismatch"}

	// First write; obtain the active row ID.
	var activeID uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		result, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		if err == nil {
			activeID = result.ID
		}
		return err
	}))

	newID := uuid.New()
	wrongNumericRevision := int64(99) // stored revision is 1
	req := baseIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &activeID           // UUID check passes
	req.ExpectedRevision = &wrongNumericRevision // numeric check fails

	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	})
	// UUID check passed; numeric check fires and returns ErrMemoryRevisionConflict.
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict,
		"numeric ExpectedRevision check must fire after UUID check passes")
	// Must NOT be an ExpectedActiveRevisionIDMismatchError.
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	assert.False(t, errors.As(err, &mismatchErr),
		"error must not be ExpectedActiveRevisionIDMismatchError when UUID check passed")
}

// ──────────────────────────────────────────────────────────────────────────────
// Eviction behaviour
// ──────────────────────────────────────────────────────────────────────────────

// TestPgPutMemory_EvictedRowTreatedAsNewWrite verifies that after HardDeleteEvictableUpdates
// removes a superseded row, the same UUID is treated as a new write.
func TestPgPutMemory_EvictedRowTreatedAsNewWrite(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "eviction"}
	nsEncoded := "user\x1ealice\x1eeviction"

	// First write with caller-supplied ID.
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	}))

	// Second write (different ID) supersedes first — first becomes deleted_reason=0.
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"rev": 2}, MemoryKind: "default/v1",
		})
		return err
	}))

	// Simulate indexer marking the superseded row as indexed so it's eligible for eviction.
	require.NoError(t,
		store.s.db.Exec(`UPDATE memories SET indexed_at = NOW() WHERE id = ?`, id).Error)

	// Evict it.
	n, err := store.HardDeleteEvictableUpdates(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "one superseded row must be evicted")

	// LookupMemoryByID now returns (nil, nil) — no row found.
	lookup, err := store.LookupMemoryByID(ctx, id, req)
	require.NoError(t, err)
	assert.Nil(t, lookup, "evicted row must appear as not found")

	// PutMemory must succeed as a new write (ExactRetry=false).
	var result *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.PutMemory(txCtx, req)
		return err
	}))
	require.NotNil(t, result)
	assert.False(t, result.ExactRetry, "post-eviction write must not be ExactRetry")
	assert.Equal(t, id, result.ID)

	// Verify at most one active row.
	var activeCount int64
	require.NoError(t,
		store.s.db.Raw(`SELECT COUNT(*) FROM memories WHERE namespace = ? AND key = ? AND archived_at IS NULL`,
			nsEncoded, "k1").Scan(&activeCount).Error)
	assert.Equal(t, int64(1), activeCount)
}

// ──────────────────────────────────────────────────────────────────────────────
// Concurrency: advisory lock ensures exactly one creator
// ──────────────────────────────────────────────────────────────────────────────

// TestPgPutMemory_ConcurrentSameID verifies that N goroutines writing the same
// RevisionID produce exactly one ExactRetry=false result and all others succeed
// with ExactRetry=true (none return ErrMemoryRevisionConflict).
func TestPgPutMemory_ConcurrentSameID(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "concurrent"}
	id := uuid.New()
	req := baseIdempotencyReq(ns, "k1", id)

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
			err := store.InWriteTx(ctx, func(txCtx context.Context) error {
				var e error
				r, e = store.PutMemory(txCtx, req)
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
	assert.Equal(t, 0, failures,
		"no goroutine must return an error (advisory lock must prevent spurious conflicts)")
	assert.Equal(t, 1, creators,
		"exactly one goroutine must be the creator (ExactRetry=false)")
	assert.Equal(t, n-1, retries,
		"all other goroutines must detect ExactRetry=true")

	// Exactly one row in the database.
	var count int64
	require.NoError(t,
		store.s.db.Raw("SELECT COUNT(*) FROM memories WHERE id = ?", id).Scan(&count).Error)
	assert.Equal(t, int64(1), count, "exactly one row must exist after concurrent writes")
}

// TestPgPutMemory_ConcurrentDifferentPayload verifies that two goroutines racing
// with the same RevisionID but different payloads produce exactly one winner and
// one MemoryRevisionIDConflictError — the loser sees the committed row via the
// in-transaction advisory-lock lookup and detects a content mismatch.
func TestPgPutMemory_ConcurrentDifferentPayload(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "concurrent-diff"}
	id := uuid.New()

	reqA := baseIdempotencyReq(ns, "k1", id)
	reqA.Value = map[string]interface{}{"writer": "A"}

	reqB := baseIdempotencyReq(ns, "k1", id)
	reqB.Value = map[string]interface{}{"writer": "B"}

	// Use putAfterAdvisoryLock to pause writer A inside its transaction (while
	// holding the advisory lock) so writer B can start and block on the same lock.
	// When A resumes and commits, B's advisory lock unblocks, finds A's committed
	// row, and returns MemoryRevisionIDConflictError.
	lockAcquired := make(chan struct{})
	resume := make(chan struct{})
	store.putAfterAdvisoryLock = func() {
		close(lockAcquired)
		<-resume
	}
	t.Cleanup(func() { store.putAfterAdvisoryLock = nil })

	doneA := make(chan error, 1)
	go func() {
		doneA <- store.InWriteTx(ctx, func(txCtx context.Context) error {
			_, err := store.PutMemory(txCtx, reqA)
			return err
		})
	}()

	// Wait until A holds the advisory lock, then disable the hook so B won't pause too.
	<-lockAcquired
	store.putAfterAdvisoryLock = nil

	// Start writer B in a separate goroutine — it will block on the advisory lock
	// until A commits.
	doneB := make(chan error, 1)
	go func() {
		doneB <- store.InWriteTx(ctx, func(txCtx context.Context) error {
			_, err := store.PutMemory(txCtx, reqB)
			return err
		})
	}()

	// Let writer A commit.
	close(resume)
	errA := <-doneA
	errB := <-doneB

	// A must succeed; B must return MemoryRevisionIDConflictError.
	require.NoError(t, errA, "writer A (first acquirer) must succeed")
	var conflictErr *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, errB, &conflictErr,
		"writer B (second acquirer) must return MemoryRevisionIDConflictError")

	// Exactly one row must exist.
	var count int64
	require.NoError(t,
		store.s.db.Raw("SELECT COUNT(*) FROM memories WHERE id = ?", id).Scan(&count).Error)
	assert.Equal(t, int64(1), count, "exactly one row must exist after conflict")
}

// TestPgPutMemory_AuthorizedPredecessorConflict_IsNotCreateOnly verifies that an
// AuthorizedPredecessor conflict stays the bare sentinel and is not classified as create-only,
// so REST can tell the two apart. CreateOnly is left false: this is the non-idempotent path.
func TestPgPutMemory_AuthorizedPredecessorConflict_IsNotCreateOnly(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "predecessor-conflict"}

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"existing": true}, MemoryKind: "default/v1",
		})
		return err
	}))

	req := baseIdempotencyReq(ns, "k1", uuid.New())
	// The caller believed no active row existed, but one does.
	req.AuthorizedPredecessor = &registryepisodic.MemoryPredecessorExpectation{Exists: false}
	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.PutMemory(txCtx, req)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict)
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.False(t, errors.As(err, &createOnly), "an AuthorizedPredecessor conflict must not be classified as create-only")
}
