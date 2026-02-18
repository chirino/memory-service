package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.model.AdminSearchQuery;

public interface SearchStore {

    boolean isEnabled();

    SearchResultsDto search(String userId, SearchEntriesRequest request);

    /**
     * Admin search without membership restrictions. Supports the same search types (semantic,
     * fulltext, auto) as the agent search but can search across all users.
     *
     * @param query the admin search query with optional userId filter and includeDeleted flag
     * @return search results
     */
    SearchResultsDto adminSearch(AdminSearchQuery query);

    void upsertTranscriptEmbedding(
            String conversationGroupId, String conversationId, String entryId, float[] embedding);

    /**
     * Delete all embeddings for a conversation group. Used by the background task queue for cleanup
     * after eviction.
     */
    void deleteByConversationGroupId(String conversationGroupId);
}
