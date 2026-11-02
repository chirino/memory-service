//go:build !nosqlite && sqlite_fts5

package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSQLiteIdempotencyStore creates a fresh SQLite database and returns a raw
// *sqliteEpisodicStore for white-box access plus a context.
func setupSQLiteIdempotencyStore(t *testing.T) (*sqliteEpisodicStore, context.Context) {
	t.Helper()
	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "idem.db"),
		DatastoreMigrateAtStart: true,
		EncryptionDBDisabled:    true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	loader, err := registryepisodic.Select("sqlite")
	require.NoError(t, err)
	loaded, err := loader(ctx)
	require.NoError(t, err)
	return loaded.(*sqliteEpisodicStore), ctx
}

// baseReq builds a minimal valid PutMemoryRequest with a caller-supplied RevisionID.
func baseSQLiteIdempotencyReq(ns []string, key string, id uuid.UUID) registryepisodic.PutMemoryRequest {
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

// TestSQLiteLookupMemoryByID_NotFound verifies (nil, nil) when no row exists.
func TestSQLiteLookupMemoryByID_NotFound(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	id := uuid.New()
	req := baseSQLiteIdempotencyReq([]string{"user", "alice", "notfound"}, "k1", id)

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		assert.Nil(t, result, "must return nil when no row exists")
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_ExactRetry verifies ExactRetry=true for a matching stored row.
func TestSQLiteLookupMemoryByID_ExactRetry(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "exactretry"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

	var first *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		first, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, first)
	assert.False(t, first.ExactRetry)

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.ExactRetry)
		assert.Equal(t, id, result.ID)
		assert.Equal(t, first.Revision, result.Revision)
		// Timestamps must be identical (spec: response stability).
		assert.Equal(t,
			first.CreatedAt.UTC().Truncate(time.Second),
			result.CreatedAt.UTC().Truncate(time.Second),
			"CreatedAt must be stable across retries")
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_NamespaceMismatch verifies MemoryRevisionIDConflictError
// when the caller uses an ID belonging to a different namespace.
func TestSQLiteLookupMemoryByID_NamespaceMismatch(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	id := uuid.New()
	req := baseSQLiteIdempotencyReq([]string{"user", "alice", "ns-mismatch"}, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqBob := baseSQLiteIdempotencyReq([]string{"user", "bob", "ns-mismatch"}, "k1", id)
	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, reqBob)
		assert.Nil(t, result)
		var conflictErr *registrystore.MemoryRevisionIDConflictError
		require.ErrorAs(t, err, &conflictErr)
		assert.Equal(t, id.String(), conflictErr.RevisionID)
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_KeyMismatch verifies conflict when key differs.
func TestSQLiteLookupMemoryByID_KeyMismatch(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "key-mismatch"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqOtherKey := baseSQLiteIdempotencyReq(ns, "different-key", id)
	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, reqOtherKey)
		assert.Nil(t, result)
		var conflictErr *registrystore.MemoryRevisionIDConflictError
		require.ErrorAs(t, err, &conflictErr)
		assert.Equal(t, id.String(), conflictErr.RevisionID)
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_ValueMismatch verifies conflict when value differs.
func TestSQLiteLookupMemoryByID_ValueMismatch(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "value-mismatch"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqDiff := baseSQLiteIdempotencyReq(ns, "k1", id)
	reqDiff.Value = map[string]interface{}{"data": "different"}

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, reqDiff)
		assert.Nil(t, result)
		var conflictErr *registrystore.MemoryRevisionIDConflictError
		require.ErrorAs(t, err, &conflictErr)
		assert.Equal(t, id.String(), conflictErr.RevisionID)
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_KindMismatch verifies conflict when memory_kind differs.
func TestSQLiteLookupMemoryByID_KindMismatch(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "kind-mismatch"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqDiff := baseSQLiteIdempotencyReq(ns, "k1", id)
	reqDiff.MemoryKind = "profile/v1"

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, reqDiff)
		assert.Nil(t, result)
		var conflictErr *registrystore.MemoryRevisionIDConflictError
		require.ErrorAs(t, err, &conflictErr)
		assert.Equal(t, id.String(), conflictErr.RevisionID)
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_ArchivedRow verifies NotFoundError for deleted_reason=1.
func TestSQLiteLookupMemoryByID_ArchivedRow(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "archived-row"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		return store.ArchiveMemory(wctx, ns, "k1", nil)
	}))

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, req)
		assert.Nil(t, result)
		var notFound *registrystore.NotFoundError
		require.ErrorAs(t, err, &notFound, "archived row must return NotFoundError")
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_ExpiredRow verifies NotFoundError for deleted_reason=2.
func TestSQLiteLookupMemoryByID_ExpiredRow(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "expired-row"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 1

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))
	// Force-expire via raw SQL.
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		return store.writeDBFor(wctx, "force expire").Exec(
			`UPDATE memories SET archived_at = ?, archived_at_ns = ?, deleted_reason = 2 WHERE id = ?`,
			time.Now(), id.String(),
		).Error
	}))

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, req)
		assert.Nil(t, result)
		var notFound *registrystore.NotFoundError
		require.ErrorAs(t, err, &notFound, "expired row (deleted_reason=2) must return NotFoundError")
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_SupersededNotEvicted verifies ExactRetry=true for
// a superseded row (deleted_reason=0) that has not yet been evicted.
func TestSQLiteLookupMemoryByID_SupersededNotEvicted(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "superseded-notevicted"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))
	// Supersede with a second write.
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace:  ns,
			Key:        "k1",
			Value:      map[string]interface{}{"rev": 2},
			MemoryKind: "default/v1",
		})
		return err
	}))

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		require.NotNil(t, result, "superseded but not evicted must still match")
		assert.True(t, result.ExactRetry)
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_TTLMismatch verifies conflict when TTL differs.
func TestSQLiteLookupMemoryByID_TTLMismatch(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "ttl-mismatch"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 3600

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	reqDiff := baseSQLiteIdempotencyReq(ns, "k1", id)
	reqDiff.TTLSeconds = 7200

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, reqDiff)
		assert.Nil(t, result)
		var conflictErr *registrystore.MemoryRevisionIDConflictError
		require.ErrorAs(t, err, &conflictErr)
		assert.Equal(t, id.String(), conflictErr.RevisionID)
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_NilIndexEqualsEmpty verifies nil index matches stored {}.
func TestSQLiteLookupMemoryByID_NilIndexEqualsEmpty(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "nil-index"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)
	req.Index = nil

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		require.NotNil(t, result, "nil index must match stored {}")
		assert.True(t, result.ExactRetry)
		return nil
	}))
}

// ──────────────────────────────────────────────────────────────────────────────
// PutMemory — caller-supplied ID and timestamp
// ──────────────────────────────────────────────────────────────────────────────

// TestSQLitePutMemory_CallerSuppliedID verifies the stored PK equals RevisionID.
func TestSQLitePutMemory_CallerSuppliedID(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	id := uuid.New()
	req := baseSQLiteIdempotencyReq([]string{"user", "alice", "caller-id"}, "k1", id)

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

// TestSQLitePutMemory_UTCTimestamp verifies that the stored CreatedAt round-trips
// through UTC (spec: "needs .UTC() but no truncation for SQLite").
func TestSQLitePutMemory_UTCTimestamp(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "utc-ts"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

	var writeResult *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		writeResult, err = store.PutMemory(wctx, req)
		return err
	}))

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		lookup, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		require.NotNil(t, lookup)

		// SQLite reads timestamps back as UTC (time.Unix(s,ns).UTC()).
		assert.Equal(t, time.UTC, lookup.CreatedAt.Location(), "stored CreatedAt must be UTC")
		// The write result and stored row must agree on instant.
		assert.Equal(t,
			writeResult.CreatedAt.UTC().Truncate(time.Second),
			lookup.CreatedAt.UTC().Truncate(time.Second),
			"PutMemory result and lookup must agree on CreatedAt")
		return nil
	}))
}

// TestSQLitePutMemory_ExactRetryNoNewRow verifies N retries leave exactly 1 row
// and 1 active row, and indexed_at remains NULL.
func TestSQLitePutMemory_ExactRetryNoNewRow(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "no-new-row"}
	nsEncoded := "user\x1ealice\x1eno-new-row"
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	for i := 0; i < 3; i++ {
		require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
			result, err := store.LookupMemoryByID(rctx, id, req)
			require.NoError(t, err, "retry %d must not error", i)
			require.NotNil(t, result)
			assert.True(t, result.ExactRetry)
			return nil
		}))
	}

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		var totalCount int64
		require.NoError(t, store.dbFor(rctx).Raw("SELECT COUNT(*) FROM memories WHERE id = ?", id.String()).Scan(&totalCount).Error)
		assert.Equal(t, int64(1), totalCount, "exactly one row must exist after retries")

		var activeCount int64
		require.NoError(t, store.dbFor(rctx).Raw(
			"SELECT COUNT(*) FROM memories WHERE namespace = ? AND key = ? AND archived_at IS NULL",
			nsEncoded, "k1").Scan(&activeCount).Error)
		assert.Equal(t, int64(1), activeCount, "exactly one active row must exist")

		var indexedAtIsNull bool
		require.NoError(t, store.dbFor(rctx).Raw("SELECT indexed_at IS NULL FROM memories WHERE id = ?", id.String()).Scan(&indexedAtIsNull).Error)
		assert.True(t, indexedAtIsNull, "indexed_at must remain NULL after retries (not re-triggered)")
		return nil
	}))
}

// TestSQLitePutMemory_ExactRetryPredecessorArchivedOnce verifies the predecessor
// is archived exactly once across retries.
func TestSQLitePutMemory_ExactRetryPredecessorArchivedOnce(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "archive-once"}
	nsEncoded := "user\x1ealice\x1earchive-once"

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace:  ns,
			Key:        "k1",
			Value:      map[string]interface{}{"rev": 1},
			MemoryKind: "default/v1",
		})
		return err
	}))

	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	for i := 0; i < 3; i++ {
		require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
			result, err := store.LookupMemoryByID(rctx, id, req)
			require.NoError(t, err)
			assert.True(t, result.ExactRetry)
			return nil
		}))
	}

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		var archivedCount int64
		require.NoError(t, store.dbFor(rctx).Raw(
			`SELECT COUNT(*) FROM memories WHERE namespace = ? AND key = ? AND archived_at IS NOT NULL AND deleted_reason = 0`,
			nsEncoded, "k1").Scan(&archivedCount).Error)
		assert.Equal(t, int64(1), archivedCount, "predecessor must be archived exactly once")
		return nil
	}))
}

// ──────────────────────────────────────────────────────────────────────────────
// Create-only mode
// ──────────────────────────────────────────────────────────────────────────────

// TestSQLitePutMemory_CreateOnly_NoActiveRow verifies create-only succeeds with revision=1.
func TestSQLitePutMemory_CreateOnly_NoActiveRow(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	id := uuid.New()
	req := baseSQLiteIdempotencyReq([]string{"user", "alice", "create-only-new"}, "k1", id)
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

// TestSQLitePutMemory_CreateOnly_ExactRetry verifies ExactRetry=true on exact retry.
func TestSQLitePutMemory_CreateOnly_ExactRetry(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	id := uuid.New()
	req := baseSQLiteIdempotencyReq([]string{"user", "alice", "create-only-retry"}, "k1", id)
	req.CreateOnly = true

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))
	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.ExactRetry)
		return nil
	}))
}

// TestSQLitePutMemory_CreateOnly_ActiveRowConflict verifies conflict when active row exists.
func TestSQLitePutMemory_CreateOnly_ActiveRowConflict(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-conflict"}

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"existing": true}, MemoryKind: "default/v1",
		})
		return err
	}))

	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)
	req.CreateOnly = true

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict)
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.ErrorAs(t, err, &createOnly, "a create-only conflict must be distinguishable from the numeric conflicts")

	// The rejected write has no side effects: no row for the new ID, and the existing
	// active row is still the only active row, unarchived and at revision 1.
	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		var byID int64
		require.NoError(t, store.dbFor(rctx).Raw("SELECT COUNT(*) FROM memories WHERE id = ?", id.String()).Scan(&byID).Error)
		assert.Equal(t, int64(0), byID, "no row may be created for the rejected revision_id")
		var rows []struct {
			Revision   int64
			ArchivedAt *time.Time
		}
		require.NoError(t, store.dbFor(rctx).Raw(
			"SELECT revision, archived_at FROM memories WHERE key = 'k1'").Scan(&rows).Error)
		require.Len(t, rows, 1, "exactly the original row must exist")
		assert.Equal(t, int64(1), rows[0].Revision)
		assert.Nil(t, rows[0].ArchivedAt, "the existing active row must not be archived")
		return nil
	}))
}

// ──────────────────────────────────────────────────────────────────────────────
// ExpectedRevisionID mode
// ──────────────────────────────────────────────────────────────────────────────

// TestSQLitePutMemory_ExpectedRevisionID_Match verifies update succeeds when UUID matches.
func TestSQLitePutMemory_ExpectedRevisionID_Match(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
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
	req := baseSQLiteIdempotencyReq(ns, "k1", newID)
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

// TestSQLitePutMemory_ExpectedRevisionID_Mismatch verifies ExpectedActiveRevisionIDMismatchError.
func TestSQLitePutMemory_ExpectedRevisionID_Mismatch(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-mismatch"}

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"v": 1}, MemoryKind: "default/v1",
		})
		return err
	}))

	newID := uuid.New()
	wrongExpected := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &wrongExpected

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	require.ErrorAs(t, err, &mismatchErr)
	assert.Equal(t, wrongExpected.String(), mismatchErr.ExpectedID)
}

// TestSQLitePutMemory_ExpectedRevisionID_NoActiveRow verifies mismatch error when no active row.
func TestSQLitePutMemory_ExpectedRevisionID_NoActiveRow(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "exprid-norow"}
	newID := uuid.New()
	someExpected := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &someExpected

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	require.ErrorAs(t, err, &mismatchErr)
}

// TestSQLitePutMemory_ExpectedRevisionID_ExactRetry verifies exact retry short-circuits
// before the ExpectedRevisionID check.
func TestSQLitePutMemory_ExpectedRevisionID_ExactRetry(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
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
	req := baseSQLiteIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &activeID

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, newID, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.ExactRetry)
		return nil
	}))
}

// TestSQLitePutMemory_ExpectedRevisionID_NumericMismatch verifies numeric check fires
// after UUID check passes.
func TestSQLitePutMemory_ExpectedRevisionID_NumericMismatch(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
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
	req := baseSQLiteIdempotencyReq(ns, "k1", newID)
	req.ExpectedRevisionID = &activeID
	req.ExpectedRevision = &wrongNumeric

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict)
	var mismatchErr *registrystore.ExpectedActiveRevisionIDMismatchError
	assert.False(t, errors.As(err, &mismatchErr))
}

// ──────────────────────────────────────────────────────────────────────────────
// Eviction and concurrency
// ──────────────────────────────────────────────────────────────────────────────

// TestSQLitePutMemory_EvictedRowTreatedAsNewWrite verifies that after eviction,
// the same UUID is treated as a new write.
func TestSQLitePutMemory_EvictedRowTreatedAsNewWrite(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "eviction"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

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
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		return store.writeDBFor(wctx, "mark indexed").Exec(
			`UPDATE memories SET indexed_at = ?, indexed_at_ns = ? WHERE id = ?`,
			time.Now(), id.String(),
		).Error
	}))
	// Evict — HardDeleteEvictableUpdates requires InWriteTx scope on SQLite.
	var n int64
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		n, err = store.HardDeleteEvictableUpdates(wctx, 10)
		return err
	}))
	assert.Equal(t, int64(1), n)

	// LookupMemoryByID must return (nil, nil) after eviction.
	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		lookup, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		assert.Nil(t, lookup, "evicted row must appear as not found")
		return nil
	}))

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

// TestSQLitePutMemory_ConcurrentSameID verifies that N goroutines writing the
// same RevisionID produce exactly one ExactRetry=false (writeMu serializes all).
func TestSQLitePutMemory_ConcurrentSameID(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "concurrent"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)

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
	assert.Equal(t, 0, failures, "no goroutine must error (writeMu serializes)")
	assert.Equal(t, 1, creators, "exactly one goroutine must be the creator")
	assert.Equal(t, n-1, retries, "all others must detect ExactRetry=true")

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		var count int64
		require.NoError(t, store.dbFor(rctx).Raw("SELECT COUNT(*) FROM memories WHERE id = ?", id.String()).Scan(&count).Error)
		assert.Equal(t, int64(1), count, "exactly one row must exist")
		return nil
	}))
}

// TestSQLiteLookupMemoryByID_TTLMatch verifies ExactRetry=true when TTL matches.
// This is the positive counterpart to TestSQLiteLookupMemoryByID_TTLMismatch.
func TestSQLiteLookupMemoryByID_TTLMatch(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "ttl-match"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 3600

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	}))

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		result, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.ExactRetry, "same TTL must be recognized as exact retry")
		return nil
	}))
}

// TestSQLitePutMemory_ExactRetryExpiresAtStable verifies that ExpiresAt on the exact-retry
// result equals the value produced by the first write.
// Spec: "stored createdAt and expiresAt equal the first result".
func TestSQLitePutMemory_ExactRetryExpiresAtStable(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "expires-stable"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)
	req.TTLSeconds = 7200

	var first *registryepisodic.MemoryWriteResult
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		var err error
		first, err = store.PutMemory(wctx, req)
		return err
	}))
	require.NotNil(t, first)
	require.NotNil(t, first.ExpiresAt, "first write with TTL must set ExpiresAt")

	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		retry, err := store.LookupMemoryByID(rctx, id, req)
		require.NoError(t, err)
		require.NotNil(t, retry)
		assert.True(t, retry.ExactRetry)
		require.NotNil(t, retry.ExpiresAt, "retry result must carry ExpiresAt")
		assert.Equal(t,
			first.ExpiresAt.UTC().Truncate(time.Second),
			retry.ExpiresAt.UTC().Truncate(time.Second),
			"ExpiresAt must be stable across retries")
		return nil
	}))
}

// TestSQLitePutMemory_CreateOnly_NumericExpectedRevisionStillRuns verifies that when
// create-only mode passes (no active row), the numeric ExpectedRevision check still fires.
// Spec: "Create-only write with revision: 1 does not suppress the existing ExpectedRevision
// numeric check."
func TestSQLitePutMemory_CreateOnly_NumericExpectedRevisionStillRuns(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-numeric"}
	id := uuid.New()
	req := baseSQLiteIdempotencyReq(ns, "k1", id)
	req.CreateOnly = true
	wrongRevision := int64(99) // no active row exists, so any numeric check fails
	req.ExpectedRevision = &wrongRevision

	err := store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, req)
		return err
	})
	// Create-only check passed (no active row), but numeric check fires and fails.
	require.ErrorIs(t, err, registryepisodic.ErrMemoryRevisionConflict,
		"numeric ExpectedRevision check must fire after create-only check passes")
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.False(t, errors.As(err, &createOnly), "a numeric conflict must not be classified as create-only")

	// No row must have been created.
	require.NoError(t, store.InReadTx(ctx, func(rctx context.Context) error {
		var count int64
		require.NoError(t, store.dbFor(rctx).Raw(
			"SELECT COUNT(*) FROM memories WHERE id = ?", id.String(),
		).Scan(&count).Error)
		assert.Equal(t, int64(0), count, "no row must exist when numeric check fails")
		return nil
	}))
}

// TestSQLitePutMemory_AuthorizedPredecessorConflict_IsNotCreateOnly verifies that the
// AuthorizedPredecessor conflict stays the bare sentinel and is not classified as create-only,
// so REST can tell the two apart. CreateOnly is left false: this is the non-idempotent path.
func TestSQLitePutMemory_AuthorizedPredecessorConflict_IsNotCreateOnly(t *testing.T) {
	t.Parallel()
	store, ctx := setupSQLiteIdempotencyStore(t)
	ns := []string{"user", "alice", "predecessor-conflict"}

	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "k1", Value: map[string]interface{}{"existing": true}, MemoryKind: "default/v1",
		})
		return err
	}))

	req := baseSQLiteIdempotencyReq(ns, "k1", uuid.New())
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
