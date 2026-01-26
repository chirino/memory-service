package io.github.chirino.memory.config;

import io.github.chirino.memory.cache.ConversationCache;
import io.github.chirino.memory.cache.NoopConversationCache;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class CacheSelector {

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @Inject NoopConversationCache noopConversationCache;

    // TODO: Inject RedisConversationCache and InfinispanConversationCache when implemented
    // @Inject RedisConversationCache redisConversationCache;
    // @Inject InfinispanConversationCache infinispanConversationCache;

    public ConversationCache getCache() {
        String type = cacheType == null ? "none" : cacheType.trim().toLowerCase();
        return switch (type) {
            case "redis" -> {
                // TODO: Return redisConversationCache when implemented
                yield noopConversationCache;
            }
            case "infinispan" -> {
                // TODO: Return infinispanConversationCache when implemented
                yield noopConversationCache;
            }
            default -> noopConversationCache;
        };
    }

    public String getCacheType() {
        return cacheType;
    }
}
