package io.github.chirino.memory.subagent.runtime;

import io.github.chirino.memory.client.model.Conversation;
import io.github.chirino.memory.history.runtime.ConversationStore;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.infrastructure.Infrastructure;
import io.smallrye.mutiny.subscription.Cancellable;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import org.jboss.logging.Logger;

@ApplicationScoped
public class SubAgentTaskManager {

    private static final Logger LOG = Logger.getLogger(SubAgentTaskManager.class);

    @Inject ConversationStore conversationStore;

    private final ConcurrentMap<String, TaskState> tasks = new ConcurrentHashMap<>();
    private final Object taskStartLock = new Object();
    private static final SubAgentTaskInvoker NOOP_INVOKER =
            request -> SubAgentTaskExecution.immediate("");

    public SubAgentTaskResult messageTask(
            String parentConversationId,
            String childConversationId,
            String message,
            String mode,
            String childAgentId,
            Integer maxConcurrency,
            String userId,
            String bearerToken,
            SubAgentTaskInvoker invoker) {
        requireBearerToken(bearerToken);
        SubAgentMessageMode parsedMode = SubAgentMessageMode.parse(mode);
        boolean newChildConversation = childConversationId == null || childConversationId.isBlank();
        if (newChildConversation) {
            childConversationId = UUID.randomUUID().toString();
            TaskState state =
                    new TaskState(
                            parentConversationId,
                            childConversationId,
                            childAgentId,
                            maxConcurrency,
                            invoker);
            synchronized (taskStartLock) {
                ensureCanStart(parentConversationId, childConversationId, maxConcurrency);
                tasks.put(childConversationId, state);
                state.markRunning(message, userId, bearerToken);
            }
            submitStartedRun(state, message, userId, bearerToken, true);
            return state.snapshot();
        }

        TaskState state =
                requireTask(
                        parentConversationId,
                        childConversationId,
                        bearerToken,
                        childAgentId,
                        maxConcurrency,
                        invoker);
        synchronized (state) {
            if (parsedMode == null) {
                throw new IllegalArgumentException(
                        "mode is required when taskId is provided. Expected one of:"
                                + " queue, interrupt");
            }
            if (childAgentId != null && !childAgentId.isBlank()) {
                state.childAgentId = childAgentId;
            }
            if (maxConcurrency != null) {
                state.maxConcurrency = maxConcurrency;
            }
            if (state.status == SubAgentTaskStatus.RUNNING) {
                if (parsedMode == SubAgentMessageMode.QUEUE) {
                    state.queueMessage(message, userId, bearerToken);
                    return state.snapshot();
                }
                state.queueMessage(message, userId, bearerToken);
                state.requestStop("Interrupted by caller");
                interruptCurrentRun(state, state.runId);
                return state.snapshot();
            }
        }

        synchronized (taskStartLock) {
            ensureCanStart(
                    state.parentConversationId, state.childConversationId, state.maxConcurrency);
            state.markRunning(message, userId, bearerToken);
        }
        submitStartedRun(state, message, userId, bearerToken, false);
        return state.snapshot();
    }

    public SubAgentTaskResult getStatus(String parentConversationId, String childConversationId) {
        return getStatus(parentConversationId, childConversationId, null);
    }

    public SubAgentTaskResult getStatus(
            String parentConversationId, String childConversationId, String bearerToken) {
        return requireTask(parentConversationId, childConversationId, bearerToken, null, null, null)
                .snapshot();
    }

    public SubAgentTaskResult waitForTask(
            String parentConversationId, String childConversationId, int maxWaitSeconds) {
        return waitForTask(parentConversationId, childConversationId, maxWaitSeconds, null);
    }

    public SubAgentTaskResult waitForTask(
            String parentConversationId,
            String childConversationId,
            int maxWaitSeconds,
            String bearerToken) {
        return requireTask(parentConversationId, childConversationId, bearerToken, null, null, null)
                .awaitCurrentRun(maxWaitSeconds);
    }

    public SubAgentWaitResult waitForTasks(
            String parentConversationId, List<String> childConversationIds, int maxWaitSeconds) {
        return waitForTasks(parentConversationId, childConversationIds, maxWaitSeconds, null);
    }

    public SubAgentWaitResult waitForTasks(
            String parentConversationId,
            List<String> childConversationIds,
            int maxWaitSeconds,
            String bearerToken) {
        List<TaskState> matchingStates;
        if (childConversationIds == null || childConversationIds.isEmpty()) {
            matchingStates =
                    tasks.values().stream()
                            .filter(
                                    state ->
                                            state.parentConversationId.equals(parentConversationId))
                            .toList();
        } else {
            matchingStates =
                    childConversationIds.stream()
                            .map(
                                    childConversationId ->
                                            requireTask(
                                                    parentConversationId,
                                                    childConversationId,
                                                    bearerToken,
                                                    null,
                                                    null,
                                                    null))
                            .toList();
        }
        if (matchingStates.isEmpty()) {
            return new SubAgentWaitResult(parentConversationId, true, List.of());
        }

        long timeoutNanos = maxWaitSeconds <= 0 ? 0 : TimeUnit.SECONDS.toNanos(maxWaitSeconds);
        long deadline = System.nanoTime() + timeoutNanos;
        List<SubAgentTaskResult> results = new java.util.ArrayList<>(matchingStates.size());
        boolean allCompleted = true;
        for (TaskState state : matchingStates) {
            int remainingSeconds = 0;
            if (timeoutNanos > 0) {
                long remainingNanos = deadline - System.nanoTime();
                if (remainingNanos > 0) {
                    remainingSeconds =
                            (int) Math.max(1, TimeUnit.NANOSECONDS.toSeconds(remainingNanos));
                }
            }
            SubAgentTaskResult result = state.awaitCurrentRun(remainingSeconds);
            results.add(result);
            if (result.status() == SubAgentTaskStatus.RUNNING) {
                allCompleted = false;
            }
        }
        return new SubAgentWaitResult(parentConversationId, allCompleted, results);
    }

    public SubAgentTaskResult stopTask(String parentConversationId, String childConversationId) {
        return stopTask(parentConversationId, childConversationId, null);
    }

    public SubAgentTaskResult stopTask(
            String parentConversationId, String childConversationId, String bearerToken) {
        TaskState state =
                requireTask(
                        parentConversationId, childConversationId, bearerToken, null, null, null);
        synchronized (state) {
            state.clearQueued();
            if (!state.requestStop("Stopped by caller")) {
                return state.snapshot();
            }
            state.status = SubAgentTaskStatus.STOPPED;
            interruptCurrentRun(state, state.runId);
        }
        return state.snapshot();
    }

    private void submitStartedRun(
            TaskState state,
            String message,
            String userId,
            String bearerToken,
            boolean newChildConversation) {
        conversationStore.appendUserMessage(
                state.childConversationId,
                message,
                List.of(),
                state.childAgentId,
                null,
                null,
                newChildConversation ? state.parentConversationId : null,
                null);
        submit(state, message, userId, bearerToken);
    }

    private void submit(TaskState state, String message, String userId, String bearerToken) {
        CompletableFuture<SubAgentTaskResult> future = new CompletableFuture<>();
        state.currentRun = future;
        long runId = state.runId;
        String parentConversationId = state.parentConversationId;
        String childConversationId = state.childConversationId;
        String childAgentId = state.childAgentId;
        SubAgentTaskInvoker taskInvoker = state.taskInvoker;
        LOG.infof(
                "Starting sub-agent task run %d for child conversation %s (parent=%s, message=%s)",
                runId, childConversationId, parentConversationId, abbreviate(message));
        SubAgentExecutionContext.bindConversation(childConversationId, userId, bearerToken);
        Infrastructure.getDefaultExecutor()
                .execute(
                        () -> {
                            try {
                                SubAgentExecutionContext.with(
                                        userId,
                                        bearerToken,
                                        () -> {
                                            try {
                                                if (state.shouldStopRun(runId)) {
                                                    throw new StoppedException();
                                                }
                                                SubAgentTaskExecution execution =
                                                        taskInvoker.handle(
                                                                new SubAgentTaskRequest(
                                                                        parentConversationId,
                                                                        childConversationId,
                                                                        message,
                                                                        childAgentId));
                                                if (state.shouldStopRun(runId)) {
                                                    throw new StoppedException();
                                                }
                                                ExecutionOutcome outcome =
                                                        consumeExecution(
                                                                state,
                                                                execution,
                                                                runId,
                                                                childAgentId);
                                                onSuccess(
                                                        state,
                                                        runId,
                                                        message,
                                                        outcome.response(),
                                                        outcome.historyRecorded(),
                                                        childAgentId,
                                                        future);
                                            } catch (Exception e) {
                                                onFailure(
                                                        state,
                                                        runId,
                                                        message,
                                                        e,
                                                        childAgentId,
                                                        future);
                                            }
                                            return null;
                                        });
                            } catch (Exception e) {
                                LOG.warnf(
                                        e,
                                        "Failed to run sub-agent conversation %s",
                                        childConversationId);
                                onFailure(state, runId, message, e, childAgentId, future);
                            }
                        });
    }

    private ExecutionOutcome consumeExecution(
            TaskState state, SubAgentTaskExecution execution, long runId, String childAgentId) {
        if (execution instanceof SubAgentTaskExecution.Immediate immediate) {
            return new ExecutionOutcome(immediate.response(), false);
        }
        if (execution instanceof SubAgentTaskExecution.Streaming streaming) {
            return new ExecutionOutcome(
                    consumeStream(state, streaming.events(), runId, childAgentId), true);
        }
        throw new IllegalStateException("Unknown sub-agent execution type");
    }

    private String consumeStream(
            TaskState state,
            io.smallrye.mutiny.Multi<ChatEvent> events,
            long runId,
            String childAgentId) {
        if (events == null) {
            return "";
        }
        Multi<ChatEvent> recordedEvents =
                conversationStore.appendAgentEvents(
                        state.childConversationId, events, childAgentId);
        CompletableFuture<String> runningExecution = new CompletableFuture<>();
        state.runningExecution = runningExecution;
        Cancellable subscription =
                recordedEvents
                        .subscribe()
                        .with(
                                event -> onStreamEvent(state, event),
                                runningExecution::completeExceptionally,
                                () -> runningExecution.complete("completed"));
        state.activeStreamSubscription = subscription;
        if (state.shouldStopRun(runId)) {
            interruptCurrentRun(state, runId);
        }
        try {
            runningExecution.join();
        } catch (Exception e) {
            Throwable cause = e.getCause();
            if (cause instanceof StoppedException stopped) {
                throw stopped;
            }
            throw e;
        } finally {
            state.activeStreamSubscription = null;
            state.runningExecution = null;
        }
        String finalResponse = state.streamFinalResponse;
        return finalResponse == null || finalResponse.isBlank()
                ? state.streamedResponseSoFar.toString()
                : finalResponse;
    }

    private void onStreamEvent(TaskState state, ChatEvent event) {
        synchronized (state) {
            state.updatedAt = Instant.now();
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
            long runId,
            String message,
            String response,
            boolean historyRecorded,
            String childAgentId,
            CompletableFuture<SubAgentTaskResult> future) {
        String safeResponse = response == null ? "" : response;
        if (!historyRecorded) {
            conversationStore.appendAgentMessage(
                    state.childConversationId, safeResponse, childAgentId);
            conversationStore.markCompleted(state.childConversationId);
        }
        synchronized (state) {
            state.status = SubAgentTaskStatus.COMPLETED;
            state.lastMessage = message;
            state.streamFinalResponse = safeResponse;
            state.lastResponse = safeResponse;
            state.lastError = null;
            state.updatedAt = Instant.now();
        }
        LOG.infof(
                "Completed sub-agent task run %d for child conversation %s in %dms"
                        + " (responseLength=%d, historyRecorded=%s)",
                runId,
                state.childConversationId,
                elapsedMillis(state.startedAt, state.updatedAt),
                safeResponse.length(),
                historyRecorded);
        future.complete(state.snapshot());
        continueWithQueuedOrClear(state);
    }

    private void onFailure(
            TaskState state,
            long runId,
            String message,
            Exception failure,
            String childAgentId,
            CompletableFuture<SubAgentTaskResult> future) {
        if (failure instanceof StoppedException || failure.getCause() instanceof StoppedException) {
            conversationStore.appendAgentMessage(
                    state.childConversationId, "Sub-agent task stopped.", childAgentId);
            conversationStore.markCompleted(state.childConversationId);
            synchronized (state) {
                state.status = SubAgentTaskStatus.STOPPED;
                state.lastMessage = message;
                state.lastResponse =
                        state.streamedResponseSoFar.length() == 0
                                ? null
                                : state.streamedResponseSoFar.toString();
                state.lastError =
                        state.stopReason == null || state.stopReason.isBlank()
                                ? "Stopped by caller"
                                : state.stopReason;
                state.updatedAt = Instant.now();
            }
            LOG.infof(
                    "Stopped sub-agent task run %d for child conversation %s in %dms"
                            + " (reason=%s)",
                    runId,
                    state.childConversationId,
                    elapsedMillis(state.startedAt, state.updatedAt),
                    state.lastError);
            future.complete(state.snapshot());
            continueWithQueuedOrClear(state);
            return;
        }

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
                state.childConversationId, "Sub-agent task failed: " + error, childAgentId);
        conversationStore.markCompleted(state.childConversationId);
        synchronized (state) {
            state.status = SubAgentTaskStatus.FAILED;
            state.lastMessage = message;
            state.lastResponse = null;
            state.lastError = error;
            state.updatedAt = Instant.now();
        }
        LOG.infof(
                "Failed sub-agent task run %d for child conversation %s in %dms" + " (error=%s)",
                runId,
                state.childConversationId,
                elapsedMillis(state.startedAt, state.updatedAt),
                error);
        future.complete(state.snapshot());
        continueWithQueuedOrClear(state);
    }

    private static long elapsedMillis(Instant startedAt, Instant finishedAt) {
        if (startedAt == null || finishedAt == null || finishedAt.isBefore(startedAt)) {
            return -1;
        }
        return Duration.between(startedAt, finishedAt).toMillis();
    }

    private static String abbreviate(String value) {
        if (value == null) {
            return "";
        }
        return value.length() <= 80 ? value : value.substring(0, 77) + "...";
    }

    private void continueWithQueuedOrClear(TaskState state) {
        QueuedRun queued;
        synchronized (state) {
            queued = state.takeQueued();
        }
        if (queued == null) {
            SubAgentExecutionContext.unbindConversation(
                    state.childConversationId, state.authBearerToken);
            return;
        }
        synchronized (taskStartLock) {
            ensureCanStart(
                    state.parentConversationId, state.childConversationId, state.maxConcurrency);
            state.markRunning(queued.message(), queued.userId(), queued.bearerToken());
        }
        submitStartedRun(state, queued.message(), queued.userId(), queued.bearerToken(), false);
    }

    private void interruptCurrentRun(TaskState state, long runId) {
        Cancellable streamSubscription;
        CompletableFuture<String> execution;
        synchronized (state) {
            if (state.runId != runId) {
                return;
            }
            streamSubscription = state.activeStreamSubscription;
            execution = state.runningExecution;
        }
        if (streamSubscription != null) {
            streamSubscription.cancel();
        }
        if (execution != null) {
            execution.completeExceptionally(new StoppedException());
        }
    }

    private TaskState requireTask(
            String parentConversationId,
            String childConversationId,
            String bearerToken,
            String childAgentId,
            Integer maxConcurrency,
            SubAgentTaskInvoker invoker) {
        TaskState state = tasks.get(childConversationId);
        if (state == null) {
            state =
                    rehydrateTask(
                            parentConversationId,
                            childConversationId,
                            bearerToken,
                            childAgentId,
                            maxConcurrency,
                            invoker);
        }
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
        if (invoker != null) {
            state.taskInvoker = invoker;
        }
        return state;
    }

    private TaskState rehydrateTask(
            String parentConversationId,
            String childConversationId,
            String bearerToken,
            String childAgentId,
            Integer maxConcurrency,
            SubAgentTaskInvoker invoker) {
        if (bearerToken == null || bearerToken.isBlank()) {
            return null;
        }
        Conversation conversation =
                conversationStore.getConversation(childConversationId, bearerToken);
        if (conversation == null || conversation.getStartedByConversationId() == null) {
            return null;
        }
        if (!parentConversationId.equals(conversation.getStartedByConversationId().toString())) {
            return null;
        }
        return tasks.computeIfAbsent(
                childConversationId,
                ignored -> {
                    TaskState state =
                            new TaskState(
                                    parentConversationId,
                                    childConversationId,
                                    childAgentId != null ? childAgentId : conversation.getAgentId(),
                                    maxConcurrency,
                                    invoker != null ? invoker : NOOP_INVOKER);
                    state.markRecoveredCompleted();
                    return state;
                });
    }

    private static void requireBearerToken(String bearerToken) {
        if (bearerToken == null || bearerToken.isBlank()) {
            throw new IllegalStateException("Missing bearer token for child task execution");
        }
    }

    private void ensureCanStart(
            String parentConversationId, String childConversationId, Integer maxConcurrency) {
        if (maxConcurrency == null) {
            return;
        }
        int running = runningTaskCount(parentConversationId, childConversationId);
        if (running >= maxConcurrency) {
            throw new IllegalStateException(
                    "Cannot start more than "
                            + maxConcurrency
                            + " concurrent RUNNING tasks for parent conversation "
                            + parentConversationId);
        }
    }

    private int runningTaskCount(String parentConversationId, String excludedChildConversationId) {
        int running = 0;
        for (TaskState state : tasks.values()) {
            synchronized (state) {
                if (!state.parentConversationId.equals(parentConversationId)) {
                    continue;
                }
                if (state.childConversationId.equals(excludedChildConversationId)) {
                    continue;
                }
                if (state.status == SubAgentTaskStatus.RUNNING) {
                    running++;
                }
            }
        }
        return running;
    }

    private record QueuedRun(String message, String userId, String bearerToken, Instant queuedAt) {}

    private static final class TaskState {
        private final String parentConversationId;
        private final String childConversationId;
        private volatile String childAgentId;
        private volatile Integer maxConcurrency;
        private volatile SubAgentTaskStatus status;
        private volatile String lastMessage;
        private final StringBuilder streamedResponseSoFar = new StringBuilder();
        private volatile String streamFinalResponse;
        private volatile String lastResponse;
        private volatile String lastError;
        private volatile CompletableFuture<SubAgentTaskResult> currentRun;
        private volatile CompletableFuture<String> runningExecution;
        private volatile Cancellable activeStreamSubscription;
        private volatile String stopReason;
        private volatile String queuedMessage;
        private volatile String queuedUserId;
        private volatile String queuedBearerToken;
        private volatile Instant queuedAt;
        private volatile long runId;
        private volatile Instant startedAt;
        private volatile Instant updatedAt;
        private volatile String authUserId;
        private volatile String authBearerToken;
        private volatile SubAgentTaskInvoker taskInvoker;
        private volatile long stopRequestedRunId;

        private TaskState(
                String parentConversationId,
                String childConversationId,
                String childAgentId,
                Integer maxConcurrency,
                SubAgentTaskInvoker taskInvoker) {
            this.parentConversationId = parentConversationId;
            this.childConversationId = childConversationId;
            this.childAgentId = childAgentId;
            this.maxConcurrency = maxConcurrency;
            this.taskInvoker = taskInvoker;
            this.status = SubAgentTaskStatus.STOPPED;
            this.updatedAt = Instant.now();
        }

        private synchronized void markRunning(String message, String userId, String bearerToken) {
            Instant now = Instant.now();
            this.status = SubAgentTaskStatus.RUNNING;
            this.lastMessage = message;
            this.streamedResponseSoFar.setLength(0);
            this.streamFinalResponse = null;
            this.lastResponse = null;
            this.lastError = null;
            this.stopReason = null;
            this.runId++;
            this.startedAt = now;
            this.updatedAt = now;
            this.authUserId = userId;
            this.authBearerToken = bearerToken;
            this.stopRequestedRunId = 0;
        }

        private synchronized void queueMessage(String message, String userId, String bearerToken) {
            this.queuedMessage = message;
            this.queuedUserId = userId;
            this.queuedBearerToken = bearerToken;
            this.queuedAt = Instant.now();
            this.updatedAt = this.queuedAt;
        }

        private synchronized void clearQueued() {
            this.queuedMessage = null;
            this.queuedUserId = null;
            this.queuedBearerToken = null;
            this.queuedAt = null;
            this.updatedAt = Instant.now();
        }

        private synchronized QueuedRun takeQueued() {
            if (queuedMessage == null || queuedMessage.isBlank()) {
                return null;
            }
            QueuedRun queued =
                    new QueuedRun(queuedMessage, queuedUserId, queuedBearerToken, queuedAt);
            clearQueued();
            return queued;
        }

        private synchronized boolean requestStop(String reason) {
            if (status != SubAgentTaskStatus.RUNNING) {
                return false;
            }
            this.stopReason = reason;
            this.updatedAt = Instant.now();
            this.stopRequestedRunId = this.runId;
            return true;
        }

        private boolean shouldStopRun(long runId) {
            return stopRequestedRunId == runId;
        }

        private synchronized void markRecoveredCompleted() {
            Instant now = Instant.now();
            this.status = SubAgentTaskStatus.COMPLETED;
            this.updatedAt = now;
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
                    lastError,
                    queuedMessage,
                    queuedAt,
                    runId,
                    startedAt,
                    updatedAt);
        }

        private SubAgentTaskResult awaitCurrentRun(int maxWaitSeconds) {
            CompletableFuture<SubAgentTaskResult> future = currentRun;
            if (future == null) {
                return snapshot();
            }
            if (maxWaitSeconds <= 0) {
                return future.isDone() ? future.join() : snapshot();
            }
            try {
                return future.get(maxWaitSeconds, TimeUnit.SECONDS);
            } catch (TimeoutException e) {
                LOG.infof(
                        "Timed out waiting %ds for sub-agent task run %d on child conversation %s"
                                + " (status=%s, startedAt=%s, updatedAt=%s)",
                        maxWaitSeconds, runId, childConversationId, status, startedAt, updatedAt);
                return snapshot();
            } catch (InterruptedException e) {
                LOG.infof(
                        "Interrupted while waiting %ds for sub-agent task run %d on child"
                                + " conversation %s (status=%s, startedAt=%s, updatedAt=%s)",
                        maxWaitSeconds, runId, childConversationId, status, startedAt, updatedAt);
                Thread.currentThread().interrupt();
                return snapshot();
            } catch (ExecutionException e) {
                Throwable cause = e.getCause();
                if (cause instanceof RuntimeException runtimeException) {
                    throw runtimeException;
                }
                throw new RuntimeException(cause);
            }
        }
    }

    private static final class StoppedException extends RuntimeException {}

    private record ExecutionOutcome(String response, boolean historyRecorded) {}
}
