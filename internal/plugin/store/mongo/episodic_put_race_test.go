//go:build !nomongo

package mongo

import (
	"context"
	"sync/atomic"
	"testing"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestMongoPutMemory_CreateOnlyConcurrentCreationNotArchived audits the create-only race
// found in Postgres: a create-only writer that saw no active row must not archive a
// concurrently committed winner. Mongo session transactions use snapshot isolation, so the
// loser's insert hits a WriteConflict, the callback replays, and the replay reports
// CreateOnlyConflictError. The hook pauses only the first caller, deterministically.
func TestMongoPutMemory_CreateOnlyConcurrentCreationNotArchived(t *testing.T) {
	store, ctx := setupMongoIdempotencyStore(t)
	ns := []string{"user", "alice", "create-only-race"}
	const key = "same-key"

	paused := make(chan struct{})
	resume := make(chan struct{})
	var calls atomic.Int32
	store.beforeMemoryInsert = func() error {
		if calls.Add(1) == 1 {
			close(paused)
			<-resume
		}
		return nil
	}
	t.Cleanup(func() { store.beforeMemoryInsert = nil })

	idLoser, idWinner := uuid.New(), uuid.New()
	loserReq := baseMongoIdempotencyReq(ns, key, idLoser)
	loserReq.CreateOnly = true
	loserDone := make(chan error, 1)
	go func() {
		loserDone <- store.InWriteTx(ctx, func(wctx context.Context) error {
			_, err := store.PutMemory(wctx, loserReq)
			return err
		})
	}()
	<-paused // loser saw no active row and archived nothing

	winnerReq := baseMongoIdempotencyReq(ns, key, idWinner)
	winnerReq.CreateOnly = true
	require.NoError(t, store.InWriteTx(ctx, func(wctx context.Context) error {
		_, err := store.PutMemory(wctx, winnerReq)
		return err
	}))
	close(resume)

	var createOnly *registryepisodic.CreateOnlyConflictError
	require.ErrorAs(t, <-loserDone, &createOnly)

	count, err := store.col.CountDocuments(ctx, bson.M{"namespace": mustEncodeNS(t, ns), "key": key})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "no second revision may exist")
	var winner memoryDoc
	require.NoError(t, store.col.FindOne(ctx, bson.M{"_id": idWinner.String()}).Decode(&winner))
	assert.Nil(t, winner.ArchivedAt, "the winner must remain active")
	assert.Equal(t, int64(1), winner.Revision, "the winner must not have been archived")
}

func mustEncodeNS(t *testing.T, ns []string) string {
	t.Helper()
	encoded, err := encodeNS(ns)
	require.NoError(t, err)
	return encoded
}
