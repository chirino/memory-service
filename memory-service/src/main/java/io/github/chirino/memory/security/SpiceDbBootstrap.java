package io.github.chirino.memory.security;

import com.authzed.api.v1.WriteSchemaRequest;
import io.github.chirino.memory.mongo.repo.MongoConversationGroupRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoOrganizationMemberRepository;
import io.github.chirino.memory.mongo.repo.MongoTeamMemberRepository;
import io.github.chirino.memory.mongo.repo.MongoTeamRepository;
import io.github.chirino.memory.persistence.repo.ConversationGroupRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.OrganizationMemberRepository;
import io.github.chirino.memory.persistence.repo.TeamMemberRepository;
import io.github.chirino.memory.persistence.repo.TeamRepository;
import io.quarkus.runtime.Startup;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Optional startup component that loads the SpiceDB schema and synchronizes existing
 * memberships from the database into SpiceDB. Only active when
 * {@code memory-service.authz.type=spicedb} and
 * {@code memory-service.authz.spicedb.sync-on-startup=true}.
 */
@ApplicationScoped
public class SpiceDbBootstrap {

    private static final Logger LOG = Logger.getLogger(SpiceDbBootstrap.class);

    @ConfigProperty(name = "memory-service.authz.type", defaultValue = "local")
    String authzType;

    @ConfigProperty(name = "memory-service.authz.spicedb.sync-on-startup", defaultValue = "false")
    boolean syncOnStartup;

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject Instance<SpiceDbAuthorizationService> spiceDbAuthzService;

    @Inject Instance<ConversationMembershipRepository> pgMembershipRepo;

    @Inject Instance<MongoConversationMembershipRepository> mongoMembershipRepo;

    @Inject Instance<OrganizationMemberRepository> pgOrgMemberRepo;

    @Inject Instance<MongoOrganizationMemberRepository> mongoOrgMemberRepo;

    @Inject Instance<TeamRepository> pgTeamRepo;

    @Inject Instance<MongoTeamRepository> mongoTeamRepo;

    @Inject Instance<TeamMemberRepository> pgTeamMemberRepo;

    @Inject Instance<MongoTeamMemberRepository> mongoTeamMemberRepo;

    @Inject Instance<ConversationGroupRepository> pgConversationGroupRepo;

    @Inject Instance<MongoConversationGroupRepository> mongoConversationGroupRepo;

    @Startup
    void onStartup() {
        if (!"spicedb".equals(authzType)) {
            return;
        }

        SpiceDbAuthorizationService authzService = spiceDbAuthzService.get();

        loadSchema(authzService);

        if (syncOnStartup) {
            syncMemberships(authzService);
            syncOrgMemberships(authzService);
            syncTeams(authzService);
            syncTeamMemberships(authzService);
            syncConversationGroupScoping(authzService);
        }
    }

    private void loadSchema(SpiceDbAuthorizationService authzService) {
        try (InputStream is =
                Thread.currentThread()
                        .getContextClassLoader()
                        .getResourceAsStream("spicedb/schema.zed")) {
            if (is == null) {
                throw new IllegalStateException(
                        "SpiceDB schema file not found on classpath: spicedb/schema.zed");
            }
            String schema = new String(is.readAllBytes(), StandardCharsets.UTF_8);
            WriteSchemaRequest request = WriteSchemaRequest.newBuilder().setSchema(schema).build();
            authzService.getSchemaService().writeSchema(request);
            LOG.info("SpiceDB schema loaded successfully");
        } catch (IOException e) {
            throw new IllegalStateException("Failed to read SpiceDB schema file", e);
        } catch (Exception e) {
            throw new IllegalStateException("Failed to write SpiceDB schema", e);
        }
    }

    private void syncMemberships(SpiceDbAuthorizationService authzService) {
        LOG.info("Syncing existing memberships to SpiceDB...");
        int count = 0;
        try {
            if (isMongo()) {
                if (mongoMembershipRepo.isResolvable()) {
                    var memberships = mongoMembershipRepo.get().listAll();
                    for (var m : memberships) {
                        authzService.writeRelationship(
                                m.conversationGroupId, m.userId, m.accessLevel);
                        count++;
                    }
                }
            } else {
                if (pgMembershipRepo.isResolvable()) {
                    var memberships = pgMembershipRepo.get().listAll();
                    for (var m : memberships) {
                        authzService.writeRelationship(
                                m.getId().getConversationGroupId().toString(),
                                m.getId().getUserId(),
                                m.getAccessLevel());
                        count++;
                    }
                }
            }
            LOG.infof("Synced %d memberships to SpiceDB", count);
        } catch (Exception e) {
            LOG.errorf(
                    e, "Failed to sync memberships to SpiceDB (synced %d before failure)", count);
        }
    }

    private void syncOrgMemberships(SpiceDbAuthorizationService authzService) {
        LOG.info("Syncing organization memberships to SpiceDB...");
        int count = 0;
        try {
            if (isMongo()) {
                if (mongoOrgMemberRepo.isResolvable()) {
                    var members = mongoOrgMemberRepo.get().listAll();
                    for (var m : members) {
                        authzService.writeOrgMembership(m.organizationId, m.userId, m.role);
                        count++;
                    }
                }
            } else {
                if (pgOrgMemberRepo.isResolvable()) {
                    var members = pgOrgMemberRepo.get().listAll();
                    for (var m : members) {
                        authzService.writeOrgMembership(
                                m.getId().getOrganizationId().toString(),
                                m.getId().getUserId(),
                                m.getRole());
                        count++;
                    }
                }
            }
            LOG.infof("Synced %d org memberships to SpiceDB", count);
        } catch (Exception e) {
            LOG.errorf(
                    e,
                    "Failed to sync org memberships to SpiceDB (synced %d before failure)",
                    count);
        }
    }

    private void syncTeams(SpiceDbAuthorizationService authzService) {
        LOG.info("Syncing team-org relationships to SpiceDB...");
        int count = 0;
        try {
            if (isMongo()) {
                if (mongoTeamRepo.isResolvable()) {
                    var teams = mongoTeamRepo.get().listAll();
                    for (var t : teams) {
                        if (t.deletedAt == null) {
                            authzService.writeTeamOrg(t.id, t.organizationId);
                            count++;
                        }
                    }
                }
            } else {
                if (pgTeamRepo.isResolvable()) {
                    var teams = pgTeamRepo.get().listAll();
                    for (var t : teams) {
                        if (!t.isDeleted()) {
                            authzService.writeTeamOrg(
                                    t.getId().toString(), t.getOrganization().getId().toString());
                            count++;
                        }
                    }
                }
            }
            LOG.infof("Synced %d team-org relationships to SpiceDB", count);
        } catch (Exception e) {
            LOG.errorf(
                    e,
                    "Failed to sync team-org relationships to SpiceDB (synced %d before failure)",
                    count);
        }
    }

    private void syncTeamMemberships(SpiceDbAuthorizationService authzService) {
        LOG.info("Syncing team memberships to SpiceDB...");
        int count = 0;
        try {
            if (isMongo()) {
                if (mongoTeamMemberRepo.isResolvable()) {
                    var members = mongoTeamMemberRepo.get().listAll();
                    for (var m : members) {
                        authzService.writeTeamMembership(m.teamId, m.userId);
                        count++;
                    }
                }
            } else {
                if (pgTeamMemberRepo.isResolvable()) {
                    var members = pgTeamMemberRepo.get().listAll();
                    for (var m : members) {
                        authzService.writeTeamMembership(
                                m.getId().getTeamId().toString(), m.getId().getUserId());
                        count++;
                    }
                }
            }
            LOG.infof("Synced %d team memberships to SpiceDB", count);
        } catch (Exception e) {
            LOG.errorf(
                    e,
                    "Failed to sync team memberships to SpiceDB (synced %d before failure)",
                    count);
        }
    }

    private void syncConversationGroupScoping(SpiceDbAuthorizationService authzService) {
        LOG.info("Syncing conversation group org/team scoping to SpiceDB...");
        int count = 0;
        try {
            if (isMongo()) {
                if (mongoConversationGroupRepo.isResolvable()) {
                    var groups = mongoConversationGroupRepo.get().listAll();
                    for (var g : groups) {
                        if (g.deletedAt == null) {
                            if (g.organizationId != null) {
                                authzService.writeOrgConversationGroup(g.id, g.organizationId);
                                count++;
                            }
                            if (g.teamId != null) {
                                authzService.writeTeamConversationGroup(g.id, g.teamId);
                                count++;
                            }
                        }
                    }
                }
            } else {
                if (pgConversationGroupRepo.isResolvable()) {
                    var groups = pgConversationGroupRepo.get().listAll();
                    for (var g : groups) {
                        if (!g.isDeleted()) {
                            if (g.getOrganization() != null) {
                                authzService.writeOrgConversationGroup(
                                        g.getId().toString(),
                                        g.getOrganization().getId().toString());
                                count++;
                            }
                            if (g.getTeam() != null) {
                                authzService.writeTeamConversationGroup(
                                        g.getId().toString(), g.getTeam().getId().toString());
                                count++;
                            }
                        }
                    }
                }
            }
            LOG.infof("Synced %d conversation group scoping relationships to SpiceDB", count);
        } catch (Exception e) {
            LOG.errorf(
                    e,
                    "Failed to sync conversation group scoping to SpiceDB (synced %d before"
                            + " failure)",
                    count);
        }
    }

    private boolean isMongo() {
        String ds = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        return "mongo".equals(ds) || "mongodb".equals(ds);
    }
}
