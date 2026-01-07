package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchMessagesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import java.util.List;

public interface VectorStore {

    boolean isEnabled();

    List<SearchResultDto> search(String userId, SearchMessagesRequest request);

    void upsertSummaryEmbedding(String conversationId, String messageId, float[] embedding);
}
