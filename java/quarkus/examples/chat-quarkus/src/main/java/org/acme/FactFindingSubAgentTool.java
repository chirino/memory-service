package org.acme;

import dev.langchain4j.agent.tool.P;
import dev.langchain4j.agent.tool.Tool;
import dev.langchain4j.agent.tool.ToolMemoryId;
import io.github.chirino.memory.subagent.runtime.StreamingSubAgentTaskTool;
import io.github.chirino.memory.subagent.runtime.SubAgentTaskRequest;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

@ApplicationScoped
public class FactFindingSubAgentTool extends StreamingSubAgentTaskTool {

    @Inject FactFindingSubAgent subAgent;

    @Override
    @Tool(
            """
            Delegate focused fact-finding work to a child conversation. Use this when you need
            concrete facts, constraints, vendor details, requirements, or other evidence gathered
            before you answer. The child agent can use Tavily-backed web search and streams
            progress while it researches before finishing with one concise final result. Omit
            childConversationId to create a new fact-finding child conversation, or provide it to
            continue an existing one. The tool returns a JSON object with the childConversationId.
            """)
    public String messageSubAgent(
            @ToolMemoryId String parentConversationId,
            @P(
                            value =
                                    "Optional child conversation id. Omit it to create a new"
                                            + " fact-finding child conversation.",
                            required = false)
                    String childConversationId,
            @P("The fact-finding task or follow-up message to send to the sub-agent.")
                    String message) {
        return super.messageSubAgent(parentConversationId, childConversationId, message);
    }

    @Override
    protected Multi<ChatEvent> handleTaskStream(SubAgentTaskRequest request) {
        return subAgent.chat(request.childConversationId(), request.message());
    }
}
