package io.github.chirino.memory.cache;

import java.util.Optional;
import java.util.UUID;

/**
 * Cache for storing memory entries per conversation/client pair. Entries are stored in their
 * encrypted form for security. Implementations must handle unavailability gracefully.
 */
public interface MemoryEntriesCache {

    /** Returns true if the cache backend is available and configured. */
    boolean available();

    /**
     * Get cached memory entries for a conversation/client pair. Refreshes the TTL on cache hit.
     *
     * @return Optional.empty() if not cached or cache unavailable
     */
    Optional<CachedMemoryEntries> get(UUID conversationId, String clientId);

    /**
     * Store memory entries for a conversation/client pair. The entries should contain encrypted
     * content (not decrypted). Sets/refreshes the TTL based on memory-service.cache.epoch.ttl
     * config.
     */
    void set(UUID conversationId, String clientId, CachedMemoryEntries entries);

    /** Remove cached entries (e.g., on conversation delete or eviction). */
    void remove(UUID conversationId, String clientId);
}
