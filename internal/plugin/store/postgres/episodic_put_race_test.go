//go:build !nopostgresql

package postgres

import (
	"context"
	"sync/atomic"
	"testing"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pgMemoryRowState struct {
	ID         uuid.UUID `gorm:"column:id"`
	Revision   int64     `gorm:"column:revision"`
	Archived   bool      `gorm:"column:archived"`
	WriterName string    `gorm:"column:writer"`
}

func pgKeyRows(t *testing.T, store *postgresEpisodicStore, ns []string, key string) []pgMemoryRowState {
	t.Helper()
	nsEncoded, err := store.encodeNS(ns)
	require.NoError(t, err)
	var rows []pgMemoryRowState
	require.NoError(t, store.s.db.Raw(`
		SELECT id, revision, archived_at IS NOT NULL AS archived, '' AS writer
		FROM memories WHERE namespace = ? AND key = ? ORDER BY created_at, id`,
		nsEncoded, key).Scan(&rows).Error)
	return rows
}

func pgOutboxCount(t *testing.T, store *postgresEpisodicStore) int64 {
	t.Helper()
	var n int64
	require.NoError(t, store.s.db.Raw("SELECT COUNT(*) FROM outbox_events").Scan(&n).Error)
	return n
}

func pgPut(ctx context.Context, store *postgresEpisodicStore, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
	var result *registryepisodic.MemoryWriteResult
	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.PutMemory(txCtx, req)
		return err
	})
	return result, err
}

// pauseFirstCheck installs putAfterRevisionCheck so that only the FIRST caller blocks
// (after its precondition checks, before its archive). Later callers pass straight
// through. It returns the channel signalled when the first caller is paused and the
// function that releases it.
func pauseFirstCheck(store *postgresEpisodicStore, t *testing.T) (checked <-chan struct{}, release func()) {
	t.Helper()
	paused := make(chan struct{})
	resume := make(chan struct{})
	var calls atomic.Int32
	store.putAfterRevisionCheck = func() {
		if calls.Add(1) == 1 {
			close(paused)
			<-resume
		}
	}
	t.Cleanup(func() { store.putAfterRevisionCheck = nil })
	return paused, func() { close(resume) }
}

// TestPostgresExpectedRevisionIDBindsArchivedPredecessor reproduces the admin-path race:
// a request carrying only expected_revision_id validates active revision A, a competing
// writer replaces A with B, and the stale request must be rejected instead of archiving B.
func TestPostgresExpectedRevisionIDBindsArchivedPredecessor(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "expected-revision-id-race"}
	const key = "same-key"

	idA := uuid.New()
	_, err := pgPut(ctx, store, registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"writer": "A"}, MemoryKind: "default/v1", RevisionID: &idA,
	})
	require.NoError(t, err)

	checked, release := pauseFirstCheck(store, t)
	idStale := uuid.New()
	staleDone := make(chan error, 1)
	go func() {
		// Admin-style request: only the UUID precondition, no ExpectedRevision / AuthorizedPredecessor.
		_, err := pgPut(ctx, store, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: key, Value: map[string]interface{}{"writer": "stale"}, MemoryKind: "default/v1",
			RevisionID: &idStale, ExpectedRevisionID: &idA,
		})
		staleDone <- err
	}()
	<-checked // stale request has validated A and is paused before its archive

	idB := uuid.New()
	_, err = pgPut(ctx, store, registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"writer": "B"}, MemoryKind: "default/v1", RevisionID: &idB,
	})
	require.NoError(t, err)
	release()

	var mismatch *registrystore.ExpectedActiveRevisionIDMismatchError
	staleErr := <-staleDone
	require.ErrorAs(t, staleErr, &mismatch, "stale request must report an expected_revision_id mismatch")
	assert.Equal(t, idA.String(), mismatch.ExpectedID)
	assert.Equal(t, idB.String(), mismatch.ActualID, "the mismatch must name the current active revision")

	rows := pgKeyRows(t, store, ns, key)
	require.Len(t, rows, 2, "only A and B may exist; the failed request must leave no row")
	assert.Equal(t, idA, rows[0].ID)
	assert.True(t, rows[0].Archived, "A is archived by B")
	assert.Equal(t, idB, rows[1].ID)
	assert.False(t, rows[1].Archived, "B must stay active; the stale request must not archive it")
	assert.Zero(t, pgOutboxCount(t, store), "the store layer emits no outbox events and a failed write leaves none")

	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		active, err := store.GetMemory(txCtx, ns, key, registryepisodic.ArchiveFilterExclude)
		require.NoError(t, err)
		assert.Equal(t, "B", active.Value["writer"])
		return nil
	}))
}

// TestPostgresExpectedRevisionIDMatchAndMismatch preserves the non-racing behavior.
func TestPostgresExpectedRevisionIDMatchAndMismatch(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "expected-revision-id-basic"}
	const key = "k"

	idA := uuid.New()
	_, err := pgPut(ctx, store, registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"writer": "A"}, MemoryKind: "default/v1", RevisionID: &idA,
	})
	require.NoError(t, err)

	wrong := uuid.New()
	_, err = pgPut(ctx, store, registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"writer": "x"}, MemoryKind: "default/v1",
		RevisionID: ptrUUID(uuid.New()), ExpectedRevisionID: &wrong,
	})
	var mismatch *registrystore.ExpectedActiveRevisionIDMismatchError
	require.ErrorAs(t, err, &mismatch)

	idB := uuid.New()
	res, err := pgPut(ctx, store, registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"writer": "B"}, MemoryKind: "default/v1",
		RevisionID: &idB, ExpectedRevisionID: &idA,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.Revision, "B follows A's revision")

	rows := pgKeyRows(t, store, ns, key)
	require.Len(t, rows, 2)
	assert.True(t, rows[0].Archived)
	assert.False(t, rows[1].Archived)
}

// TestPostgresCreateOnlyDoesNotArchiveConcurrentCreation reproduces the create-only race:
// two create-only requests with different revision IDs both see no active row; one
// commits first; the other must report CreateOnlyConflictError and must not archive the winner.
func TestPostgresCreateOnlyDoesNotArchiveConcurrentCreation(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-race"}
	const key = "same-key"

	checked, release := pauseFirstCheck(store, t)
	idLoser := uuid.New()
	loserDone := make(chan error, 1)
	go func() {
		_, err := pgPut(ctx, store, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: key, Value: map[string]interface{}{"writer": "loser"}, MemoryKind: "default/v1",
			RevisionID: &idLoser, CreateOnly: true,
		})
		loserDone <- err
	}()
	<-checked // loser saw no active row and is paused before its archive/insert

	idWinner := uuid.New()
	winner, err := pgPut(ctx, store, registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"writer": "winner"}, MemoryKind: "default/v1",
		RevisionID: &idWinner, CreateOnly: true,
	})
	require.NoError(t, err)
	assert.False(t, winner.ExactRetry)
	assert.Equal(t, int64(1), winner.Revision)
	release()

	var createOnly *registryepisodic.CreateOnlyConflictError
	loserErr := <-loserDone
	require.ErrorAs(t, loserErr, &createOnly, "the losing create-only request must report CreateOnlyConflictError")
	require.ErrorIs(t, loserErr, registryepisodic.ErrMemoryRevisionConflict)
	var mismatch *registrystore.ExpectedActiveRevisionIDMismatchError
	assert.NotErrorAs(t, loserErr, &mismatch)

	rows := pgKeyRows(t, store, ns, key)
	require.Len(t, rows, 1, "no second revision may exist")
	assert.Equal(t, idWinner, rows[0].ID)
	assert.False(t, rows[0].Archived, "the winner must remain active")
	assert.Equal(t, int64(1), rows[0].Revision, "the winner must not have been archived (revision bump)")
	assert.Zero(t, pgOutboxCount(t, store))
}

// TestPostgresCreateOnlyBasic preserves the non-racing create-only behavior and shows that
// non-create-only updates still archive the previous active row.
func TestPostgresCreateOnlyBasic(t *testing.T) {
	store, ctx := setupPgIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-basic"}
	const key = "k"

	id1 := uuid.New()
	req1 := registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"v": "1"}, MemoryKind: "default/v1", RevisionID: &id1, CreateOnly: true,
	}
	res, err := pgPut(ctx, store, req1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Revision)

	// Exact retry is unchanged.
	retry, err := pgPut(ctx, store, req1)
	require.NoError(t, err)
	assert.True(t, retry.ExactRetry)

	// A different ID create-only against an active row conflicts and leaves it untouched.
	id2 := uuid.New()
	_, err = pgPut(ctx, store, registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"v": "2"}, MemoryKind: "default/v1", RevisionID: &id2, CreateOnly: true,
	})
	var createOnly *registryepisodic.CreateOnlyConflictError
	require.ErrorAs(t, err, &createOnly)

	// A plain update still archives the previous row.
	_, err = pgPut(ctx, store, registryepisodic.PutMemoryRequest{
		Namespace: ns, Key: key, Value: map[string]interface{}{"v": "3"}, MemoryKind: "default/v1",
	})
	require.NoError(t, err)
	rows := pgKeyRows(t, store, ns, key)
	require.Len(t, rows, 2)
	assert.True(t, rows[0].Archived)
	assert.False(t, rows[1].Archived)
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }
