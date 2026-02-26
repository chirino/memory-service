package service

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
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
	var afterCursor *string
	for {
		attachments, cursor, err := s.store.AdminListAttachments(ctx, registrystore.AdminAttachmentQuery{
			Status:      "expired",
			Limit:       200,
			AfterCursor: afterCursor,
		})
		if err != nil {
			log.Error("Attachment cleanup list failed", "err", err)
			return
		}
		for _, attachment := range attachments {
			// Cleanup only unlinked attachments.
			if attachment.EntryID != nil {
				continue
			}
			if err := s.store.AdminDeleteAttachment(ctx, attachment.ID); err != nil {
				log.Error("Attachment cleanup delete failed", "attachmentId", attachment.ID.String(), "err", err)
				continue
			}
			if s.attachStore != nil && attachment.StorageKey != nil && attachment.RefCount <= 1 {
				if err := s.attachStore.Delete(ctx, *attachment.StorageKey); err != nil {
					log.Warn("Attachment cleanup blob delete failed", "attachmentId", attachment.ID.String(), "err", err)
				}
			}
		}
		if cursor == nil {
			return
		}
		afterCursor = cursor
	}
}
