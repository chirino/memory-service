package io.github.chirino.memory.attachment;

import io.github.chirino.memory.model.AdminAttachmentQuery;
import io.github.chirino.memory.mongo.model.MongoAttachment;
import io.github.chirino.memory.mongo.model.MongoEntry;
import io.github.chirino.memory.mongo.repo.MongoEntryRepository;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import io.quarkus.panache.common.Sort;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class MongoAttachmentStore implements AttachmentStore {

    @Inject MongoAttachmentRepository attachmentRepository;

    @Inject MongoEntryRepository entryRepository;

    @Override
    @Transactional
    public AttachmentDto create(
            String userId, String contentType, String filename, Instant expiresAt) {
        MongoAttachment doc = new MongoAttachment();
        doc.id = UUID.randomUUID().toString();
        doc.userId = userId;
        doc.contentType = contentType;
        doc.filename = filename;
        doc.expiresAt = expiresAt;
        doc.createdAt = Instant.now();
        attachmentRepository.persist(doc);
        return toDto(doc);
    }

    @Override
    @Transactional
    public AttachmentDto createFromSource(String userId, AttachmentDto source) {
        MongoAttachment doc = new MongoAttachment();
        doc.id = UUID.randomUUID().toString();
        doc.userId = userId;
        doc.storageKey = source.storageKey();
        doc.sha256 = source.sha256();
        doc.size = source.size();
        doc.contentType = source.contentType();
        doc.filename = source.filename();
        doc.expiresAt = Instant.now().plusSeconds(300);
        doc.createdAt = Instant.now();
        attachmentRepository.persist(doc);
        return toDto(doc);
    }

    @Override
    @Transactional
    public void updateAfterUpload(
            String id, String storageKey, long size, String sha256, Instant expiresAt) {
        MongoAttachment doc = findActive(id);
        if (doc != null) {
            doc.storageKey = storageKey;
            doc.size = size;
            doc.sha256 = sha256;
            doc.expiresAt = expiresAt;
            attachmentRepository.update(doc);
        }
    }

    @Override
    public Optional<AttachmentDto> findById(String id) {
        MongoAttachment doc = findActive(id);
        return doc != null ? Optional.of(toDto(doc)) : Optional.empty();
    }

    @Override
    public Optional<AttachmentDto> findByIdForUser(String id, String userId) {
        MongoAttachment doc =
                attachmentRepository
                        .find("_id = ?1 and userId = ?2 and deletedAt is null", id, userId)
                        .firstResult();
        return doc != null ? Optional.of(toDto(doc)) : Optional.empty();
    }

    @Override
    @Transactional
    public void linkToEntry(String attachmentId, String entryId) {
        MongoAttachment doc = findActive(attachmentId);
        if (doc != null) {
            doc.entryId = entryId;
            doc.expiresAt = null;
            attachmentRepository.update(doc);
        }
    }

    @Override
    public List<AttachmentDto> findByEntryId(String entryId) {
        return attachmentRepository.find("entryId = ?1 and deletedAt is null", entryId).stream()
                .map(this::toDto)
                .toList();
    }

    @Override
    public List<AttachmentDto> findExpired() {
        return attachmentRepository
                .find(
                        "deletedAt is null and expiresAt is not null and expiresAt < ?1",
                        Instant.now())
                .stream()
                .map(this::toDto)
                .toList();
    }

    @Override
    @Transactional
    public void delete(String id) {
        attachmentRepository.deleteById(id);
    }

    @Override
    @Transactional
    public void softDelete(String id) {
        MongoAttachment doc = findActive(id);
        if (doc != null) {
            doc.deletedAt = Instant.now();
            attachmentRepository.update(doc);
        }
    }

    @Override
    public List<AttachmentDto> findSoftDeleted() {
        return attachmentRepository.find("deletedAt is not null").stream()
                .map(this::toDto)
                .toList();
    }

    @Override
    public List<AttachmentDto> findByStorageKeyForUpdate(String storageKey) {
        // MongoDB doesn't support SELECT FOR UPDATE, but single-document operations are atomic.
        // For the reference-counting protocol we rely on the fact that MongoDB operations on
        // individual documents are atomic. This is acceptable since cross-group references
        // are not allowed, limiting the blast radius of concurrent deletes.
        return attachmentRepository.find("storageKey", storageKey).stream()
                .map(this::toDto)
                .toList();
    }

    @Override
    public List<AttachmentDto> findByEntryIds(List<String> entryIds) {
        if (entryIds.isEmpty()) {
            return List.of();
        }
        return attachmentRepository.find("entryId in ?1 and deletedAt is null", entryIds).stream()
                .map(this::toDto)
                .toList();
    }

    @Override
    public String getConversationGroupIdForEntry(String entryId) {
        MongoEntry entry = entryRepository.findById(entryId);
        return entry != null ? entry.conversationGroupId : null;
    }

    @Override
    public List<AttachmentDto> adminList(AdminAttachmentQuery query) {
        List<String> conditions = new ArrayList<>();
        List<Object> params = new ArrayList<>();
        int paramIndex = 1;

        if (query.getUserId() != null) {
            conditions.add("userId = ?" + paramIndex++);
            params.add(query.getUserId());
        }
        if (query.getEntryId() != null) {
            conditions.add("entryId = ?" + paramIndex++);
            params.add(query.getEntryId());
        }

        Instant now = Instant.now();
        switch (query.getStatus()) {
            case LINKED -> conditions.add("entryId is not null");
            case UNLINKED -> {
                conditions.add(
                        "entryId is null and (expiresAt is null or expiresAt >= ?"
                                + paramIndex++
                                + ")");
                params.add(now);
            }
            case EXPIRED -> {
                conditions.add("expiresAt is not null and expiresAt < ?" + paramIndex++);
                params.add(now);
            }
            case ALL -> {} // no filter
        }

        if (query.getAfter() != null) {
            MongoAttachment afterDoc = attachmentRepository.findById(query.getAfter());
            if (afterDoc != null) {
                conditions.add(
                        "(createdAt < ?"
                                + paramIndex++
                                + " or (createdAt = ?"
                                + paramIndex++
                                + " and _id < ?"
                                + paramIndex++
                                + "))");
                params.add(afterDoc.createdAt);
                params.add(afterDoc.createdAt);
                params.add(afterDoc.id);
            }
        }

        Sort sort = Sort.descending("createdAt", "_id");

        if (conditions.isEmpty()) {
            return attachmentRepository.findAll(sort).page(0, query.getLimit()).stream()
                    .map(this::toDto)
                    .toList();
        }

        String queryStr = String.join(" and ", conditions);
        return attachmentRepository
                .find(queryStr, sort, params.toArray())
                .page(0, query.getLimit())
                .stream()
                .map(this::toDto)
                .toList();
    }

    @Override
    public Optional<AttachmentDto> adminFindById(String id) {
        // No deletedAt filter - returns soft-deleted records too
        MongoAttachment doc = attachmentRepository.findById(id);
        return doc != null ? Optional.of(toDto(doc)) : Optional.empty();
    }

    @Override
    public long adminCountByStorageKey(String storageKey) {
        return attachmentRepository.count("storageKey = ?1 and deletedAt is null", storageKey);
    }

    @Override
    @Transactional
    public void adminUnlinkFromEntry(String attachmentId) {
        MongoAttachment doc = attachmentRepository.findById(attachmentId);
        if (doc != null) {
            doc.entryId = null;
            doc.expiresAt = Instant.now().plusSeconds(300);
            attachmentRepository.update(doc);
        }
    }

    private MongoAttachment findActive(String id) {
        MongoAttachment doc = attachmentRepository.findById(id);
        if (doc != null && doc.deletedAt != null) {
            return null;
        }
        return doc;
    }

    private AttachmentDto toDto(MongoAttachment doc) {
        return new AttachmentDto(
                doc.id,
                doc.storageKey,
                doc.filename,
                doc.contentType,
                doc.size,
                doc.sha256,
                doc.userId,
                doc.entryId,
                doc.expiresAt,
                doc.createdAt,
                doc.deletedAt);
    }

    @ApplicationScoped
    public static class MongoAttachmentRepository
            implements PanacheMongoRepositoryBase<MongoAttachment, String> {}
}
