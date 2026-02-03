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
        // Return the configured cache implementation directly, not noop.
        // The cache implementations handle unavailability gracefully by incrementing
        // miss metrics and returning empty results. This avoids a startup race condition
        // where the cache might not be ready when select() is first called.
        return switch (type) {
            case "redis" -> redisCache;
            case "infinispan" -> infinispanCache;
            default -> noopCache;
        };
    }
}
