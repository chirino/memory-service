package org.acme;

import io.github.chirino.memory.history.annotations.ConversationId;
import io.github.chirino.memory.history.annotations.RecordConversation;
import io.github.chirino.memory.history.annotations.UserMessage;
import io.github.chirino.memory.subagent.runtime.SubAgentTurnRunner;
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
    public String chat(@ConversationId String conversationId, @UserMessage String userMessage) {
        return subAgentTurnRunner.runStringTurn(
                conversationId, userMessage, prompt -> agent.chat(conversationId, prompt));
    }
}
