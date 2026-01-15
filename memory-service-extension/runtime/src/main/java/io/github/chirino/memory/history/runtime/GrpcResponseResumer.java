package io.github.chirino.memory.history.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import com.google.protobuf.Empty;
import io.github.chirino.memory.grpc.v1.CancelResponseRequest;
import io.github.chirino.memory.grpc.v1.CancelResponseResponse;
import io.github.chirino.memory.grpc.v1.CheckConversationsRequest;
import io.github.chirino.memory.grpc.v1.CheckConversationsResponse;
import io.github.chirino.memory.grpc.v1.IsEnabledResponse;
import io.github.chirino.memory.grpc.v1.MutinyResponseResumerServiceGrpc;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensResponse;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenRequest;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.MetadataUtils;
import io.quarkus.grpc.GrpcClient;
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
    private static final Metadata.Key<String> REDIRECT_HOST_HEADER =
            Metadata.Key.of("x-resumer-redirect-host", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> REDIRECT_PORT_HEADER =
            Metadata.Key.of("x-resumer-redirect-port", Metadata.ASCII_STRING_MARSHALLER);

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
    public ResponseRecorder recorder(String conversationId, String bearerToken) {
        return new GrpcResponseRecorder(stub(bearerToken), conversationId);
    }

    @Override
    public Multi<String> replay(String conversationId, long resumePosition) {
        ReplayResponseTokensRequest request =
                ReplayResponseTokensRequest.newBuilder()
                        .setConversationId(conversationId)
                        .setResumePosition(resumePosition)
                        .build();

        return replayWithRedirect(request, null, 1)
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

        return replayWithRedirect(request, bearerToken, 1)
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
    public void requestCancel(String conversationId, String bearerToken) {
        if (conversationId == null || conversationId.isBlank()) {
            return;
        }
        try {
            CancelResponseRequest request =
                    CancelResponseRequest.newBuilder().setConversationId(conversationId).build();
            CancelResponseResponse response =
                    cancelWithRedirect(request, bearerToken, 1).await().indefinitely();
            if (!response.getAccepted()) {
                LOG.warnf(
                        "Cancel response request was not accepted for conversationId=%s",
                        conversationId);
            }
        } catch (Exception e) {
            LOG.warnf(e, "Failed to request cancel for conversationId=%s", conversationId);
        }
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
        return resumerService.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    }

    private Metadata buildMetadata(String bearerToken) {
        Metadata metadata = new Metadata();
        boolean usedBearerToken = false;
        if (bearerToken != null && metadata.get(AUTHORIZATION_HEADER) == null) {
            metadata.put(AUTHORIZATION_HEADER, "Bearer " + bearerToken);
            usedBearerToken = true;
            return metadata;
        }

        SecurityIdentity identity = getSecurityIdentity();
        if (identity == null) {
        } else {
            if (metadata.get(AUTHORIZATION_HEADER) == null) {
                String token = bearerToken(identity);
                if (token != null) {
                    metadata.put(AUTHORIZATION_HEADER, "Bearer " + token);
                    usedBearerToken = true;
                }
            }
        }

        if (LOG.isInfoEnabled()) {
            boolean hasApiKey = configuredApiKey != null && !configuredApiKey.isBlank();
            LOG.infof(
                    "gRPC resumer metadata: bearerToken=%b apiKey=%b", usedBearerToken, hasApiKey);
        }
        if (configuredApiKey != null && metadata.get(API_KEY_HEADER) == null) {
            metadata.put(API_KEY_HEADER, configuredApiKey);
        }

        return metadata;
    }

    private Multi<String> replayWithRedirect(
            ReplayResponseTokensRequest request, String bearerToken, int redirectsRemaining) {
        return replayWithRedirect(request, bearerToken, redirectsRemaining, null);
    }

    private Multi<String> replayWithRedirect(
            ReplayResponseTokensRequest request,
            String bearerToken,
            int redirectsRemaining,
            RedirectClient redirectClient) {
        MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub client =
                redirectClient == null ? stub(bearerToken) : redirectClient.stub();
        Multi<String> tokens =
                client.replayResponseTokens(request)
                        .onItem()
                        .transform(ReplayResponseTokensResponse::getToken);
        if (redirectClient != null) {
            tokens = tokens.onTermination().invoke(redirectClient::close);
        }
        return tokens.onFailure()
                .recoverWithMulti(
                        failure -> {
                            RedirectTarget redirect = resolveRedirect(failure);
                            if (redirect == null || redirectsRemaining <= 0) {
                                return Multi.createFrom().failure(failure);
                            }
                            RedirectClient nextClient = redirectClient(redirect, bearerToken);
                            if (nextClient == null) {
                                return Multi.createFrom().failure(failure);
                            }
                            return replayWithRedirect(
                                    request, bearerToken, redirectsRemaining - 1, nextClient);
                        });
    }

    private io.smallrye.mutiny.Uni<CancelResponseResponse> cancelWithRedirect(
            CancelResponseRequest request, String bearerToken, int redirectsRemaining) {
        return stub(bearerToken)
                .cancelResponse(request)
                .onFailure()
                .recoverWithUni(
                        failure -> {
                            RedirectTarget redirect = resolveRedirect(failure);
                            if (redirect == null || redirectsRemaining <= 0) {
                                return io.smallrye.mutiny.Uni.createFrom().failure(failure);
                            }
                            RedirectClient client = redirectClient(redirect, bearerToken);
                            if (client == null) {
                                return io.smallrye.mutiny.Uni.createFrom().failure(failure);
                            }
                            return client.stub()
                                    .cancelResponse(request)
                                    .onTermination()
                                    .invoke(client::close);
                        });
    }

    private RedirectTarget resolveRedirect(Throwable failure) {
        if (failure instanceof StatusRuntimeException statusException) {
            Metadata trailers = statusException.getTrailers();
            if (trailers == null) {
                return null;
            }
            String host = trailers.get(REDIRECT_HOST_HEADER);
            String portValue = trailers.get(REDIRECT_PORT_HEADER);
            if (host == null || portValue == null) {
                return null;
            }
            try {
                int port = Integer.parseInt(portValue);
                if (port <= 0) {
                    return null;
                }
                return new RedirectTarget(host, port);
            } catch (NumberFormatException e) {
                return null;
            }
        }
        return null;
    }

    private RedirectClient redirectClient(RedirectTarget target, String bearerToken) {
        ManagedChannel channel =
                ManagedChannelBuilder.forAddress(target.host(), target.port())
                        .usePlaintext()
                        .build();
        MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub client =
                MutinyResponseResumerServiceGrpc.newMutinyStub(channel);
        Metadata metadata = buildMetadata(bearerToken);
        if (metadata.keys() != null && !metadata.keys().isEmpty()) {
            client = client.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        return new RedirectClient(client, channel);
    }

    private record RedirectTarget(String host, int port) {}

    private record RedirectClient(
            MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub stub,
            ManagedChannel channel) {
        void close() {
            channel.shutdown();
        }
    }

    private SecurityIdentity getSecurityIdentity() {
        if (securityIdentityAssociationInstance != null
                && securityIdentityAssociationInstance.isResolvable()) {
            return securityIdentityAssociationInstance.get().getIdentity();
        }
        return null;
    }

    private static final class GrpcResponseRecorder implements ResponseRecorder {
        private final MutinyResponseResumerServiceGrpc.MutinyResponseResumerServiceStub
                resumerService;
        private final String conversationId;
        private final List<StreamResponseTokenRequest> pendingRequests = new ArrayList<>();
        private MultiEmitter<? super StreamResponseTokenRequest> emitter;
        private MultiEmitter<? super ResponseCancelSignal> cancelEmitter;
        private boolean firstMessage = true;
        private boolean completed = false;
        private boolean cancelEmitted = false;

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

        @Override
        public Multi<ResponseCancelSignal> cancelStream() {
            return Multi.createFrom()
                    .emitter(
                            emitter -> {
                                synchronized (GrpcResponseRecorder.this) {
                                    cancelEmitter = emitter;
                                    if (cancelEmitted) {
                                        emitter.emit(ResponseCancelSignal.CANCEL);
                                        emitter.complete();
                                    } else if (completed) {
                                        emitter.complete();
                                    }
                                }
                            });
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
                            response -> {
                                if (!response.getSuccess()) {
                                    LOG.warnf(
                                            "Failed to stream response tokens for"
                                                    + " conversationId=%s: %s",
                                            conversationId, response.getErrorMessage());
                                }
                                if (response.getCancelRequested()) {
                                    emitCancel();
                                }
                            },
                            failure ->
                                    LOG.warnf(
                                            failure,
                                            "Failed to stream response tokens for"
                                                    + " conversationId=%s",
                                            conversationId),
                            this::completeCancelStream);
        }

        private synchronized void emitCancel() {
            if (cancelEmitted) {
                return;
            }
            cancelEmitted = true;
            if (cancelEmitter != null) {
                cancelEmitter.emit(ResponseCancelSignal.CANCEL);
                cancelEmitter.complete();
            }
        }

        private synchronized void completeCancelStream() {
            if (cancelEmitter != null) {
                cancelEmitter.complete();
            }
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
