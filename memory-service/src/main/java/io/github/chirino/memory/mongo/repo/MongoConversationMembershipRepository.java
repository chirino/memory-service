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
        return find("conversationGroupId", conversationGroupId).list();
    }

    public List<MongoConversationMembership> listForUser(String userId, int limit) {
        return find("userId", userId).page(0, limit).list();
    }

    public List<String> listConversationGroupIdsForUser(String userId) {
        return find("userId", userId).list().stream()
                .map(m -> m.conversationGroupId)
                .distinct()
                .toList();
    }

    public Optional<MongoConversationMembership> findMembership(
            String conversationId, String userId) {
        String id = conversationId + ":" + userId;
        return Optional.ofNullable(findById(id));
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
