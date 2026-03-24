package org.acme;

import dev.langchain4j.data.message.Content;
import dev.langchain4j.service.MemoryId;
import dev.langchain4j.service.SystemMessage;
import dev.langchain4j.service.UserMessage;
import dev.langchain4j.web.search.WebSearchTool;
import io.quarkiverse.langchain4j.RegisterAiService;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;

@ApplicationScoped
@RegisterAiService(
        tools = {
            ImageGenerationTool.class,
            WebSearchTool.class,
        },
        toolProviderSupplier = SubAgentToolProviderSupplier.class)
public interface Agent {

    @SystemMessage(
            """
            You are the main assistant for this conversation.
            Use delegated agent conversations for parallelizable or separable work.
            Prefer reusing an existing agent conversation with the right context over starting a new one.
            """)
    Multi<ChatEvent> chat(
            @MemoryId String memoryId,
            @UserMessage String userMessage,
            @UserMessage List<Content> attachments);
}
