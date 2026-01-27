package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchMessagesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.store.impl.PostgresMemoryStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.List;
import java.util.Locale;
import org.jboss.logging.Logger;

/**
 * Placeholder PgVector-backed implementation.
 *
 * For now this delegates to PostgresMemoryStore.searchMessages, which
 * performs keyword-based search. Once pgvector and the message_embeddings
 * table are provisioned, this class is the right place to switch to a true
 * vector similarity query.
 */
@ApplicationScoped
public class PgVectorStore implements VectorStore {

    private static final Logger LOG = Logger.getLogger(PgVectorStore.class);

    @Inject PostgresMemoryStore postgresMemoryStore;

    @Inject PgVectorEmbeddingRepository embeddingRepository;

    @Override
    public boolean isEnabled() {
        return true;
    }

    @Override
    public List<SearchResultDto> search(String userId, SearchMessagesRequest request) {
        return postgresMemoryStore.searchMessages(userId, request);
    }

    @Override
    public void upsertSummaryEmbedding(String conversationId, String messageId, float[] embedding) {
        if (embedding == null || embedding.length == 0) {
            return;
        }
        embeddingRepository.upsertEmbedding(
                messageId, conversationId, toPgVectorLiteral(embedding));
    }

    @Override
    public void deleteByConversationGroupId(String conversationGroupId) {
        try {
            embeddingRepository.deleteByConversationGroupId(conversationGroupId);
        } catch (Exception e) {
            // May fail if message_embeddings table does not exist yet
            LOG.debugf(
                    "Could not delete embeddings for group %s: %s",
                    conversationGroupId, e.getMessage());
        }
    }

    private String toPgVectorLiteral(float[] embedding) {
        StringBuilder builder = new StringBuilder(embedding.length * 8);
        builder.append('[');
        for (int i = 0; i < embedding.length; i++) {
            if (i > 0) {
                builder.append(',');
            }
            builder.append(String.format(Locale.ROOT, "%.6f", embedding[i]));
        }
        builder.append(']');
        return builder.toString();
    }
}
