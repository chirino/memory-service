package org.acme;

import dev.langchain4j.agent.tool.P;
import dev.langchain4j.agent.tool.Tool;
import dev.langchain4j.agent.tool.ToolMemoryId;
import io.github.chirino.memory.subagent.runtime.SubAgentTaskRequest;
import io.github.chirino.memory.subagent.runtime.SubAgentTaskTool;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

@ApplicationScoped
public class FeedbackSubAgentTool extends SubAgentTaskTool {

    @Inject FeedbackSubAgent subAgent;

    @Override
    @Tool(
            """
            Delegate cross-result evaluation and feedback to a child conversation. Use this after
            fact-finding sub-agents have gathered evidence and you need one agent to compare the
            results across multiple CRMs, identify gaps, and suggest follow-up research questions.
            This tool returns a concise evaluation or targeted follow-up questions after the child
            agent has considered the full set of results. Omit childConversationId to create a new
            feedback child conversation, or provide it to continue an existing one. The tool
            returns a JSON object with the childConversationId.
            """)
    public String messageSubAgent(
            @ToolMemoryId String parentConversationId,
            @P(
                            value =
                                    "Optional child conversation id. Omit it to create a new"
                                            + " feedback child conversation.",
                            required = false)
                    String childConversationId,
            @P("The evaluation task or follow-up message to send to the sub-agent.")
                    String message) {
        return super.messageSubAgent(parentConversationId, childConversationId, message);
    }

    @Override
    protected String handleTask(SubAgentTaskRequest request) {
        return subAgent.chat(request.childConversationId(), request.message());
    }
}
