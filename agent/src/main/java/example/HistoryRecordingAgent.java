package example;

import io.github.chirino.memory.conversation.annotations.ConversationAware;
import io.github.chirino.memory.conversation.annotations.ConversationId;
import io.github.chirino.memory.conversation.annotations.UserMessage;
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

    @ConversationAware
    public Multi<String> chat(
            @ConversationId String conversationId, @UserMessage String userMessage) {
        return agent.chat(conversationId, userMessage);
    }
}
