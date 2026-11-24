//go:build !nomongo

package mongo

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Real MongoDB replica set (Testcontainers). The hook pauses the first creator
// after its lookup found no row and before its insert. The second creator
// commits meanwhile, so the paused insert hits a WriteConflict and
// WithTransaction replays the callback. The test counts callback runs to prove
// the replay really happened, and checks that the replay reports the winner.
func TestMongoCreateAttachmentWithIDReplaysAfterLosingRace(t *testing.T) {
	_, store, ctx := setupTestMongoStore(t, false)
	user, id := "user-"+uuid.NewString(), uuid.New()

	lookedUp := make(chan struct{})
	resume := make(chan struct{})
	var hookCalls atomic.Int32
	store.afterAttachmentCreateLookup = func() error {
		// Only the first lookup that found no row pauses; the replay finds the
		// committed row and never reaches the hook.
		if hookCalls.Add(1) == 1 {
			close(lookedUp)
			<-resume
		}
		return nil
	}
	t.Cleanup(func() { store.afterAttachmentCreateLookup = nil })

	var callbackRuns atomic.Int32
	type outcome struct {
		created *registrystore.CreateAttachmentResult
		err     error
	}
	loserDone := make(chan outcome, 1)
	go func() {
		var out outcome
		out.err = store.InWriteTx(ctx, func(txCtx context.Context) error {
			callbackRuns.Add(1)
			var err error
			out.created, err = store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading"})
			return err
		})
		loserDone <- out
	}()

	select {
	case <-lookedUp:
	case <-time.After(30 * time.Second):
		t.Fatal("first creator never reached the post-lookup hook")
	}

	var winner *registrystore.CreateAttachmentResult
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		winner, err = store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		return err
	}))
	require.True(t, winner.Created, "the unpaused creator wins")

	close(resume)
	var loser outcome
	select {
	case loser = <-loserDone:
	case <-time.After(60 * time.Second):
		t.Fatal("paused creator did not finish")
	}
	require.NoError(t, loser.err)
	require.EqualValues(t, 2, callbackRuns.Load(), "WriteConflict must replay the losing callback exactly once")
	require.False(t, loser.created.Created, "the replay must report the existing row, not claim it")
	require.Equal(t, *winner.Attachment.AttemptID, *loser.created.Attachment.AttemptID, "the replay must return the winner's attempt ID")

	count, err := store.attachments().CountDocuments(ctx, map[string]any{"_id": uuidToStr(id)})
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "exactly one row exists")
}
