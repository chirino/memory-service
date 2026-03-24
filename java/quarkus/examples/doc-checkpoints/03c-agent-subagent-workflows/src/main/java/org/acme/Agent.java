package org.acme;

import dev.langchain4j.service.MemoryId;
import dev.langchain4j.service.SystemMessage;
import io.quarkiverse.langchain4j.RegisterAiService;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
@RegisterAiService(toolProviderSupplier = SubAgentToolProviderSupplier.class)
public interface Agent {

    @SystemMessage(
            """
            You are the parent agent.
            Use delegated agent conversations for parallelizable or separable work.
            Prefer reusing an existing agent conversation with the right context over starting a new one.
            """)
    String chat(@MemoryId String conversationId, String userMessage);
}
