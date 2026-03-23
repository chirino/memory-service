package org.acme;

import dev.langchain4j.service.MemoryId;
import dev.langchain4j.service.SystemMessage;
import io.quarkiverse.langchain4j.RegisterAiService;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
@RegisterAiService(tools = {FactFindingSubAgentTool.class, FeedbackSubAgentTool.class})
public interface Agent {

    @SystemMessage(
            """
            You are the parent agent.
            Delegate focused work to sub-agent tools when decomposition helps.
            Prefer parallel delegation when fact-finding tasks can proceed independently.
            The runtime will provide joined sub-agent results back to you before the final
            user-visible response is sent.
            """)
    String chat(@MemoryId String conversationId, String userMessage);
}
