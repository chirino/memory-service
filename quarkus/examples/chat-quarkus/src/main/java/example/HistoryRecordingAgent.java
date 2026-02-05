package example;

import io.github.chirino.memory.history.annotations.ConversationId;
import io.github.chirino.memory.history.annotations.RecordConversation;
import io.github.chirino.memory.history.annotations.UserMessage;
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
    public Multi<String> chat(
            @ConversationId String conversationId, @UserMessage String userMessage) {
        return agent.chat(conversationId, userMessage);
    }

    @RecordConversation
    public Multi<ChatEvent> chatDetailed(
            @ConversationId String conversationId, @UserMessage String userMessage) {
        return agent.chatDetailed(conversationId, userMessage);
    }
}
