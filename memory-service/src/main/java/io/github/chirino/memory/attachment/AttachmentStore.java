package io.github.chirino.memory.attachment;

import io.github.chirino.memory.model.AdminAttachmentQuery;
import java.time.Instant;
import java.util.List;
import java.util.Optional;

public interface AttachmentStore {

    AttachmentDto create(String userId, String contentType, String filename, Instant expiresAt);

    AttachmentDto createFromSource(String userId, AttachmentDto source);

    void updateAfterUpload(
            String id, String storageKey, long size, String sha256, Instant expiresAt);

    Optional<AttachmentDto> findById(String id);

    Optional<AttachmentDto> findByIdForUser(String id, String userId);

    void linkToEntry(String attachmentId, String entryId);

    List<AttachmentDto> findByEntryId(String entryId);

    List<AttachmentDto> findExpired();

    void delete(String id);

    void softDelete(String id);

    List<AttachmentDto> findSoftDeleted();

    List<AttachmentDto> findByStorageKeyForUpdate(String storageKey);

    List<AttachmentDto> findByEntryIds(List<String> entryIds);

    String getConversationGroupIdForEntry(String entryId);

    // Admin methods

    List<AttachmentDto> adminList(AdminAttachmentQuery query);

    /** Find by ID without ownership filter, includes soft-deleted records. */
    Optional<AttachmentDto> adminFindById(String id);

    /** Count of attachment records sharing the same storageKey. */
    long adminCountByStorageKey(String storageKey);

    /** Clear entryId and set expiresAt (for admin delete of linked attachments). */
    void adminUnlinkFromEntry(String attachmentId);
}
