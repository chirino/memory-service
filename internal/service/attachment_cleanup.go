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
	store       registrystore.MemoryStore
	attachStore registryattach.AttachmentStore
	interval    time.Duration
}

func NewAttachmentCleanupService(store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, interval time.Duration) *AttachmentCleanupService {
	return &AttachmentCleanupService{
		store:       store,
		attachStore: attachStore,
		interval:    interval,
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

func (s *AttachmentCleanupService) cleanupOnce(ctx context.Context) {
	event := operationevent.New("job.attachment_cleanup")
	var work, failures int64
	started := false
	defer func() {
		event.SetWorkCount(work)
		event.SetFailureCount(failures)
		emitJobTerminal(event, ctx, failures)
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
			if err := s.store.InWriteTx(ctx, func(writeCtx context.Context) error {
				return s.store.AdminDeleteAttachment(writeCtx, attachment.ID)
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
			work++
			if s.attachStore != nil && attachment.StorageKey != nil && attachment.RefCount <= 1 {
				if err := s.attachStore.Delete(ctx, *attachment.StorageKey); err != nil {
					if markJobInterrupted(event, ctx, err) {
						return
					}
					log.Warn("Attachment cleanup blob delete failed", "attachmentId", attachment.ID.String(), "err", err)
					event.SetReason("blob_delete_failed")
					event.EnrichError(err)
					failures++
				}
			}
		}
		if cursor == nil {
			return
		}
		afterCursor = cursor
	}
}
