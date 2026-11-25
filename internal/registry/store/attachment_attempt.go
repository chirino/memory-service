package store

import (
	"context"

	"github.com/google/uuid"
)

// MarkAttachmentAttemptFailed marks the row created by one attempt as failed. It
// runs in its own write transaction on a context that the client disconnect
// cannot cancel, because the failure is exactly what a disconnect causes.
//
// It reports whether a row changed. false is not an error: the attempt already
// finished (a finalize whose acknowledgment was lost), or the row was deleted or
// replaced under the same ID, and in both cases the row must stay as it is.
func MarkAttachmentAttemptFailed(ctx context.Context, store MemoryStore, userID string, attachmentID, attemptID uuid.UUID) (bool, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attachmentBlobCleanupEnqueueTimeout)
	defer cancel()
	var changed bool
	err := store.InWriteTx(cleanupCtx, func(txCtx context.Context) error {
		// The callback can be replayed, so it must not keep a result from an
		// aborted attempt.
		changed = false
		var err error
		changed, err = store.MarkAttachmentFailed(txCtx, userID, attachmentID, attemptID)
		return err
	})
	return changed, err
}
