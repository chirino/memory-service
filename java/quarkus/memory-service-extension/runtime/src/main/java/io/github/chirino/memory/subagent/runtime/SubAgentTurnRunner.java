package io.github.chirino.memory.subagent.runtime;

import io.github.chirino.memory.history.runtime.Attachments;
import io.github.chirino.memory.history.runtime.ConversationStore;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.List;
import java.util.function.BiFunction;
import java.util.function.Function;

@ApplicationScoped
public class SubAgentTurnRunner {

    @Inject SubAgentTaskManager taskManager;
    @Inject ConversationStore conversationStore;

    public String runStringTurn(
            String conversationId, String userMessage, Function<String, String> invoker) {
        String currentPrompt = userMessage;
        for (int round = 0; round < taskManager.maxJoinRounds(); round++) {
            String candidate = invoker.apply(currentPrompt);
            List<SubAgentTaskResult> joined = taskManager.awaitPendingJoinTasks(conversationId);
            if (joined.isEmpty()) {
                return candidate;
            }
            currentPrompt = buildJoinedResultsPrompt(joined);
            conversationStore.appendUserMessage(conversationId, currentPrompt);
        }
        throw new IllegalStateException(
                "Exceeded max joined sub-agent rounds for " + conversationId);
    }

    public Multi<ChatEvent> runEventTurn(
            String conversationId,
            String userMessage,
            Attachments attachments,
            BiFunction<String, Attachments, Multi<ChatEvent>> invoker) {
        String currentPrompt = userMessage;
        Attachments currentAttachments = attachments;
        for (int round = 0; round < taskManager.maxJoinRounds(); round++) {
            List<ChatEvent> candidate =
                    invoker.apply(currentPrompt, currentAttachments)
                            .collect()
                            .asList()
                            .await()
                            .indefinitely();
            List<SubAgentTaskResult> joined = taskManager.awaitPendingJoinTasks(conversationId);
            if (joined.isEmpty()) {
                return Multi.createFrom().iterable(candidate);
            }
            currentPrompt = buildJoinedResultsPrompt(joined);
            conversationStore.appendUserMessage(conversationId, currentPrompt);
            currentAttachments = Attachments.empty();
        }
        throw new IllegalStateException(
                "Exceeded max joined sub-agent rounds for " + conversationId);
    }

    private static String buildJoinedResultsPrompt(List<SubAgentTaskResult> joined) {
        StringBuilder builder =
                new StringBuilder(
                        "The following joined sub-agent tasks have completed. Incorporate these"
                                + " results before responding to the user.\n");
        for (SubAgentTaskResult result : joined) {
            builder.append("\nChild conversation: ")
                    .append(result.childConversationId())
                    .append('\n');
            if (result.childAgentId() != null && !result.childAgentId().isBlank()) {
                builder.append("Child agent: ").append(result.childAgentId()).append('\n');
            }
            builder.append("Status: ").append(result.status()).append('\n');
            if (result.lastResponse() != null && !result.lastResponse().isBlank()) {
                builder.append("Result:\n").append(result.lastResponse()).append('\n');
            }
            if (result.lastError() != null && !result.lastError().isBlank()) {
                builder.append("Error:\n").append(result.lastError()).append('\n');
            }
        }
        return builder.toString().trim();
    }
}
