package org.acme;

import dev.langchain4j.service.MemoryId;
import io.quarkiverse.langchain4j.RegisterAiService;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
@RegisterAiService
public interface Agent {
    Multi<String> chat(@MemoryId String conversationId, String userMessage);
}
