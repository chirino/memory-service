package io.github.chirino.memory.grpc;

import static io.github.chirino.memory.grpc.UuidUtils.byteStringToString;

import com.google.protobuf.Empty;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.grpc.v1.CancelResponseRequest;
import io.github.chirino.memory.grpc.v1.CancelResponseResponse;
import io.github.chirino.memory.grpc.v1.CheckConversationsRequest;
import io.github.chirino.memory.grpc.v1.CheckConversationsResponse;
import io.github.chirino.memory.grpc.v1.IsEnabledResponse;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensResponse;
import io.github.chirino.memory.grpc.v1.ResponseResumerService;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenRequest;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenResponse;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.resumer.AdvertisedAddress;
import io.github.chirino.memory.resumer.ResponseResumerBackend;
import io.github.chirino.memory.resumer.ResponseResumerRedirectException;
import io.github.chirino.memory.resumer.ResponseResumerSelector;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.grpc.Metadata;
import io.grpc.Status;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.Uni;
import io.smallrye.mutiny.subscription.Cancellable;
import io.smallrye.mutiny.subscription.MultiEmitter;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.stream.Collectors;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@GrpcService
@Blocking
public class ResponseResumerGrpcService extends AbstractGrpcService
        implements ResponseResumerService {

    private static final Logger LOG = Logger.getLogger(ResponseResumerGrpcService.class);
    private static final Metadata.Key<String> REDIRECT_HOST_HEADER =
            Metadata.Key.of("x-resumer-redirect-host", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> REDIRECT_PORT_HEADER =
            Metadata.Key.of("x-resumer-redirect-port", Metadata.ASCII_STRING_MARSHALLER);

    @Inject ResponseResumerSelector resumerSelector;

    @ConfigProperty(name = "memory-service.grpc-advertised-address")
    Optional<String> advertisedAddress;

    @ConfigProperty(name = "quarkus.grpc.server.port")
    Optional<Integer> grpcPort;

    @ConfigProperty(name = "quarkus.http.port", defaultValue = "8080")
    int httpPort;

    @Override
    public Multi<StreamResponseTokenResponse> streamResponseTokens(
            Multi<StreamResponseTokenRequest> tokenStream) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            LOG.infof("Response resumer is not enabled");
            return Multi.createFrom()
                    .item(
                            StreamResponseTokenResponse.newBuilder()
                                    .setSuccess(false)
                                    .setErrorMessage("Response resumer is not enabled")
                                    .build());
        }

        return Multi.createFrom()
                .<StreamResponseTokenResponse>emitter(
                        emitter -> {
                            AtomicLong currentOffset = new AtomicLong(0);
                            AtomicBoolean initialized = new AtomicBoolean(false);
                            AtomicBoolean completed = new AtomicBoolean(false);
                            AtomicReference<ResponseResumerBackend.ResponseRecorder> recorderRef =
                                    new AtomicReference<>();
                            AtomicReference<Cancellable> cancelSubscription =
                                    new AtomicReference<>();
                            AtomicReference<Cancellable> tokenSubscriptionRef =
                                    new AtomicReference<>();

                            Cancellable tokenSubscription =
                                    tokenStream
                                            .subscribe()
                                            .with(
                                                    request -> {
                                                        if (initialized.compareAndSet(
                                                                false, true)) {
                                                            String conversationId =
                                                                    byteStringToString(
                                                                            request
                                                                                    .getConversationId());
                                                            if (conversationId == null
                                                                    || conversationId.isBlank()) {
                                                                failStream(
                                                                        emitter,
                                                                        tokenSubscriptionRef,
                                                                        cancelSubscription,
                                                                        recorderRef,
                                                                        completed,
                                                                        Status.INVALID_ARGUMENT
                                                                                .withDescription(
                                                                                        "conversation_id"
                                                                                            + " is required"
                                                                                            + " in first"
                                                                                            + " message")
                                                                                .asRuntimeException());
                                                                return;
                                                            }
                                                            try {
                                                                ensureConversationAccess(
                                                                        conversationId,
                                                                        AccessLevel.WRITER);
                                                            } catch (AccessDeniedException e) {
                                                                failStream(
                                                                        emitter,
                                                                        tokenSubscriptionRef,
                                                                        cancelSubscription,
                                                                        recorderRef,
                                                                        completed,
                                                                        Status.PERMISSION_DENIED
                                                                                .withDescription(
                                                                                        "User does"
                                                                                            + " not have"
                                                                                            + " WRITER"
                                                                                            + " access"
                                                                                            + " to conversation")
                                                                                .asRuntimeException());
                                                                return;
                                                            } catch (ResourceNotFoundException e) {
                                                                failStream(
                                                                        emitter,
                                                                        tokenSubscriptionRef,
                                                                        cancelSubscription,
                                                                        recorderRef,
                                                                        completed,
                                                                        Status.NOT_FOUND
                                                                                .withDescription(
                                                                                        "Conversation"
                                                                                            + " not found")
                                                                                .asRuntimeException());
                                                                return;
                                                            }

                                                            recorderRef.set(
                                                                    backend.recorder(
                                                                            conversationId,
                                                                            resolveAdvertisedAddress()));

                                                            Cancellable cancelWatch =
                                                                    backend.cancelStream(
                                                                                    conversationId)
                                                                            .subscribe()
                                                                            .with(
                                                                                    signal -> {
                                                                                        if (!completed
                                                                                                .compareAndSet(
                                                                                                        false,
                                                                                                        true)) {
                                                                                            return;
                                                                                        }
                                                                                        Cancellable
                                                                                                tokenHandle =
                                                                                                        tokenSubscriptionRef
                                                                                                                .get();
                                                                                        if (tokenHandle
                                                                                                != null) {
                                                                                            tokenHandle
                                                                                                    .cancel();
                                                                                        }
                                                                                        ResponseResumerBackend
                                                                                                        .ResponseRecorder
                                                                                                recorder =
                                                                                                        recorderRef
                                                                                                                .get();
                                                                                        if (recorder
                                                                                                != null) {
                                                                                            recorder
                                                                                                    .complete();
                                                                                        }
                                                                                        emitter
                                                                                                .emit(
                                                                                                        StreamResponseTokenResponse
                                                                                                                .newBuilder()
                                                                                                                .setSuccess(
                                                                                                                        true)
                                                                                                                .setCurrentOffset(
                                                                                                                        currentOffset
                                                                                                                                .get())
                                                                                                                .setCancelRequested(
                                                                                                                        true)
                                                                                                                .build());
                                                                                        emitter
                                                                                                .complete();
                                                                                    },
                                                                                    emitter::fail);
                                                            cancelSubscription.set(cancelWatch);
                                                        }

                                                        ResponseResumerBackend.ResponseRecorder
                                                                recorder = recorderRef.get();
                                                        if (recorder == null) {
                                                            return;
                                                        }
                                                        String token = request.getToken();
                                                        if (token != null && !token.isEmpty()) {
                                                            recorder.record(token);
                                                            currentOffset.addAndGet(token.length());
                                                        }
                                                        if (request.getComplete()
                                                                && completed.compareAndSet(
                                                                        false, true)) {
                                                            recorder.complete();
                                                            emitter.emit(
                                                                    StreamResponseTokenResponse
                                                                            .newBuilder()
                                                                            .setSuccess(true)
                                                                            .setCurrentOffset(
                                                                                    currentOffset
                                                                                            .get())
                                                                            .build());
                                                            emitter.complete();
                                                        }
                                                    },
                                                    failure ->
                                                            failStream(
                                                                    emitter,
                                                                    tokenSubscriptionRef,
                                                                    cancelSubscription,
                                                                    recorderRef,
                                                                    completed,
                                                                    failure),
                                                    () -> {
                                                        if (completed.compareAndSet(false, true)) {
                                                            ResponseResumerBackend.ResponseRecorder
                                                                    recorder = recorderRef.get();
                                                            if (recorder != null) {
                                                                recorder.complete();
                                                            }
                                                            emitter.emit(
                                                                    StreamResponseTokenResponse
                                                                            .newBuilder()
                                                                            .setSuccess(true)
                                                                            .setCurrentOffset(
                                                                                    currentOffset
                                                                                            .get())
                                                                            .build());
                                                            emitter.complete();
                                                        }
                                                    });

                            tokenSubscriptionRef.set(tokenSubscription);

                            emitter.onTermination(
                                    () -> {
                                        tokenSubscription.cancel();
                                        Cancellable cancelWatch = cancelSubscription.get();
                                        if (cancelWatch != null) {
                                            cancelWatch.cancel();
                                        }
                                    });
                        })
                .onFailure()
                .transform(
                        failure -> {
                            Throwable rootCause =
                                    failure.getCause() != null ? failure.getCause() : failure;
                            if (rootCause instanceof java.util.NoSuchElementException) {
                                return Status.INVALID_ARGUMENT
                                        .withDescription("At least one request message is required")
                                        .asRuntimeException();
                            }
                            if (failure instanceof io.grpc.StatusRuntimeException) {
                                return failure;
                            }
                            LOG.warnf(failure, "Failed to stream response tokens");
                            return Status.INTERNAL
                                    .withDescription(
                                            "Failed to stream response tokens: "
                                                    + failure.getMessage())
                                    .asRuntimeException();
                        });
    }

    private static void failStream(
            MultiEmitter<? super StreamResponseTokenResponse> emitter,
            AtomicReference<Cancellable> tokenSubscriptionRef,
            AtomicReference<Cancellable> cancelSubscription,
            AtomicReference<ResponseResumerBackend.ResponseRecorder> recorderRef,
            AtomicBoolean completed,
            Throwable failure) {
        if (!completed.compareAndSet(false, true)) {
            return;
        }
        Cancellable tokenHandle = tokenSubscriptionRef.get();
        if (tokenHandle != null) {
            tokenHandle.cancel();
        }
        Cancellable cancelHandle = cancelSubscription.get();
        if (cancelHandle != null) {
            cancelHandle.cancel();
        }
        ResponseResumerBackend.ResponseRecorder recorder = recorderRef.get();
        if (recorder != null) {
            recorder.complete();
        }
        emitter.fail(failure);
    }

    @Override
    public Multi<ReplayResponseTokensResponse> replayResponseTokens(
            ReplayResponseTokensRequest request) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            LOG.infof("Response resumer is not enabled");
            return Multi.createFrom().empty();
        }

        String conversationId = byteStringToString(request.getConversationId());
        if (conversationId == null || conversationId.isBlank()) {
            return Multi.createFrom()
                    .failure(
                            Status.INVALID_ARGUMENT
                                    .withDescription("conversation_id is required")
                                    .asRuntimeException());
        }

        // Check access - requires READER access or valid API key
        try {
            ensureConversationAccess(conversationId, AccessLevel.READER);
        } catch (AccessDeniedException e) {
            return Multi.createFrom()
                    .failure(
                            Status.PERMISSION_DENIED
                                    .withDescription(
                                            "User does not have READER access to conversation")
                                    .asRuntimeException());
        } catch (ResourceNotFoundException e) {
            return Multi.createFrom()
                    .failure(
                            Status.NOT_FOUND
                                    .withDescription("Conversation not found")
                                    .asRuntimeException());
        }

        AtomicLong currentOffset = new AtomicLong(0);
        String finalConversationId = conversationId;

        return backend.replay(finalConversationId, resolveAdvertisedAddress())
                .onItem()
                .transform(
                        token -> {
                            long offset = currentOffset.addAndGet(token.length());
                            return ReplayResponseTokensResponse.newBuilder()
                                    .setToken(token)
                                    .setOffset(offset)
                                    .build();
                        })
                .onFailure()
                .transform(
                        e -> {
                            if (e instanceof ResponseResumerRedirectException redirect) {
                                return toRedirectStatus(redirect);
                            }
                            if (e instanceof io.grpc.StatusRuntimeException) {
                                return e;
                            }
                            LOG.warnf(
                                    e,
                                    "Failed to replay response tokens for conversation %s",
                                    finalConversationId);
                            return Status.INTERNAL
                                    .withDescription(
                                            "Failed to replay response tokens: " + e.getMessage())
                                    .asRuntimeException();
                        });
    }

    @Override
    public Uni<CancelResponseResponse> cancelResponse(CancelResponseRequest request) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            return Uni.createFrom()
                    .failure(
                            Status.FAILED_PRECONDITION
                                    .withDescription("Response resumer is not enabled")
                                    .asRuntimeException());
        }

        String conversationId = byteStringToString(request.getConversationId());
        if (conversationId == null || conversationId.isBlank()) {
            return Uni.createFrom()
                    .failure(
                            Status.INVALID_ARGUMENT
                                    .withDescription("conversation_id is required")
                                    .asRuntimeException());
        }

        try {
            ensureConversationAccess(conversationId, AccessLevel.WRITER, false);
        } catch (AccessDeniedException e) {
            return Uni.createFrom()
                    .failure(
                            Status.PERMISSION_DENIED
                                    .withDescription(
                                            "User does not have WRITER access to conversation")
                                    .asRuntimeException());
        } catch (ResourceNotFoundException e) {
            return Uni.createFrom()
                    .failure(
                            Status.NOT_FOUND
                                    .withDescription("Conversation not found")
                                    .asRuntimeException());
        }

        try {
            backend.requestCancel(conversationId, resolveAdvertisedAddress());
        } catch (ResponseResumerRedirectException redirect) {
            return Uni.createFrom().failure(toRedirectStatus(redirect));
        }
        waitForResponseCompletion(conversationId, backend, Duration.ofSeconds(30));

        return Uni.createFrom().item(CancelResponseResponse.newBuilder().setAccepted(true).build());
    }

    @Override
    public Uni<IsEnabledResponse> isEnabled(Empty request) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        return Uni.createFrom()
                .item(IsEnabledResponse.newBuilder().setEnabled(backend.enabled()).build());
    }

    @Override
    public Uni<CheckConversationsResponse> checkConversations(CheckConversationsRequest request) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            LOG.infof("Response resumer is not enabled");
            return Uni.createFrom().item(CheckConversationsResponse.newBuilder().build());
        }

        List<String> conversationIds =
                request.getConversationIdsList().stream()
                        .map(UuidUtils::byteStringToString)
                        .collect(Collectors.toList());
        if (conversationIds.isEmpty()) {
            return Uni.createFrom().item(CheckConversationsResponse.newBuilder().build());
        }

        // Filter conversations the user has access to
        List<String> accessibleConversationIds = new ArrayList<>();
        for (String conversationId : conversationIds) {
            try {
                ensureConversationAccess(conversationId, AccessLevel.READER);
                accessibleConversationIds.add(conversationId);
            } catch (AccessDeniedException | ResourceNotFoundException e) {
                // Skip conversations the user doesn't have access to
                LOG.debugf("Skipping conversation %s in check: %s", conversationId, e.getMessage());
            }
        }

        // Check which accessible conversations have responses in progress
        List<String> inProgress = backend.check(accessibleConversationIds);

        return Uni.createFrom()
                .item(
                        CheckConversationsResponse.newBuilder()
                                .addAllConversationIds(
                                        inProgress.stream()
                                                .map(UuidUtils::stringToByteString)
                                                .collect(Collectors.toList()))
                                .build());
    }

    private void ensureConversationAccess(String conversationId, AccessLevel requiredLevel) {
        ensureConversationAccess(conversationId, requiredLevel, true);
    }

    private void ensureConversationAccess(
            String conversationId, AccessLevel requiredLevel, boolean allowApiKey) {
        // Agents with valid API keys can always access unless explicitly disallowed.
        if (allowApiKey && hasValidApiKey()) {
            return;
        }

        // For users, verify they have access via store().getConversation()
        // This will throw AccessDeniedException if user lacks READER access
        ConversationDto conversation;
        try {
            conversation = store().getConversation(currentUserId(), conversationId);
        } catch (AccessDeniedException e) {
            throw e;
        } catch (ResourceNotFoundException e) {
            throw e;
        }

        // For WRITER access, check if user has at least WRITER level
        if (requiredLevel == AccessLevel.WRITER) {
            AccessLevel userAccessLevel = conversation.getAccessLevel();
            if (userAccessLevel != AccessLevel.WRITER
                    && userAccessLevel != AccessLevel.MANAGER
                    && userAccessLevel != AccessLevel.OWNER) {
                throw new AccessDeniedException("User does not have WRITER access to conversation");
            }
        }
        // For READER access, getConversation() already verified access
    }

    private void waitForResponseCompletion(
            String conversationId, ResponseResumerBackend backend, Duration timeout) {
        long deadline = System.nanoTime() + timeout.toNanos();
        while (System.nanoTime() < deadline) {
            if (!backend.hasResponseInProgress(conversationId)) {
                return;
            }
            try {
                Thread.sleep(200);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return;
            }
        }
    }

    private AdvertisedAddress resolveAdvertisedAddress() {
        Optional<AdvertisedAddress> configured =
                advertisedAddress.flatMap(AdvertisedAddress::parse);
        if (configured.isPresent()) {
            return configured.get();
        }

        GrpcRequestMetadata metadata = GrpcRequestMetadata.current();
        if (metadata != null) {
            String localAddress = metadata.localAddress();
            Integer localPort = metadata.localPort();
            if (localAddress != null && !localAddress.isBlank()) {
                int resolvedPort = localPort != null ? localPort : grpcPort.orElse(httpPort);
                return new AdvertisedAddress(localAddress, resolvedPort);
            }
        }

        int port = grpcPort.orElse(httpPort);
        String host = resolveLocalHost();
        return new AdvertisedAddress(host, port);
    }

    private String resolveLocalHost() {
        try {
            return java.net.InetAddress.getLocalHost().getHostName();
        } catch (Exception e) {
            return "localhost";
        }
    }

    private static Throwable toRedirectStatus(ResponseResumerRedirectException redirect) {
        Metadata trailers = new Metadata();
        AdvertisedAddress target = redirect.target();
        if (target != null) {
            trailers.put(REDIRECT_HOST_HEADER, target.host());
            trailers.put(REDIRECT_PORT_HEADER, Integer.toString(target.port()));
        }
        return Status.UNAVAILABLE
                .withDescription("Response resumer redirect")
                .asRuntimeException(trailers);
    }
}
