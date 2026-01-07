package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.persistence.entity.ConversationGroupEntity;
import io.github.chirino.memory.persistence.entity.ConversationMembershipEntity;
import io.github.chirino.memory.persistence.entity.ConversationMembershipEntity.ConversationMembershipId;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.persistence.EntityManager;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class ConversationMembershipRepository
        implements PanacheRepositoryBase<ConversationMembershipEntity, ConversationMembershipId> {

    @jakarta.inject.Inject EntityManager entityManager;

    public List<ConversationMembershipEntity> listForConversationGroup(UUID conversationGroupId) {
        return list("id.conversationGroupId", conversationGroupId);
    }

    public List<ConversationMembershipEntity> listForUser(String userId, int limit) {
        return find("id.userId = ?1", userId).page(0, limit).list();
    }

    public Optional<ConversationMembershipEntity> findMembership(
            UUID conversationGroupId, String userId) {
        return find("id.conversationGroupId = ?1 and id.userId = ?2", conversationGroupId, userId)
                .firstResultOptional();
    }

    public Optional<AccessLevel> findAccessLevel(UUID conversationGroupId, String userId) {
        return entityManager
                .createQuery(
                        "select m.accessLevel from ConversationMembershipEntity m where"
                            + " m.id.conversationGroupId = :conversationGroupId and m.id.userId ="
                            + " :userId",
                        AccessLevel.class)
                .setParameter("conversationGroupId", conversationGroupId)
                .setParameter("userId", userId)
                .getResultStream()
                .findFirst();
    }

    public boolean hasAtLeastAccess(UUID conversationGroupId, String userId, AccessLevel required) {
        Optional<ConversationMembershipEntity> membership =
                findMembership(conversationGroupId, userId);
        if (membership.isEmpty()) {
            return false;
        }
        AccessLevel level = membership.get().getAccessLevel();
        return level == AccessLevel.OWNER
                || level == AccessLevel.MANAGER
                || (required == AccessLevel.READER
                        || (required == AccessLevel.WRITER && (level == AccessLevel.WRITER)));
    }

    public ConversationMembershipEntity createMembership(
            ConversationGroupEntity conversationGroup, String userId, AccessLevel accessLevel) {
        ConversationMembershipEntity entity = new ConversationMembershipEntity();
        entity.setId(new ConversationMembershipId(conversationGroup.getId(), userId));
        entity.setConversationGroup(conversationGroup);
        entity.setAccessLevel(accessLevel);
        persist(entity);
        return entity;
    }
}
