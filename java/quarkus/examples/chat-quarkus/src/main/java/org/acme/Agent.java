package org.acme;

import dev.langchain4j.data.message.Content;
import dev.langchain4j.service.MemoryId;
import dev.langchain4j.service.SystemMessage;
import dev.langchain4j.service.UserMessage;
import io.quarkiverse.langchain4j.RegisterAiService;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;

@ApplicationScoped
@RegisterAiService(
        tools = {
            ImageGenerationTool.class,
            FactFindingSubAgentTool.class,
            FeedbackSubAgentTool.class
        })
public interface Agent {

    @SystemMessage(
            """
            You are the main assistant for this conversation.
            Delegate focused work to sub-agent tools when decomposition helps.
            Prefer parallel delegation when fact-finding tasks can proceed independently.
            The runtime will provide joined sub-agent results back to you before the user-visible
            response is finalized, so incorporate those results instead of answering early.
            """)
    Multi<ChatEvent> chat(
            @MemoryId String memoryId,
            @UserMessage String userMessage,
            @UserMessage List<Content> attachments);
}
