package io.github.chirino.memory.config;

import io.github.chirino.memory.cache.ConversationCache;
import io.github.chirino.memory.cache.NoopConversationCache;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class CacheSelector {

    @ConfigProperty(name = "memory.cache.type", defaultValue = "none")
    String cacheType;

    @Inject NoopConversationCache noopConversationCache;

    public ConversationCache getCache() {
        String type = cacheType == null ? "none" : cacheType.trim().toLowerCase();
        if ("none".equals(type)) {
            return noopConversationCache;
        }
        // Placeholders for redis / infinispan implementations can be added here.
        return noopConversationCache;
    }
}
