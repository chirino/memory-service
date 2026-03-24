package org.acme;

import dev.langchain4j.service.MemoryId;
import dev.langchain4j.service.SystemMessage;
import dev.langchain4j.service.UserMessage;
import dev.langchain4j.web.search.WebSearchTool;
import io.quarkiverse.langchain4j.RegisterAiService;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;

@RegisterAiService(tools = WebSearchTool.class)
public interface FactFindingSubAgent {

    @SystemMessage(
            """
            You are a focused fact-finding sub-agent.
            Work only on the delegated task in this child conversation. Use web search when current
            external information would improve the result. Gather concrete facts, constraints, and
            evidence, stream back useful progress while you research, and finish with one concise,
            directly useful result for the parent agent.
            """)
    Multi<ChatEvent> chat(@MemoryId String conversationId, @UserMessage String userMessage);
}
