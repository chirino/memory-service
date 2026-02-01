package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultsDto;

public interface VectorStore {

    boolean isEnabled();

    SearchResultsDto search(String userId, SearchEntriesRequest request);

    void upsertTranscriptEmbedding(String conversationId, String entryId, float[] embedding);

    /**
     * Delete all embeddings for a conversation group.
     * Used by the background task queue for cleanup after eviction.
     */
    void deleteByConversationGroupId(String conversationGroupId);
}
