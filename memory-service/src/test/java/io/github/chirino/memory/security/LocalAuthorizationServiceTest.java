package io.github.chirino.memory.security;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import io.github.chirino.memory.config.TestInstance;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.mongo.model.MongoConversationGroup;
import io.github.chirino.memory.mongo.repo.MongoConversationGroupRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoOrganizationMemberRepository;
import io.github.chirino.memory.mongo.repo.MongoTeamMemberRepository;
import io.github.chirino.memory.persistence.entity.ConversationGroupEntity;
import io.github.chirino.memory.persistence.entity.OrganizationEntity;
import io.github.chirino.memory.persistence.entity.TeamEntity;
import io.github.chirino.memory.persistence.repo.ConversationGroupRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.OrganizationMemberRepository;
import io.github.chirino.memory.persistence.repo.TeamMemberRepository;
import java.util.Optional;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class LocalAuthorizationServiceTest {

    private LocalAuthorizationService createPostgresService(
            ConversationMembershipRepository pgRepo,
            ConversationGroupRepository groupRepo,
            OrganizationMemberRepository orgMemberRepo,
            TeamMemberRepository teamMemberRepo) {
        LocalAuthorizationService service = new LocalAuthorizationService();
        service.datastoreType = "postgres";
        service.pgMembershipRepo = TestInstance.of(pgRepo);
        service.mongoMembershipRepo = TestInstance.unsatisfied();
        service.pgConversationGroupRepo = TestInstance.of(groupRepo);
        service.pgOrgMemberRepo = TestInstance.of(orgMemberRepo);
        service.pgTeamMemberRepo = TestInstance.of(teamMemberRepo);
        service.mongoConversationGroupRepo = TestInstance.unsatisfied();
        service.mongoOrgMemberRepo = TestInstance.unsatisfied();
        service.mongoTeamMemberRepo = TestInstance.unsatisfied();
        return service;
    }

    @Test
    void delegates_to_postgres_repository() {
        ConversationMembershipRepository pgRepo = mock(ConversationMembershipRepository.class);
        UUID groupId = UUID.randomUUID();
        when(pgRepo.hasAtLeastAccess(eq(groupId), eq("user1"), eq(AccessLevel.READER)))
                .thenReturn(true);
        when(pgRepo.hasAtLeastAccess(eq(groupId), eq("user2"), eq(AccessLevel.OWNER)))
                .thenReturn(false);

        LocalAuthorizationService service =
                createPostgresService(
                        pgRepo,
                        mock(ConversationGroupRepository.class),
                        mock(OrganizationMemberRepository.class),
                        mock(TeamMemberRepository.class));

        assertTrue(service.hasAtLeastAccess(groupId.toString(), "user1", AccessLevel.READER));
        assertFalse(service.hasAtLeastAccess(groupId.toString(), "user2", AccessLevel.OWNER));
    }

    @Test
    void delegates_to_mongo_repository() {
        MongoConversationMembershipRepository mongoRepo =
                mock(MongoConversationMembershipRepository.class);
        when(mongoRepo.hasAtLeastAccess("conv1", "user1", AccessLevel.WRITER)).thenReturn(true);

        LocalAuthorizationService service = new LocalAuthorizationService();
        service.datastoreType = "mongodb";
        service.pgMembershipRepo = TestInstance.unsatisfied();
        service.mongoMembershipRepo = TestInstance.of(mongoRepo);
        service.pgConversationGroupRepo = TestInstance.unsatisfied();
        service.pgOrgMemberRepo = TestInstance.unsatisfied();
        service.pgTeamMemberRepo = TestInstance.unsatisfied();
        service.mongoConversationGroupRepo =
                TestInstance.of(mock(MongoConversationGroupRepository.class));
        service.mongoOrgMemberRepo = TestInstance.of(mock(MongoOrganizationMemberRepository.class));
        service.mongoTeamMemberRepo = TestInstance.of(mock(MongoTeamMemberRepository.class));

        assertTrue(service.hasAtLeastAccess("conv1", "user1", AccessLevel.WRITER));
    }

    @Test
    void write_and_delete_are_noops() {
        // These should not throw
        LocalAuthorizationService service = new LocalAuthorizationService();
        service.datastoreType = "postgres";
        service.pgMembershipRepo = TestInstance.unsatisfied();
        service.mongoMembershipRepo = TestInstance.unsatisfied();
        service.pgConversationGroupRepo = TestInstance.unsatisfied();
        service.pgOrgMemberRepo = TestInstance.unsatisfied();
        service.pgTeamMemberRepo = TestInstance.unsatisfied();
        service.mongoConversationGroupRepo = TestInstance.unsatisfied();
        service.mongoOrgMemberRepo = TestInstance.unsatisfied();
        service.mongoTeamMemberRepo = TestInstance.unsatisfied();

        service.writeRelationship("conv1", "user1", AccessLevel.OWNER);
        service.deleteRelationship("conv1", "user1", AccessLevel.OWNER);
        service.writeOrgMembership("org1", "user1", "admin");
        service.deleteOrgMembership("org1", "user1", "admin");
        service.writeOrgConversationGroup("conv1", "org1");
        service.writeTeamMembership("team1", "user1");
        service.deleteTeamMembership("team1", "user1");
        service.writeTeamOrg("team1", "org1");
        service.writeTeamConversationGroup("conv1", "team1");
    }

    @Test
    void org_admin_gets_implicit_manager_access_postgres() {
        UUID groupId = UUID.randomUUID();
        UUID orgId = UUID.randomUUID();

        ConversationMembershipRepository pgRepo = mock(ConversationMembershipRepository.class);
        when(pgRepo.hasAtLeastAccess(eq(groupId), eq("admin-user"), eq(AccessLevel.WRITER)))
                .thenReturn(false);

        ConversationGroupRepository groupRepo = mock(ConversationGroupRepository.class);
        ConversationGroupEntity group = new ConversationGroupEntity();
        OrganizationEntity org = new OrganizationEntity();
        org.setId(orgId);
        group.setOrganization(org);
        when(groupRepo.findActiveById(groupId)).thenReturn(Optional.of(group));

        OrganizationMemberRepository orgMemberRepo = mock(OrganizationMemberRepository.class);
        when(orgMemberRepo.findRole(orgId, "admin-user")).thenReturn(Optional.of("admin"));

        LocalAuthorizationService service =
                createPostgresService(
                        pgRepo, groupRepo, orgMemberRepo, mock(TeamMemberRepository.class));

        // Admin should have implicit MANAGER (satisfies WRITER and READER)
        assertTrue(service.hasAtLeastAccess(groupId.toString(), "admin-user", AccessLevel.WRITER));
        assertTrue(service.hasAtLeastAccess(groupId.toString(), "admin-user", AccessLevel.READER));
        assertTrue(service.hasAtLeastAccess(groupId.toString(), "admin-user", AccessLevel.MANAGER));
        // But NOT OWNER
        assertFalse(service.hasAtLeastAccess(groupId.toString(), "admin-user", AccessLevel.OWNER));
    }

    @Test
    void team_member_gets_implicit_writer_access_postgres() {
        UUID groupId = UUID.randomUUID();
        UUID orgId = UUID.randomUUID();
        UUID teamId = UUID.randomUUID();

        ConversationMembershipRepository pgRepo = mock(ConversationMembershipRepository.class);
        when(pgRepo.hasAtLeastAccess(eq(groupId), eq("team-user"), eq(AccessLevel.WRITER)))
                .thenReturn(false);
        when(pgRepo.hasAtLeastAccess(eq(groupId), eq("team-user"), eq(AccessLevel.READER)))
                .thenReturn(false);

        ConversationGroupRepository groupRepo = mock(ConversationGroupRepository.class);
        ConversationGroupEntity group = new ConversationGroupEntity();
        OrganizationEntity org = new OrganizationEntity();
        org.setId(orgId);
        group.setOrganization(org);
        TeamEntity team = new TeamEntity();
        team.setId(teamId);
        group.setTeam(team);
        when(groupRepo.findActiveById(groupId)).thenReturn(Optional.of(group));

        OrganizationMemberRepository orgMemberRepo = mock(OrganizationMemberRepository.class);
        when(orgMemberRepo.findRole(orgId, "team-user")).thenReturn(Optional.of("member"));

        TeamMemberRepository teamMemberRepo = mock(TeamMemberRepository.class);
        when(teamMemberRepo.isMember(teamId, "team-user")).thenReturn(true);

        LocalAuthorizationService service =
                createPostgresService(pgRepo, groupRepo, orgMemberRepo, teamMemberRepo);

        // Team member should have implicit WRITER (satisfies WRITER and READER)
        assertTrue(service.hasAtLeastAccess(groupId.toString(), "team-user", AccessLevel.WRITER));
        assertTrue(service.hasAtLeastAccess(groupId.toString(), "team-user", AccessLevel.READER));
        // But NOT MANAGER or OWNER
        assertFalse(service.hasAtLeastAccess(groupId.toString(), "team-user", AccessLevel.MANAGER));
        assertFalse(service.hasAtLeastAccess(groupId.toString(), "team-user", AccessLevel.OWNER));
    }

    @Test
    void org_admin_gets_implicit_manager_access_mongo() {
        String groupId = "group1";
        String orgId = "org1";

        MongoConversationMembershipRepository mongoRepo =
                mock(MongoConversationMembershipRepository.class);
        when(mongoRepo.hasAtLeastAccess(groupId, "admin-user", AccessLevel.WRITER))
                .thenReturn(false);

        MongoConversationGroupRepository mongoGroupRepo =
                mock(MongoConversationGroupRepository.class);
        MongoConversationGroup group = new MongoConversationGroup();
        group.id = groupId;
        group.organizationId = orgId;
        when(mongoGroupRepo.findById(groupId)).thenReturn(group);

        MongoOrganizationMemberRepository mongoOrgRepo =
                mock(MongoOrganizationMemberRepository.class);
        when(mongoOrgRepo.findRole(orgId, "admin-user")).thenReturn(Optional.of("admin"));

        LocalAuthorizationService service = new LocalAuthorizationService();
        service.datastoreType = "mongodb";
        service.pgMembershipRepo = TestInstance.unsatisfied();
        service.mongoMembershipRepo = TestInstance.of(mongoRepo);
        service.pgConversationGroupRepo = TestInstance.unsatisfied();
        service.pgOrgMemberRepo = TestInstance.unsatisfied();
        service.pgTeamMemberRepo = TestInstance.unsatisfied();
        service.mongoConversationGroupRepo = TestInstance.of(mongoGroupRepo);
        service.mongoOrgMemberRepo = TestInstance.of(mongoOrgRepo);
        service.mongoTeamMemberRepo = TestInstance.of(mock(MongoTeamMemberRepository.class));

        assertTrue(service.hasAtLeastAccess(groupId, "admin-user", AccessLevel.WRITER));
    }
}
