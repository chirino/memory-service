package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.Collections;
import java.util.List;

@ApplicationScoped
public class NoopVectorStore implements VectorStore {

    @Override
    public boolean isEnabled() {
        return false;
    }

    @Override
    public List<SearchResultDto> search(String userId, SearchEntriesRequest request) {
        return Collections.emptyList();
    }

    @Override
    public void upsertSummaryEmbedding(String conversationId, String entryId, float[] embedding) {
        // no-op
    }

    @Override
    public void deleteByConversationGroupId(String conversationGroupId) {
        // no-op
    }
}
