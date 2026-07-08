package org.acme;

import dev.langchain4j.service.MemoryId;
import io.github.chirino.memory.langchain4j.MemoryService;
import io.quarkiverse.langchain4j.RegisterAiService;
import jakarta.enterprise.context.ApplicationScoped;

/**
 * An agent that explicitly opts into Memory Service-backed chat memory.
 * Works even when {@code memory-service.chat-memory.enabled=false} (selective mode).
 */
@ApplicationScoped
@RegisterAiService(chatMemoryProviderSupplier = MemoryService.class)
public interface SelectiveAgent {
    String chat(@MemoryId String conversationId, String userMessage);
}
