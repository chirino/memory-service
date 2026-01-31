package io.github.chirino.memory.cache;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

/** Selects the appropriate MemoryEntriesCache implementation based on configuration. */
@ApplicationScoped
public class MemoryEntriesCacheSelector {

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @Inject RedisMemoryEntriesCache redisCache;

    @Inject InfinispanMemoryEntriesCache infinispanCache;

    @Inject NoopMemoryEntriesCache noopCache;

    public MemoryEntriesCache select() {
        String type = cacheType == null ? "none" : cacheType.trim().toLowerCase();
        return switch (type) {
            case "redis" -> redisCache.available() ? redisCache : noopCache;
            case "infinispan" -> infinispanCache.available() ? infinispanCache : noopCache;
            default -> noopCache;
        };
    }
}
