package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchMessagesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import java.util.List;

public interface VectorStore {

    boolean isEnabled();

    List<SearchResultDto> search(String userId, SearchMessagesRequest request);

    void upsertSummaryEmbedding(String conversationId, String messageId, float[] embedding);

    /**
     * Delete all embeddings for a conversation group.
     * Used by the background task queue for cleanup after eviction.
     */
    void deleteByConversationGroupId(String conversationGroupId);
}
