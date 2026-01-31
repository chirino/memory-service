package io.github.chirino.memory.cache;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonProperty;
import java.time.Instant;
import java.util.List;
import java.util.UUID;

/**
 * Cached memory entries for a conversation/client pair. Contains the current epoch and all entries
 * in their encrypted form.
 */
public record CachedMemoryEntries(long epoch, List<CachedEntry> entries) {

    @JsonCreator
    public CachedMemoryEntries(
            @JsonProperty("epoch") long epoch, @JsonProperty("entries") List<CachedEntry> entries) {
        this.epoch = epoch;
        this.entries = entries;
    }

    /** Individual cached entry. Stores encrypted content as-is from database. */
    public record CachedEntry(
            UUID id, String contentType, byte[] encryptedContent, Instant createdAt) {

        @JsonCreator
        public CachedEntry(
                @JsonProperty("id") UUID id,
                @JsonProperty("contentType") String contentType,
                @JsonProperty("encryptedContent") byte[] encryptedContent,
                @JsonProperty("createdAt") Instant createdAt) {
            this.id = id;
            this.contentType = contentType;
            this.encryptedContent = encryptedContent;
            this.createdAt = createdAt;
        }
    }
}
