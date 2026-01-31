package io.github.chirino.memory.cache;

import jakarta.enterprise.context.ApplicationScoped;
import java.util.Optional;
import java.util.UUID;

/** No-op implementation of MemoryEntriesCache used when caching is disabled. */
@ApplicationScoped
public class NoopMemoryEntriesCache implements MemoryEntriesCache {

    @Override
    public boolean available() {
        return false;
    }

    @Override
    public Optional<CachedMemoryEntries> get(UUID conversationId, String clientId) {
        return Optional.empty();
    }

    @Override
    public void set(UUID conversationId, String clientId, CachedMemoryEntries entries) {
        // No-op
    }

    @Override
    public void remove(UUID conversationId, String clientId) {
        // No-op
    }
}
