package io.github.chirino.memory.subagent.runtime;

import io.github.chirino.memory.history.runtime.ConversationStore;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.infrastructure.Infrastructure;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentMap;
import org.jboss.logging.Logger;

@ApplicationScoped
public class SubAgentTaskManager {

    private static final Logger LOG = Logger.getLogger(SubAgentTaskManager.class);
    private static final int MAX_JOIN_ROUNDS = 8;

    @Inject ConversationStore conversationStore;

    private final ConcurrentMap<String, TaskState> tasks = new ConcurrentHashMap<>();
    private final ConcurrentMap<String, Set<String>> pendingJoinTasks = new ConcurrentHashMap<>();

    public SubAgentTaskResult messageTask(
            String parentConversationId,
            String childConversationId,
            String message,
            String childAgentId,
            String userId,
            String bearerToken,
            SubAgentTaskInvoker invoker) {
        TaskState state;
        boolean newChildConversation = childConversationId == null || childConversationId.isBlank();
        if (newChildConversation) {
            childConversationId = UUID.randomUUID().toString();
            state = new TaskState(parentConversationId, childConversationId, childAgentId, invoker);
            state.markRunning(message);
            tasks.put(childConversationId, state);
        } else {
            state = requireTask(parentConversationId, childConversationId);
            synchronized (state) {
                if (state.status == SubAgentTaskStatus.RUNNING) {
                    throw new IllegalStateException(
                            "Sub-agent conversation " + childConversationId + " is still running");
                }
                state.markRunning(message);
                if (childAgentId != null && !childAgentId.isBlank()) {
                    state.childAgentId = childAgentId;
                }
            }
        }
        registerPendingJoinTask(parentConversationId, childConversationId);

        conversationStore.appendUserMessage(
                childConversationId,
                message,
                List.of(),
                state.childAgentId,
                null,
                null,
                newChildConversation ? parentConversationId : null,
                null);
        submit(state, message, userId, bearerToken);
        return state.snapshot();
    }

    public SubAgentTaskResult getStatus(String parentConversationId, String childConversationId) {
        return requireTask(parentConversationId, childConversationId).snapshot();
    }

    public List<SubAgentTaskResult> awaitPendingJoinTasks(String parentConversationId) {
        Set<String> childIds = pendingJoinTasks.remove(parentConversationId);
        if (childIds == null || childIds.isEmpty()) {
            return List.of();
        }
        return childIds.stream()
                .map(id -> requireTask(parentConversationId, id))
                .map(TaskState::awaitCurrentRun)
                .toList();
    }

    public int maxJoinRounds() {
        return MAX_JOIN_ROUNDS;
    }

    private void submit(TaskState state, String message, String userId, String bearerToken) {
        CompletableFuture<SubAgentTaskResult> future = new CompletableFuture<>();
        state.currentRun = future;
        Infrastructure.getDefaultExecutor()
                .execute(
                        () -> {
                            try {
                                SubAgentExecutionContext.with(
                                        userId,
                                        bearerToken,
                                        () -> {
                                            try {
                                                SubAgentTaskExecution execution =
                                                        state.taskInvoker.handle(
                                                                new SubAgentTaskRequest(
                                                                        state.parentConversationId,
                                                                        state.childConversationId,
                                                                        message,
                                                                        state.childAgentId));
                                                String response =
                                                        consumeExecution(state, execution);
                                                onSuccess(state, message, response, future);
                                            } catch (Exception e) {
                                                onFailure(state, message, e, future);
                                            }
                                            return null;
                                        });
                            } catch (Exception e) {
                                LOG.warnf(
                                        e,
                                        "Failed to run sub-agent conversation %s",
                                        state.childConversationId);
                            }
                        });
    }

    private String consumeExecution(TaskState state, SubAgentTaskExecution execution) {
        if (execution instanceof SubAgentTaskExecution.Immediate immediate) {
            return immediate.response();
        }
        if (execution instanceof SubAgentTaskExecution.Streaming streaming) {
            return consumeStream(state, streaming.events());
        }
        throw new IllegalStateException("Unknown sub-agent execution type");
    }

    private String consumeStream(TaskState state, io.smallrye.mutiny.Multi<ChatEvent> events) {
        if (events == null) {
            return "";
        }
        events.onItem()
                .invoke(event -> onStreamEvent(state, event))
                .collect()
                .asList()
                .await()
                .indefinitely();
        String finalResponse = state.streamFinalResponse;
        return finalResponse == null || finalResponse.isBlank()
                ? state.streamedResponseSoFar.toString()
                : finalResponse;
    }

    private void onStreamEvent(TaskState state, ChatEvent event) {
        synchronized (state) {
            if (event instanceof ChatEvent.PartialResponseEvent partial
                    && partial.getChunk() != null) {
                state.streamedResponseSoFar.append(partial.getChunk());
            } else if (event instanceof ChatEvent.AccumulatedResponseEvent accumulated
                    && accumulated.getMessage() != null) {
                state.streamFinalResponse = accumulated.getMessage();
            } else if (event instanceof ChatEvent.ChatCompletedEvent completed
                    && completed.getChatResponse() != null
                    && completed.getChatResponse().aiMessage() != null
                    && completed.getChatResponse().aiMessage().text() != null) {
                state.streamFinalResponse = completed.getChatResponse().aiMessage().text();
            }
        }
    }

    private void onSuccess(
            TaskState state,
            String message,
            String response,
            CompletableFuture<SubAgentTaskResult> future) {
        String safeResponse = response == null ? "" : response;
        conversationStore.appendAgentMessage(
                state.childConversationId, safeResponse, state.childAgentId);
        conversationStore.markCompleted(state.childConversationId);
        synchronized (state) {
            state.status = SubAgentTaskStatus.COMPLETED;
            state.lastMessage = message;
            state.streamFinalResponse = safeResponse;
            state.lastResponse = safeResponse;
            state.lastError = null;
        }
        future.complete(state.snapshot());
    }

    private void onFailure(
            TaskState state,
            String message,
            Exception failure,
            CompletableFuture<SubAgentTaskResult> future) {
        String error =
                failure.getMessage() == null || failure.getMessage().isBlank()
                        ? failure.getClass().getSimpleName()
                        : failure.getMessage();
        LOG.warnf(
                failure,
                "Sub-agent conversation %s failed for parent conversation %s",
                state.childConversationId,
                state.parentConversationId);
        conversationStore.appendAgentMessage(
                state.childConversationId, "Sub-agent task failed: " + error, state.childAgentId);
        conversationStore.markCompleted(state.childConversationId);
        synchronized (state) {
            state.status = SubAgentTaskStatus.FAILED;
            state.lastMessage = message;
            state.lastResponse = null;
            state.lastError = error;
        }
        future.complete(state.snapshot());
    }

    private TaskState requireTask(String parentConversationId, String childConversationId) {
        TaskState state = tasks.get(childConversationId);
        if (state == null) {
            throw new IllegalArgumentException(
                    "Unknown sub-agent conversation " + childConversationId);
        }
        if (!state.parentConversationId.equals(parentConversationId)) {
            throw new IllegalArgumentException(
                    "Sub-agent conversation "
                            + childConversationId
                            + " does not belong to parent conversation "
                            + parentConversationId);
        }
        return state;
    }

    private void registerPendingJoinTask(String parentConversationId, String childConversationId) {
        pendingJoinTasks
                .computeIfAbsent(parentConversationId, ignored -> ConcurrentHashMap.newKeySet())
                .add(childConversationId);
    }

    private static final class TaskState {
        private final String parentConversationId;
        private final String childConversationId;
        private volatile String childAgentId;
        private volatile SubAgentTaskStatus status;
        private volatile String lastMessage;
        private final StringBuilder streamedResponseSoFar = new StringBuilder();
        private volatile String streamFinalResponse;
        private volatile String lastResponse;
        private volatile String lastError;
        private volatile CompletableFuture<SubAgentTaskResult> currentRun;
        private final SubAgentTaskInvoker taskInvoker;

        private TaskState(
                String parentConversationId,
                String childConversationId,
                String childAgentId,
                SubAgentTaskInvoker taskInvoker) {
            this.parentConversationId = parentConversationId;
            this.childConversationId = childConversationId;
            this.childAgentId = childAgentId;
            this.taskInvoker = taskInvoker;
        }

        private synchronized void markRunning(String message) {
            this.status = SubAgentTaskStatus.RUNNING;
            this.lastMessage = message;
            this.streamedResponseSoFar.setLength(0);
            this.streamFinalResponse = null;
            this.lastResponse = null;
            this.lastError = null;
        }

        private SubAgentTaskResult snapshot() {
            return new SubAgentTaskResult(
                    parentConversationId,
                    childConversationId,
                    childAgentId,
                    status,
                    lastMessage,
                    streamedResponseSoFar.toString(),
                    lastResponse,
                    lastError);
        }

        private SubAgentTaskResult awaitCurrentRun() {
            CompletableFuture<SubAgentTaskResult> future = currentRun;
            if (future == null) {
                return snapshot();
            }
            return future.join();
        }
    }
}
