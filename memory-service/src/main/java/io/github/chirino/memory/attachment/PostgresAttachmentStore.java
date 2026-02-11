package io.github.chirino.memory.attachment;

import io.github.chirino.memory.persistence.entity.AttachmentEntity;
import io.github.chirino.memory.persistence.entity.EntryEntity;
import io.github.chirino.memory.persistence.repo.AttachmentRepository;
import io.github.chirino.memory.persistence.repo.EntryRepository;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class PostgresAttachmentStore implements AttachmentStore {

    @Inject AttachmentRepository attachmentRepository;

    @Inject EntryRepository entryRepository;

    @Override
    @Transactional
    public AttachmentDto create(
            String userId, String contentType, String filename, Instant expiresAt) {
        AttachmentEntity entity = new AttachmentEntity();
        entity.setUserId(userId);
        entity.setContentType(contentType);
        entity.setFilename(filename);
        entity.setExpiresAt(expiresAt != null ? expiresAt.atOffset(ZoneOffset.UTC) : null);
        attachmentRepository.persist(entity);
        return toDto(entity);
    }

    @Override
    @Transactional
    public AttachmentDto createFromSource(String userId, AttachmentDto source) {
        AttachmentEntity entity = new AttachmentEntity();
        entity.setUserId(userId);
        entity.setStorageKey(source.storageKey());
        entity.setSha256(source.sha256());
        entity.setSize(source.size());
        entity.setContentType(source.contentType());
        entity.setFilename(source.filename());
        entity.setExpiresAt(OffsetDateTime.now(ZoneOffset.UTC).plusMinutes(5));
        attachmentRepository.persist(entity);
        return toDto(entity);
    }

    @Override
    @Transactional
    public void updateAfterUpload(
            String id, String storageKey, long size, String sha256, Instant expiresAt) {
        AttachmentEntity entity = attachmentRepository.findActiveById(UUID.fromString(id));
        if (entity != null) {
            entity.setStorageKey(storageKey);
            entity.setSize(size);
            entity.setSha256(sha256);
            entity.setExpiresAt(expiresAt != null ? expiresAt.atOffset(ZoneOffset.UTC) : null);
        }
    }

    @Override
    public Optional<AttachmentDto> findById(String id) {
        AttachmentEntity entity = attachmentRepository.findActiveById(UUID.fromString(id));
        return entity != null ? Optional.of(toDto(entity)) : Optional.empty();
    }

    @Override
    public Optional<AttachmentDto> findByIdForUser(String id, String userId) {
        AttachmentEntity entity =
                attachmentRepository.findByIdAndUserId(UUID.fromString(id), userId);
        return entity != null ? Optional.of(toDto(entity)) : Optional.empty();
    }

    @Override
    @Transactional
    public void linkToEntry(String attachmentId, String entryId) {
        AttachmentEntity entity =
                attachmentRepository.findActiveById(UUID.fromString(attachmentId));
        if (entity != null) {
            EntryEntity entryEntity = entryRepository.findById(UUID.fromString(entryId));
            entity.setEntry(entryEntity);
            entity.setExpiresAt(null);
        }
    }

    @Override
    public List<AttachmentDto> findByEntryId(String entryId) {
        return attachmentRepository.findByEntryId(UUID.fromString(entryId)).stream()
                .map(this::toDto)
                .toList();
    }

    @Override
    public List<AttachmentDto> findExpired() {
        return attachmentRepository.findExpired().stream().map(this::toDto).toList();
    }

    @Override
    @Transactional
    public void delete(String id) {
        attachmentRepository.deleteById(UUID.fromString(id));
    }

    @Override
    @Transactional
    public void softDelete(String id) {
        AttachmentEntity entity = attachmentRepository.findActiveById(UUID.fromString(id));
        if (entity != null) {
            entity.setDeletedAt(OffsetDateTime.now(ZoneOffset.UTC));
        }
    }

    @Override
    public List<AttachmentDto> findSoftDeleted() {
        return attachmentRepository.findSoftDeleted().stream().map(this::toDto).toList();
    }

    @Override
    public List<AttachmentDto> findByStorageKeyForUpdate(String storageKey) {
        return attachmentRepository.findByStorageKeyForUpdate(storageKey).stream()
                .map(this::toDto)
                .toList();
    }

    @Override
    public List<AttachmentDto> findByEntryIds(List<String> entryIds) {
        List<UUID> uuids = entryIds.stream().map(UUID::fromString).toList();
        return attachmentRepository.findByEntryIds(uuids).stream().map(this::toDto).toList();
    }

    @Override
    public String getConversationGroupIdForEntry(String entryId) {
        EntryEntity entry = entryRepository.findById(UUID.fromString(entryId));
        return entry != null ? entry.getConversationGroupId().toString() : null;
    }

    private AttachmentDto toDto(AttachmentEntity entity) {
        return new AttachmentDto(
                entity.getId().toString(),
                entity.getStorageKey(),
                entity.getFilename(),
                entity.getContentType(),
                entity.getSize(),
                entity.getSha256(),
                entity.getUserId(),
                entity.getEntry() != null ? entity.getEntry().getId().toString() : null,
                entity.getExpiresAt() != null ? entity.getExpiresAt().toInstant() : null,
                entity.getCreatedAt() != null ? entity.getCreatedAt().toInstant() : null,
                entity.getDeletedAt() != null ? entity.getDeletedAt().toInstant() : null);
    }
}
