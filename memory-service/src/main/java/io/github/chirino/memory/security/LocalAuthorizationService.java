package io.github.chirino.memory.security;

import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.mongo.repo.MongoConversationGroupRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoOrganizationMemberRepository;
import io.github.chirino.memory.mongo.repo.MongoTeamMemberRepository;
import io.github.chirino.memory.persistence.entity.ConversationGroupEntity;
import io.github.chirino.memory.persistence.repo.ConversationGroupRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.OrganizationMemberRepository;
import io.github.chirino.memory.persistence.repo.TeamMemberRepository;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.util.Optional;
import java.util.UUID;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Local (database-backed) authorization service. Permission checks delegate to the existing
 * {@link ConversationMembershipRepository} or {@link MongoConversationMembershipRepository}
 * depending on the configured datastore type. All write/delete methods are no-ops because the
 * database is already the source of truth for memberships.
 *
 * <p>Implicit access rules for org/team-scoped conversations:
 * <ul>
 *   <li>Org owner/admin → implicit MANAGER access</li>
 *   <li>Team member → implicit WRITER access</li>
 * </ul>
 */
@ApplicationScoped
public class LocalAuthorizationService implements AuthorizationService {

    private static final Logger LOG = Logger.getLogger(LocalAuthorizationService.class);

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject Instance<ConversationMembershipRepository> pgMembershipRepo;

    @Inject Instance<MongoConversationMembershipRepository> mongoMembershipRepo;

    @Inject Instance<ConversationGroupRepository> pgConversationGroupRepo;

    @Inject Instance<OrganizationMemberRepository> pgOrgMemberRepo;

    @Inject Instance<TeamMemberRepository> pgTeamMemberRepo;

    @Inject Instance<MongoConversationGroupRepository> mongoConversationGroupRepo;

    @Inject Instance<MongoOrganizationMemberRepository> mongoOrgMemberRepo;

    @Inject Instance<MongoTeamMemberRepository> mongoTeamMemberRepo;

    @Override
    public boolean hasAtLeastAccess(
            String conversationGroupId, String userId, AccessLevel required) {
        // First check direct membership
        if (isMongo()) {
            if (mongoMembershipRepo.get().hasAtLeastAccess(conversationGroupId, userId, required)) {
                return true;
            }
            return checkMongoImplicitAccess(conversationGroupId, userId, required);
        }

        UUID groupId = UUID.fromString(conversationGroupId);
        if (pgMembershipRepo.get().hasAtLeastAccess(groupId, userId, required)) {
            return true;
        }
        return checkPgImplicitAccess(groupId, userId, required);
    }

    private boolean checkPgImplicitAccess(UUID groupId, String userId, AccessLevel required) {
        if (!pgConversationGroupRepo.isResolvable() || !pgOrgMemberRepo.isResolvable()) {
            return false;
        }
        Optional<ConversationGroupEntity> groupOpt =
                pgConversationGroupRepo.get().findActiveById(groupId);
        if (groupOpt.isEmpty()) {
            return false;
        }
        ConversationGroupEntity group = groupOpt.get();

        // Check org admin/owner → implicit MANAGER
        if (group.getOrganization() != null) {
            UUID orgId = group.getOrganization().getId();
            Optional<String> role = pgOrgMemberRepo.get().findRole(orgId, userId);
            if (role.isPresent()) {
                String r = role.get();
                if ("owner".equals(r) || "admin".equals(r)) {
                    // Implicit MANAGER satisfies MANAGER, WRITER, READER but not OWNER
                    if (required != AccessLevel.OWNER) {
                        return true;
                    }
                }
            }
        }

        // Check team member → implicit WRITER
        if (group.getTeam() != null && pgTeamMemberRepo.isResolvable()) {
            UUID teamId = group.getTeam().getId();
            if (pgTeamMemberRepo.get().isMember(teamId, userId)) {
                // Implicit WRITER satisfies WRITER and READER
                if (required == AccessLevel.WRITER || required == AccessLevel.READER) {
                    return true;
                }
            }
        }

        return false;
    }

    private boolean checkMongoImplicitAccess(String groupId, String userId, AccessLevel required) {
        if (!mongoConversationGroupRepo.isResolvable() || !mongoOrgMemberRepo.isResolvable()) {
            return false;
        }
        var group = mongoConversationGroupRepo.get().findById(groupId);
        if (group == null || group.deletedAt != null) {
            return false;
        }

        // Check org admin/owner → implicit MANAGER
        if (group.organizationId != null) {
            Optional<String> role = mongoOrgMemberRepo.get().findRole(group.organizationId, userId);
            if (role.isPresent()) {
                String r = role.get();
                if ("owner".equals(r) || "admin".equals(r)) {
                    if (required != AccessLevel.OWNER) {
                        return true;
                    }
                }
            }
        }

        // Check team member → implicit WRITER
        if (group.teamId != null && mongoTeamMemberRepo.isResolvable()) {
            if (mongoTeamMemberRepo.get().isMember(group.teamId, userId)) {
                if (required == AccessLevel.WRITER || required == AccessLevel.READER) {
                    return true;
                }
            }
        }

        return false;
    }

    @Override
    public void writeRelationship(String conversationGroupId, String userId, AccessLevel level) {
        // No-op: database is the source of truth
    }

    @Override
    public void deleteRelationship(String conversationGroupId, String userId, AccessLevel level) {
        // No-op: database is the source of truth
    }

    @Override
    public void writeOrgMembership(String orgId, String userId, String orgRole) {
        // No-op: database is the source of truth
    }

    @Override
    public void deleteOrgMembership(String orgId, String userId, String orgRole) {
        // No-op: database is the source of truth
    }

    @Override
    public void writeOrgConversationGroup(String conversationGroupId, String orgId) {
        // No-op: database is the source of truth
    }

    @Override
    public void writeTeamMembership(String teamId, String userId) {
        // No-op: database is the source of truth
    }

    @Override
    public void deleteTeamMembership(String teamId, String userId) {
        // No-op: database is the source of truth
    }

    @Override
    public void writeTeamOrg(String teamId, String orgId) {
        // No-op: database is the source of truth
    }

    @Override
    public void writeTeamConversationGroup(String conversationGroupId, String teamId) {
        // No-op: database is the source of truth
    }

    private boolean isMongo() {
        String ds = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        return "mongo".equals(ds) || "mongodb".equals(ds);
    }
}
