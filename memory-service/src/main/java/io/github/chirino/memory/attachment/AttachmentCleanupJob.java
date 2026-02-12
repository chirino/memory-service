package io.github.chirino.memory.attachment;

import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.List;
import org.jboss.logging.Logger;

@ApplicationScoped
public class AttachmentCleanupJob {

    private static final Logger LOG = Logger.getLogger(AttachmentCleanupJob.class);

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject AttachmentDeletionService deletionService;

    @Scheduled(every = "${memory-service.attachments.cleanup-interval:5m}")
    public void cleanupExpiredAttachments() {
        AttachmentStore attachmentStore = attachmentStoreSelector.getStore();

        List<AttachmentDto> expired = attachmentStore.findExpired();
        if (!expired.isEmpty()) {
            LOG.infof("Cleaning up %d expired attachments", expired.size());
            deletionService.deleteAttachments(expired);
        }

        // Also retry cleanup of soft-deleted records (crash recovery)
        deletionService.cleanupSoftDeleted();
    }
}
