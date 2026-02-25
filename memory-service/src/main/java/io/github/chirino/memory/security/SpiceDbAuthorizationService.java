package io.github.chirino.memory.security;

import com.authzed.api.v1.CheckPermissionRequest;
import com.authzed.api.v1.CheckPermissionResponse;
import com.authzed.api.v1.Consistency;
import com.authzed.api.v1.ObjectReference;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.Relationship;
import com.authzed.api.v1.RelationshipUpdate;
import com.authzed.api.v1.SchemaServiceGrpc;
import com.authzed.api.v1.SubjectReference;
import com.authzed.api.v1.WriteRelationshipsRequest;
import com.authzed.grpcutil.BearerToken;
import io.github.chirino.memory.model.AccessLevel;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.concurrent.TimeUnit;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * SpiceDB-backed authorization service. Uses the authzed-java gRPC client to check permissions and
 * write relationships against a SpiceDB instance.
 */
@ApplicationScoped
public class SpiceDbAuthorizationService implements AuthorizationService {

    private static final Logger LOG = Logger.getLogger(SpiceDbAuthorizationService.class);

    private static final String RESOURCE_TYPE_CONVERSATION_GROUP = "conversation_group";
    private static final String RESOURCE_TYPE_ORGANIZATION = "organization";
    private static final String RESOURCE_TYPE_TEAM = "team";
    private static final String SUBJECT_TYPE_USER = "user";

    /** Default gRPC call deadline in seconds. */
    private static final long GRPC_DEADLINE_SECONDS = 10;

    @ConfigProperty(
            name = "memory-service.authz.spicedb.endpoint",
            defaultValue = "localhost:50051")
    String endpoint;

    @ConfigProperty(
            name = "memory-service.authz.spicedb.token",
            defaultValue = "memory-service-dev-key")
    String token;

    @ConfigProperty(name = "memory-service.authz.spicedb.tls-enabled", defaultValue = "false")
    boolean tlsEnabled;

    private ManagedChannel channel;
    private PermissionsServiceGrpc.PermissionsServiceBlockingStub permissionsService;
    private SchemaServiceGrpc.SchemaServiceBlockingStub schemaService;

    @PostConstruct
    void init() {
        ManagedChannelBuilder<?> builder = ManagedChannelBuilder.forTarget(endpoint);
        if (tlsEnabled) {
            builder.useTransportSecurity();
        } else {
            builder.usePlaintext();
        }
        channel = builder.build();
        BearerToken bearerToken = new BearerToken(token);
        permissionsService =
                PermissionsServiceGrpc.newBlockingStub(channel).withCallCredentials(bearerToken);
        schemaService = SchemaServiceGrpc.newBlockingStub(channel).withCallCredentials(bearerToken);
        LOG.infof(
                "SpiceDB authorization service initialized (endpoint=%s, tls=%s)",
                endpoint, tlsEnabled);
    }

    @PreDestroy
    void shutdown() {
        if (channel != null) {
            try {
                channel.shutdown().awaitTermination(5, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                channel.shutdownNow();
            }
        }
    }

    SchemaServiceGrpc.SchemaServiceBlockingStub getSchemaService() {
        return schemaService;
    }

    @Override
    public boolean hasAtLeastAccess(
            String conversationGroupId, String userId, AccessLevel required) {
        String permission = accessLevelToPermission(required);

        CheckPermissionRequest request =
                CheckPermissionRequest.newBuilder()
                        .setResource(
                                ObjectReference.newBuilder()
                                        .setObjectType(RESOURCE_TYPE_CONVERSATION_GROUP)
                                        .setObjectId(conversationGroupId)
                                        .build())
                        .setPermission(permission)
                        .setSubject(
                                SubjectReference.newBuilder()
                                        .setObject(
                                                ObjectReference.newBuilder()
                                                        .setObjectType(SUBJECT_TYPE_USER)
                                                        .setObjectId(userId)
                                                        .build())
                                        .build())
                        .setConsistency(Consistency.newBuilder().setFullyConsistent(true).build())
                        .build();

        try {
            CheckPermissionResponse response =
                    permissionsService
                            .withDeadlineAfter(GRPC_DEADLINE_SECONDS, TimeUnit.SECONDS)
                            .checkPermission(request);
            return response.getPermissionship()
                    == CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION;
        } catch (Exception e) {
            LOG.errorf(
                    e,
                    "SpiceDB CheckPermission failed for user=%s, resource=%s, permission=%s",
                    userId,
                    conversationGroupId,
                    permission);
            throw new AuthorizationException("Authorization check failed", e);
        }
    }

    @Override
    public void writeRelationship(String conversationGroupId, String userId, AccessLevel level) {
        String relation = accessLevelToRelation(level);
        writeRelationshipUpdate(
                RESOURCE_TYPE_CONVERSATION_GROUP,
                conversationGroupId,
                relation,
                SUBJECT_TYPE_USER,
                userId,
                RelationshipUpdate.Operation.OPERATION_TOUCH);
    }

    @Override
    public void deleteRelationship(String conversationGroupId, String userId, AccessLevel level) {
        String relation = accessLevelToRelation(level);
        writeRelationshipUpdate(
                RESOURCE_TYPE_CONVERSATION_GROUP,
                conversationGroupId,
                relation,
                SUBJECT_TYPE_USER,
                userId,
                RelationshipUpdate.Operation.OPERATION_DELETE);
    }

    @Override
    public void writeOrgMembership(String orgId, String userId, String orgRole) {
        writeRelationshipUpdate(
                RESOURCE_TYPE_ORGANIZATION,
                orgId,
                orgRole,
                SUBJECT_TYPE_USER,
                userId,
                RelationshipUpdate.Operation.OPERATION_TOUCH);
    }

    @Override
    public void deleteOrgMembership(String orgId, String userId, String orgRole) {
        writeRelationshipUpdate(
                RESOURCE_TYPE_ORGANIZATION,
                orgId,
                orgRole,
                SUBJECT_TYPE_USER,
                userId,
                RelationshipUpdate.Operation.OPERATION_DELETE);
    }

    @Override
    public void writeOrgConversationGroup(String conversationGroupId, String orgId) {
        writeRelationshipUpdate(
                RESOURCE_TYPE_CONVERSATION_GROUP,
                conversationGroupId,
                "org",
                RESOURCE_TYPE_ORGANIZATION,
                orgId,
                RelationshipUpdate.Operation.OPERATION_TOUCH);
    }

    @Override
    public void writeTeamMembership(String teamId, String userId) {
        writeRelationshipUpdate(
                RESOURCE_TYPE_TEAM,
                teamId,
                "member",
                SUBJECT_TYPE_USER,
                userId,
                RelationshipUpdate.Operation.OPERATION_TOUCH);
    }

    @Override
    public void deleteTeamMembership(String teamId, String userId) {
        writeRelationshipUpdate(
                RESOURCE_TYPE_TEAM,
                teamId,
                "member",
                SUBJECT_TYPE_USER,
                userId,
                RelationshipUpdate.Operation.OPERATION_DELETE);
    }

    @Override
    public void writeTeamOrg(String teamId, String orgId) {
        writeRelationshipUpdate(
                RESOURCE_TYPE_TEAM,
                teamId,
                "org",
                RESOURCE_TYPE_ORGANIZATION,
                orgId,
                RelationshipUpdate.Operation.OPERATION_TOUCH);
    }

    @Override
    public void writeTeamConversationGroup(String conversationGroupId, String teamId) {
        writeRelationshipUpdate(
                RESOURCE_TYPE_CONVERSATION_GROUP,
                conversationGroupId,
                "team",
                RESOURCE_TYPE_TEAM,
                teamId,
                RelationshipUpdate.Operation.OPERATION_TOUCH);
    }

    private void writeRelationshipUpdate(
            String resourceType,
            String resourceId,
            String relation,
            String subjectType,
            String subjectId,
            RelationshipUpdate.Operation operation) {

        RelationshipUpdate update =
                RelationshipUpdate.newBuilder()
                        .setOperation(operation)
                        .setRelationship(
                                Relationship.newBuilder()
                                        .setResource(
                                                ObjectReference.newBuilder()
                                                        .setObjectType(resourceType)
                                                        .setObjectId(resourceId)
                                                        .build())
                                        .setRelation(relation)
                                        .setSubject(
                                                SubjectReference.newBuilder()
                                                        .setObject(
                                                                ObjectReference.newBuilder()
                                                                        .setObjectType(subjectType)
                                                                        .setObjectId(subjectId)
                                                                        .build())
                                                        .build())
                                        .build())
                        .build();

        WriteRelationshipsRequest request =
                WriteRelationshipsRequest.newBuilder().addUpdates(update).build();

        try {
            permissionsService
                    .withDeadlineAfter(GRPC_DEADLINE_SECONDS, TimeUnit.SECONDS)
                    .writeRelationships(request);
        } catch (Exception e) {
            LOG.errorf(
                    e,
                    "SpiceDB WriteRelationships failed: %s %s/%s#%s@%s/%s",
                    operation,
                    resourceType,
                    resourceId,
                    relation,
                    subjectType,
                    subjectId);
            throw new AuthorizationException("Authorization relationship write failed", e);
        }
    }

    /**
     * Maps AccessLevel to SpiceDB permission names for CheckPermission calls. Higher levels include
     * lower ones via the schema (e.g., can_write implies can_read).
     */
    static String accessLevelToPermission(AccessLevel level) {
        return switch (level) {
            case OWNER -> "can_own";
            case MANAGER -> "can_manage";
            case WRITER -> "can_write";
            case READER -> "can_read";
        };
    }

    /**
     * Maps AccessLevel to SpiceDB relation names for WriteRelationships calls. Each level maps to a
     * direct relation on the conversation_group definition.
     */
    static String accessLevelToRelation(AccessLevel level) {
        return switch (level) {
            case OWNER -> "owner";
            case MANAGER -> "manager";
            case WRITER -> "writer";
            case READER -> "reader";
        };
    }
}
