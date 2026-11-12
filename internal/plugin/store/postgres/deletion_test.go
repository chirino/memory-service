//go:build !nopostgresql

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventstream"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestDescendantDeletionAtomicityAndClosure(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	var parent, child, fork, grandchild, unrelated *registrystore.ConversationDetail
	require.NoError(t, store.InWriteTx(ctx, func(ctx context.Context) error {
		var err error
		parent, err = store.CreateConversation(ctx, "owner", "app", "parent", nil, nil, nil, nil)
		require.NoError(t, err)
		result, err := store.createConversationWithID(ctx, "owner", "app", "child", "child", nil, nil, nil, nil, &parent.ID, nil)
		require.NoError(t, err)
		child = result.Conversation
		require.NoError(t, err)
		_, err = store.ShareConversation(ctx, "owner", child.ID, "child-reader", model.AccessLevelReader)
		require.NoError(t, err)
		fork, err = store.CreateConversation(ctx, "owner", "app", "child fork", nil, nil, &child.ID, nil)
		require.NoError(t, err)
		result, err = store.createConversationWithID(ctx, "owner", "app", "grandchild", "grandchild", nil, nil, nil, nil, &fork.ID, nil)
		require.NoError(t, err)
		grandchild = result.Conversation
		require.NoError(t, err)
		unrelated, err = store.CreateConversation(ctx, "owner", "app", "unrelated", nil, nil, nil, nil)
		return err
	}))

	// Overlapping roots and duplicate inputs must not duplicate deletion events.
	roots := []uuid.UUID{parent.ConversationGroupID, grandchild.ConversationGroupID, parent.ConversationGroupID}
	abort := errors.New("abort deletion")
	require.ErrorIs(t, store.InWriteTx(ctx, func(ctx context.Context) error {
		events, err := eventstream.DeleteConversationGroups(ctx, store, roots)
		require.NoError(t, err)
		require.Len(t, events, 4)
		return abort
	}), abort)

	var count int64
	for table, expected := range map[string]int64{"conversations": 5, "conversation_groups": 4, "tasks": 0, "outbox_events": 5} {
		require.NoError(t, store.db.Table(table).Count(&count).Error)
		require.Equal(t, expected, count, table)
	}

	require.NoError(t, store.InWriteTx(ctx, func(ctx context.Context) error {
		events, err := eventstream.DeleteConversationGroups(ctx, store, roots)
		require.NoError(t, err)
		require.Len(t, events, 4)
		seen := make(map[string]bool)
		for _, event := range events {
			id := event.Data.(map[string]any)["conversation"].(string)
			require.False(t, seen[id])
			seen[id] = true
			if id == parent.ID {
				require.ElementsMatch(t, []string{"owner"}, event.UserIDs)
			} else {
				require.ElementsMatch(t, []string{"owner", "child-reader"}, event.UserIDs)
			}
		}
		return nil
	}))
	for table, expected := range map[string]int64{"conversations": 1, "conversation_groups": 1, "conversation_memberships": 1, "tasks": 3, "outbox_events": 9} {
		require.NoError(t, store.db.Table(table).Count(&count).Error)
		require.Equal(t, expected, count, table)
	}
	_, err := store.GetConversation(ctx, "owner", unrelated.ID)
	require.NoError(t, err)
}

func TestDescendantDeletionBlocksConcurrentChildAndFork(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	parent, err := store.CreateConversation(ctx, "owner", "app", "parent", nil, nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, store.InWriteTx(ctx, func(deleteCtx context.Context) error {
		_, err := store.LoadDeletedConversationGroups(deleteCtx, []uuid.UUID{parent.ConversationGroupID})
		require.NoError(t, err)
		for _, isFork := range []bool{false, true} {
			err := store.InWriteTx(ctx, func(createCtx context.Context) error {
				require.NoError(t, store.dbFor(createCtx).Exec("SET LOCAL lock_timeout = '100ms'").Error)
				if isFork {
					_, err := store.CreateConversation(createCtx, "owner", "app", "fork", nil, nil, &parent.ID, nil)
					return err
				}
				_, err := store.createConversationWithID(createCtx, "owner", "app", "late-child", "late child", nil, nil, nil, nil, &parent.ID, nil)
				return err
			})
			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			require.Equal(t, "55P03", pgErr.Code)
		}
		return nil
	}))
}

func TestNamedTaskRetryInsideTransaction(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	require.NoError(t, store.InWriteTx(ctx, func(ctx context.Context) error {
		body := map[string]any{"taskName": "same-task"}
		require.NoError(t, store.CreateTask(ctx, "test", body))
		require.NoError(t, store.CreateTask(ctx, "test", body))
		var count int64
		require.NoError(t, store.dbFor(ctx).Table("tasks").Count(&count).Error)
		require.EqualValues(t, 1, count)
		return nil
	}))
}

func TestPostgresAttachmentBlobDeleteCoordination(t *testing.T) {
	t.Run("queued task is canceled by new reference", func(t *testing.T) {
		store, ctx := setupPostgresOutboxStore(t)
		storageKey := "queued-key"
		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			if err := registrystore.CreateAttachmentBlobDeleteTask(txCtx, store, storageKey); err != nil {
				return err
			}
			_, err := store.CreateAttachment(txCtx, "user1", "", model.Attachment{StorageKey: &storageKey})
			return err
		}))
		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			tasks, err := store.ClaimReadyTasks(txCtx, 10)
			require.NoError(t, err)
			require.Empty(t, tasks)
			return nil
		}))
		queuedUpdateKey := "queued-update-key"
		var updateAttachment *model.Attachment
		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			var err error
			updateAttachment, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{})
			if err != nil {
				return err
			}
			if err := registrystore.CreateAttachmentBlobDeleteTask(txCtx, store, queuedUpdateKey); err != nil {
				return err
			}
			_, err = store.UpdateAttachment(txCtx, "user1", updateAttachment.ID, registrystore.AttachmentUpdate{StorageKey: &queuedUpdateKey})
			return err
		}))
		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			tasks, err := store.ClaimReadyTasks(txCtx, 10)
			require.NoError(t, err)
			require.Empty(t, tasks)
			return nil
		}))
	})

	t.Run("claimed tasks block new references and completion reclaims the guard", func(t *testing.T) {
		store, ctx := setupPostgresOutboxStore(t)
		storageKey := "claimed-key"
		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			return registrystore.CreateAttachmentBlobDeleteTask(txCtx, store, storageKey)
		}))
		var taskID uuid.UUID
		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			tasks, err := store.ClaimReadyTasks(txCtx, 1)
			if err != nil {
				return err
			}
			require.Len(t, tasks, 1)
			taskID = tasks[0].ID
			return nil
		}))
		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			return store.FailTask(txCtx, taskID, "provider unavailable", time.Hour)
		}))
		err := store.InWriteTx(ctx, func(txCtx context.Context) error {
			_, err := store.CreateAttachment(txCtx, "user1", "", model.Attachment{StorageKey: &storageKey})
			return err
		})
		var conflict *registrystore.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, registrystore.AttachmentBlobDeleteConflictCode, conflict.Code)
		var attachment *model.Attachment
		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			var err error
			attachment, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{})
			return err
		}))
		err = store.InWriteTx(ctx, func(txCtx context.Context) error {
			_, err := store.UpdateAttachment(txCtx, "user1", attachment.ID, registrystore.AttachmentUpdate{StorageKey: &storageKey})
			return err
		})
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, registrystore.AttachmentBlobDeleteConflictCode, conflict.Code)

		require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
			return store.CompleteAttachmentBlobDelete(txCtx, taskID)
		}))
		require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
			var count int64
			require.NoError(t, store.dbFor(txCtx).Where("task_name = ?", registrystore.AttachmentBlobDeleteTaskName(storageKey)).Model(&model.Task{}).Count(&count).Error)
			require.Zero(t, count)
			return nil
		}))
	})
}

func TestPostgresAttachmentBlobGuardSerializesAbsentRow(t *testing.T) {
	for _, mode := range []string{"create", "update"} {
		t.Run(mode, func(t *testing.T) {
			store, ctx := setupPostgresOutboxStore(t)
			storageKey := "absent-guard-" + mode
			var target *model.Attachment
			if mode == "update" {
				require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
					var err error
					target, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{})
					return err
				}))
			}

			guardWritten := make(chan struct{})
			releaseReference := make(chan struct{})
			referenceDone := make(chan error, 1)
			go func() {
				referenceDone <- store.InWriteTx(ctx, func(txCtx context.Context) error {
					if err := store.RegisterAttachmentBlobReference(txCtx, storageKey); err != nil {
						return err
					}
					close(guardWritten)
					<-releaseReference
					if mode == "create" {
						_, err := store.CreateAttachment(txCtx, "user1", "", model.Attachment{StorageKey: &storageKey})
						return err
					}
					_, err := store.UpdateAttachment(txCtx, "user1", target.ID, registrystore.AttachmentUpdate{StorageKey: &storageKey})
					return err
				})
			}()
			<-guardWritten

			cleanupStarted := make(chan struct{})
			cleanupDone := make(chan error, 1)
			go func() {
				cleanupDone <- store.InWriteTx(ctx, func(txCtx context.Context) error {
					close(cleanupStarted)
					return store.QueueAttachmentBlobDelete(txCtx, storageKey)
				})
			}()
			<-cleanupStarted
			close(releaseReference)
			require.NoError(t, <-referenceDone)
			require.NoError(t, <-cleanupDone)

			var task model.Task
			require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
				tasks, err := store.ClaimReadyTasks(txCtx, 1)
				if err != nil {
					return err
				}
				require.Len(t, tasks, 1)
				task = tasks[0]
				return nil
			}))
			require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
				referenced, err := store.PrepareAttachmentBlobDelete(txCtx, task.ID, storageKey)
				require.NoError(t, err)
				require.True(t, referenced, "provider deletion must be suppressed for the concurrently committed reference")
				return nil
			}))
		})
	}
}

func TestPostgresAttachmentPrepareSerializesConcurrentFinalDelete(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	storageKey := "prepare-final-delete"
	var first, surviving *model.Attachment
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		first, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{StorageKey: &storageKey})
		if err != nil {
			return err
		}
		surviving, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{StorageKey: &storageKey})
		return err
	}))
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.AdminDeleteAttachment(txCtx, first.ID)
	}))

	var task model.Task
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		tasks, err := store.ClaimReadyTasks(txCtx, 1)
		if err != nil {
			return err
		}
		require.Len(t, tasks, 1)
		task = tasks[0]
		return nil
	}))

	counted := make(chan struct{})
	resumePrepare := make(chan struct{})
	var once sync.Once
	store.afterAttachmentDeleteCount = func() error {
		once.Do(func() {
			close(counted)
			<-resumePrepare
		})
		return nil
	}
	t.Cleanup(func() { store.afterAttachmentDeleteCount = nil })

	type prepareOutcome struct {
		referenced bool
		err        error
	}
	prepareDone := make(chan prepareOutcome, 1)
	go func() {
		var referenced bool
		err := store.InWriteTx(ctx, func(txCtx context.Context) error {
			var err error
			referenced, err = store.PrepareAttachmentBlobDelete(txCtx, task.ID, storageKey)
			return err
		})
		prepareDone <- prepareOutcome{referenced: referenced, err: err}
	}()
	<-counted

	metadataDeleted := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- store.InWriteTx(ctx, func(txCtx context.Context) error {
			// Keep the metadata deletion and queue update separate in this test so
			// the prepare transaction can be paused between its reference count and
			// task-state update. Production deletion performs both atomically.
			if err := store.dbFor(txCtx).Where("id = ?", surviving.ID).Delete(&model.Attachment{}).Error; err != nil {
				return err
			}
			close(metadataDeleted)
			return store.QueueAttachmentBlobDelete(txCtx, storageKey)
		})
	}()
	<-metadataDeleted
	close(resumePrepare)
	outcome := <-prepareDone
	require.NoError(t, outcome.err)
	require.True(t, outcome.referenced)
	require.NoError(t, <-deleteDone)

	store.afterAttachmentDeleteCount = nil
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		tasks, err := store.ClaimReadyTasks(txCtx, 1)
		if err != nil {
			return err
		}
		require.Len(t, tasks, 1)
		taskID := tasks[0].ID
		referenced, err := store.PrepareAttachmentBlobDelete(txCtx, taskID, storageKey)
		require.NoError(t, err)
		require.False(t, referenced)
		return store.CompleteAttachmentBlobDelete(txCtx, taskID)
	}))
}

func TestPostgresAttachmentDeleteRevalidatesFinalizedStorageKey(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	var attachment *model.Attachment
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		attachment, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{Status: "downloading"})
		return err
	}))

	initialRead := make(chan struct{})
	resumeDelete := make(chan struct{})
	var once sync.Once
	store.afterAttachmentDeleteRead = func() error {
		once.Do(func() {
			close(initialRead)
			<-resumeDelete
		})
		return nil
	}
	t.Cleanup(func() { store.afterAttachmentDeleteRead = nil })

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- store.InWriteTx(ctx, func(txCtx context.Context) error {
			return store.DeleteAttachment(txCtx, "user1", "", attachment.ID)
		})
	}()
	<-initialRead

	storageKey := "finalized-after-delete-read"
	status := "ready"
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.UpdateAttachment(txCtx, "user1", attachment.ID, registrystore.AttachmentUpdate{
			StorageKey: &storageKey,
			Status:     &status,
		})
		return err
	}))
	close(resumeDelete)
	require.NoError(t, <-deleteDone)

	store.afterAttachmentDeleteRead = nil
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		tasks, err := store.ClaimReadyTasks(txCtx, 1)
		if err != nil {
			return err
		}
		require.Len(t, tasks, 1)
		require.Equal(t, storageKey, tasks[0].TaskBody[registrystore.AttachmentBlobDeleteStorageKeyField])
		referenced, err := store.PrepareAttachmentBlobDelete(txCtx, tasks[0].ID, storageKey)
		require.NoError(t, err)
		require.False(t, referenced)
		return store.CompleteAttachmentBlobDelete(txCtx, tasks[0].ID)
	}))
}

func TestPostgresConcurrentFinalAdminDeletesQueueCleanup(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	storageKey := "concurrent-final-admin-deletes"
	attachments := make([]*model.Attachment, 2)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		for i := range attachments {
			attachment, err := store.CreateAttachment(txCtx, "user1", "", model.Attachment{StorageKey: &storageKey})
			if err != nil {
				return err
			}
			attachments[i] = attachment
		}
		return nil
	}))

	start := make(chan struct{})
	done := make(chan error, len(attachments))
	for _, attachment := range attachments {
		attachment := attachment
		go func() {
			<-start
			done <- store.InWriteTx(ctx, func(txCtx context.Context) error {
				return store.AdminDeleteAttachment(txCtx, attachment.ID)
			})
		}()
	}
	close(start)
	for range attachments {
		require.NoError(t, <-done)
	}

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		tasks, err := store.ClaimReadyTasks(txCtx, 2)
		if err != nil {
			return err
		}
		require.Len(t, tasks, 1)
		referenced, err := store.PrepareAttachmentBlobDelete(txCtx, tasks[0].ID, storageKey)
		require.NoError(t, err)
		require.False(t, referenced)
		return store.CompleteAttachmentBlobDelete(txCtx, tasks[0].ID)
	}))
}

func TestPostgresCompletedAttachmentBlobDeletesDoNotAccumulateTasks(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		for i := 0; i < 100; i++ {
			if err := store.QueueAttachmentBlobDelete(txCtx, fmt.Sprintf("completed-key-%d", i)); err != nil {
				return err
			}
		}
		return nil
	}))
	var tasks []model.Task
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		tasks, err = store.ClaimReadyTasks(txCtx, 100)
		return err
	}))
	require.Len(t, tasks, 100)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		for _, task := range tasks {
			if err := store.CompleteAttachmentBlobDelete(txCtx, task.ID); err != nil {
				return err
			}
		}
		return nil
	}))
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		var count int64
		require.NoError(t, store.dbFor(txCtx).Where("task_type = ?", registrystore.AttachmentBlobDeleteTaskType).Model(&model.Task{}).Count(&count).Error)
		require.Zero(t, count)
		return nil
	}))
}

func TestPostgresReadyTaskScanSkipsReferenceGuards(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		for i := 0; i < 1000; i++ {
			if err := store.RegisterAttachmentBlobReference(txCtx, fmt.Sprintf("guard-%d", i)); err != nil {
				return err
			}
		}
		return store.CreateTask(txCtx, "generic-ready", map[string]any{"value": "ready"})
	}))
	// Simulate a version-2 datastore created before the ready-time index was
	// added. Startup migration must add it without a schema reset.
	require.NoError(t, store.db.WithContext(ctx).Exec("DROP INDEX idx_tasks_ready_v2").Error)
	require.NoError(t, (&postgresMigrator{}).Migrate(ctx))

	rows, err := store.db.WithContext(ctx).Raw(`
		EXPLAIN SELECT id
		FROM tasks
		WHERE retry_at <= NOW()
		  AND (task_type <> ? OR COALESCE(task_body->>'state', 'queued') IN ('queued', 'claimed'))
		ORDER BY retry_at, created_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`, registrystore.AttachmentBlobDeleteTaskType).Rows()
	require.NoError(t, err)
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	require.NoError(t, rows.Err())
	require.Contains(t, plan.String(), "idx_tasks_ready_v2")

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		tasks, err := store.ClaimReadyTasks(txCtx, 1)
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		require.Equal(t, "generic-ready", tasks[0].TaskType)
		return nil
	}))
}

func TestPostgresCloneRejectsSourceDeletedBeforeLegacyGuardAcquisition(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	storageKey := "legacy-unguarded-key"
	source := model.Attachment{ID: uuid.New(), StorageKey: &storageKey, UserID: "user1", Status: "ready"}
	require.NoError(t, store.db.WithContext(ctx).Create(&source).Error)

	sourceRead := make(chan struct{})
	resumeClone := make(chan struct{})
	var once sync.Once
	store.afterAttachmentCloneRead = func() error {
		once.Do(func() { close(sourceRead) })
		<-resumeClone
		return nil
	}
	t.Cleanup(func() { store.afterAttachmentCloneRead = nil })

	cloneDone := make(chan error, 1)
	go func() {
		cloneDone <- store.InWriteTx(ctx, func(txCtx context.Context) error {
			_, err := store.CloneAttachment(txCtx, "user1", source.ID)
			return err
		})
	}()
	<-sourceRead

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.AdminDeleteAttachment(txCtx, source.ID)
	}))
	var cleanup model.Task
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		tasks, err := store.ClaimReadyTasks(txCtx, 1)
		if err != nil {
			return err
		}
		require.Len(t, tasks, 1)
		cleanup = tasks[0]
		referenced, err := store.PrepareAttachmentBlobDelete(txCtx, cleanup.ID, storageKey)
		require.NoError(t, err)
		require.False(t, referenced)
		return nil
	}))
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.CompleteAttachmentBlobDelete(txCtx, cleanup.ID)
	}))

	close(resumeClone)
	err := <-cloneDone
	var notFound *registrystore.NotFoundError
	require.ErrorAs(t, err, &notFound)
	var count int64
	require.NoError(t, store.db.WithContext(ctx).Model(&model.Attachment{}).Where("storage_key = ?", storageKey).Count(&count).Error)
	require.Zero(t, count)
}
