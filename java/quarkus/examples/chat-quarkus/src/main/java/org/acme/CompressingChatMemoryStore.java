package org.acme;

import dev.langchain4j.data.message.AiMessage;
import dev.langchain4j.data.message.ChatMessage;
import dev.langchain4j.data.message.SystemMessage;
import dev.langchain4j.data.message.ToolExecutionResultMessage;
import dev.langchain4j.data.message.UserMessage;
import dev.langchain4j.store.memory.chat.ChatMemoryStore;
import io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryStore;
import jakarta.annotation.Priority;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Alternative;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.List;
import java.util.stream.Collectors;
import java.util.stream.Stream;
import org.jboss.logging.Logger;

/**
 * Wraps MemoryServiceChatMemoryStore with semantic compression: when message count exceeds
 * the threshold, older messages are summarized and replaced with a SystemMessage summary,
 * preserving context without growing the context window unboundedly.
 */
@ApplicationScoped
@Alternative
@Priority(1)
public class CompressingChatMemoryStore implements ChatMemoryStore {

    private static final Logger LOG = Logger.getLogger(CompressingChatMemoryStore.class);
    private static final int THRESHOLD = 10;
    private static final String SUMMARY_PREFIX = "Summary of earlier conversation:\n";

    @Inject MemoryServiceChatMemoryStore delegate;

    @Inject SummarizingAssistant summarizer;

    @Override
    public List<ChatMessage> getMessages(Object memoryId) {
        return delegate.getMessages(memoryId);
    }

    @Override
    public void updateMessages(Object memoryId, List<ChatMessage> messages) {
        LOG.infof("updateMessages called");
        if (messages.size() > THRESHOLD) {
            messages = compress(memoryId, messages);
        }
        delegate.updateMessages(memoryId, messages);
    }

    @Override
    public void deleteMessages(Object memoryId) {
        delegate.deleteMessages(memoryId);
    }

    private List<ChatMessage> compress(Object memoryId, List<ChatMessage> messages) {
        // Safety: skip compression if the last message is a tool result or system message
        ChatMessage last = messages.get(messages.size() - 1);
        if (last instanceof ToolExecutionResultMessage || last instanceof SystemMessage) {
            return messages;
        }

        // Preserve original system messages (e.g. @SystemMessage from the AI service).
        // Previously-generated summaries (start with SUMMARY_PREFIX) are folded back into
        // the transcript so the new summary replaces them rather than accumulating.
        List<ChatMessage> originalSystemMessages =
                messages.stream()
                        .filter(
                                m ->
                                        m instanceof SystemMessage sm
                                                && !sm.text().startsWith(SUMMARY_PREFIX))
                        .collect(Collectors.toList());

        List<ChatMessage> previousSummaries =
                messages.stream()
                        .filter(
                                m ->
                                        m instanceof SystemMessage sm
                                                && sm.text().startsWith(SUMMARY_PREFIX))
                        .collect(Collectors.toList());

        List<ChatMessage> nonSystem =
                messages.stream()
                        .filter(m -> !(m instanceof SystemMessage))
                        .collect(Collectors.toList());

        // Keep about 25% of the context..
        int keepCount = THRESHOLD / 4;
        List<ChatMessage> toSummarize = nonSystem.subList(0, nonSystem.size() - keepCount);
        List<ChatMessage> toKeep =
                nonSystem.subList(nonSystem.size() - keepCount, nonSystem.size());

        // Include previous summaries at the top of the transcript so the new summary
        // consolidates all prior context into a single SystemMessage.
        String transcript =
                Stream.concat(previousSummaries.stream(), toSummarize.stream())
                        .map(CompressingChatMemoryStore::formatMessage)
                        .collect(Collectors.joining("\n"));

        LOG.infof(
                "Compressing %d messages (+ %d prior summaries) for conversationId=%s",
                toSummarize.size(), previousSummaries.size(), memoryId);
        String summary = summarizer.summarize(transcript);

        List<ChatMessage> compressed = new ArrayList<>(originalSystemMessages);
        compressed.add(SystemMessage.from(SUMMARY_PREFIX + summary));
        compressed.addAll(toKeep);
        return compressed;
    }

    private static String formatMessage(ChatMessage message) {
        if (message instanceof UserMessage um) {
            try {
                return "User: " + um.singleText();
            } catch (Exception e) {
                return "User: " + um;
            }
        } else if (message instanceof AiMessage am) {
            return "Assistant: " + (am.text() != null ? am.text() : "");
        } else if (message instanceof SystemMessage sm) {
            return "System: " + sm.text();
        }
        return message.type() + ": " + message;
    }
}
