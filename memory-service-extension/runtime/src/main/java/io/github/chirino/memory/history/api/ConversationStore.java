package io.github.chirino.memory.history.api;

public interface ConversationStore {

    void appendUserMessage(String conversationId, String content);

    void appendAgentMessage(String conversationId, String content, String bearerToken);

    default void appendAgentMessage(String conversationId, String content) {
        appendAgentMessage(conversationId, content, null);
    }

    default void appendPartialAgentMessage(String conversationId, String delta) {}

    default void markCompleted(String conversationId) {}
}
