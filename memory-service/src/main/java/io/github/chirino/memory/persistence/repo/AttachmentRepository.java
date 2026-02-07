package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.AttachmentEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.time.OffsetDateTime;
import java.util.List;
import java.util.UUID;

@ApplicationScoped
public class AttachmentRepository implements PanacheRepositoryBase<AttachmentEntity, UUID> {

    public List<AttachmentEntity> findExpired() {
        return find("expiresAt IS NOT NULL AND expiresAt < ?1", OffsetDateTime.now()).list();
    }

    public List<AttachmentEntity> findByEntryId(UUID entryId) {
        return find("entry.id", entryId).list();
    }

    public AttachmentEntity findByIdAndUserId(UUID id, String userId) {
        return find("id = ?1 AND userId = ?2", id, userId).firstResult();
    }

    public List<AttachmentEntity> findByEntryIds(List<UUID> entryIds) {
        if (entryIds.isEmpty()) {
            return List.of();
        }
        return find("entry.id IN ?1", entryIds).list();
    }
}
