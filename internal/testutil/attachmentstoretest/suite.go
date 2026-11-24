// Package attachmentstoretest checks the conditional attachment operations
// (CreateAttachmentWithID, FinalizeAttachment, MarkAttachmentFailed and
// DeleteExpiredAttachment) identically on every datastore. Callers supply a
// store backed by a real database and a context carrying its configuration.
package attachmentstoretest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var errRollback = errors.New("rollback requested by test")

// SetArchived flips archived_at on an attachment row directly in the database.
// Nothing in production archives attachments, so the archived-row scenarios
// need the datastore itself to create that state.
type SetArchived func(t *testing.T, attachmentID uuid.UUID, archived bool)

// Run executes every conditional attachment scenario against store.
func Run(t *testing.T, store registrystore.MemoryStore, ctx context.Context, setArchived SetArchived) {
	s := &suite{store: store, ctx: ctx, setArchived: setArchived}
	for name, fn := range map[string]func(*testing.T){
		"CreateWithID": s.createWithID,
		"DuplicateCreateReturnsExistingUnchanged":    s.duplicateCreate,
		"ForeignOwnerLookupIsNotFound":               s.foreignOwner,
		"CreateRejectsInvalidInput":                  s.invalidCreate,
		"FinalizeSucceedsForOriginalAttempt":         s.finalizeSucceeds,
		"FinalizeRejectsWrongAttemptOwnerOrStatus":   s.finalizeRejects,
		"MarkFailedFollowsSameConditions":            s.markFailedConditions,
		"MarkFailedAfterFinalizeKeepsRowReady":       s.markFailedAfterFinalize,
		"OldAttemptCannotTouchReplacement":           s.replacementFence,
		"DeleteExpiredLosesToRefreshingFinalize":     s.cleanupRaceFinalize,
		"DeleteExpiredLosesToDeleteAndReuse":         s.cleanupRaceReuse,
		"DeleteExpiredConditions":                    s.deleteExpiredConditions,
		"DeleteExpiredQueuesBlobOnlyWhenDeleted":     s.deleteExpiredBlobQueue,
		"LegacyNullAttemptComparison":                s.legacyNullAttempt,
		"AdminListReturnsAttemptIDForCleanup":        s.adminListAttemptID,
		"ConcurrentCreatesProduceOneWinnerAndOneRow": s.concurrentCreate,
		"TransactionContinuesAfterLosingCreate":      s.transactionContinuesAfterDuplicate,
		"FinalizeMarkFailedAndDeleteRollBackWithTx":  s.rollback,
		"CreateAttachmentStillReturnsNoAttemptID":    s.legacyCreateUnchanged,
		"UpdateAttachmentStillWorksOnPendingWithID":  s.updateAttachmentUnchanged,
		"CreateRejectsCallerManagedFields":           s.createRejectsCallerManagedFields,
		"CreateReturnsWhatItPersisted":               s.createReturnsPersistedValues,
		"FinalizeRejectsEmptyStorageKey":             s.finalizeRejectsEmptyKey,
		"FinalizeRegistersBlobReference":             s.finalizeRegistersReference,
		"FinalizeRefusesKeyWithDeleteInProgress":     s.finalizeRefusesDeleteInProgress,
		"ArchivedRowsAreNotFinalizedOrFailed":        s.archivedRows,
		"LinkSucceedsForReadyAttachment":             s.linkReady,
		"LinkRejectsPendingAndFailedAttachments":     s.linkRejectsNotReady,
		"LinkNotReadyDoesNotMaskOtherOutcomes":       s.linkOutcomesKeepPrecedence,
		"LinkFollowsStatusProgression":               s.linkFollowsStatusProgression,
		"LinkNotReadyRollsBackWithTx":                s.linkRollsBack,
	} {
		t.Run(name, fn)
	}
}

type suite struct {
	store       registrystore.MemoryStore
	ctx         context.Context
	setArchived SetArchived
}

func past() *time.Time   { v := time.Now().Add(-time.Hour).UTC().Truncate(time.Second); return &v }
func future() *time.Time { v := time.Now().Add(time.Hour).UTC().Truncate(time.Second); return &v }
func newUser() string    { return "user-" + uuid.NewString() }
func newKey() string     { return "key-" + uuid.NewString() }

func (s *suite) write(t *testing.T, fn func(context.Context) error) error {
	t.Helper()
	return s.store.InWriteTx(s.ctx, fn)
}

func (s *suite) mustWrite(t *testing.T, fn func(context.Context) error) {
	t.Helper()
	require.NoError(t, s.write(t, fn))
}

func (s *suite) create(t *testing.T, user string, id uuid.UUID, status string, expires *time.Time) *model.Attachment {
	t.Helper()
	var res *registrystore.CreateAttachmentResult
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		res, err = s.store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: status, ExpiresAt: expires})
		return err
	})
	require.True(t, res.Created)
	require.NotNil(t, res.Attachment.AttemptID)
	return res.Attachment
}

func (s *suite) get(t *testing.T, user string, id uuid.UUID) *model.Attachment {
	t.Helper()
	var a *model.Attachment
	require.NoError(t, s.store.InReadTx(s.ctx, func(txCtx context.Context) error {
		var err error
		a, err = s.store.GetAttachment(txCtx, user, "", id)
		return err
	}))
	return a
}

func (s *suite) exists(t *testing.T, user string, id uuid.UUID) bool {
	t.Helper()
	err := s.store.InReadTx(s.ctx, func(txCtx context.Context) error {
		_, err := s.store.GetAttachment(txCtx, user, "", id)
		return err
	})
	if err == nil {
		return true
	}
	var nf *registrystore.NotFoundError
	require.ErrorAs(t, err, &nf)
	return false
}

func (s *suite) finalize(user string, id, attempt uuid.UUID, key string, expires *time.Time) (*model.Attachment, error) {
	var a *model.Attachment
	err := s.store.InWriteTx(s.ctx, func(txCtx context.Context) error {
		var err error
		a, err = s.store.FinalizeAttachment(txCtx, user, id, attempt, registrystore.AttachmentFinalize{
			StorageKey: key, Size: 5, SHA256: "abc123", ExpiresAt: expires,
		})
		return err
	})
	return a, err
}

func (s *suite) markFailed(user string, id, attempt uuid.UUID) (bool, error) {
	var changed bool
	err := s.store.InWriteTx(s.ctx, func(txCtx context.Context) error {
		var err error
		changed, err = s.store.MarkAttachmentFailed(txCtx, user, id, attempt)
		return err
	})
	return changed, err
}

func (s *suite) deleteExpired(id uuid.UUID, attempt *uuid.UUID) (bool, error) {
	var deleted bool
	err := s.store.InWriteTx(s.ctx, func(txCtx context.Context) error {
		var err error
		deleted, err = s.store.DeleteExpiredAttachment(txCtx, id, attempt)
		return err
	})
	return deleted, err
}

func (s *suite) mustDeleteExpired(t *testing.T, id uuid.UUID, attempt *uuid.UUID) bool {
	t.Helper()
	deleted, err := s.deleteExpired(id, attempt)
	require.NoError(t, err)
	return deleted
}

// queuedBlobKeys claims every ready blob-delete task and returns their keys.
// Each scenario uses unique keys, so earlier claims cannot hide a later key.
func (s *suite) queuedBlobKeys(t *testing.T) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	s.mustWrite(t, func(txCtx context.Context) error {
		keys = map[string]bool{}
		tasks, err := s.store.ClaimReadyTasks(txCtx, 1000)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.TaskType == registrystore.AttachmentBlobDeleteTaskType {
				keys[task.TaskBody[registrystore.AttachmentBlobDeleteStorageKeyField].(string)] = true
			}
		}
		return nil
	})
	return keys
}

func requireNotFound(t *testing.T, err error) {
	t.Helper()
	var nf *registrystore.NotFoundError
	require.ErrorAs(t, err, &nf)
}

func (s *suite) createWithID(t *testing.T) {
	user, id := newUser(), uuid.New()
	expires := future()
	got := s.create(t, user, id, "uploading", expires)

	require.Equal(t, id, got.ID)
	require.Equal(t, user, got.UserID)
	require.Equal(t, "uploading", got.Status)
	require.Nil(t, got.StorageKey)
	require.Nil(t, got.EntryID)

	stored := s.get(t, user, id)
	require.Equal(t, "uploading", stored.Status)
	require.NotNil(t, stored.AttemptID)
	require.Equal(t, *got.AttemptID, *stored.AttemptID, "the returned attempt must be the stored one")
	require.NotNil(t, stored.ExpiresAt)
	require.WithinDuration(t, *expires, *stored.ExpiresAt, time.Second)

	downloading := s.create(t, user, uuid.New(), "downloading", future())
	require.Equal(t, "downloading", downloading.Status)
	require.NotEqual(t, *got.AttemptID, *downloading.AttemptID, "each created row gets its own attempt ID")
}

func (s *suite) duplicateCreate(t *testing.T) {
	user, id := newUser(), uuid.New()
	first := s.create(t, user, id, "uploading", future())

	var second *registrystore.CreateAttachmentResult
	other := "other.txt"
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		second, err = s.store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{Filename: &other, ContentType: "image/png", Status: "downloading"})
		return err
	})
	require.False(t, second.Created)
	require.Equal(t, id, second.Attachment.ID)
	require.Equal(t, *first.AttemptID, *second.Attachment.AttemptID, "the existing attempt ID must be returned, not replaced")
	require.Equal(t, "uploading", second.Attachment.Status, "the existing status must not be overwritten")
	require.Equal(t, "text/plain", second.Attachment.ContentType)
	require.Nil(t, second.Attachment.Filename)

	stored := s.get(t, user, id)
	require.Equal(t, *first.AttemptID, *stored.AttemptID)
	require.Equal(t, "uploading", stored.Status)
	require.Nil(t, stored.Filename)

	// Once the upload is ready, a duplicate still reports the finished row.
	key := newKey()
	_, err := s.finalize(user, id, *first.AttemptID, key, future())
	require.NoError(t, err)
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		second, err = s.store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		return err
	})
	require.False(t, second.Created)
	require.Equal(t, "ready", second.Attachment.Status)
	require.Equal(t, key, *second.Attachment.StorageKey)
}

func (s *suite) foreignOwner(t *testing.T) {
	owner, intruder, id := newUser(), newUser(), uuid.New()
	first := s.create(t, owner, id, "uploading", future())

	err := s.write(t, func(txCtx context.Context) error {
		res, err := s.store.CreateAttachmentWithID(txCtx, intruder, id, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		require.Nil(t, res, "another user's row must not be returned")
		return err
	})
	requireNotFound(t, err)

	stored := s.get(t, owner, id)
	require.Equal(t, owner, stored.UserID)
	require.Equal(t, *first.AttemptID, *stored.AttemptID)
	require.Equal(t, "uploading", stored.Status)

	// A foreign owner's linked row is equally hidden.
	linkedID := uuid.New()
	linked := s.create(t, owner, linkedID, "uploading", future())
	_, err = s.finalize(owner, linkedID, *linked.AttemptID, newKey(), future())
	require.NoError(t, err)
	s.mustWrite(t, func(txCtx context.Context) error {
		_, err := s.store.LinkAttachmentToEntry(txCtx, owner, linkedID, uuid.New())
		return err
	})
	err = s.write(t, func(txCtx context.Context) error {
		_, err := s.store.CreateAttachmentWithID(txCtx, intruder, linkedID, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		return err
	})
	requireNotFound(t, err)
}

func (s *suite) invalidCreate(t *testing.T) {
	user := newUser()
	for name, att := range map[string]model.Attachment{
		"ready status":        {ContentType: "text/plain", Status: "ready"},
		"failed status":       {ContentType: "text/plain", Status: "failed"},
		"empty status":        {ContentType: "text/plain"},
		"preset storage key":  {ContentType: "text/plain", Status: "uploading", StorageKey: ptr(newKey())},
		"preset linked entry": {ContentType: "text/plain", Status: "uploading", EntryID: ptr(uuid.New())},
	} {
		id := uuid.New()
		err := s.write(t, func(txCtx context.Context) error {
			_, err := s.store.CreateAttachmentWithID(txCtx, user, id, att)
			return err
		})
		var validation *registrystore.ValidationError
		require.ErrorAs(t, err, &validation, name)
		require.False(t, s.exists(t, user, id), "%s: no row may be created", name)
	}
	err := s.write(t, func(txCtx context.Context) error {
		_, err := s.store.CreateAttachmentWithID(txCtx, user, uuid.Nil, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		return err
	})
	var validation *registrystore.ValidationError
	require.ErrorAs(t, err, &validation, "nil attachment ID")
}

func ptr[T any](v T) *T { return &v }

func (s *suite) finalizeSucceeds(t *testing.T) {
	user := newUser()
	for _, status := range []string{"uploading", "downloading"} {
		id := uuid.New()
		created := s.create(t, user, id, status, past())
		key, refreshed := newKey(), future()
		var got *model.Attachment
		contentType := "text/markdown"
		s.mustWrite(t, func(txCtx context.Context) error {
			var err error
			got, err = s.store.FinalizeAttachment(txCtx, user, id, *created.AttemptID, registrystore.AttachmentFinalize{
				StorageKey: key, Size: 42, SHA256: "deadbeef", ContentType: &contentType, ExpiresAt: refreshed,
			})
			return err
		})
		require.Equal(t, "ready", got.Status, status)
		require.Equal(t, key, *got.StorageKey)

		stored := s.get(t, user, id)
		require.Equal(t, "ready", stored.Status)
		require.Equal(t, key, *stored.StorageKey)
		require.EqualValues(t, 42, *stored.Size)
		require.Equal(t, "deadbeef", *stored.SHA256)
		require.Equal(t, "text/markdown", stored.ContentType)
		require.WithinDuration(t, *refreshed, *stored.ExpiresAt, time.Second, "expiry must be refreshed")
		require.Equal(t, *created.AttemptID, *stored.AttemptID, "finalize must keep the attempt ID")
	}

	// Without ContentType or ExpiresAt the stored values are kept.
	id := uuid.New()
	expires := future()
	created := s.create(t, user, id, "uploading", expires)
	_, err := s.finalize(user, id, *created.AttemptID, newKey(), nil)
	require.NoError(t, err)
	stored := s.get(t, user, id)
	require.Equal(t, "text/plain", stored.ContentType)
	require.WithinDuration(t, *expires, *stored.ExpiresAt, time.Second)
}

func (s *suite) finalizeRejects(t *testing.T) {
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "uploading", future())

	_, err := s.finalize(user, id, uuid.New(), newKey(), future())
	requireNotFound(t, err)
	_, err = s.finalize(newUser(), id, *created.AttemptID, newKey(), future())
	requireNotFound(t, err)
	_, err = s.finalize(user, uuid.New(), *created.AttemptID, newKey(), future())
	requireNotFound(t, err)
	stored := s.get(t, user, id)
	require.Equal(t, "uploading", stored.Status, "a rejected finalize must change nothing")
	require.Nil(t, stored.StorageKey)

	winnerKey := newKey()
	_, err = s.finalize(user, id, *created.AttemptID, winnerKey, future())
	require.NoError(t, err)
	// A second finalize of the same attempt (status no longer pending) is rejected.
	_, err = s.finalize(user, id, *created.AttemptID, newKey(), future())
	requireNotFound(t, err)
	require.Equal(t, winnerKey, *s.get(t, user, id).StorageKey, "the first finalize must win")

	// A failed row cannot be finalized.
	failedID := uuid.New()
	failed := s.create(t, user, failedID, "uploading", future())
	changed, err := s.markFailed(user, failedID, *failed.AttemptID)
	require.NoError(t, err)
	require.True(t, changed)
	_, err = s.finalize(user, failedID, *failed.AttemptID, newKey(), future())
	requireNotFound(t, err)
	require.Equal(t, "failed", s.get(t, user, failedID).Status)
}

func (s *suite) markFailedConditions(t *testing.T) {
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "downloading", future())

	changed, err := s.markFailed(user, id, uuid.New())
	require.NoError(t, err)
	require.False(t, changed, "a different attempt must not match")
	changed, err = s.markFailed(newUser(), id, *created.AttemptID)
	require.NoError(t, err)
	require.False(t, changed, "a different owner must not match")
	changed, err = s.markFailed(user, uuid.New(), *created.AttemptID)
	require.NoError(t, err)
	require.False(t, changed, "a different ID must not match")
	require.Equal(t, "downloading", s.get(t, user, id).Status)

	changed, err = s.markFailed(user, id, *created.AttemptID)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "failed", s.get(t, user, id).Status)

	changed, err = s.markFailed(user, id, *created.AttemptID)
	require.NoError(t, err)
	require.False(t, changed, "an already failed row is not changed again")
}

func (s *suite) markFailedAfterFinalize(t *testing.T) {
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "uploading", future())
	key := newKey()
	_, err := s.finalize(user, id, *created.AttemptID, key, future())
	require.NoError(t, err)

	// The lost-acknowledgment case: the error handler marks the request failed.
	changed, err := s.markFailed(user, id, *created.AttemptID)
	require.NoError(t, err)
	require.False(t, changed)
	stored := s.get(t, user, id)
	require.Equal(t, "ready", stored.Status)
	require.Equal(t, key, *stored.StorageKey)
}

func (s *suite) replacementFence(t *testing.T) {
	user, id := newUser(), uuid.New()
	original := s.create(t, user, id, "uploading", future())
	s.mustWrite(t, func(txCtx context.Context) error {
		return s.store.DeleteAttachment(txCtx, user, "", id)
	})
	replacement := s.create(t, user, id, "uploading", future())
	require.NotEqual(t, *original.AttemptID, *replacement.AttemptID, "a recreated ID must get a new attempt ID")

	_, err := s.finalize(user, id, *original.AttemptID, newKey(), future())
	requireNotFound(t, err)
	changed, err := s.markFailed(user, id, *original.AttemptID)
	require.NoError(t, err)
	require.False(t, changed)

	stored := s.get(t, user, id)
	require.Equal(t, "uploading", stored.Status, "the replacement must be untouched")
	require.Nil(t, stored.StorageKey)
	require.Equal(t, *replacement.AttemptID, *stored.AttemptID)

	_, err = s.finalize(user, id, *replacement.AttemptID, newKey(), future())
	require.NoError(t, err)
	require.Equal(t, "ready", s.get(t, user, id).Status)
}

func (s *suite) cleanupRaceFinalize(t *testing.T) {
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "uploading", past())
	listedAttempt := created.AttemptID // cleanup listed the expired row

	key := newKey()
	_, err := s.finalize(user, id, *created.AttemptID, key, future()) // upload finalizes and refreshes expiry
	require.NoError(t, err)

	require.False(t, s.mustDeleteExpired(t, id, listedAttempt), "a refreshed expiry must stop the delete")
	stored := s.get(t, user, id)
	require.Equal(t, "ready", stored.Status)
	require.Equal(t, key, *stored.StorageKey)
	require.False(t, s.queuedBlobKeys(t)[key], "no blob delete may be queued for a row that survived")
}

func (s *suite) cleanupRaceReuse(t *testing.T) {
	user, id := newUser(), uuid.New()
	stale := s.create(t, user, id, "uploading", past())
	staleAttempt := stale.AttemptID // cleanup listed this attempt

	require.True(t, s.mustDeleteExpired(t, id, staleAttempt), "another cleanup pass deleted the listed row")
	// The ID is reused and the replacement is expired too, so only the attempt
	// fence separates it from the stale listing.
	replacement := s.create(t, user, id, "uploading", past())

	require.False(t, s.mustDeleteExpired(t, id, staleAttempt), "a stale attempt must not delete the replacement")
	stored := s.get(t, user, id)
	require.Equal(t, "uploading", stored.Status)
	require.Equal(t, *replacement.AttemptID, *stored.AttemptID)

	require.True(t, s.mustDeleteExpired(t, id, replacement.AttemptID))
	require.False(t, s.exists(t, user, id))
}

func (s *suite) deleteExpiredConditions(t *testing.T) {
	user := newUser()

	notExpired, noExpiry := uuid.New(), uuid.New()
	a := s.create(t, user, notExpired, "uploading", future())
	require.False(t, s.mustDeleteExpired(t, notExpired, a.AttemptID), "an unexpired row must stay")
	b := s.create(t, user, noExpiry, "uploading", nil)
	require.False(t, s.mustDeleteExpired(t, noExpiry, b.AttemptID), "a row without expiry must stay")
	require.True(t, s.exists(t, user, notExpired))
	require.True(t, s.exists(t, user, noExpiry))

	unknown := uuid.New()
	require.False(t, s.mustDeleteExpired(t, unknown, ptr(uuid.New())), "an unknown ID is a skip, not an error")

	wrongAttempt := uuid.New()
	c := s.create(t, user, wrongAttempt, "uploading", past())
	require.False(t, s.mustDeleteExpired(t, wrongAttempt, ptr(uuid.New())))
	require.False(t, s.mustDeleteExpired(t, wrongAttempt, nil), "a nil attempt must not match a row that has one")
	require.True(t, s.exists(t, user, wrongAttempt))

	// Linked rows are never deleted by cleanup, whatever their expiry.
	linked := uuid.New()
	l := s.create(t, user, linked, "uploading", past())
	_, err := s.finalize(user, linked, *l.AttemptID, newKey(), past())
	require.NoError(t, err)
	entryID := uuid.New()
	s.mustWrite(t, func(txCtx context.Context) error {
		_, err := s.store.LinkAttachmentToEntry(txCtx, user, linked, entryID)
		return err
	})
	require.False(t, s.mustDeleteExpired(t, linked, l.AttemptID), "a linked row must stay")
	require.NotNil(t, s.get(t, user, linked).EntryID)

	require.True(t, s.mustDeleteExpired(t, wrongAttempt, c.AttemptID))
	require.False(t, s.exists(t, user, wrongAttempt))
}

func (s *suite) deleteExpiredBlobQueue(t *testing.T) {
	// Scenarios share one datastore and Run visits them in random map order, so an
	// earlier scenario that deleted an expired row with a storage key leaves its
	// queued blob delete behind. Drain it, or the "nothing queued yet" assertion
	// below depends on scenario order.
	s.queuedBlobKeys(t)

	user := newUser()

	// Deleted row with a storage key: the key is queued.
	deletedID := uuid.New()
	d := s.create(t, user, deletedID, "uploading", future())
	deletedKey := newKey()
	_, err := s.finalize(user, deletedID, *d.AttemptID, deletedKey, past())
	require.NoError(t, err)

	// Deleted row without a storage key: nothing is queued.
	pendingID := uuid.New()
	p := s.create(t, user, pendingID, "uploading", past())

	// Rows that are not deleted never queue their key.
	freshID := uuid.New()
	f := s.create(t, user, freshID, "uploading", future())
	freshKey := newKey()
	_, err = s.finalize(user, freshID, *f.AttemptID, freshKey, future())
	require.NoError(t, err)

	linkedID := uuid.New()
	l := s.create(t, user, linkedID, "uploading", future())
	linkedKey := newKey()
	_, err = s.finalize(user, linkedID, *l.AttemptID, linkedKey, past())
	require.NoError(t, err)
	s.mustWrite(t, func(txCtx context.Context) error {
		_, err := s.store.LinkAttachmentToEntry(txCtx, user, linkedID, uuid.New())
		return err
	})

	staleID := uuid.New()
	st := s.create(t, user, staleID, "uploading", future())
	staleKey := newKey()
	_, err = s.finalize(user, staleID, *st.AttemptID, staleKey, past())
	require.NoError(t, err)

	require.False(t, s.mustDeleteExpired(t, freshID, f.AttemptID))
	require.False(t, s.mustDeleteExpired(t, linkedID, l.AttemptID))
	require.False(t, s.mustDeleteExpired(t, staleID, ptr(uuid.New())), "a mismatched attempt deletes nothing")
	require.Empty(t, s.queuedBlobKeys(t), "no delete succeeded yet, so nothing may be queued")

	require.True(t, s.mustDeleteExpired(t, deletedID, d.AttemptID))
	require.True(t, s.mustDeleteExpired(t, pendingID, p.AttemptID))
	queued := s.queuedBlobKeys(t)
	require.Equal(t, map[string]bool{deletedKey: true}, queued, "exactly the deleted row's key is queued")
	require.False(t, queued[freshKey] || queued[linkedKey] || queued[staleKey])
}

func (s *suite) legacyNullAttempt(t *testing.T) {
	user := newUser()
	create := func(expires *time.Time) *model.Attachment {
		var a *model.Attachment
		s.mustWrite(t, func(txCtx context.Context) error {
			var err error
			a, err = s.store.CreateAttachment(txCtx, user, "", model.Attachment{ContentType: "text/plain", ExpiresAt: expires})
			return err
		})
		require.Nil(t, a.AttemptID)
		return a
	}

	legacy := create(past())
	require.False(t, s.mustDeleteExpired(t, legacy.ID, ptr(uuid.New())), "a non-null attempt must not match a legacy row")
	require.True(t, s.exists(t, user, legacy.ID))
	require.True(t, s.mustDeleteExpired(t, legacy.ID, nil), "a nil attempt matches a legacy row with no attempt ID")
	require.False(t, s.exists(t, user, legacy.ID))

	live := create(future())
	require.False(t, s.mustDeleteExpired(t, live.ID, nil), "an unexpired legacy row stays")

	// A legacy row can never be finalized or failed through the attempt fence.
	_, err := s.finalize(user, live.ID, uuid.New(), newKey(), future())
	requireNotFound(t, err)
	_, err = s.finalize(user, live.ID, uuid.Nil, newKey(), future())
	requireNotFound(t, err)
	changed, err := s.markFailed(user, live.ID, uuid.Nil)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "ready", s.get(t, user, live.ID).Status)
}

// adminListAttemptID covers the input of the cleanup fence: the cleanup job
// passes the AttemptID it read from AdminListAttachments to DeleteExpiredAttachment.
// If a store's list dropped the field, every row with an attempt would be passed
// as a legacy row and never deleted, and a legacy row could not be told apart.
func (s *suite) adminListAttemptID(t *testing.T) {
	user := newUser()
	listExpired := func() map[uuid.UUID]registrystore.AdminAttachment {
		var rows []registrystore.AdminAttachment
		require.NoError(t, s.store.InReadTx(s.ctx, func(txCtx context.Context) error {
			var err error
			rows, _, err = s.store.AdminListAttachments(txCtx, registrystore.AdminAttachmentQuery{UserID: &user, Status: "expired", Limit: 100})
			return err
		}))
		byID := map[uuid.UUID]registrystore.AdminAttachment{}
		for _, row := range rows {
			byID[row.ID] = row
		}
		return byID
	}

	pendingID, readyID, liveID := uuid.New(), uuid.New(), uuid.New()
	pending := s.create(t, user, pendingID, "uploading", past())
	ready := s.create(t, user, readyID, "uploading", future())
	_, err := s.finalize(user, readyID, *ready.AttemptID, newKey(), past())
	require.NoError(t, err)
	s.create(t, user, liveID, "uploading", future())
	var legacy *model.Attachment
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		legacy, err = s.store.CreateAttachment(txCtx, user, "", model.Attachment{ContentType: "text/plain", ExpiresAt: past()})
		return err
	})
	require.Nil(t, legacy.AttemptID)

	listed := listExpired()
	require.Len(t, listed, 3, "only the expired rows are listed")
	require.NotNil(t, listed[pendingID].AttemptID, "an expired uploading row must list its attempt ID")
	require.Equal(t, *pending.AttemptID, *listed[pendingID].AttemptID)
	require.NotNil(t, listed[readyID].AttemptID, "a finalized row keeps its attempt ID")
	require.Equal(t, *ready.AttemptID, *listed[readyID].AttemptID)
	require.Contains(t, listed, legacy.ID)
	require.Nil(t, listed[legacy.ID].AttemptID, "a legacy row lists no attempt ID")

	// The listed values are exactly what the conditional delete needs.
	for id, row := range listed {
		require.True(t, s.mustDeleteExpired(t, id, row.AttemptID), "row %s must be deletable with its listed attempt ID", id)
	}
	require.Empty(t, listExpired())
	require.True(t, s.exists(t, user, liveID), "the unexpired row is untouched")
}

func (s *suite) concurrentCreate(t *testing.T) {
	const workers = 8
	user, id := newUser(), uuid.New()

	var wg sync.WaitGroup
	results := make([]*registrystore.CreateAttachmentResult, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = s.store.InWriteTx(s.ctx, func(txCtx context.Context) error {
				var err error
				results[i], err = s.store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading", ExpiresAt: future()})
				return err
			})
		}(i)
	}
	close(start)
	wg.Wait()

	created := 0
	var winner uuid.UUID
	for i := range results {
		require.NoError(t, errs[i], "worker %d", i)
		if results[i].Created {
			created++
			winner = *results[i].Attachment.AttemptID
		}
	}
	require.Equal(t, 1, created, "exactly one request creates the row")
	for i := range results {
		require.Equal(t, winner, *results[i].Attachment.AttemptID, "worker %d must report the winner's attempt", i)
	}

	var rows []registrystore.AdminAttachment
	require.NoError(t, s.store.InReadTx(s.ctx, func(txCtx context.Context) error {
		var err error
		rows, _, err = s.store.AdminListAttachments(txCtx, registrystore.AdminAttachmentQuery{UserID: &user, Limit: 100})
		return err
	}))
	require.Len(t, rows, 1, "exactly one row exists")
	require.Equal(t, id, rows[0].ID)
}

func (s *suite) transactionContinuesAfterDuplicate(t *testing.T) {
	user, id := newUser(), uuid.New()
	pre := s.create(t, user, id, "uploading", future())

	// A duplicate must not poison the surrounding transaction (PostgreSQL aborts
	// a transaction after any failed statement).
	var dup *registrystore.CreateAttachmentResult
	var got *model.Attachment
	var finalized *model.Attachment
	key := newKey()
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		dup, err = s.store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		if err != nil {
			return err
		}
		got, err = s.store.GetAttachment(txCtx, user, "", id)
		if err != nil {
			return err
		}
		finalized, err = s.store.FinalizeAttachment(txCtx, user, id, *pre.AttemptID, registrystore.AttachmentFinalize{StorageKey: key, Size: 1, SHA256: "x", ExpiresAt: future()})
		return err
	})
	require.False(t, dup.Created)
	require.Equal(t, *pre.AttemptID, *got.AttemptID)
	require.Equal(t, "ready", finalized.Status)

	// Create, duplicate and finalize inside a single transaction.
	freshID := uuid.New()
	var first, second *registrystore.CreateAttachmentResult
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		first, err = s.store.CreateAttachmentWithID(txCtx, user, freshID, model.Attachment{ContentType: "text/plain", Status: "uploading", ExpiresAt: future()})
		if err != nil {
			return err
		}
		second, err = s.store.CreateAttachmentWithID(txCtx, user, freshID, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		if err != nil {
			return err
		}
		_, err = s.store.FinalizeAttachment(txCtx, user, freshID, *first.Attachment.AttemptID, registrystore.AttachmentFinalize{StorageKey: newKey(), Size: 1, SHA256: "x", ExpiresAt: future()})
		return err
	})
	require.True(t, first.Created)
	require.False(t, second.Created)
	require.Equal(t, *first.Attachment.AttemptID, *second.Attachment.AttemptID)
	require.Equal(t, "ready", s.get(t, user, freshID).Status)
}

func (s *suite) rollback(t *testing.T) {
	user := newUser()

	// Finalize rolls back with its transaction.
	id := uuid.New()
	created := s.create(t, user, id, "uploading", future())
	key := newKey()
	err := s.write(t, func(txCtx context.Context) error {
		if _, err := s.store.FinalizeAttachment(txCtx, user, id, *created.AttemptID, registrystore.AttachmentFinalize{StorageKey: key, Size: 1, SHA256: "x", ExpiresAt: future()}); err != nil {
			return err
		}
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	stored := s.get(t, user, id)
	require.Equal(t, "uploading", stored.Status)
	require.Nil(t, stored.StorageKey)

	// MarkAttachmentFailed rolls back too.
	err = s.write(t, func(txCtx context.Context) error {
		changed, err := s.store.MarkAttachmentFailed(txCtx, user, id, *created.AttemptID)
		require.NoError(t, err)
		require.True(t, changed)
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	require.Equal(t, "uploading", s.get(t, user, id).Status)

	// Create rolls back: the ID stays free.
	freeID := uuid.New()
	err = s.write(t, func(txCtx context.Context) error {
		res, err := s.store.CreateAttachmentWithID(txCtx, user, freeID, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		require.NoError(t, err)
		require.True(t, res.Created)
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	require.False(t, s.exists(t, user, freeID))

	// The blob delete is queued in the same transaction as the metadata delete.
	expiredID := uuid.New()
	e := s.create(t, user, expiredID, "uploading", future())
	expiredKey := newKey()
	_, err = s.finalize(user, expiredID, *e.AttemptID, expiredKey, past())
	require.NoError(t, err)
	err = s.write(t, func(txCtx context.Context) error {
		deleted, err := s.store.DeleteExpiredAttachment(txCtx, expiredID, e.AttemptID)
		require.NoError(t, err)
		require.True(t, deleted)
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	require.True(t, s.exists(t, user, expiredID), "a rolled back delete must keep the row")
	require.False(t, s.queuedBlobKeys(t)[expiredKey], "a rolled back delete must not queue the blob")

	require.True(t, s.mustDeleteExpired(t, expiredID, e.AttemptID))
	require.True(t, s.queuedBlobKeys(t)[expiredKey], "a committed delete queues the blob")
}

func (s *suite) legacyCreateUnchanged(t *testing.T) {
	user := newUser()
	var a *model.Attachment
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		a, err = s.store.CreateAttachment(txCtx, user, "", model.Attachment{ContentType: "text/plain"})
		return err
	})
	require.Nil(t, a.AttemptID)
	require.Equal(t, "ready", a.Status)
	stored := s.get(t, user, a.ID)
	require.Nil(t, stored.AttemptID)
	require.Equal(t, "ready", stored.Status)
}

func (s *suite) updateAttachmentUnchanged(t *testing.T) {
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "downloading", future())
	status, key := "ready", newKey()
	s.mustWrite(t, func(txCtx context.Context) error {
		_, err := s.store.UpdateAttachment(txCtx, user, id, registrystore.AttachmentUpdate{Status: &status, StorageKey: &key})
		return err
	})
	stored := s.get(t, user, id)
	require.Equal(t, "ready", stored.Status)
	require.Equal(t, key, *stored.StorageKey)
	require.Equal(t, *created.AttemptID, *stored.AttemptID, "the unfenced update path must leave the attempt ID alone")
}

func (s *suite) createRejectsCallerManagedFields(t *testing.T) {
	user := newUser()
	archived := time.Now().Add(-time.Minute)
	for name, att := range map[string]model.Attachment{
		"size":        {ContentType: "text/plain", Status: "uploading", Size: ptr(int64(5))},
		"sha256":      {ContentType: "text/plain", Status: "uploading", SHA256: ptr("abc")},
		"archived at": {ContentType: "text/plain", Status: "uploading", ArchivedAt: &archived},
	} {
		id := uuid.New()
		err := s.write(t, func(txCtx context.Context) error {
			_, err := s.store.CreateAttachmentWithID(txCtx, user, id, att)
			return err
		})
		var validation *registrystore.ValidationError
		require.ErrorAs(t, err, &validation, name)
		require.False(t, s.exists(t, user, id), "%s: no row may be created", name)
	}

	// ID, owner, attempt and creation time belong to the store. Whatever the
	// caller puts there is replaced, and the response says so.
	id, forgedAttempt := uuid.New(), uuid.New()
	forgedCreated := time.Now().Add(-72 * time.Hour)
	var res *registrystore.CreateAttachmentResult
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		res, err = s.store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{
			ID: uuid.New(), UserID: "someone-else", AttemptID: &forgedAttempt, CreatedAt: forgedCreated,
			ContentType: "text/plain", Status: "uploading",
		})
		return err
	})
	require.True(t, res.Created)
	require.Equal(t, id, res.Attachment.ID)
	require.Equal(t, user, res.Attachment.UserID)
	require.NotEqual(t, forgedAttempt, *res.Attachment.AttemptID)
	require.WithinDuration(t, time.Now(), res.Attachment.CreatedAt, time.Minute, "CreatedAt is set by the store")

	stored := s.get(t, user, id)
	require.Equal(t, user, stored.UserID)
	require.Equal(t, *res.Attachment.AttemptID, *stored.AttemptID)
	require.WithinDuration(t, time.Now(), stored.CreatedAt, time.Minute, "a forged CreatedAt must not be persisted")
	require.Nil(t, stored.ArchivedAt)
}

func (s *suite) createReturnsPersistedValues(t *testing.T) {
	// A value with sub-microsecond digits exposes any store that returns the
	// caller's input instead of what it wrote.
	user, id := newUser(), uuid.New()
	expires := time.Now().Add(time.Hour).UTC().Add(123456789 * time.Nanosecond)
	var res *registrystore.CreateAttachmentResult
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		res, err = s.store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading", ExpiresAt: &expires})
		return err
	})
	stored := s.get(t, user, id)
	require.NotNil(t, res.Attachment.ExpiresAt)
	require.True(t, res.Attachment.ExpiresAt.UTC().Equal(stored.ExpiresAt.UTC()),
		"returned expiry %v must equal stored expiry %v", res.Attachment.ExpiresAt.UTC(), stored.ExpiresAt.UTC())
	require.True(t, res.Attachment.CreatedAt.UTC().Equal(stored.CreatedAt.UTC()),
		"returned creation time %v must equal stored %v", res.Attachment.CreatedAt.UTC(), stored.CreatedAt.UTC())
	require.Equal(t, res.Attachment.Status, stored.Status)
	require.Equal(t, *res.Attachment.AttemptID, *stored.AttemptID)
}

func (s *suite) finalizeRejectsEmptyKey(t *testing.T) {
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "uploading", future())
	_, err := s.finalize(user, id, *created.AttemptID, "", future())
	var validation *registrystore.ValidationError
	require.ErrorAs(t, err, &validation)
	stored := s.get(t, user, id)
	require.Equal(t, "uploading", stored.Status, "a rejected finalize must change nothing")
	require.Nil(t, stored.StorageKey)
}

// finalizeRegistersReference checks the guard a finalize leaves behind. A blob
// delete is queued for the key before the finalize; registering the reference
// moves that task to the referenced state, where the cleanup worker cannot claim
// it. Without the registration the task stays queued and is claimed. (Counting
// attachment rows cannot tell the two apart, since the finalized row itself
// references the key.)
func (s *suite) finalizeRegistersReference(t *testing.T) {
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "uploading", future())
	key := newKey()
	s.mustWrite(t, func(txCtx context.Context) error { return s.store.QueueAttachmentBlobDelete(txCtx, key) })

	_, err := s.finalize(user, id, *created.AttemptID, key, future())
	require.NoError(t, err)

	var claimedForKey int
	s.mustWrite(t, func(txCtx context.Context) error {
		tasks, err := s.store.ClaimReadyTasks(txCtx, 1000)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.TaskType == registrystore.AttachmentBlobDeleteTaskType && task.TaskBody[registrystore.AttachmentBlobDeleteStorageKeyField] == key {
				claimedForKey++
			}
		}
		return nil
	})
	require.Zero(t, claimedForKey, "a finalized key must not have a claimable blob delete task")
	require.Equal(t, key, *s.get(t, user, id).StorageKey)
}

// finalizeRefusesDeleteInProgress covers the Register error path: once a blob
// delete has been claimed, a new reference to that key is refused, and the
// failed finalize must leave the row pending.
func (s *suite) finalizeRefusesDeleteInProgress(t *testing.T) {
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "uploading", future())
	key := newKey()
	s.mustWrite(t, func(txCtx context.Context) error { return s.store.QueueAttachmentBlobDelete(txCtx, key) })
	claimed := false
	s.mustWrite(t, func(txCtx context.Context) error {
		tasks, err := s.store.ClaimReadyTasks(txCtx, 1000)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.TaskBody[registrystore.AttachmentBlobDeleteStorageKeyField] == key {
				claimed = true
			}
		}
		return nil
	})
	require.True(t, claimed, "the delete task must be claimed before the finalize")

	_, err := s.finalize(user, id, *created.AttemptID, key, future())
	var conflict *registrystore.ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, registrystore.AttachmentBlobDeleteConflictCode, conflict.Code)

	stored := s.get(t, user, id)
	require.Equal(t, "uploading", stored.Status, "the failed finalize must roll back with its transaction")
	require.Nil(t, stored.StorageKey)
}

func (s *suite) archivedRows(t *testing.T) {
	require.NotNil(t, s.setArchived, "the store runner must supply a way to archive a row")
	user, id := newUser(), uuid.New()
	created := s.create(t, user, id, "uploading", future())
	s.setArchived(t, id, true)

	_, err := s.finalize(user, id, *created.AttemptID, newKey(), future())
	requireNotFound(t, err)
	changed, err := s.markFailed(user, id, *created.AttemptID)
	require.NoError(t, err)
	require.False(t, changed, "an archived row must not be marked failed")

	// The ID is still taken, so a create reports the existing row and replaces
	// nothing.
	var dup *registrystore.CreateAttachmentResult
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		dup, err = s.store.CreateAttachmentWithID(txCtx, user, id, model.Attachment{ContentType: "text/plain", Status: "uploading"})
		return err
	})
	require.False(t, dup.Created)
	require.Equal(t, *created.AttemptID, *dup.Attachment.AttemptID)

	// Un-archiving shows the rejected calls changed nothing.
	s.setArchived(t, id, false)
	stored := s.get(t, user, id)
	require.Equal(t, "uploading", stored.Status)
	require.Nil(t, stored.StorageKey)
	require.Equal(t, *created.AttemptID, *stored.AttemptID)
	_, err = s.finalize(user, id, *created.AttemptID, newKey(), future())
	require.NoError(t, err, "the original attempt can finalize once the row is live again")
}

func (s *suite) link(user string, id, entryID uuid.UUID) (*model.Attachment, error) {
	var a *model.Attachment
	err := s.store.InWriteTx(s.ctx, func(txCtx context.Context) error {
		var err error
		a, err = s.store.LinkAttachmentToEntry(txCtx, user, id, entryID)
		return err
	})
	return a, err
}

func requireNotReadyConflict(t *testing.T, err error, status string) {
	t.Helper()
	var conflict *registrystore.ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, registrystore.AttachmentNotReadyConflictCode, conflict.Code)
	require.Contains(t, conflict.Message, status, "the message must name the attachment's status")
}

// pendingOrFailed creates an unlinked attachment in the given non-ready status.
func (s *suite) pendingOrFailed(t *testing.T, user, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if status == "failed" {
		created := s.create(t, user, id, "uploading", future())
		changed, err := s.markFailed(user, id, *created.AttemptID)
		require.NoError(t, err)
		require.True(t, changed)
		return id
	}
	s.create(t, user, id, status, future())
	return id
}

func (s *suite) linkReady(t *testing.T) {
	user := newUser()

	// A finalized attachment links.
	id := uuid.New()
	created := s.create(t, user, id, "uploading", future())
	_, err := s.finalize(user, id, *created.AttemptID, newKey(), future())
	require.NoError(t, err)
	entryID := uuid.New()
	linked, err := s.link(user, id, entryID)
	require.NoError(t, err)
	require.Equal(t, entryID, *linked.EntryID)
	require.Equal(t, "ready", linked.Status)
	require.Equal(t, entryID, *s.get(t, user, id).EntryID, "the link must be persisted")

	// A no-ID attachment is created ready and links without a finalize step.
	var legacy *model.Attachment
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		legacy, err = s.store.CreateAttachment(txCtx, user, "", model.Attachment{ContentType: "text/plain"})
		return err
	})
	legacyEntry := uuid.New()
	linked, err = s.link(user, legacy.ID, legacyEntry)
	require.NoError(t, err)
	require.Equal(t, legacyEntry, *linked.EntryID)
}

func (s *suite) linkRejectsNotReady(t *testing.T) {
	user := newUser()
	for _, status := range []string{"uploading", "downloading", "failed"} {
		t.Run(status, func(t *testing.T) {
			id := s.pendingOrFailed(t, user, status)
			before := s.get(t, user, id)

			_, err := s.link(user, id, uuid.New())
			requireNotReadyConflict(t, err, status)

			after := s.get(t, user, id)
			require.Nil(t, after.EntryID, "a rejected link must not link")
			require.Equal(t, status, after.Status, "a rejected link must not change the status")
			require.Equal(t, before.AttemptID, after.AttemptID)
		})
	}
}

func (s *suite) linkOutcomesKeepPrecedence(t *testing.T) {
	user, other := newUser(), newUser()

	// Missing attachment.
	_, err := s.link(user, uuid.New(), uuid.New())
	requireNotFound(t, err)

	// Another user's pending row is forbidden, not a status conflict that would
	// reveal its state.
	pendingID := uuid.New()
	s.create(t, user, pendingID, "uploading", future())
	_, err = s.link(other, pendingID, uuid.New())
	var forbidden *registrystore.ForbiddenError
	require.ErrorAs(t, err, &forbidden)
	require.Nil(t, s.get(t, user, pendingID).EntryID)

	// An archived pending row is not found, whatever its status.
	require.NotNil(t, s.setArchived, "the store runner must supply a way to archive a row")
	archivedID := uuid.New()
	s.create(t, user, archivedID, "uploading", future())
	s.setArchived(t, archivedID, true)
	_, err = s.link(user, archivedID, uuid.New())
	requireNotFound(t, err)
	s.setArchived(t, archivedID, false)

	// A ready row linked to one entry: the same entry is an idempotent success,
	// a different entry is a conflict that is not the not-ready one.
	readyID := uuid.New()
	created := s.create(t, user, readyID, "uploading", future())
	_, err = s.finalize(user, readyID, *created.AttemptID, newKey(), future())
	require.NoError(t, err)
	entryID := uuid.New()
	_, err = s.link(user, readyID, entryID)
	require.NoError(t, err)
	again, err := s.link(user, readyID, entryID)
	require.NoError(t, err, "linking the same entry again is idempotent")
	require.Equal(t, entryID, *again.EntryID)
	_, err = s.link(user, readyID, uuid.New())
	var conflict *registrystore.ConflictError
	require.ErrorAs(t, err, &conflict)
	require.NotEqual(t, registrystore.AttachmentNotReadyConflictCode, conflict.Code)
	require.Equal(t, entryID, *s.get(t, user, readyID).EntryID, "the existing link is preserved")

	// A row linked before this rule existed may still be pending. Re-linking its
	// own entry stays idempotent so append recovery can finish; another entry is
	// still a conflict and the link is preserved.
	legacyID := uuid.New()
	s.create(t, user, legacyID, "downloading", future())
	legacyEntry := uuid.New()
	s.mustWrite(t, func(txCtx context.Context) error {
		_, err := s.store.UpdateAttachment(txCtx, user, legacyID, registrystore.AttachmentUpdate{EntryID: &legacyEntry})
		return err
	})
	relinked, err := s.link(user, legacyID, legacyEntry)
	require.NoError(t, err)
	require.Equal(t, legacyEntry, *relinked.EntryID)
	_, err = s.link(user, legacyID, uuid.New())
	require.ErrorAs(t, err, &conflict)
	require.NotEqual(t, registrystore.AttachmentNotReadyConflictCode, conflict.Code)
	require.Equal(t, legacyEntry, *s.get(t, user, legacyID).EntryID)
}

func (s *suite) linkFollowsStatusProgression(t *testing.T) {
	user := newUser()

	// The status is read at link time: a row that was uploading links once it is
	// finalized.
	id := uuid.New()
	created := s.create(t, user, id, "uploading", future())
	_, err := s.link(user, id, uuid.New())
	requireNotReadyConflict(t, err, "uploading")
	_, err = s.finalize(user, id, *created.AttemptID, newKey(), future())
	require.NoError(t, err)
	entryID := uuid.New()
	linked, err := s.link(user, id, entryID)
	require.NoError(t, err)
	require.Equal(t, entryID, *linked.EntryID)

	// A failed row never links; a late finalize from its attempt cannot revive it.
	failedID := s.pendingOrFailed(t, user, "failed")
	_, err = s.link(user, failedID, uuid.New())
	requireNotReadyConflict(t, err, "failed")
	require.Nil(t, s.get(t, user, failedID).EntryID)
}

// linkRollsBack covers the append path: the entry is written first and the
// attachment link second, so a rejected link has to take the entry with it.
func (s *suite) linkRollsBack(t *testing.T) {
	user := newUser()
	var conv *registrystore.ConversationDetail
	s.mustWrite(t, func(txCtx context.Context) error {
		var err error
		conv, err = s.store.CreateConversation(txCtx, user, "", "link rollback", nil, nil, nil, nil)
		return err
	})
	entries := func() []model.Entry {
		var page *registrystore.PagedEntries
		require.NoError(t, s.store.InReadTx(s.ctx, func(txCtx context.Context) error {
			var err error
			page, err = s.store.GetEntries(txCtx, user, conv.ID, registrystore.EntryListQuery{Limit: 50})
			return err
		}))
		return page.Data
	}
	require.Empty(t, entries())

	// Rejected link: the append that preceded it is not left behind.
	id := s.pendingOrFailed(t, user, "downloading")
	err := s.write(t, func(txCtx context.Context) error {
		written, err := s.store.AppendEntries(txCtx, user, conv.ID, []registrystore.CreateEntryRequest{{
			Channel: "history", ContentType: "history", Content: []byte(`[{"role":"USER","text":"hello"}]`),
		}}, nil, nil, nil)
		if err != nil {
			return err
		}
		require.Len(t, written, 1)
		_, err = s.store.LinkAttachmentToEntry(txCtx, user, id, written[0].ID)
		return err
	})
	requireNotReadyConflict(t, err, "downloading")
	require.Empty(t, entries(), "the rejected link must roll the entry back")
	require.Nil(t, s.get(t, user, id).EntryID)

	// A successful link rolls back with its transaction too.
	readyID := uuid.New()
	created := s.create(t, user, readyID, "uploading", future())
	_, err = s.finalize(user, readyID, *created.AttemptID, newKey(), future())
	require.NoError(t, err)
	err = s.write(t, func(txCtx context.Context) error {
		_, err := s.store.LinkAttachmentToEntry(txCtx, user, readyID, uuid.New())
		require.NoError(t, err)
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	require.Nil(t, s.get(t, user, readyID).EntryID, "a rolled back link must not persist")
}
