package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.Collections;

@ApplicationScoped
public class NoopVectorStore implements VectorStore {

    @Override
    public boolean isEnabled() {
        return false;
    }

    @Override
    public SearchResultsDto search(String userId, SearchEntriesRequest request) {
        SearchResultsDto result = new SearchResultsDto();
        result.setResults(Collections.emptyList());
        result.setNextCursor(null);
        return result;
    }

    @Override
    public void upsertTranscriptEmbedding(
            String conversationGroupId, String conversationId, String entryId, float[] embedding) {
        // no-op
    }

    @Override
    public void deleteByConversationGroupId(String conversationGroupId) {
        // no-op
    }
}
