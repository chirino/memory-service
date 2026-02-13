package io.github.chirino.memoryservice.history;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.protobuf.ByteString;
import com.google.protobuf.Empty;
import io.github.chirino.memory.grpc.v1.CancelRecordRequest;
import io.github.chirino.memory.grpc.v1.CancelRecordResponse;
import io.github.chirino.memory.grpc.v1.CheckRecordingsRequest;
import io.github.chirino.memory.grpc.v1.CheckRecordingsResponse;
import io.github.chirino.memory.grpc.v1.IsEnabledResponse;
import io.github.chirino.memory.grpc.v1.RecordRequest;
import io.github.chirino.memory.grpc.v1.RecordResponse;
import io.github.chirino.memory.grpc.v1.RecordStatus;
import io.github.chirino.memory.grpc.v1.ReplayRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponse;
import io.github.chirino.memory.grpc.v1.ResponseRecorderServiceGrpc;
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
        return Flux.<String>create(
                        sink -> {
                            ReplayRequest request =
                                    ReplayRequest.newBuilder()
                                            .setConversationId(toByteString(conversationId))
                                            .build();
                            stub(bearerToken)
                                    .replay(
                                            request,
                                            new StreamObserver<>() {
                                                @Override
                                                public void onNext(ReplayResponse response) {
                                                    String content = response.getContent();
                                                    if (content != null && !content.isEmpty()) {
                                                        sink.next(content);
                                                    }
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
            CheckRecordingsRequest request =
                    CheckRecordingsRequest.newBuilder()
                            .addAllConversationIds(byteStringIds)
                            .build();
            CheckRecordingsResponse response = blockingStub(bearerToken).checkRecordings(request);
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
            LOG.warn("Failed to check recordings", e);
            return List.of();
        } catch (Exception e) {
            LOG.warn("Failed to check recordings", e);
            return List.of();
        }
    }

    @Override
    public boolean enabled() {
        try {
            IsEnabledResponse response = blockingStub(null).isEnabled(Empty.getDefaultInstance());
            return response.getEnabled();
        } catch (Exception e) {
            LOG.warn("Failed to check if response recorder is enabled", e);
            return false;
        }
    }

    @Override
    public void requestCancel(String conversationId, @Nullable String bearerToken) {
        if (!StringUtils.hasText(conversationId)) {
            LOG.warn("Cancel request skipped: no conversationId provided");
            return;
        }
        CancelRecordRequest request =
                CancelRecordRequest.newBuilder()
                        .setConversationId(toByteString(conversationId))
                        .build();
        try {
            CancelRecordResponse response = blockingStub(bearerToken).cancel(request);
            if (!response.getAccepted()) {
                LOG.warn("Cancel request was not accepted for conversationId={}", conversationId);
            }
        } catch (Exception e) {
            LOG.warn("Failed to request cancel for conversationId={}", conversationId, e);
        }
    }

    private ResponseRecorderServiceGrpc.ResponseRecorderServiceStub stub(
            @Nullable String bearerToken) {
        Metadata metadata = buildMetadata(bearerToken);
        if (metadata.keys().isEmpty()) {
            return stubs.responseRecorderService();
        }
        return stubs.responseRecorderService()
                .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    }

    private ResponseRecorderServiceGrpc.ResponseRecorderServiceBlockingStub blockingStub(
            @Nullable String bearerToken) {
        Metadata metadata = buildMetadata(bearerToken);
        ResponseRecorderServiceGrpc.ResponseRecorderServiceBlockingStub stub =
                ResponseRecorderServiceGrpc.newBlockingStub(channel);
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

        private final ResponseRecorderServiceGrpc.ResponseRecorderServiceStub service;
        private final String conversationId;
        private final Sinks.Many<ResponseCancelSignal> cancelSink =
                Sinks.many().multicast().onBackpressureBuffer();
        private final StreamObserver<RecordRequest> requestObserver;
        private final AtomicBoolean firstMessage = new AtomicBoolean(true);
        private final AtomicBoolean completed = new AtomicBoolean(false);

        GrpcResponseRecorder(
                ResponseRecorderServiceGrpc.ResponseRecorderServiceStub service,
                String conversationId) {
            this.service = service;
            this.conversationId = conversationId;
            this.requestObserver =
                    this.service.record(
                            new StreamObserver<>() {
                                @Override
                                public void onNext(RecordResponse response) {
                                    if (response.getStatus()
                                            == RecordStatus.RECORD_STATUS_CANCELLED) {
                                        cancelSink.tryEmitNext(ResponseCancelSignal.CANCEL);
                                    }
                                    cancelSink.tryEmitComplete();
                                }

                                @Override
                                public void onError(Throwable t) {
                                    LOG.warn(
                                            "Record stream error for conversationId={}",
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
            RecordRequest.Builder builder = RecordRequest.newBuilder().setContent(token);
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
            RecordRequest.Builder builder = RecordRequest.newBuilder().setComplete(true);
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
