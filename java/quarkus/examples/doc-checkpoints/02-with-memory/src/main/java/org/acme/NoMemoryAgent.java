package org.acme;

import io.quarkiverse.langchain4j.RegisterAiService;
import jakarta.enterprise.context.ApplicationScoped;

/**
 * An agent that explicitly opts out of all chat memory.
 */
@ApplicationScoped
@RegisterAiService(
        chatMemoryProviderSupplier = RegisterAiService.NoChatMemoryProviderSupplier.class)
public interface NoMemoryAgent {
    String chat(String userMessage);
}
