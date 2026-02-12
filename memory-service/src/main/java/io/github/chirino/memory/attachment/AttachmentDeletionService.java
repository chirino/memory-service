package io.github.chirino.memory.attachment;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import java.util.List;
import org.jboss.logging.Logger;

/**
 * Handles attachment deletion with reference-counting on shared storage keys.
 *
 * <p>Multiple attachment records can share the same {@code storageKey} (when an attachment is
 * referenced from a forked entry). The blob in the FileStore must only be deleted when the last
 * record referencing it is removed.
 *
 * <p>The protocol uses soft-delete to ensure that a crash between database and file operations
 * never loses data:
 *
 * <ol>
 *   <li>Lock all records with the same storageKey (SELECT FOR UPDATE)
 *   <li>If other active records remain, hard-delete this record (blob still referenced)
 *   <li>If this is the last active record, soft-delete it (set deletedAt), commit, then delete the
 *       file and hard-delete the record
 * </ol>
 */
@ApplicationScoped
public class AttachmentDeletionService {

    private static final Logger LOG = Logger.getLogger(AttachmentDeletionService.class);

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject FileStoreSelector fileStoreSelector;

    /**
     * Delete an attachment record, safely handling shared storage keys. If this is the last record
     * referencing the blob, the file is also deleted from the FileStore.
     */
    public void deleteAttachment(String attachmentId) {
        AttachmentStore store = attachmentStoreSelector.getStore();
        FileStore fileStore = fileStoreSelector.getFileStore();

        var optAtt = store.findById(attachmentId);
        if (optAtt.isEmpty()) {
            // Already deleted or doesn't exist
            return;
        }

        String storageKey = optAtt.get().storageKey();
        if (storageKey == null) {
            // No blob to worry about — just delete the record
            store.delete(attachmentId);
            return;
        }

        boolean needsFileDelete = deleteRecordWithRefCount(attachmentId, storageKey);

        if (needsFileDelete) {
            // File deletion happens outside the transaction
            try {
                fileStore.delete(storageKey);
            } catch (Exception e) {
                LOG.warnf(
                        "Failed to delete file for attachment %s (storageKey=%s): %s",
                        attachmentId, storageKey, e.getMessage());
            }
            // Hard-delete the soft-deleted record
            store.delete(attachmentId);
        }
    }

    /**
     * Deletes a list of attachments, handling shared storage keys correctly.
     */
    public void deleteAttachments(List<AttachmentDto> attachments) {
        for (AttachmentDto att : attachments) {
            try {
                deleteAttachment(att.id());
            } catch (Exception e) {
                LOG.warnf("Failed to delete attachment %s: %s", att.id(), e.getMessage());
            }
        }
    }

    /**
     * Cleans up soft-deleted attachment records by retrying file deletion and then hard-deleting
     * the record. This handles crash recovery scenarios.
     */
    public void cleanupSoftDeleted() {
        AttachmentStore store = attachmentStoreSelector.getStore();
        FileStore fileStore = fileStoreSelector.getFileStore();

        List<AttachmentDto> softDeleted = store.findSoftDeleted();
        if (softDeleted.isEmpty()) {
            return;
        }

        LOG.infof("Cleaning up %d soft-deleted attachments", softDeleted.size());
        for (AttachmentDto att : softDeleted) {
            try {
                if (att.storageKey() != null) {
                    fileStore.delete(att.storageKey()); // Idempotent
                }
                store.delete(att.id());
            } catch (Exception e) {
                LOG.warnf(
                        "Failed to cleanup soft-deleted attachment %s: %s",
                        att.id(), e.getMessage());
            }
        }
    }

    /**
     * Checks reference count and either hard-deletes or soft-deletes the record.
     *
     * @return true if the caller needs to delete the file and hard-delete the record
     */
    @Transactional
    boolean deleteRecordWithRefCount(String attachmentId, String storageKey) {
        AttachmentStore store = attachmentStoreSelector.getStore();

        // Lock all records sharing this storageKey
        List<AttachmentDto> siblings = store.findByStorageKeyForUpdate(storageKey);

        long activeCount = siblings.stream().filter(a -> a.deletedAt() == null).count();

        if (activeCount > 1) {
            // Other references remain — just hard delete this record
            store.delete(attachmentId);
            return false;
        } else {
            // Last reference — soft delete, caller will delete file then hard delete
            store.softDelete(attachmentId);
            return true;
        }
    }
}
