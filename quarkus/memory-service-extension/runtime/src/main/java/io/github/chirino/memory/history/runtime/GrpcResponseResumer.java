package io.github.chirino.memory.history.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.protobuf.ByteString;
import com.google.protobuf.Empty;
import io.github.chirino.memory.grpc.v1.CancelRecordRequest;
import io.github.chirino.memory.grpc.v1.CancelRecordResponse;
import io.github.chirino.memory.grpc.v1.CheckRecordingsRequest;
import io.github.chirino.memory.grpc.v1.CheckRecordingsResponse;
import io.github.chirino.memory.grpc.v1.IsEnabledResponse;
import io.github.chirino.memory.grpc.v1.MutinyResponseRecorderServiceGrpc;
import io.github.chirino.memory.grpc.v1.RecordRequest;
import io.github.chirino.memory.grpc.v1.RecordStatus;
import io.github.chirino.memory.grpc.v1.ReplayRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponse;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.stub.MetadataUtils;
import io.quarkus.grpc.GrpcClient;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.subscription.MultiEmitter;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.nio.ByteBuffer;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.stream.Collectors;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

@ApplicationScoped
public class GrpcResponseResumer implements ResponseResumer {

    private static final Logger LOG = Logger.getLogger(GrpcResponseResumer.class);
    private static final Metadata.Key<String> AUTHORIZATION_HEADER =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> API_KEY_HEADER =
            Metadata.Key.of("x-api-key", Metadata.ASCII_STRING_MARSHALLER);

    private static ByteString toByteString(String uuidStr) {
        if (uuidStr == null || uuidStr.isBlank()) {
            return ByteString.EMPTY;
        }
        UUID uuid = UUID.fromString(uuidStr);
        ByteBuffer buffer = ByteBuffer.allocate(16);
        buffer.putLong(uuid.getMostSignificantBits());
        buffer.putLong(uuid.getLeastSignificantBits());
        return ByteString.copyFrom(buffer.array());
    }

    private static String fromByteString(ByteString bytes) {
        if (bytes == null || bytes.isEmpty()) {
            return null;
        }
        ByteBuffer buffer = ByteBuffer.wrap(bytes.toByteArray());
        long mostSig = buffer.getLong();
        long leastSig = buffer.getLong();
        return new UUID(mostSig, leastSig).toString();
    }

    @Inject Instance<SecurityIdentityAssociation> securityIdentityAssociationInstance;

    @Inject
    @GrpcClient("responserecorder")
    MutinyResponseRecorderServiceGrpc.MutinyResponseRecorderServiceStub recorderService;

    @Inject ObjectMapper objectMapper;

    private final String configuredApiKey;

    public GrpcResponseResumer() {
        this.configuredApiKey =
                ConfigProvider.getConfig()
                        .getOptionalValue("memory-service.client.api-key", String.class)
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
    public Multi<String> replay(String conversationId, String bearerToken) {
        ReplayRequest request =
                ReplayRequest.newBuilder().setConversationId(toByteString(conversationId)).build();

        return replayWithRedirect(request, bearerToken, 1)
                .onFailure()
                .invoke(
                        failure ->
                                LOG.warnf(
                                        failure,
                                        "Failed to replay response tokens for conversationId=%s",
                                        conversationId));
    }

    @Override
    @SuppressWarnings("unchecked")
    public <T> Multi<T> replayEvents(String conversationId, String bearerToken, Class<T> type) {
        Multi<String> raw = replay(conversationId, bearerToken);
        Multi<String> buffered = JsonLineBufferingTransformer.bufferLines(raw);

        if (type == String.class) {
            // Return raw JSON lines - efficient for SSE, no deserialize/re-serialize
            return (Multi<T>) buffered;
        }

        // Deserialize each line to the requested type
        return buffered.map(
                json -> {
                    try {
                        return objectMapper.readValue(json, type);
                    } catch (Exception e) {
                        throw new RuntimeException(
                                "Failed to deserialize event JSON to " + type.getName(), e);
                    }
                });
    }

    @Override
    public boolean enabled() {
        try {
            IsEnabledResponse response =
                    stub(null).isEnabled(Empty.getDefaultInstance()).await().indefinitely();
            return response.getEnabled();
        } catch (Exception e) {
            LOG.warnf(e, "Failed to check if response recorder is enabled");
            return false;
        }
    }

    @Override
    public void requestCancel(String conversationId, String bearerToken) {
        if (conversationId == null || conversationId.isBlank()) {
            return;
        }
        try {
            CancelRecordRequest request =
                    CancelRecordRequest.newBuilder()
                            .setConversationId(toByteString(conversationId))
                            .build();
            CancelRecordResponse response =
                    cancelWithRedirect(request, bearerToken, 1).await().indefinitely();
            if (!response.getAccepted()) {
                LOG.warnf("Cancel request was not accepted for conversationId=%s", conversationId);
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
            List<ByteString> byteIds =
                    conversationIds.stream()
                            .map(GrpcResponseResumer::toByteString)
                            .collect(Collectors.toList());
            CheckRecordingsRequest request =
                    CheckRecordingsRequest.newBuilder().addAllConversationIds(byteIds).build();
            CheckRecordingsResponse response =
                    stub(bearerToken).checkRecordings(request).await().indefinitely();
            return response.getConversationIdsList().stream()
                    .map(GrpcResponseResumer::fromByteString)
                    .collect(Collectors.toList());
        } catch (Exception e) {
            return handleCheckRecordingsFailure(e);
        }
    }

    private List<String> handleCheckRecordingsFailure(Exception failure) {
        if (failure instanceof io.grpc.StatusRuntimeException statusException) {
            io.grpc.Status status = statusException.getStatus();
            if (status.getCode() == io.grpc.Status.Code.UNIMPLEMENTED
                    || status.getCode() == io.grpc.Status.Code.NOT_FOUND) {
                LOG.debugf(
                        failure,
                        "Response recorder gRPC check not supported, returning empty list: %s",
                        status);
                return List.of();
            }
        }
        LOG.warnf(failure, "Failed to check recordings for responses in progress");
        return List.of();
    }

    private MutinyResponseRecorderServiceGrpc.MutinyResponseRecorderServiceStub stub() {
        return stub(null);
    }

    private MutinyResponseRecorderServiceGrpc.MutinyResponseRecorderServiceStub stub(
            String bearerToken) {
        Metadata metadata = buildMetadata(bearerToken);
        Set<String> keys = metadata.keys();
        if (keys == null || keys.isEmpty()) {
            return recorderService;
        }
        return recorderService.withInterceptors(
                MetadataUtils.newAttachHeadersInterceptor(metadata));
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
                    "gRPC recorder metadata: bearerToken=%b apiKey=%b", usedBearerToken, hasApiKey);
        }
        if (configuredApiKey != null && metadata.get(API_KEY_HEADER) == null) {
            metadata.put(API_KEY_HEADER, configuredApiKey);
        }

        return metadata;
    }

    private Multi<String> replayWithRedirect(
            ReplayRequest request, String bearerToken, int redirectsRemaining) {
        return replayWithRedirect(request, bearerToken, redirectsRemaining, null);
    }

    private Multi<String> replayWithRedirect(
            ReplayRequest request,
            String bearerToken,
            int redirectsRemaining,
            RedirectClient redirectClient) {
        MutinyResponseRecorderServiceGrpc.MutinyResponseRecorderServiceStub client =
                redirectClient == null ? stub(bearerToken) : redirectClient.stub();
        Multi<ReplayResponse> responses = client.replay(request);
        if (redirectClient != null) {
            responses = responses.onTermination().invoke(redirectClient::close);
        }

        // Check for redirect responses and extract content
        return responses
                .onItem()
                .transformToMultiAndMerge(
                        response -> {
                            String redirectAddress = response.getRedirectAddress();
                            if (redirectAddress != null && !redirectAddress.isEmpty()) {
                                if (redirectsRemaining <= 0) {
                                    return Multi.createFrom()
                                            .failure(new RuntimeException("Too many redirects"));
                                }
                                RedirectTarget target = parseRedirectAddress(redirectAddress);
                                if (target == null) {
                                    return Multi.createFrom()
                                            .failure(
                                                    new RuntimeException(
                                                            "Invalid redirect address: "
                                                                    + redirectAddress));
                                }
                                RedirectClient nextClient = redirectClient(target, bearerToken);
                                if (nextClient == null) {
                                    return Multi.createFrom()
                                            .failure(
                                                    new RuntimeException(
                                                            "Failed to create redirect client"));
                                }
                                return replayWithRedirect(
                                        request, bearerToken, redirectsRemaining - 1, nextClient);
                            }
                            // Normal content response
                            String content = response.getContent();
                            if (content != null && !content.isEmpty()) {
                                return Multi.createFrom().item(content);
                            }
                            return Multi.createFrom().empty();
                        });
    }

    private io.smallrye.mutiny.Uni<CancelRecordResponse> cancelWithRedirect(
            CancelRecordRequest request, String bearerToken, int redirectsRemaining) {
        return stub(bearerToken)
                .cancel(request)
                .onItem()
                .transformToUni(
                        response -> {
                            String redirectAddress = response.getRedirectAddress();
                            if (redirectAddress != null && !redirectAddress.isEmpty()) {
                                if (redirectsRemaining <= 0) {
                                    return io.smallrye.mutiny.Uni.createFrom()
                                            .failure(new RuntimeException("Too many redirects"));
                                }
                                RedirectTarget target = parseRedirectAddress(redirectAddress);
                                if (target == null) {
                                    return io.smallrye.mutiny.Uni.createFrom()
                                            .failure(
                                                    new RuntimeException(
                                                            "Invalid redirect address: "
                                                                    + redirectAddress));
                                }
                                RedirectClient client = redirectClient(target, bearerToken);
                                if (client == null) {
                                    return io.smallrye.mutiny.Uni.createFrom()
                                            .failure(
                                                    new RuntimeException(
                                                            "Failed to create redirect client"));
                                }
                                return client.stub()
                                        .cancel(request)
                                        .onTermination()
                                        .invoke(client::close);
                            }
                            return io.smallrye.mutiny.Uni.createFrom().item(response);
                        });
    }

    private static RedirectTarget parseRedirectAddress(String address) {
        if (address == null || address.isEmpty()) {
            return null;
        }
        int lastColon = address.lastIndexOf(':');
        if (lastColon <= 0) {
            return null;
        }
        String host = address.substring(0, lastColon);
        try {
            int port = Integer.parseInt(address.substring(lastColon + 1));
            if (port <= 0) {
                return null;
            }
            return new RedirectTarget(host, port);
        } catch (NumberFormatException e) {
            return null;
        }
    }

    private RedirectClient redirectClient(RedirectTarget target, String bearerToken) {
        ManagedChannel channel =
                ManagedChannelBuilder.forAddress(target.host(), target.port())
                        .usePlaintext()
                        .build();
        MutinyResponseRecorderServiceGrpc.MutinyResponseRecorderServiceStub client =
                MutinyResponseRecorderServiceGrpc.newMutinyStub(channel);
        Metadata metadata = buildMetadata(bearerToken);
        if (metadata.keys() != null && !metadata.keys().isEmpty()) {
            client = client.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        return new RedirectClient(client, channel);
    }

    private record RedirectTarget(String host, int port) {}

    private record RedirectClient(
            MutinyResponseRecorderServiceGrpc.MutinyResponseRecorderServiceStub stub,
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
        private final MutinyResponseRecorderServiceGrpc.MutinyResponseRecorderServiceStub
                recorderService;
        private final ByteString conversationIdBytes;
        private final String conversationId;
        private final List<RecordRequest> pendingRequests = new ArrayList<>();
        private MultiEmitter<? super RecordRequest> emitter;
        private MultiEmitter<? super ResponseCancelSignal> cancelEmitter;
        private boolean firstMessage = true;
        private boolean completed = false;
        private boolean cancelEmitted = false;

        GrpcResponseRecorder(
                MutinyResponseRecorderServiceGrpc.MutinyResponseRecorderServiceStub recorderService,
                String conversationId) {
            this.recorderService = recorderService;
            this.conversationId = conversationId;
            this.conversationIdBytes = toByteString(conversationId);
            startStream();
        }

        @Override
        public synchronized void record(String token) {
            if (token == null || token.isEmpty() || completed) {
                return;
            }

            RecordRequest.Builder builder = RecordRequest.newBuilder().setContent(token);

            if (firstMessage) {
                builder.setConversationId(conversationIdBytes);
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

            RecordRequest.Builder builder = RecordRequest.newBuilder().setComplete(true);
            if (firstMessage) {
                builder.setConversationId(conversationIdBytes);
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
            Multi<RecordRequest> requestStream =
                    Multi.createFrom()
                            .emitter(
                                    newEmitter -> {
                                        synchronized (GrpcResponseRecorder.this) {
                                            emitter = newEmitter;
                                            if (!pendingRequests.isEmpty()) {
                                                for (RecordRequest request : pendingRequests) {
                                                    emitter.emit(request);
                                                }
                                                pendingRequests.clear();
                                            }
                                            if (completed) {
                                                emitter.complete();
                                            }
                                        }
                                    });

            recorderService
                    .record(requestStream)
                    .subscribe()
                    .with(
                            response -> {
                                if (response.getStatus() == RecordStatus.RECORD_STATUS_CANCELLED) {
                                    emitCancel();
                                } else if (response.getStatus()
                                        == RecordStatus.RECORD_STATUS_ERROR) {
                                    LOG.warnf(
                                            "Failed to record response for"
                                                    + " conversationId=%s: %s",
                                            conversationId, response.getErrorMessage());
                                }
                                completeCancelStream();
                            },
                            failure -> {
                                LOG.warnf(
                                        failure,
                                        "Failed to record response for" + " conversationId=%s",
                                        conversationId);
                                completeCancelStream();
                            });
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

        private void emit(RecordRequest request) {
            if (emitter != null) {
                emitter.emit(request);
                return;
            }
            pendingRequests.add(request);
        }
    }
}
