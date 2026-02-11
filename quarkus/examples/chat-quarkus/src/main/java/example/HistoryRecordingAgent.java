package example;

import io.github.chirino.memory.history.annotations.ConversationId;
import io.github.chirino.memory.history.annotations.ForkedAtConversationId;
import io.github.chirino.memory.history.annotations.ForkedAtEntryId;
import io.github.chirino.memory.history.annotations.RecordConversation;
import io.github.chirino.memory.history.annotations.UserMessage;
import io.github.chirino.memory.history.runtime.Attachments;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
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
    public Multi<ChatEvent> chat(
            @ConversationId String conversationId,
            @UserMessage String userMessage,
            Attachments attachments,
            @ForkedAtConversationId String forkedAtConversationId,
            @ForkedAtEntryId String forkedAtEntryId) {
        return agent.chat(conversationId, userMessage, attachments.contents());
    }
}
