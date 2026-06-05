package org.acme;

import dev.langchain4j.rag.content.Content;
import io.github.chirino.memory.client.api.MemoriesApi;
import io.github.chirino.memory.client.model.MemoryItem;
import io.quarkus.logging.Log;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.List;

@ApplicationScoped
public class CognitionMemoryProfileContext {

    @Inject CognitionMemoryRagConfig config;
    @Inject CognitionMemoryAccess access;

    public List<Content> retrieve() {
        if (!config.enabled() || !config.profileContext().enabled()) {
            return List.of();
        }
        try {
            String userId = access.userId();
            MemoriesApi api = access.memoriesApi();
            MemoryItem item =
                    api.getMemory(
                            access.profileContextNamespace(userId),
                            config.profileContext().key(),
                            false,
                            "exclude");
            String content = CognitionMemoryValues.supportedContent(item);
            if (content == null) {
                return List.of();
            }
            String formatted =
                    """
                    Durable profile context
                    source: profile_context/%s

                    %s
                    """
                            .formatted(config.profileContext().key(), content.strip())
                            .strip();
            return List.of(Content.from(formatted));
        } catch (RuntimeException e) {
            if (CognitionMemoryValues.isHttpNotFound(e)) {
                return List.of();
            }
            if (config.failOpen()) {
                Log.warnf(e, "Cognition profile context retrieval failed");
                return List.of();
            }
            throw e;
        }
    }
}
