package io.github.chirino.memory.security;

import io.github.chirino.memory.model.AccessLevel;

/**
 * Abstraction for resource-level authorization checks. Implementations can delegate to the local
 * database (via ConversationMembershipRepository) or to an external authorization service such as
 * SpiceDB.
 */
public interface AuthorizationService {

    /**
     * Returns {@code true} if the user has at least the given access level on the conversation
     * group.
     */
    boolean hasAtLeastAccess(String conversationGroupId, String userId, AccessLevel required);

    /** Writes a relationship granting the user the given access level on the conversation group. */
    void writeRelationship(String conversationGroupId, String userId, AccessLevel level);

    /** Deletes a relationship for the user on the conversation group. */
    void deleteRelationship(String conversationGroupId, String userId, AccessLevel level);

    /** Writes an organization membership relationship. */
    void writeOrgMembership(String orgId, String userId, String orgRole);

    /** Deletes an organization membership relationship. */
    void deleteOrgMembership(String orgId, String userId, String orgRole);

    /** Associates a conversation group with an organization. */
    void writeOrgConversationGroup(String conversationGroupId, String orgId);

    /** Writes a team membership relationship. */
    void writeTeamMembership(String teamId, String userId);

    /** Deletes a team membership relationship. */
    void deleteTeamMembership(String teamId, String userId);

    /** Associates a team with an organization. */
    void writeTeamOrg(String teamId, String orgId);

    /** Associates a conversation group with a team. */
    void writeTeamConversationGroup(String conversationGroupId, String teamId);
}
