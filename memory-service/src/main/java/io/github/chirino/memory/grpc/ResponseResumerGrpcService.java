package io.github.chirino.memory.grpc;

import com.google.protobuf.Empty;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.grpc.v1.CheckConversationsRequest;
import io.github.chirino.memory.grpc.v1.CheckConversationsResponse;
import io.github.chirino.memory.grpc.v1.HasResponseInProgressRequest;
import io.github.chirino.memory.grpc.v1.HasResponseInProgressResponse;
import io.github.chirino.memory.grpc.v1.IsEnabledResponse;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensResponse;
import io.github.chirino.memory.grpc.v1.ResponseResumerService;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenRequest;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenResponse;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.resumer.ResponseResumerBackend;
import io.github.chirino.memory.resumer.ResponseResumerSelector;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.grpc.Status;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import org.jboss.logging.Logger;

@GrpcService
@Blocking
public class ResponseResumerGrpcService extends AbstractGrpcService
        implements ResponseResumerService {

    private static final Logger LOG = Logger.getLogger(ResponseResumerGrpcService.class);

    @Inject ResponseResumerSelector resumerSelector;

    @Override
    public Uni<StreamResponseTokenResponse> streamResponseTokens(
            Multi<StreamResponseTokenRequest> tokenStream) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            LOG.infof("Response resumer is not enabled");
            return Uni.createFrom()
                    .item(
                            StreamResponseTokenResponse.newBuilder()
                                    .setSuccess(false)
                                    .setErrorMessage("Response resumer is not enabled")
                                    .build());
        }

        AtomicLong currentOffset = new AtomicLong(0);
        AtomicBoolean initialized = new AtomicBoolean(false);
        AtomicReference<ResponseResumerBackend.ResponseRecorder> recorderRef =
                new AtomicReference<>();

        return tokenStream
                .onItem()
                .invoke(
                        request -> {
                            if (initialized.compareAndSet(false, true)) {
                                String conversationId = request.getConversationId();
                                if (conversationId == null || conversationId.isBlank()) {
                                    throw Status.INVALID_ARGUMENT
                                            .withDescription(
                                                    "conversation_id is required in first message")
                                            .asRuntimeException();
                                }

                                try {
                                    ensureConversationAccess(conversationId, AccessLevel.WRITER);
                                } catch (AccessDeniedException e) {
                                    throw Status.PERMISSION_DENIED
                                            .withDescription(
                                                    "User does not have WRITER access to "
                                                            + "conversation")
                                            .asRuntimeException();
                                } catch (ResourceNotFoundException e) {
                                    throw Status.NOT_FOUND
                                            .withDescription("Conversation not found")
                                            .asRuntimeException();
                                }

                                recorderRef.set(backend.recorder(conversationId));
                            }

                            ResponseResumerBackend.ResponseRecorder recorder = recorderRef.get();
                            String token = request.getToken();
                            if (token != null && !token.isEmpty()) {
                                recorder.record(token);
                                currentOffset.addAndGet(token.length());
                            }
                            if (request.getComplete()) {
                                recorder.complete();
                            }
                        })
                .collect()
                .last()
                .replaceWith(
                        () ->
                                StreamResponseTokenResponse.newBuilder()
                                        .setSuccess(true)
                                        .setCurrentOffset(currentOffset.get())
                                        .build())
                .onFailure()
                .recoverWithUni(
                        failure -> {
                            Throwable rootCause =
                                    failure.getCause() != null ? failure.getCause() : failure;
                            if (rootCause instanceof java.util.NoSuchElementException) {
                                return Uni.createFrom()
                                        .failure(
                                                Status.INVALID_ARGUMENT
                                                        .withDescription(
                                                                "At least one request message is"
                                                                        + " required")
                                                        .asRuntimeException());
                            }
                            if (failure instanceof io.grpc.StatusRuntimeException) {
                                return Uni.createFrom().failure(failure);
                            }
                            LOG.warnf(failure, "Failed to stream response tokens");
                            return Uni.createFrom()
                                    .failure(
                                            Status.INTERNAL
                                                    .withDescription(
                                                            "Failed to stream response tokens: "
                                                                    + failure.getMessage())
                                                    .asRuntimeException());
                        });
    }

    @Override
    public Multi<ReplayResponseTokensResponse> replayResponseTokens(
            ReplayResponseTokensRequest request) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            LOG.infof("Response resumer is not enabled");
            return Multi.createFrom().empty();
        }

        String conversationId = request.getConversationId();
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

        long resumePosition = request.getResumePosition();
        AtomicLong currentOffset = new AtomicLong(resumePosition);

        return backend.replay(conversationId, resumePosition)
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
                            if (e instanceof io.grpc.StatusRuntimeException) {
                                return e;
                            }
                            LOG.warnf(
                                    e,
                                    "Failed to replay response tokens for conversation %s",
                                    conversationId);
                            return Status.INTERNAL
                                    .withDescription(
                                            "Failed to replay response tokens: " + e.getMessage())
                                    .asRuntimeException();
                        });
    }

    @Override
    public Uni<IsEnabledResponse> isEnabled(Empty request) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        return Uni.createFrom()
                .item(IsEnabledResponse.newBuilder().setEnabled(backend.enabled()).build());
    }

    @Override
    public Uni<HasResponseInProgressResponse> hasResponseInProgress(
            HasResponseInProgressRequest request) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            LOG.infof("Response resumer is not enabled");
            return Uni.createFrom()
                    .item(HasResponseInProgressResponse.newBuilder().setInProgress(false).build());
        }

        String conversationId = request.getConversationId();
        if (conversationId == null || conversationId.isBlank()) {
            return Uni.createFrom()
                    .failure(
                            Status.INVALID_ARGUMENT
                                    .withDescription("conversation_id is required")
                                    .asRuntimeException());
        }

        // Check access - requires READER access or valid API key
        try {
            ensureConversationAccess(conversationId, AccessLevel.READER);
        } catch (AccessDeniedException e) {
            return Uni.createFrom()
                    .failure(
                            Status.PERMISSION_DENIED
                                    .withDescription(
                                            "User does not have READER access to conversation")
                                    .asRuntimeException());
        } catch (ResourceNotFoundException e) {
            return Uni.createFrom()
                    .failure(
                            Status.NOT_FOUND
                                    .withDescription("Conversation not found")
                                    .asRuntimeException());
        }

        boolean inProgress = backend.hasResponseInProgress(conversationId);
        return Uni.createFrom()
                .item(HasResponseInProgressResponse.newBuilder().setInProgress(inProgress).build());
    }

    @Override
    public Uni<CheckConversationsResponse> checkConversations(CheckConversationsRequest request) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            LOG.infof("Response resumer is not enabled");
            return Uni.createFrom().item(CheckConversationsResponse.newBuilder().build());
        }

        List<String> conversationIds = request.getConversationIdsList();
        if (conversationIds == null || conversationIds.isEmpty()) {
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
                                .addAllConversationIds(inProgress)
                                .build());
    }

    private void ensureConversationAccess(String conversationId, AccessLevel requiredLevel) {
        // Agents with valid API keys can always access
        if (hasValidApiKey()) {
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
}
