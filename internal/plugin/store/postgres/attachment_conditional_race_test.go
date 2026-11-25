//go:build !nopostgresql

package postgres

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

// Real PostgreSQL (Testcontainers). The hook pauses the first creator between
// its lookup and its INSERT, so the ON CONFLICT DO NOTHING branch runs every
// time instead of only when goroutines happen to interleave.
func TestPostgresCreateAttachmentWithIDLosesRaceAfterLookup(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	user, id := "user-"+uuid.NewString(), uuid.New()

	lookedUp := make(chan struct{})
	resume := make(chan struct{})
	// Only the first caller pauses. sync.Once would block the second caller too.
	var paused atomic.Bool
	store.afterAttachmentCreateLookup = func() error {
		if paused.CompareAndSwap(false, true) {
			close(lookedUp)
			<-resume
		}
		return nil
	}
	t.Cleanup(func() { store.afterAttachmentCreateLookup = nil })

	type outcome struct {
		created    *registrystore.CreateAttachmentResult
		reread     *model.Attachment
		finalized  *model.Attachment
		err        error
		txContinue bool
	}
	loserDone := make(chan outcome, 1)
	key := "key-" + uuid.NewString()
	go func() {
		var out outcome
		out.err = store.InWriteTx(ctx, func(txCtx context.Context) error {
			var err error
			out.created, err = store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading"})
			if err != nil {
				return err
			}
			// After the lost race the transaction must still be usable (no 25P02).
			if out.reread, err = store.GetAttachment(txCtx, user, "", id); err != nil {
				return err
			}
			out.finalized, err = store.FinalizeAttachment(txCtx, user, id, *out.created.Attachment.AttemptID,
				registrystore.AttachmentFinalize{StorageKey: key, Size: 1, SHA256: "x"})
			out.txContinue = err == nil
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
		winner, err = store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading", ExpiresAt: ptrTime(time.Now().Add(time.Hour))})
		return err
	}))
	require.True(t, winner.Created, "the unpaused creator wins")

	close(resume)
	var loser outcome
	select {
	case loser = <-loserDone:
	case <-time.After(30 * time.Second):
		t.Fatal("paused creator did not finish")
	}
	require.NoError(t, loser.err)
	require.False(t, loser.created.Created, "the paused creator lost the race and must not claim the row")
	require.Equal(t, *winner.Attachment.AttemptID, *loser.created.Attachment.AttemptID, "the loser must report the winner's attempt ID")
	require.True(t, loser.txContinue, "the transaction must stay usable after the lost race")
	require.Equal(t, *winner.Attachment.AttemptID, *loser.reread.AttemptID)
	require.Equal(t, "ready", loser.finalized.Status)

	var stored *model.Attachment
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		stored, err = store.GetAttachment(txCtx, user, "", id)
		return err
	}))
	require.Equal(t, *winner.Attachment.AttemptID, *stored.AttemptID)
	require.Equal(t, key, *stored.StorageKey)
}

func ptrTime(t time.Time) *time.Time { return &t }
