package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.ConversationEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class ConversationRepository implements PanacheRepositoryBase<ConversationEntity, UUID> {

    public List<ConversationEntity> listByOwner(String ownerUserId, int limit) {
        return find(
                        "ownerUserId = ?1 AND deletedAt IS NULL AND conversationGroup.deletedAt IS"
                                + " NULL order by createdAt desc",
                        ownerUserId)
                .page(0, limit)
                .list();
    }

    public Optional<ConversationEntity> findActiveById(UUID id) {
        return find("id = ?1 AND deletedAt IS NULL AND conversationGroup.deletedAt IS NULL", id)
                .firstResultOptional();
    }

    public List<ConversationEntity> findActiveByGroupId(UUID groupId) {
        return find(
                        "conversationGroup.id = ?1 AND deletedAt IS NULL AND"
                                + " conversationGroup.deletedAt IS NULL",
                        groupId)
                .list();
    }
}
