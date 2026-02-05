package io.github.chirino.memoryservice.history;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.protobuf.ByteString;
import com.google.protobuf.Empty;
import io.github.chirino.memory.grpc.v1.CancelResponseRequest;
import io.github.chirino.memory.grpc.v1.CancelResponseResponse;
import io.github.chirino.memory.grpc.v1.CheckConversationsRequest;
import io.github.chirino.memory.grpc.v1.CheckConversationsResponse;
import io.github.chirino.memory.grpc.v1.IsEnabledResponse;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensResponse;
import io.github.chirino.memory.grpc.v1.ResponseResumerServiceGrpc;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenRequest;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenResponse;
import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcClients;
import io.github.chirino.memoryservice.security.SecurityHelper;
import io.grpc.ManagedChannel;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.MetadataUtils;
import io.grpc.stub.StreamObserver;
import java.nio.ByteBuffer;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicBoolean;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Sinks;

public class GrpcResponseResumer implements ResponseResumer {

    private static final Logger LOG = LoggerFactory.getLogger(GrpcResponseResumer.class);
    private static final Metadata.Key<String> AUTHORIZATION_HEADER =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> API_KEY_HEADER =
            Metadata.Key.of("x-api-key", Metadata.ASCII_STRING_MARSHALLER);

    private final MemoryServiceGrpcClients.MemoryServiceStubs stubs;
    private final ManagedChannel channel;
    private final MemoryServiceClientProperties clientProperties;
    private final OAuth2AuthorizedClientService authorizedClientService;
    private final ObjectMapper objectMapper;

    public GrpcResponseResumer(
            MemoryServiceGrpcClients.MemoryServiceStubs stubs,
            ManagedChannel channel,
            MemoryServiceClientProperties clientProperties,
            @Nullable OAuth2AuthorizedClientService authorizedClientService,
            ObjectMapper objectMapper) {
        this.stubs = stubs;
        this.channel = channel;
        this.clientProperties = clientProperties;
        this.authorizedClientService = authorizedClientService;
        this.objectMapper = objectMapper;
    }

    @Override
    public ResponseRecorder recorder(String conversationId) {
        return recorder(conversationId, null);
    }

    @Override
    public ResponseRecorder recorder(String conversationId, @Nullable String bearerToken) {
        return new GrpcResponseRecorder(stub(bearerToken), conversationId);
    }

    @Override
    public Flux<String> replay(String conversationId, @Nullable String bearerToken) {
        final long[] tokenCount = {0};
        return Flux.<String>create(
                        sink -> {
                            ReplayResponseTokensRequest request =
                                    ReplayResponseTokensRequest.newBuilder()
                                            .setConversationId(toByteString(conversationId))
                                            .build();
                            stub(bearerToken)
                                    .replayResponseTokens(
                                            request,
                                            new StreamObserver<>() {
                                                @Override
                                                public void onNext(
                                                        ReplayResponseTokensResponse response) {
                                                    tokenCount[0]++;
                                                    sink.next(response.getToken());
                                                }

                                                @Override
                                                public void onError(Throwable throwable) {
                                                    sink.error(throwable);
                                                }

                                                @Override
                                                public void onCompleted() {
                                                    sink.complete();
                                                }
                                            });
                        })
                .doOnError(
                        failure ->
                                LOG.warn(
                                        "Failed to replay tokens for conversationId={}",
                                        conversationId,
                                        failure));
    }

    @Override
    @SuppressWarnings("unchecked")
    public <T> Flux<T> replayEvents(
            String conversationId, @Nullable String bearerToken, Class<T> type) {
        Flux<String> raw = replay(conversationId, bearerToken);
        Flux<String> buffered = JsonLineBufferingTransformer.bufferLinesWithFlush(raw);

        if (type == String.class) {
            // Return raw JSON lines - efficient for SSE, no deserialize/re-serialize
            return (Flux<T>) buffered;
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
    public List<String> check(List<String> conversationIds, @Nullable String bearerToken) {
        if (conversationIds == null || conversationIds.isEmpty()) {
            LOG.warn("Check called with empty conversationIds, returning empty list");
            return List.of();
        }
        try {
            List<ByteString> byteStringIds =
                    conversationIds.stream().map(GrpcResponseResumer::toByteString).toList();
            CheckConversationsRequest request =
                    CheckConversationsRequest.newBuilder()
                            .addAllConversationIds(byteStringIds)
                            .build();
            CheckConversationsResponse response =
                    blockingStub(bearerToken).checkConversations(request);
            List<String> resumable = new ArrayList<>();
            for (ByteString bs : response.getConversationIdsList()) {
                String id = fromByteString(bs);
                if (id != null) {
                    resumable.add(id);
                }
            }
            return resumable;
        } catch (StatusRuntimeException e) {
            Status status = e.getStatus();
            if (status.getCode() == Status.Code.UNIMPLEMENTED
                    || status.getCode() == Status.Code.NOT_FOUND) {
                return List.of();
            }
            LOG.warn("Failed to check conversations", e);
            return List.of();
        } catch (Exception e) {
            LOG.warn("Failed to check conversations", e);
            return List.of();
        }
    }

    @Override
    public boolean enabled() {
        try {
            IsEnabledResponse response = blockingStub(null).isEnabled(Empty.getDefaultInstance());
            return response.getEnabled();
        } catch (Exception e) {
            LOG.warn("Failed to check if response resumer is enabled", e);
            return false;
        }
    }

    @Override
    public void requestCancel(String conversationId, @Nullable String bearerToken) {
        if (!StringUtils.hasText(conversationId)) {
            LOG.warn("Cancel request skipped: no conversationId provided");
            return;
        }
        CancelResponseRequest request =
                CancelResponseRequest.newBuilder()
                        .setConversationId(toByteString(conversationId))
                        .build();
        try {
            CancelResponseResponse response = blockingStub(bearerToken).cancelResponse(request);
            if (!response.getAccepted()) {
                LOG.warn("Cancel request was not accepted for conversationId={}", conversationId);
            }
        } catch (Exception e) {
            LOG.warn("Failed to request cancel for conversationId={}", conversationId, e);
        }
    }

    private ResponseResumerServiceGrpc.ResponseResumerServiceStub stub(
            @Nullable String bearerToken) {
        Metadata metadata = buildMetadata(bearerToken);
        if (metadata.keys().isEmpty()) {
            return stubs.responseResumerService();
        }
        return stubs.responseResumerService()
                .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    }

    private ResponseResumerServiceGrpc.ResponseResumerServiceBlockingStub blockingStub(
            @Nullable String bearerToken) {
        Metadata metadata = buildMetadata(bearerToken);
        ResponseResumerServiceGrpc.ResponseResumerServiceBlockingStub stub =
                ResponseResumerServiceGrpc.newBlockingStub(channel);
        if (!metadata.keys().isEmpty()) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        return stub;
    }

    private Metadata buildMetadata(@Nullable String bearerToken) {
        Metadata metadata = new Metadata();
        String token = bearerToken;
        if (!StringUtils.hasText(token)) {
            token = SecurityHelper.bearerToken(authorizedClientService);
        }
        if (StringUtils.hasText(token)) {
            metadata.put(AUTHORIZATION_HEADER, "Bearer " + token);
        }
        String apiKey = clientProperties.getApiKey();
        if (StringUtils.hasText(apiKey)) {
            metadata.put(API_KEY_HEADER, apiKey);
        }
        return metadata;
    }

    private static ByteString toByteString(String uuidString) {
        if (uuidString == null || uuidString.isEmpty()) {
            return ByteString.EMPTY;
        }
        UUID uuid = UUID.fromString(uuidString);
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

    private final class GrpcResponseRecorder implements ResponseRecorder {

        private final ResponseResumerServiceGrpc.ResponseResumerServiceStub service;
        private final String conversationId;
        private final Sinks.Many<ResponseCancelSignal> cancelSink =
                Sinks.many().multicast().onBackpressureBuffer();
        private final StreamObserver<StreamResponseTokenRequest> requestObserver;
        private final AtomicBoolean firstMessage = new AtomicBoolean(true);
        private final AtomicBoolean completed = new AtomicBoolean(false);

        GrpcResponseRecorder(
                ResponseResumerServiceGrpc.ResponseResumerServiceStub service,
                String conversationId) {
            this.service = service;
            this.conversationId = conversationId;
            this.requestObserver =
                    this.service.streamResponseTokens(
                            new StreamObserver<>() {
                                @Override
                                public void onNext(StreamResponseTokenResponse response) {
                                    if (response.getCancelRequested()) {
                                        cancelSink.tryEmitNext(ResponseCancelSignal.CANCEL);
                                    }
                                }

                                @Override
                                public void onError(Throwable t) {
                                    LOG.warn(
                                            "Response stream error for conversationId={}",
                                            conversationId,
                                            t);
                                    cancelSink.tryEmitError(t);
                                }

                                @Override
                                public void onCompleted() {
                                    cancelSink.tryEmitComplete();
                                }
                            });
        }

        @Override
        public synchronized void record(String token) {
            if (token == null || token.isBlank() || completed.get()) {
                return;
            }
            StreamResponseTokenRequest.Builder builder =
                    StreamResponseTokenRequest.newBuilder().setToken(token);
            if (firstMessage.compareAndSet(true, false)) {
                builder.setConversationId(toByteString(conversationId));
            }
            requestObserver.onNext(builder.build());
        }

        @Override
        public synchronized void complete() {
            if (!completed.compareAndSet(false, true)) {
                return;
            }
            StreamResponseTokenRequest.Builder builder =
                    StreamResponseTokenRequest.newBuilder().setComplete(true);
            if (firstMessage.compareAndSet(true, false)) {
                builder.setConversationId(toByteString(conversationId));
            }
            requestObserver.onNext(builder.build());
            requestObserver.onCompleted();
            cancelSink.tryEmitComplete();
        }

        @Override
        public Flux<ResponseCancelSignal> cancelStream() {
            return cancelSink.asFlux();
        }
    }
}
