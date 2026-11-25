package service

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/operationevent"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

type AttachmentCleanupService struct {
	store    registrystore.MemoryStore
	interval time.Duration
}

func NewAttachmentCleanupService(store registrystore.MemoryStore, _ registryattach.AttachmentStore, interval time.Duration) *AttachmentCleanupService {
	return &AttachmentCleanupService{
		store:    store,
		interval: interval,
	}
}

func (s *AttachmentCleanupService) Start(ctx context.Context) {
	if s == nil || s.store == nil || s.interval <= 0 {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupOnce(ctx)
		}
	}
}

// attachmentCleanupResult reports one cleanup pass. Deleted rows are the work
// count. Skipped rows were listed as expired but no longer qualified by the time
// of the delete; they are not failures.
type attachmentCleanupResult struct {
	Deleted int64
	Skipped int64
	Failed  int64
}

func (s *AttachmentCleanupService) cleanupOnce(ctx context.Context) (result attachmentCleanupResult) {
	event := operationevent.New("job.attachment_cleanup")
	var work, failures int64
	started := false
	defer func() {
		event.SetWorkCount(work)
		event.SetFailureCount(failures)
		emitJobTerminal(event, ctx, failures)
		result.Deleted, result.Failed = work, failures
	}()
	defer recoverJobPanic(event, func() { failures++ })
	var afterCursor *string
	for {
		var attachments []registrystore.AdminAttachment
		var cursor *string
		err := s.store.InReadTx(ctx, func(readCtx context.Context) error {
			var err error
			attachments, cursor, err = s.store.AdminListAttachments(readCtx, registrystore.AdminAttachmentQuery{
				Status:      "expired",
				Limit:       200,
				AfterCursor: afterCursor,
			})
			return err
		})
		if err != nil {
			if markJobInterrupted(event, ctx, err) {
				return
			}
			log.Error("Attachment cleanup list failed", "err", err)
			event.SetReason("list_failed")
			event.EnrichError(err)
			failures++
			return
		}
		if len(attachments) > 0 && !started {
			event.EmitStart()
			started = true
		}
		for _, attachment := range attachments {
			// Cleanup only unlinked attachments.
			if attachment.EntryID != nil {
				continue
			}
			// The list and the delete are separate transactions, so the row may have
			// been refreshed, linked, deleted or replaced under the same ID in
			// between. The delete re-checks the listed attempt ID, the link and the
			// expiry, and reports false when the row no longer qualifies.
			// Assigned on every callback run so a replayed transaction cannot keep a
			// stale value.
			var deleted bool
			if err := s.store.InWriteTx(ctx, func(writeCtx context.Context) error {
				var err error
				deleted, err = s.store.DeleteExpiredAttachment(writeCtx, attachment.ID, attachment.AttemptID)
				return err
			}); err != nil {
				if markJobInterrupted(event, ctx, err) {
					return
				}
				log.Error("Attachment cleanup delete failed", "attachmentId", attachment.ID.String(), "err", err)
				event.SetReason("metadata_delete_failed")
				event.EnrichError(err)
				failures++
				continue
			}
			if !deleted {
				result.Skipped++
				continue
			}
			work++
		}
		if cursor == nil {
			return
		}
		afterCursor = cursor
	}
}
