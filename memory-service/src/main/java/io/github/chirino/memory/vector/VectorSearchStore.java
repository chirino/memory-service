package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.model.AdminSearchQuery;

public interface VectorSearchStore {

    boolean isEnabled();

    boolean isSemanticSearchAvailable();

    SearchResultsDto search(String userId, SearchEntriesRequest request);

    SearchResultsDto adminSearch(AdminSearchQuery query);

    void upsertTranscriptEmbedding(
            String conversationGroupId, String conversationId, String entryId, float[] embedding);

    /**
     * Delete all embeddings for a conversation group. Used by the background task queue for cleanup
     * after eviction.
     */
    void deleteByConversationGroupId(String conversationGroupId);
}
