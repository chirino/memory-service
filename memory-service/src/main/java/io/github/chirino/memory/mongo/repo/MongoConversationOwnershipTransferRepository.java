package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.mongo.model.MongoConversationOwnershipTransfer;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;

/**
 * Repository for ownership transfers.
 * All transfers in the database are implicitly "pending" - accepted/rejected transfers are deleted.
 */
@ApplicationScoped
public class MongoConversationOwnershipTransferRepository
        implements PanacheMongoRepositoryBase<MongoConversationOwnershipTransfer, String> {

    /** Find transfer for a conversation group (only one can exist at a time). */
    public Optional<MongoConversationOwnershipTransfer> findByConversationGroup(String groupId) {
        return find("conversationGroupId = ?1", groupId).firstResultOptional();
    }

    /** Find by ID if user is sender or recipient. */
    public Optional<MongoConversationOwnershipTransfer> findByIdAndParticipant(
            String id, String userId) {
        return find("_id = ?1 and (fromUserId = ?2 or toUserId = ?2)", id, userId)
                .firstResultOptional();
    }

    /** List transfers where user is sender. */
    public List<MongoConversationOwnershipTransfer> listByFromUser(String userId) {
        return find("fromUserId = ?1", userId).list();
    }

    /** List transfers where user is recipient. */
    public List<MongoConversationOwnershipTransfer> listByToUser(String userId) {
        return find("toUserId = ?1", userId).list();
    }

    /** List all transfers for user (sender or recipient). */
    public List<MongoConversationOwnershipTransfer> listByUser(String userId) {
        return find("fromUserId = ?1 or toUserId = ?1", userId).list();
    }

    /** Delete all transfers for a conversation group. */
    public long deleteByConversationGroup(String groupId) {
        return delete("conversationGroupId = ?1", groupId);
    }

    /** Delete transfer for a conversation group where the given user is the recipient. */
    public long deleteByConversationGroupAndToUser(String groupId, String toUserId) {
        return delete("conversationGroupId = ?1 and toUserId = ?2", groupId, toUserId);
    }
}
