package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.store.impl.MongoMemoryStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

/**
 * Placeholder MongoDB-backed implementation.
 *
 * For now this delegates to MongoMemoryStore.searchEntries, which
 * performs regex/keyword-based search. Once a vector-capable MongoDB
 * deployment (e.g., Atlas Vector Search) and a message_embeddings
 * collection are available, this class can be updated to perform true
 * vector similarity search.
 */
@ApplicationScoped
public class MongoVectorStore implements VectorStore {

    @Inject MongoMemoryStore mongoMemoryStore;

    @Override
    public boolean isEnabled() {
        return true;
    }

    @Override
    public SearchResultsDto search(String userId, SearchEntriesRequest request) {
        return mongoMemoryStore.searchEntries(userId, request);
    }

    @Override
    public void upsertTranscriptEmbedding(
            String conversationGroupId, String conversationId, String entryId, float[] embedding) {
        // no-op until Mongo vector support is implemented
    }

    @Override
    public void deleteByConversationGroupId(String conversationGroupId) {
        // no-op until Mongo vector support is implemented
    }
}
