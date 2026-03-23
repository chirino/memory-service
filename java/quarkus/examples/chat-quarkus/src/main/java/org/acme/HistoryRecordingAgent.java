package org.acme;

import io.github.chirino.memory.history.annotations.ConversationId;
import io.github.chirino.memory.history.annotations.ForkedAtConversationId;
import io.github.chirino.memory.history.annotations.ForkedAtEntryId;
import io.github.chirino.memory.history.annotations.RecordConversation;
import io.github.chirino.memory.history.annotations.UserMessage;
import io.github.chirino.memory.history.runtime.Attachments;
import io.github.chirino.memory.subagent.runtime.SubAgentTurnRunner;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

@ApplicationScoped
public class HistoryRecordingAgent {

    private final Agent agent;
    private final SubAgentTurnRunner subAgentTurnRunner;

    @Inject
    public HistoryRecordingAgent(Agent agent, SubAgentTurnRunner subAgentTurnRunner) {
        this.agent = agent;
        this.subAgentTurnRunner = subAgentTurnRunner;
    }

    @RecordConversation
    public Multi<ChatEvent> chat(
            @ConversationId String conversationId,
            @UserMessage String userMessage,
            Attachments attachments,
            @ForkedAtConversationId String forkedAtConversationId,
            @ForkedAtEntryId String forkedAtEntryId) {
        return subAgentTurnRunner.runEventTurn(
                conversationId,
                userMessage,
                attachments,
                (prompt, currentAttachments) ->
                        agent.chat(conversationId, prompt, currentAttachments.contents()));
    }
}
