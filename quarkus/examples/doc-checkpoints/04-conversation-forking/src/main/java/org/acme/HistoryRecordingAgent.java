package org.acme;

import io.github.chirino.memory.history.annotations.ConversationId;
import io.github.chirino.memory.history.annotations.ForkedAtConversationId;
import io.github.chirino.memory.history.annotations.ForkedAtEntryId;
import io.github.chirino.memory.history.annotations.RecordConversation;
import io.github.chirino.memory.history.annotations.UserMessage;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

@ApplicationScoped
public class HistoryRecordingAgent {

    private final Agent agent;

    @Inject
    public HistoryRecordingAgent(Agent agent) {
        this.agent = agent;
    }

    @RecordConversation
    public String chat(
            @ConversationId String conversationId,
            @UserMessage String userMessage,
            @ForkedAtConversationId String forkedAtConversationId,
            @ForkedAtEntryId String forkedAtEntryId) {
        return agent.chat(conversationId, userMessage);
    }
}
