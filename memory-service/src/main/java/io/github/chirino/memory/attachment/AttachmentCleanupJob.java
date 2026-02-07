package io.github.chirino.memory.attachment;

import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import java.util.List;
import org.jboss.logging.Logger;

@ApplicationScoped
public class AttachmentCleanupJob {

    private static final Logger LOG = Logger.getLogger(AttachmentCleanupJob.class);

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject FileStoreSelector fileStoreSelector;

    @Scheduled(every = "${memory-service.attachments.cleanup-interval:5m}")
    @Transactional
    public void cleanupExpiredAttachments() {
        AttachmentStore attachmentStore = attachmentStoreSelector.getStore();
        FileStore fileStore = fileStoreSelector.getFileStore();

        List<AttachmentDto> expired = attachmentStore.findExpired();
        if (expired.isEmpty()) {
            return;
        }

        LOG.infof("Cleaning up %d expired attachments", expired.size());
        for (AttachmentDto att : expired) {
            try {
                if (att.storageKey() != null) {
                    fileStore.delete(att.storageKey());
                }
                attachmentStore.delete(att.id());
            } catch (Exception e) {
                LOG.warnf("Failed to cleanup expired attachment %s: %s", att.id(), e.getMessage());
            }
        }
    }
}
