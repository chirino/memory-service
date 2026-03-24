package io.github.chirino.memory.subagent.runtime;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import io.github.chirino.memory.client.model.Conversation;
import io.github.chirino.memory.history.runtime.ConversationStore;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class SubAgentTaskManagerTest {

    private static final String PARENT_ID = "00000000-0000-0000-0000-000000000001";

    @Test
    void requiresModeWhenContinuingExistingConversation() {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager manager = manager(store);

        SubAgentTaskResult created =
                manager.messageTask(
                        PARENT_ID,
                        null,
                        "first task",
                        null,
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        request -> SubAgentTaskExecution.immediate("done"));

        assertThatThrownBy(
                        () ->
                                manager.messageTask(
                                        PARENT_ID,
                                        created.childConversationId(),
                                        "follow up",
                                        null,
                                        "SubAgent",
                                        null,
                                        "bob",
                                        "token-1",
                                        request -> SubAgentTaskExecution.immediate("done")))
                .isInstanceOf(IllegalArgumentException.class)
                .hasMessageContaining("mode is required");
    }

    @Test
    void queueReplacesPendingFollowUp() throws Exception {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager manager = manager(store);
        GateStreamingInvoker invoker = new GateStreamingInvoker();

        SubAgentTaskResult created =
                manager.messageTask(
                        PARENT_ID,
                        null,
                        "first task",
                        null,
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        invoker);

        String childId = created.childConversationId();
        SubAgentTaskResult queued1 =
                manager.messageTask(
                        PARENT_ID,
                        childId,
                        "follow up one",
                        "queue",
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        invoker);
        SubAgentTaskResult queued2 =
                manager.messageTask(
                        PARENT_ID,
                        childId,
                        "follow up two",
                        "queue",
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        invoker);

        assertThat(queued1.queuedMessage()).isEqualTo("follow up one");
        assertThat(queued2.queuedMessage()).isEqualTo("follow up two");

        invoker.complete("child response");
        waitUntil(() -> store.userMessages.size() >= 2);

        assertThat(store.userMessages)
                .extracting(message -> message.content)
                .containsExactly("first task", "follow up two");
    }

    @Test
    void interruptStopsCurrentRunAndStartsQueuedReplacement() throws Exception {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager manager = manager(store);
        GateStreamingInvoker invoker = new GateStreamingInvoker();

        SubAgentTaskResult created =
                manager.messageTask(
                        PARENT_ID,
                        null,
                        "first task",
                        null,
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        invoker);
        invoker.awaitStarted();

        manager.messageTask(
                PARENT_ID,
                created.childConversationId(),
                "replacement task",
                "interrupt",
                "SubAgent",
                null,
                "bob",
                "token-1",
                invoker);

        waitUntil(() -> store.userMessages.size() >= 2);

        assertThat(store.userMessages)
                .extracting(message -> message.content)
                .containsExactly("first task", "replacement task");
        assertThat(manager.getStatus(PARENT_ID, created.childConversationId()).status())
                .isEqualTo(SubAgentTaskStatus.RUNNING);
        assertThat(manager.getStatus(PARENT_ID, created.childConversationId()).lastMessage())
                .isEqualTo("replacement task");
    }

    @Test
    void stopClearsQueuedMessage() {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager manager = manager(store);
        GateStreamingInvoker invoker = new GateStreamingInvoker();

        SubAgentTaskResult created =
                manager.messageTask(
                        PARENT_ID,
                        null,
                        "first task",
                        null,
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        invoker);

        manager.messageTask(
                PARENT_ID,
                created.childConversationId(),
                "queued task",
                "queue",
                "SubAgent",
                null,
                "bob",
                "token-1",
                invoker);

        SubAgentTaskResult stopped = manager.stopTask(PARENT_ID, created.childConversationId());

        assertThat(stopped.status()).isEqualTo(SubAgentTaskStatus.STOPPED);
        assertThat(stopped.queuedMessage()).isNull();
    }

    @Test
    void requiresBearerToken() {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager manager = manager(store);

        assertThatThrownBy(
                        () ->
                                manager.messageTask(
                                        PARENT_ID,
                                        null,
                                        "first task",
                                        null,
                                        "SubAgent",
                                        null,
                                        "bob",
                                        null,
                                        request -> SubAgentTaskExecution.immediate("done")))
                .isInstanceOf(IllegalStateException.class)
                .hasMessageContaining("Missing bearer token");
    }

    @Test
    void waitForTasksReturnsAggregateResults() throws Exception {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager manager = manager(store);
        GateStreamingInvoker invokerA = new GateStreamingInvoker();
        GateStreamingInvoker invokerB = new GateStreamingInvoker();

        SubAgentTaskResult first =
                manager.messageTask(
                        PARENT_ID,
                        null,
                        "task one",
                        null,
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        invokerA);
        SubAgentTaskResult second =
                manager.messageTask(
                        PARENT_ID,
                        null,
                        "task two",
                        null,
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        invokerB);

        invokerA.awaitStarted();
        invokerB.awaitStarted();
        invokerA.complete("done one");

        SubAgentWaitResult waiting = manager.waitForTasks(PARENT_ID, List.of(), 0);
        assertThat(waiting.parentConversationId()).isEqualTo(PARENT_ID);
        assertThat(waiting.tasks()).hasSize(2);
        assertThat(waiting.allCompleted()).isFalse();
        assertThat(waiting.tasks())
                .extracting(SubAgentTaskResult::childConversationId)
                .contains(first.childConversationId(), second.childConversationId());

        invokerB.complete("done two");
        SubAgentWaitResult completed = manager.waitForTasks(PARENT_ID, List.of(), 1);
        assertThat(completed.allCompleted()).isTrue();
        assertThat(completed.tasks())
                .extracting(SubAgentTaskResult::status)
                .containsOnly(SubAgentTaskStatus.COMPLETED);
    }

    @Test
    void supportsAgentOverridePerStartTaskCall() throws Exception {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager manager = manager(store);
        CapturingInvoker invoker = new CapturingInvoker();

        manager.messageTask(
                PARENT_ID,
                null,
                "first task",
                null,
                "ReviewerAgent",
                null,
                "bob",
                "token-1",
                invoker);

        waitUntil(() -> invoker.lastRequest != null);
        assertThat(invoker.lastRequest.childAgentId()).isEqualTo("ReviewerAgent");
    }

    @Test
    void reusesCompletedChildConversationFromPersistedLineage() throws Exception {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager firstManager = manager(store);

        SubAgentTaskResult created =
                firstManager.messageTask(
                        PARENT_ID,
                        null,
                        "first task",
                        null,
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        request -> SubAgentTaskExecution.immediate("done"));

        SubAgentTaskManager secondManager = manager(store);
        CapturingInvoker invoker = new CapturingInvoker();

        SubAgentTaskResult continued =
                secondManager.messageTask(
                        PARENT_ID,
                        created.childConversationId(),
                        "follow up",
                        "queue",
                        "SubAgent",
                        null,
                        "bob",
                        "token-1",
                        invoker);

        waitUntil(() -> invoker.lastRequest != null);
        assertThat(continued.childConversationId()).isEqualTo(created.childConversationId());
        assertThat(invoker.lastRequest.childConversationId())
                .isEqualTo(created.childConversationId());
        assertThat(store.userMessages)
                .extracting(message -> message.content)
                .containsExactly("first task", "follow up");
    }

    @Test
    void rejectsStartingMoreThanConfiguredConcurrentRunningTasks() throws Exception {
        TestConversationStore store = new TestConversationStore();
        SubAgentTaskManager manager = manager(store);
        GateStreamingInvoker invoker = new GateStreamingInvoker();

        manager.messageTask(
                PARENT_ID, null, "first task", null, "SubAgent", 1, "bob", "token-1", invoker);
        invoker.awaitStarted();

        assertThatThrownBy(
                        () ->
                                manager.messageTask(
                                        PARENT_ID,
                                        null,
                                        "second task",
                                        null,
                                        "SubAgent",
                                        1,
                                        "bob",
                                        "token-1",
                                        invoker))
                .isInstanceOf(IllegalStateException.class)
                .hasMessageContaining("Cannot start more than 1 concurrent RUNNING tasks");
    }

    private static SubAgentTaskManager manager(TestConversationStore store) {
        SubAgentTaskManager manager = new SubAgentTaskManager();
        manager.conversationStore = store;
        return manager;
    }

    private static void waitUntil(CheckedBooleanSupplier condition) throws Exception {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
        while (System.nanoTime() < deadline) {
            if (condition.getAsBoolean()) {
                return;
            }
            Thread.sleep(20);
        }
        throw new AssertionError("Condition was not met before timeout");
    }

    @FunctionalInterface
    private interface CheckedBooleanSupplier {
        boolean getAsBoolean() throws Exception;
    }

    private static final class TestConversationStore extends ConversationStore {
        private final List<UserMessage> userMessages = new ArrayList<>();
        private final List<String> agentMessages = new ArrayList<>();
        private final Map<String, Conversation> conversations = new HashMap<>();

        @Override
        public void appendUserMessage(
                String conversationId,
                String content,
                List<Map<String, Object>> attachments,
                String agentId,
                String forkedAtConversationId,
                String forkedAtEntryId,
                String startedByConversationId,
                String startedByEntryId) {
            userMessages.add(new UserMessage(conversationId, content, startedByConversationId));
            conversations.computeIfAbsent(
                    conversationId,
                    ignored ->
                            new Conversation()
                                    .id(UUID.fromString(conversationId))
                                    .agentId(agentId)
                                    .startedByConversationId(
                                            startedByConversationId == null
                                                    ? null
                                                    : UUID.fromString(startedByConversationId)));
        }

        @Override
        public void appendAgentMessage(String conversationId, String content, String agentId) {
            agentMessages.add(content);
        }

        @Override
        public Multi<ChatEvent> appendAgentEvents(
                String conversationId, Multi<ChatEvent> eventMulti, String agentId) {
            return eventMulti;
        }

        @Override
        public void markCompleted(String conversationId) {}

        @Override
        public Conversation getConversation(String conversationId, String bearerToken) {
            return conversations.get(conversationId);
        }
    }

    private record UserMessage(
            String conversationId, String content, String startedByConversationId) {}

    private static final class GateStreamingInvoker implements SubAgentTaskInvoker {
        private volatile CompletableFuture<String> currentResult = new CompletableFuture<>();
        private volatile CountDownLatch started = new CountDownLatch(1);

        @Override
        public SubAgentTaskExecution handle(SubAgentTaskRequest request) {
            started.countDown();
            CompletableFuture<String> result = currentResult;
            return SubAgentTaskExecution.streaming(
                    Multi.createFrom()
                            .emitter(
                                    emitter -> {
                                        result.whenComplete(
                                                (value, failure) -> {
                                                    if (failure != null) {
                                                        emitter.fail(failure);
                                                        return;
                                                    }
                                                    emitter.complete();
                                                });
                                    }));
        }

        void complete(String response) throws Exception {
            awaitStarted();
            CompletableFuture<String> previous = currentResult;
            currentResult = new CompletableFuture<>();
            started = new CountDownLatch(1);
            previous.complete(response);
        }

        void awaitStarted() throws Exception {
            assertThat(started.await(5, TimeUnit.SECONDS)).isTrue();
        }
    }

    private static final class CapturingInvoker implements SubAgentTaskInvoker {
        private volatile SubAgentTaskRequest lastRequest;

        @Override
        public SubAgentTaskExecution handle(SubAgentTaskRequest request) {
            lastRequest = request;
            return SubAgentTaskExecution.immediate("done");
        }
    }
}
