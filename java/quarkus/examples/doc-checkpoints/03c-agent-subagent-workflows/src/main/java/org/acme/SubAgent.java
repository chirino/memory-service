package org.acme;

import dev.langchain4j.service.MemoryId;
import dev.langchain4j.service.SystemMessage;
import dev.langchain4j.service.UserMessage;
import dev.langchain4j.web.search.WebSearchTool;
import io.quarkiverse.langchain4j.RegisterAiService;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;

@RegisterAiService(tools = WebSearchTool.class)
public interface SubAgent {

    @SystemMessage(
            """
            You are a focused delegated agent.
            Complete the delegated task with concise, concrete results.
            Use web search when current external information would improve the result. Stream back
            useful progress while you work, then return one concise result for the parent agent.
            """)
    Multi<ChatEvent> chat(@MemoryId String conversationId, @UserMessage String userMessage);
}
