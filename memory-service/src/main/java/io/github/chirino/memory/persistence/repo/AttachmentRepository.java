package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.AttachmentEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.persistence.LockModeType;
import java.time.OffsetDateTime;
import java.util.List;
import java.util.UUID;

@ApplicationScoped
public class AttachmentRepository implements PanacheRepositoryBase<AttachmentEntity, UUID> {

    @Inject EntityManager entityManager;

    public List<AttachmentEntity> findExpired() {
        return find(
                        "deletedAt IS NULL AND expiresAt IS NOT NULL AND expiresAt < ?1",
                        OffsetDateTime.now())
                .list();
    }

    public AttachmentEntity findActiveById(UUID id) {
        return find("id = ?1 AND deletedAt IS NULL", id).firstResult();
    }

    public List<AttachmentEntity> findByEntryId(UUID entryId) {
        return find("entry.id = ?1 AND deletedAt IS NULL", entryId).list();
    }

    public AttachmentEntity findByIdAndUserId(UUID id, String userId) {
        return find("id = ?1 AND userId = ?2 AND deletedAt IS NULL", id, userId).firstResult();
    }

    public List<AttachmentEntity> findByEntryIds(List<UUID> entryIds) {
        if (entryIds.isEmpty()) {
            return List.of();
        }
        return find("entry.id IN ?1 AND deletedAt IS NULL", entryIds).list();
    }

    public List<AttachmentEntity> findSoftDeleted() {
        return find("deletedAt IS NOT NULL").list();
    }

    @SuppressWarnings("unchecked")
    public List<AttachmentEntity> findByStorageKeyForUpdate(String storageKey) {
        return entityManager
                .createQuery(
                        "SELECT a FROM AttachmentEntity a WHERE a.storageKey = :storageKey",
                        AttachmentEntity.class)
                .setParameter("storageKey", storageKey)
                .setLockMode(LockModeType.PESSIMISTIC_WRITE)
                .getResultList();
    }
}
