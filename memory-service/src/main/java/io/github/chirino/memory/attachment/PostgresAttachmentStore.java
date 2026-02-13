package io.github.chirino.memory.attachment;

import io.github.chirino.memory.model.AdminAttachmentQuery;
import io.github.chirino.memory.persistence.entity.AttachmentEntity;
import io.github.chirino.memory.persistence.entity.EntryEntity;
import io.github.chirino.memory.persistence.repo.AttachmentRepository;
import io.github.chirino.memory.persistence.repo.EntryRepository;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.persistence.TypedQuery;
import jakarta.transaction.Transactional;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class PostgresAttachmentStore implements AttachmentStore {

    @Inject AttachmentRepository attachmentRepository;

    @Inject EntryRepository entryRepository;

    @Inject EntityManager entityManager;

    @Override
    @Transactional
    public AttachmentDto create(
            String userId, String contentType, String filename, Instant expiresAt) {
        AttachmentEntity entity = new AttachmentEntity();
        entity.setUserId(userId);
        entity.setContentType(contentType);
        entity.setFilename(filename);
        entity.setStatus("uploading");
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
        entity.setStatus("ready");
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
            entity.setStatus("ready");
            entity.setExpiresAt(expiresAt != null ? expiresAt.atOffset(ZoneOffset.UTC) : null);
        }
    }

    @Override
    @Transactional
    public AttachmentDto createFromUrl(
            String userId,
            String contentType,
            String filename,
            String sourceUrl,
            Instant expiresAt) {
        AttachmentEntity entity = new AttachmentEntity();
        entity.setUserId(userId);
        entity.setContentType(contentType);
        entity.setFilename(filename);
        entity.setSourceUrl(sourceUrl);
        entity.setStatus("downloading");
        entity.setExpiresAt(expiresAt != null ? expiresAt.atOffset(ZoneOffset.UTC) : null);
        attachmentRepository.persist(entity);
        return toDto(entity);
    }

    @Override
    @Transactional
    public void updateStatus(String id, String status) {
        AttachmentEntity entity = attachmentRepository.findActiveById(UUID.fromString(id));
        if (entity != null) {
            entity.setStatus(status);
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

    @Override
    public List<AttachmentDto> adminList(AdminAttachmentQuery query) {
        StringBuilder jpql = new StringBuilder("SELECT a FROM AttachmentEntity a WHERE 1=1");
        List<String> conditions = new ArrayList<>();
        var params = new java.util.HashMap<String, Object>();

        if (query.getUserId() != null) {
            conditions.add("a.userId = :userId");
            params.put("userId", query.getUserId());
        }
        if (query.getEntryId() != null) {
            conditions.add("a.entry.id = :entryId");
            params.put("entryId", UUID.fromString(query.getEntryId()));
        }

        OffsetDateTime now = OffsetDateTime.now(ZoneOffset.UTC);
        switch (query.getStatus()) {
            case LINKED -> conditions.add("a.entry IS NOT NULL");
            case UNLINKED ->
                    conditions.add(
                            "a.entry IS NULL AND (a.expiresAt IS NULL OR a.expiresAt >= :now)");
            case EXPIRED -> conditions.add("a.expiresAt IS NOT NULL AND a.expiresAt < :now");
            case ALL -> {} // no filter
        }
        if (query.getStatus() == AdminAttachmentQuery.AttachmentStatus.UNLINKED
                || query.getStatus() == AdminAttachmentQuery.AttachmentStatus.EXPIRED) {
            params.put("now", now);
        }

        if (query.getAfter() != null) {
            // Cursor is the UUID of the last item; use createdAt + id for deterministic ordering
            UUID afterId = UUID.fromString(query.getAfter());
            AttachmentEntity afterEntity = attachmentRepository.findById(afterId);
            if (afterEntity != null) {
                conditions.add(
                        "(a.createdAt < :afterCreatedAt OR (a.createdAt = :afterCreatedAt AND a.id"
                                + " < :afterId))");
                params.put("afterCreatedAt", afterEntity.getCreatedAt());
                params.put("afterId", afterId);
            }
        }

        for (String condition : conditions) {
            jpql.append(" AND ").append(condition);
        }
        jpql.append(" ORDER BY a.createdAt DESC, a.id DESC");

        TypedQuery<AttachmentEntity> typedQuery =
                entityManager.createQuery(jpql.toString(), AttachmentEntity.class);
        params.forEach(typedQuery::setParameter);
        typedQuery.setMaxResults(query.getLimit());

        return typedQuery.getResultList().stream().map(this::toDto).toList();
    }

    @Override
    public Optional<AttachmentDto> adminFindById(String id) {
        // No deletedAt filter - returns soft-deleted records too
        AttachmentEntity entity = attachmentRepository.findById(UUID.fromString(id));
        return entity != null ? Optional.of(toDto(entity)) : Optional.empty();
    }

    @Override
    public long adminCountByStorageKey(String storageKey) {
        return attachmentRepository.count("storageKey = ?1 AND deletedAt IS NULL", storageKey);
    }

    @Override
    @Transactional
    public void adminUnlinkFromEntry(String attachmentId) {
        AttachmentEntity entity = attachmentRepository.findById(UUID.fromString(attachmentId));
        if (entity != null) {
            entity.setEntry(null);
            entity.setExpiresAt(OffsetDateTime.now(ZoneOffset.UTC).plusMinutes(5));
        }
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
                entity.getDeletedAt() != null ? entity.getDeletedAt().toInstant() : null,
                entity.getStatus(),
                entity.getSourceUrl());
    }
}
