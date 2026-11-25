//go:build !nosqlite && sqlite_fts5

package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	_ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// cleanupSpyStore wraps a real store. The only InReadTx call in a cleanup pass
// is the list, so afterList runs after the list committed and before any delete.
// That makes "changed after listing" deterministic without sleeps.
type cleanupSpyStore struct {
	registrystore.MemoryStore
	afterList    func()
	adminDeletes []uuid.UUID
	conditional  []conditionalDeleteCall
	// conditionalFn replaces the real call when set. It runs inside the service's
	// write scope, so it must use the context it is given and never open another.
	conditionalFn func(context.Context, uuid.UUID, *uuid.UUID) (bool, error)
}

type conditionalDeleteCall struct {
	ID      uuid.UUID
	Attempt *uuid.UUID
}

func (s *cleanupSpyStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	if err := s.MemoryStore.InReadTx(ctx, fn); err != nil {
		return err
	}
	if s.afterList != nil {
		hook := s.afterList
		s.afterList = nil
		hook()
	}
	return nil
}

func (s *cleanupSpyStore) AdminDeleteAttachment(ctx context.Context, id uuid.UUID) error {
	s.adminDeletes = append(s.adminDeletes, id)
	return s.MemoryStore.AdminDeleteAttachment(ctx, id)
}

func (s *cleanupSpyStore) DeleteExpiredAttachment(ctx context.Context, id uuid.UUID, attempt *uuid.UUID) (bool, error) {
	s.conditional = append(s.conditional, conditionalDeleteCall{ID: id, Attempt: attempt})
	if s.conditionalFn != nil {
		return s.conditionalFn(ctx, id, attempt)
	}
	return s.MemoryStore.DeleteExpiredAttachment(ctx, id, attempt)
}

type cleanupEnv struct {
	ctx   context.Context
	store *cleanupSpyStore
	svc   *AttachmentCleanupService
}

// newCleanupEnv uses a real file-backed SQLite store, not a mock.
func newCleanupEnv(t *testing.T) *cleanupEnv {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = filepath.Join(t.TempDir(), "attachment-cleanup.db")
	cfg.DatastoreMigrateAtStart = true
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	real, err := loader(ctx)
	require.NoError(t, err)
	spy := &cleanupSpyStore{MemoryStore: real}
	return &cleanupEnv{ctx: ctx, store: spy, svc: NewAttachmentCleanupService(spy, nil, time.Hour)}
}

func past() *time.Time   { v := time.Now().Add(-time.Hour).UTC().Truncate(time.Second); return &v }
func future() *time.Time { v := time.Now().Add(time.Hour).UTC().Truncate(time.Second); return &v }

func (e *cleanupEnv) write(t *testing.T, fn func(context.Context) error) {
	t.Helper()
	require.NoError(t, e.store.MemoryStore.InWriteTx(e.ctx, fn))
}

func (e *cleanupEnv) create(t *testing.T, user string, id uuid.UUID, status string, expires *time.Time) *model.Attachment {
	t.Helper()
	var res *registrystore.CreateAttachmentResult
	e.write(t, func(ctx context.Context) error {
		var err error
		res, err = e.store.MemoryStore.CreateAttachmentWithID(ctx, user, id, model.Attachment{ContentType: "text/plain", Status: status, ExpiresAt: expires})
		return err
	})
	require.True(t, res.Created)
	require.NotNil(t, res.Attachment.AttemptID)
	return res.Attachment
}

func (e *cleanupEnv) finalize(t *testing.T, user string, a *model.Attachment, key string, expires *time.Time) {
	t.Helper()
	e.write(t, func(ctx context.Context) error {
		_, err := e.store.MemoryStore.FinalizeAttachment(ctx, user, a.ID, *a.AttemptID, registrystore.AttachmentFinalize{
			StorageKey: key, Size: 5, SHA256: "abc123", ExpiresAt: expires,
		})
		return err
	})
}

func (e *cleanupEnv) link(t *testing.T, user string, id uuid.UUID) {
	t.Helper()
	e.write(t, func(ctx context.Context) error {
		_, err := e.store.MemoryStore.LinkAttachmentToEntry(ctx, user, id, uuid.New())
		return err
	})
}

// get returns nil when the row does not exist.
func (e *cleanupEnv) get(t *testing.T, user string, id uuid.UUID) *model.Attachment {
	t.Helper()
	var a *model.Attachment
	err := e.store.MemoryStore.InReadTx(e.ctx, func(ctx context.Context) error {
		var err error
		a, err = e.store.MemoryStore.GetAttachment(ctx, user, "", id)
		return err
	})
	if err != nil {
		var nf *registrystore.NotFoundError
		require.ErrorAs(t, err, &nf)
		return nil
	}
	return a
}

// queuedBlobKeys claims every ready blob-delete task. Each test uses unique keys.
func (e *cleanupEnv) queuedBlobKeys(t *testing.T) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	e.write(t, func(ctx context.Context) error {
		keys = map[string]bool{}
		tasks, err := e.store.MemoryStore.ClaimReadyTasks(ctx, 1000)
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

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	log.SetOutput(&out)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})
	return &out
}

func uniqueUser() string { return "user-" + uuid.NewString() }
func uniqueKey() string  { return "key-" + uuid.NewString() }

func TestCleanupDeletesExpiredUploadingAttachmentWithItsAttempt(t *testing.T) {
	env := newCleanupEnv(t)
	user, id := uniqueUser(), uuid.New()
	created := env.create(t, user, id, "uploading", past())

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Deleted: 1}, result)
	require.Nil(t, env.get(t, user, id), "the expired uploading row must be deleted")
	require.Equal(t, []conditionalDeleteCall{{ID: id, Attempt: created.AttemptID}}, env.store.conditional,
		"cleanup must pass the listed row's attempt ID to the conditional delete")
	require.Empty(t, env.store.adminDeletes, "cleanup must not use the unconditional admin delete")
}

func TestCleanupPassesNilAttemptForLegacyRows(t *testing.T) {
	env := newCleanupEnv(t)
	user := uniqueUser()
	var legacy *model.Attachment
	env.write(t, func(ctx context.Context) error {
		var err error
		legacy, err = env.store.MemoryStore.CreateAttachment(ctx, user, "", model.Attachment{ContentType: "text/plain", ExpiresAt: past()})
		return err
	})
	require.Nil(t, legacy.AttemptID)

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Deleted: 1}, result)
	require.Nil(t, env.get(t, user, legacy.ID))
	require.Equal(t, []conditionalDeleteCall{{ID: legacy.ID, Attempt: nil}}, env.store.conditional)
}

func TestCleanupKeepsAttachmentFinalizedAndRefreshedAfterListing(t *testing.T) {
	env := newCleanupEnv(t)
	user, id := uniqueUser(), uuid.New()
	created := env.create(t, user, id, "uploading", past())
	key := uniqueKey()
	env.store.afterList = func() { env.finalize(t, user, created, key, future()) }

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Skipped: 1}, result, "a refreshed row is a skip, not a deletion and not a failure")
	stored := env.get(t, user, id)
	require.NotNil(t, stored, "the refreshed row must remain")
	require.Equal(t, "ready", stored.Status)
	require.Equal(t, key, *stored.StorageKey)
	require.Equal(t, 1, len(env.store.conditional), "the delete was attempted exactly once")
	require.False(t, env.queuedBlobKeys(t)[key], "no blob delete may be queued for a row that survived")
}

func TestCleanupDoesNotDeleteReplacementUnderReusedID(t *testing.T) {
	env := newCleanupEnv(t)
	user, id := uniqueUser(), uuid.New()
	stale := env.create(t, user, id, "uploading", past())
	var replacement *model.Attachment
	replacementKey := uniqueKey()
	env.store.afterList = func() {
		// Another pass deletes the listed row and the ID is reused. The replacement
		// is expired and finalized with a key, so only the attempt fence protects it.
		deleted, err := func() (bool, error) {
			var deleted bool
			err := env.store.MemoryStore.InWriteTx(env.ctx, func(ctx context.Context) error {
				var err error
				deleted, err = env.store.MemoryStore.DeleteExpiredAttachment(ctx, id, stale.AttemptID)
				return err
			})
			return deleted, err
		}()
		require.NoError(t, err)
		require.True(t, deleted)
		replacement = env.create(t, user, id, "uploading", past())
		env.finalize(t, user, replacement, replacementKey, past())
	}

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Skipped: 1}, result)
	require.NotNil(t, replacement)
	require.NotEqual(t, *stale.AttemptID, *replacement.AttemptID)
	stored := env.get(t, user, id)
	require.NotNil(t, stored, "the replacement must remain")
	require.Equal(t, *replacement.AttemptID, *stored.AttemptID)
	require.Equal(t, "ready", stored.Status)
	require.Equal(t, replacementKey, *stored.StorageKey)
	require.False(t, env.queuedBlobKeys(t)[replacementKey], "the replacement's blob must not be queued for deletion")
}

func TestCleanupDoesNotDeleteLinkedAttachments(t *testing.T) {
	t.Run("linked before listing", func(t *testing.T) {
		env := newCleanupEnv(t)
		user, id := uniqueUser(), uuid.New()
		created := env.create(t, user, id, "uploading", past())
		key := uniqueKey()
		env.finalize(t, user, created, key, past())
		env.link(t, user, id)

		result := env.svc.cleanupOnce(env.ctx)

		require.Equal(t, attachmentCleanupResult{}, result)
		require.NotNil(t, env.get(t, user, id).EntryID, "a linked row must stay linked")
		require.Empty(t, env.store.adminDeletes)
		require.False(t, env.queuedBlobKeys(t)[key])
	})

	t.Run("linked after listing", func(t *testing.T) {
		env := newCleanupEnv(t)
		user, id := uniqueUser(), uuid.New()
		created := env.create(t, user, id, "uploading", past())
		key := uniqueKey()
		env.finalize(t, user, created, key, past())
		env.store.afterList = func() { env.link(t, user, id) }

		result := env.svc.cleanupOnce(env.ctx)

		require.Equal(t, attachmentCleanupResult{Skipped: 1}, result)
		require.NotNil(t, env.get(t, user, id), "a row linked after listing must remain")
		require.NotNil(t, env.get(t, user, id).EntryID)
		require.False(t, env.queuedBlobKeys(t)[key])
	})
}

func TestCleanupCountsNotDeletedAsSkipNotFailure(t *testing.T) {
	out := captureLog(t)
	env := newCleanupEnv(t)
	user := uniqueUser()
	first, second := uuid.New(), uuid.New()
	env.create(t, user, first, "uploading", past())
	env.create(t, user, second, "uploading", past())
	env.store.conditionalFn = func(context.Context, uuid.UUID, *uuid.UUID) (bool, error) { return false, nil }

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Skipped: 2}, result)
	text := out.String()
	require.Contains(t, text, "result=success", "skips must not fail the job")
	require.NotContains(t, text, "result=failed")
	require.NotContains(t, text, "failureCount")
	require.NotContains(t, text, "Attachment cleanup delete failed")
	require.NotContains(t, text, "workCount", "skipped rows are not work")
}

func TestCleanupDeleteErrorIsAFailureAndCleanupContinues(t *testing.T) {
	out := captureLog(t)
	env := newCleanupEnv(t)
	user := uniqueUser()
	failing, passing := uuid.New(), uuid.New()
	env.create(t, user, failing, "uploading", past())
	env.create(t, user, passing, "uploading", past())
	boom := errors.New("boom")
	env.store.conditionalFn = func(ctx context.Context, id uuid.UUID, attempt *uuid.UUID) (bool, error) {
		if id == failing {
			return false, boom
		}
		return env.store.MemoryStore.DeleteExpiredAttachment(ctx, id, attempt)
	}

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Deleted: 1, Failed: 1}, result)
	require.NotNil(t, env.get(t, user, failing), "a failed delete leaves the row")
	require.Nil(t, env.get(t, user, passing), "one failure must not stop later rows")
	text := out.String()
	require.Contains(t, text, "Attachment cleanup delete failed")
	require.Contains(t, text, "attachmentId="+failing.String())
	require.Contains(t, text, "result=failed")
	require.Contains(t, text, "reason=metadata_delete_failed")
}

func TestCleanupQueuesBlobOnlyWhenTheConditionalDeleteSucceeds(t *testing.T) {
	env := newCleanupEnv(t)
	user := uniqueUser()

	deletedID, deletedKey := uuid.New(), uniqueKey()
	d := env.create(t, user, deletedID, "uploading", future())
	env.finalize(t, user, d, deletedKey, past())

	refreshedID, refreshedKey := uuid.New(), uniqueKey()
	r := env.create(t, user, refreshedID, "uploading", future())
	env.finalize(t, user, r, refreshedKey, past())

	linkedID, linkedKey := uuid.New(), uniqueKey()
	l := env.create(t, user, linkedID, "uploading", future())
	env.finalize(t, user, l, linkedKey, past())

	env.store.afterList = func() {
		// After listing, one row is refreshed and one is linked; both must survive.
		env.write(t, func(ctx context.Context) error {
			_, err := env.store.MemoryStore.UpdateAttachment(ctx, user, refreshedID, registrystore.AttachmentUpdate{ExpiresAt: future()})
			return err
		})
		env.link(t, user, linkedID)
	}

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Deleted: 1, Skipped: 2}, result)
	require.Equal(t, map[string]bool{deletedKey: true}, env.queuedBlobKeys(t),
		"only the deleted row's blob may be queued")
	require.NotNil(t, env.get(t, user, refreshedID))
	require.NotNil(t, env.get(t, user, linkedID))
}

func TestCleanupPassesEachRowsOwnAttemptID(t *testing.T) {
	env := newCleanupEnv(t)
	user := uniqueUser()
	want := map[uuid.UUID]*uuid.UUID{}
	for i := 0; i < 3; i++ {
		id := uuid.New()
		want[id] = env.create(t, user, id, "uploading", past()).AttemptID
	}
	legacyID := uuid.New()
	env.write(t, func(ctx context.Context) error {
		legacy, err := env.store.MemoryStore.CreateAttachment(ctx, user, "", model.Attachment{ContentType: "text/plain", ExpiresAt: past()})
		legacyID = legacy.ID
		return err
	})
	want[legacyID] = nil

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Deleted: 4}, result)
	require.Len(t, env.store.conditional, 4)
	for _, call := range env.store.conditional {
		expected, ok := want[call.ID]
		require.True(t, ok, "unexpected delete of %s", call.ID)
		require.Equal(t, expected, call.Attempt, "row %s must be deleted with its own attempt ID", call.ID)
	}
}

// A row another replica already deleted used to surface as a NotFoundError from
// AdminDeleteAttachment and count as a cleanup failure.
func TestCleanupRowDeletedAfterListingIsASkipNotAFailure(t *testing.T) {
	out := captureLog(t)
	env := newCleanupEnv(t)
	user, id := uniqueUser(), uuid.New()
	created := env.create(t, user, id, "uploading", past())
	env.store.afterList = func() {
		env.write(t, func(ctx context.Context) error {
			deleted, err := env.store.MemoryStore.DeleteExpiredAttachment(ctx, id, created.AttemptID)
			require.True(t, deleted)
			return err
		})
	}

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Skipped: 1}, result)
	text := out.String()
	require.Contains(t, text, "result=success")
	require.NotContains(t, text, "Attachment cleanup delete failed")
	require.NotContains(t, text, "result=failed")
}

// Skipped rows stay in the table, so they must not end the pass: the next page
// is still listed and processed. The list page size is 200.
func TestCleanupContinuesToNextPageAfterSkips(t *testing.T) {
	env := newCleanupEnv(t)
	user := uniqueUser()
	const rows = 205
	for i := 0; i < rows; i++ {
		env.create(t, user, uuid.New(), "uploading", past())
	}
	env.store.conditionalFn = func(context.Context, uuid.UUID, *uuid.UUID) (bool, error) { return false, nil }

	result := env.svc.cleanupOnce(env.ctx)

	require.Equal(t, attachmentCleanupResult{Skipped: rows}, result, "every listed row must be attempted, across both pages")
	seen := map[uuid.UUID]bool{}
	for _, call := range env.store.conditional {
		require.False(t, seen[call.ID], "row %s was attempted twice", call.ID)
		seen[call.ID] = true
	}
	require.Len(t, seen, rows)
}

// Cancellation during the delete is an interruption, not a cleanup failure. This
// branch is unchanged, but the delete call inside it is new.
func TestCleanupCancellationDuringDeleteIsNotAFailure(t *testing.T) {
	out := captureLog(t)
	env := newCleanupEnv(t)
	user, id := uniqueUser(), uuid.New()
	env.create(t, user, id, "uploading", past())
	ctx, cancel := context.WithCancel(env.ctx)
	t.Cleanup(cancel)
	env.store.afterList = cancel

	result := env.svc.cleanupOnce(ctx)

	require.Equal(t, attachmentCleanupResult{}, result, "an interrupted run neither deletes nor fails")
	require.NotNil(t, env.get(t, user, id), "the row must remain for the next pass")
	text := out.String()
	require.Contains(t, text, "result=canceled")
	require.Contains(t, text, "reason=shutdown")
	require.NotContains(t, text, "Attachment cleanup delete failed")
	require.NotContains(t, text, "result=failed")
}

// A panic in the store is recovered and reported through the returned result,
// which is assigned in a deferred function.
func TestCleanupRecoversPanicAndReportsFailure(t *testing.T) {
	out := captureLog(t)
	env := newCleanupEnv(t)
	user, id := uniqueUser(), uuid.New()
	env.create(t, user, id, "uploading", past())
	env.store.conditionalFn = func(context.Context, uuid.UUID, *uuid.UUID) (bool, error) { panic("store exploded") }

	var result attachmentCleanupResult
	require.NotPanics(t, func() { result = env.svc.cleanupOnce(env.ctx) })

	require.Equal(t, attachmentCleanupResult{Failed: 1}, result)
	require.NotNil(t, env.get(t, user, id))
	text := out.String()
	require.Contains(t, text, "reason=panic")
	require.Contains(t, text, "result=failed")
}
