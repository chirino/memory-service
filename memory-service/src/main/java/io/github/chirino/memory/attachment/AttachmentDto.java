package io.github.chirino.memory.attachment;

import java.time.Instant;

public record AttachmentDto(
        String id,
        String storageKey,
        String filename,
        String contentType,
        Long size,
        String sha256,
        String userId,
        String entryId,
        Instant expiresAt,
        Instant createdAt,
        Instant deletedAt,
        String status,
        String sourceUrl) {}
