package io.github.chirino.memory.attachment;

import io.github.chirino.memory.mongo.model.MongoAttachment;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import java.time.Instant;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class MongoAttachmentStore implements AttachmentStore {

    @Inject MongoAttachmentRepository attachmentRepository;

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
    public void updateAfterUpload(
            String id, String storageKey, long size, String sha256, Instant expiresAt) {
        MongoAttachment doc = attachmentRepository.findById(id);
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
        MongoAttachment doc = attachmentRepository.findById(id);
        return doc != null ? Optional.of(toDto(doc)) : Optional.empty();
    }

    @Override
    public Optional<AttachmentDto> findByIdForUser(String id, String userId) {
        MongoAttachment doc =
                attachmentRepository.find("_id = ?1 and userId = ?2", id, userId).firstResult();
        return doc != null ? Optional.of(toDto(doc)) : Optional.empty();
    }

    @Override
    @Transactional
    public void linkToEntry(String attachmentId, String entryId) {
        MongoAttachment doc = attachmentRepository.findById(attachmentId);
        if (doc != null) {
            doc.entryId = entryId;
            doc.expiresAt = null;
            attachmentRepository.update(doc);
        }
    }

    @Override
    public List<AttachmentDto> findByEntryId(String entryId) {
        return attachmentRepository.find("entryId", entryId).stream().map(this::toDto).toList();
    }

    @Override
    public List<AttachmentDto> findExpired() {
        return attachmentRepository
                .find("expiresAt != null and expiresAt < ?1", Instant.now())
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
    public List<AttachmentDto> findByEntryIds(List<String> entryIds) {
        if (entryIds.isEmpty()) {
            return List.of();
        }
        return attachmentRepository.find("entryId in ?1", entryIds).stream()
                .map(this::toDto)
                .toList();
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
                doc.createdAt);
    }

    @ApplicationScoped
    public static class MongoAttachmentRepository
            implements PanacheMongoRepositoryBase<MongoAttachment, String> {}
}
