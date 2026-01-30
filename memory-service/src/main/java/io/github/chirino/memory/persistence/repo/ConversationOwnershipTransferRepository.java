package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.ConversationOwnershipTransferEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

/**
 * Repository for ownership transfers.
 * All transfers in the database are implicitly "pending" - accepted/rejected transfers are deleted.
 */
@ApplicationScoped
public class ConversationOwnershipTransferRepository
        implements PanacheRepositoryBase<ConversationOwnershipTransferEntity, UUID> {

    /** Find transfer for a conversation group (only one can exist at a time). */
    public Optional<ConversationOwnershipTransferEntity> findByConversationGroup(UUID groupId) {
        return find("conversationGroup.id = ?1", groupId).firstResultOptional();
    }

    /** Find by ID if user is sender or recipient. */
    public Optional<ConversationOwnershipTransferEntity> findByIdAndParticipant(
            UUID id, String userId) {
        return find("id = ?1 AND (fromUserId = ?2 OR toUserId = ?2)", id, userId)
                .firstResultOptional();
    }

    /** List transfers where user is sender. */
    public List<ConversationOwnershipTransferEntity> listByFromUser(String userId) {
        return find("fromUserId = ?1", userId).list();
    }

    /** List transfers where user is recipient. */
    public List<ConversationOwnershipTransferEntity> listByToUser(String userId) {
        return find("toUserId = ?1", userId).list();
    }

    /** List all transfers for user (sender or recipient). */
    public List<ConversationOwnershipTransferEntity> listByUser(String userId) {
        return find("fromUserId = ?1 OR toUserId = ?1", userId).list();
    }

    /** Delete all transfers for a conversation group. */
    public long deleteByConversationGroup(UUID groupId) {
        return delete("conversationGroup.id = ?1", groupId);
    }

    /** Delete transfer for a conversation group where the given user is the recipient. */
    public long deleteByConversationGroupAndToUser(UUID groupId, String toUserId) {
        return delete("conversationGroup.id = ?1 AND toUserId = ?2", groupId, toUserId);
    }
}
