package org.acme;

import dev.langchain4j.service.MemoryId;
import io.quarkiverse.langchain4j.RegisterAiService;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
@RegisterAiService
public interface Agent {
    String chat(@MemoryId String conversationId, String userMessage);
}
