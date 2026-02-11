package io.github.chirino.memory.attachment;

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
}
