package io.github.chirino.memory.attachment;

import java.time.Instant;
import java.util.List;
import java.util.Optional;

public interface AttachmentStore {

    AttachmentDto create(String userId, String contentType, String filename, Instant expiresAt);

    void updateAfterUpload(
            String id, String storageKey, long size, String sha256, Instant expiresAt);

    Optional<AttachmentDto> findById(String id);

    Optional<AttachmentDto> findByIdForUser(String id, String userId);

    void linkToEntry(String attachmentId, String entryId);

    List<AttachmentDto> findByEntryId(String entryId);

    List<AttachmentDto> findExpired();

    void delete(String id);

    List<AttachmentDto> findByEntryIds(List<String> entryIds);
}
