package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.mongo.model.MongoConversationMembership;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;

@ApplicationScoped
public class MongoConversationMembershipRepository
        implements PanacheMongoRepositoryBase<MongoConversationMembership, String> {

    public List<MongoConversationMembership> listForConversationGroup(String conversationGroupId) {
        return find("conversationGroupId", conversationGroupId).stream()
                .filter(m -> m.deletedAt == null)
                .collect(java.util.stream.Collectors.toList());
    }

    public List<MongoConversationMembership> listForUser(String userId, int limit) {
        return find("userId", userId).stream()
                .filter(m -> m.deletedAt == null)
                .limit(limit)
                .collect(java.util.stream.Collectors.toList());
    }

    public Optional<MongoConversationMembership> findMembership(
            String conversationId, String userId) {
        String id = conversationId + ":" + userId;
        MongoConversationMembership membership = findById(id);
        if (membership != null && membership.deletedAt != null) {
            return Optional.empty();
        }
        return Optional.ofNullable(membership);
    }

    public boolean hasAtLeastAccess(String conversationId, String userId, AccessLevel required) {
        Optional<MongoConversationMembership> membership = findMembership(conversationId, userId);
        if (membership.isEmpty()) {
            return false;
        }
        AccessLevel level = membership.get().accessLevel;
        return level == AccessLevel.OWNER
                || level == AccessLevel.MANAGER
                || (required == AccessLevel.READER
                        || (required == AccessLevel.WRITER && level == AccessLevel.WRITER));
    }

    public MongoConversationMembership createMembership(
            String conversationId, String userId, AccessLevel accessLevel) {
        MongoConversationMembership m = new MongoConversationMembership();
        m.id = conversationId + ":" + userId;
        m.conversationGroupId = conversationId;
        m.userId = userId;
        m.accessLevel = accessLevel;
        m.createdAt = java.time.Instant.now();
        persist(m);
        return m;
    }
}
