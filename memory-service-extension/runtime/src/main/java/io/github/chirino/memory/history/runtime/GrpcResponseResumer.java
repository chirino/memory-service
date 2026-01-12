package io.github.chirino.memory.history.runtime;

import com.google.protobuf.Empty;
import io.github.chirino.memory.grpc.v1.CheckConversationsRequest;
import io.github.chirino.memory.grpc.v1.CheckConversationsResponse;
import io.github.chirino.memory.grpc.v1.HasResponseInProgressRequest;
import io.github.chirino.memory.grpc.v1.HasResponseInProgressResponse;
import io.github.chirino.memory.grpc.v1.IsEnabledResponse;
import io.github.chirino.memory.grpc.v1.MutinyResponseResumerServiceGrpc;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensResponse;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenRequest;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenResponse;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.stub.MetadataUtils;
import io.quarkus.grpc.GrpcClient;
import io.quarkus.oidc.AccessTokenCredential;
import io.quarkus.security.credential.TokenCredential;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.subscription.MultiEmitter;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

@ApplicationScoped
public class GrpcResponseResumer implements ResponseResumer {

    private static final Logger LOG = Logger.getLogger(GrpcResponseResumer.class);
    private static final Metadata.Key<String> AUTHORIZATION_HEADER =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> API_KEY_HEADER =
            Metadata.Key.of("x-api-key", Metadata.ASCII_STRING_MARSHALLER);

    @Inject Instance<SecurityIdentityAssociation> securityIdentityAssociationInstance;

    @Inject
    @GrpcClient("responseresumer")
    MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub resumerService;

    private final String configuredApiKey;

    public GrpcResponseResumer() {
        this.configuredApiKey =
                ConfigProvider.getConfig()
                        .getOptionalValue("memory-service-client.api-key", String.class)
                        .orElse(null);
    }

    @Override
    public ResponseRecorder recorder(String conversationId) {
        return new GrpcResponseRecorder(stub(), conversationId);
    }

    @Override
    public Multi<String> replay(String conversationId, long resumePosition) {
        ReplayResponseTokensRequest request =
                ReplayResponseTokensRequest.newBuilder()
                        .setConversationId(conversationId)
                        .setResumePosition(resumePosition)
                        .build();

        return stub(null)
                .replayResponseTokens(request)
                .onItem()
                .transform(ReplayResponseTokensResponse::getToken)
                .onFailure()
                .invoke(
                        failure ->
                                LOG.warnf(
                                        failure,
                                        "Failed to replay response tokens for conversationId=%s"
                                                + " from resumePosition=%d",
                                        conversationId,
                                        resumePosition));
    }

    @Override
    public Multi<String> replay(String conversationId, long resumePosition, String bearerToken) {
        ReplayResponseTokensRequest request =
                ReplayResponseTokensRequest.newBuilder()
                        .setConversationId(conversationId)
                        .setResumePosition(resumePosition)
                        .build();

        return stub(bearerToken)
                .replayResponseTokens(request)
                .onItem()
                .transform(ReplayResponseTokensResponse::getToken)
                .onFailure()
                .invoke(
                        failure ->
                                LOG.warnf(
                                        failure,
                                        "Failed to replay response tokens for conversationId=%s"
                                                + " from resumePosition=%d",
                                        conversationId,
                                        resumePosition));
    }

    @Override
    public boolean enabled() {
        try {
            IsEnabledResponse response =
                    stub(null).isEnabled(Empty.getDefaultInstance()).await().indefinitely();
            return response.getEnabled();
        } catch (Exception e) {
            LOG.warnf(e, "Failed to check if response resumer is enabled");
            return false;
        }
    }

    @Override
    public boolean hasResponseInProgress(String conversationId) {
        try {
            HasResponseInProgressRequest request =
                    HasResponseInProgressRequest.newBuilder()
                            .setConversationId(conversationId)
                            .build();
            HasResponseInProgressResponse response =
                    stub(null).hasResponseInProgress(request).await().indefinitely();
            return response.getInProgress();
        } catch (Exception e) {
            LOG.warnf(e, "Failed to check if history %s has response in progress", conversationId);
            return false;
        }
    }

    @Override
    public List<String> check(List<String> conversationIds) {
        return check(conversationIds, null);
    }

    @Override
    public List<String> check(List<String> conversationIds, String bearerToken) {
        if (conversationIds == null || conversationIds.isEmpty()) {
            return List.of();
        }

        try {
            CheckConversationsRequest request =
                    CheckConversationsRequest.newBuilder()
                            .addAllConversationIds(conversationIds)
                            .build();
            CheckConversationsResponse response =
                    stub(bearerToken).checkConversations(request).await().indefinitely();
            return response.getConversationIdsList();
        } catch (Exception e) {
            return handleCheckConversationsFailure(e);
        }
    }

    private List<String> handleCheckConversationsFailure(Exception failure) {
        if (failure instanceof io.grpc.StatusRuntimeException statusException) {
            Status status = statusException.getStatus();
            if (status.getCode() == Status.Code.UNIMPLEMENTED
                    || status.getCode() == Status.Code.NOT_FOUND) {
                LOG.debugf(
                        failure,
                        "Response resumer gRPC check not supported, returning empty list: %s",
                        status);
                return List.of();
            }
        }
        LOG.warnf(failure, "Failed to check conversations for responses in progress");
        return List.of();
    }

    private MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub stub() {
        return stub(null);
    }

    private MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub stub(
            String bearerToken) {
        Metadata metadata = buildMetadata(bearerToken);
        Set<String> keys = metadata.keys();
        if (keys == null || keys.isEmpty()) {
            return resumerService;
        }
        LOG.infof("Attaching gRPC metadata headers for response resumer: %s", keys);
        return resumerService.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    }

    private Metadata buildMetadata(String bearerToken) {
        Metadata metadata = new Metadata();
        if (bearerToken != null && metadata.get(AUTHORIZATION_HEADER) == null) {
            metadata.put(AUTHORIZATION_HEADER, "Bearer " + bearerToken);
            LOG.infof(
                    "Propagating provided bearer token to gRPC response resumer (token length=%d)",
                    bearerToken.length());
            return metadata;
        }

        SecurityIdentity identity = getSecurityIdentity();
        if (identity == null) {
            LOG.info(
                    "No SecurityIdentity available for response resumer; only API key (if"
                            + " configured) will be sent");
        } else {
            String userName =
                    identity.getPrincipal() != null ? identity.getPrincipal().getName() : "unknown";
            LOG.infof("Resolved SecurityIdentity for response resumer: %s", userName);
            if (metadata.get(AUTHORIZATION_HEADER) == null) {
                String token = resolveToken(identity);
                if (token != null) {
                    metadata.put(AUTHORIZATION_HEADER, "Bearer " + token);
                    LOG.info("Propagating Authorization token to gRPC response resumer");
                } else {
                    LOG.info(
                            "SecurityIdentity resolved but no AccessToken or TokenCredential"
                                    + " present; request will rely on API key");
                }
            }
        }

        if (configuredApiKey != null && metadata.get(API_KEY_HEADER) == null) {
            metadata.put(API_KEY_HEADER, configuredApiKey);
            LOG.info("Propagating X-API-Key to gRPC response resumer");
        }

        return metadata;
    }

    private SecurityIdentity getSecurityIdentity() {
        if (securityIdentityAssociationInstance != null
                && securityIdentityAssociationInstance.isResolvable()) {
            return securityIdentityAssociationInstance.get().getIdentity();
        }
        return null;
    }

    private String resolveToken(SecurityIdentity identity) {
        AccessTokenCredential atc = identity.getCredential(AccessTokenCredential.class);
        if (atc != null) {
            return atc.getToken();
        }
        TokenCredential tc = identity.getCredential(TokenCredential.class);
        if (tc != null) {
            return tc.getToken();
        }
        return null;
    }

    private static final class GrpcResponseRecorder implements ResponseRecorder {
        private final MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub
                resumerService;
        private final String conversationId;
        private final List<StreamResponseTokenRequest> pendingRequests = new ArrayList<>();
        private MultiEmitter<? super StreamResponseTokenRequest> emitter;
        private boolean firstMessage = true;
        private boolean completed = false;

        GrpcResponseRecorder(
                MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub resumerService,
                String conversationId) {
            this.resumerService = resumerService;
            this.conversationId = conversationId;
            startStream();
        }

        @Override
        public synchronized void record(String token) {
            if (token == null || token.isEmpty() || completed) {
                return;
            }

            StreamResponseTokenRequest.Builder builder =
                    StreamResponseTokenRequest.newBuilder().setToken(token);

            if (firstMessage) {
                builder.setConversationId(conversationId);
                firstMessage = false;
            }

            emit(builder.build());
        }

        @Override
        public synchronized void complete() {
            if (completed) {
                return;
            }
            completed = true;

            StreamResponseTokenRequest.Builder builder =
                    StreamResponseTokenRequest.newBuilder().setComplete(true);
            if (firstMessage) {
                builder.setConversationId(conversationId);
                firstMessage = false;
            }
            emit(builder.build());

            if (emitter != null) {
                emitter.complete();
            }
        }

        private synchronized void startStream() {
            Multi<StreamResponseTokenRequest> requestStream =
                    Multi.createFrom()
                            .emitter(
                                    newEmitter -> {
                                        synchronized (GrpcResponseRecorder.this) {
                                            emitter = newEmitter;
                                            if (!pendingRequests.isEmpty()) {
                                                for (StreamResponseTokenRequest request :
                                                        pendingRequests) {
                                                    emitter.emit(request);
                                                }
                                                pendingRequests.clear();
                                            }
                                            if (completed) {
                                                emitter.complete();
                                            }
                                        }
                                    });

            resumerService
                    .streamResponseTokens(requestStream)
                    .subscribe()
                    .with(
                            (StreamResponseTokenResponse response) -> {
                                if (!response.getSuccess()) {
                                    LOG.warnf(
                                            "Failed to stream response tokens for"
                                                    + " conversationId=%s: %s",
                                            conversationId, response.getErrorMessage());
                                }
                            },
                            failure ->
                                    LOG.warnf(
                                            failure,
                                            "Failed to stream response tokens for"
                                                    + " conversationId=%s",
                                            conversationId));
        }

        private void emit(StreamResponseTokenRequest request) {
            if (emitter != null) {
                emitter.emit(request);
                return;
            }
            pendingRequests.add(request);
        }
    }
}
