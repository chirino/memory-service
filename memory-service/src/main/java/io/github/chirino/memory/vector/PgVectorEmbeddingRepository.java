package io.github.chirino.memory.vector;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import java.util.UUID;

@ApplicationScoped
public class PgVectorEmbeddingRepository {

    @Inject EntityManager entityManager;

    @Transactional
    public void upsertEmbedding(String entryId, String conversationId, String embedding) {
        entityManager
                .createNativeQuery(
                        "INSERT INTO entry_embeddings (entry_id, conversation_id, embedding) "
                                + "VALUES (?1, ?2, CAST(?3 AS vector)) "
                                + "ON CONFLICT (entry_id) DO UPDATE SET "
                                + "conversation_id = EXCLUDED.conversation_id, "
                                + "embedding = EXCLUDED.embedding, "
                                + "created_at = NOW()")
                .setParameter(1, UUID.fromString(entryId))
                .setParameter(2, UUID.fromString(conversationId))
                .setParameter(3, embedding)
                .executeUpdate();
    }

    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public void deleteByConversationGroupId(String conversationGroupId) {
        // Delete embeddings for all entries in conversations belonging to the group
        // This joins through entries -> conversations -> conversation_groups
        entityManager
                .createNativeQuery(
                        "DELETE FROM entry_embeddings "
                                + "WHERE entry_id IN ("
                                + "  SELECT e.id FROM entries e "
                                + "  JOIN conversations c ON e.conversation_id = c.id "
                                + "  WHERE c.conversation_group_id = ?1"
                                + ")")
                .setParameter(1, UUID.fromString(conversationGroupId))
                .executeUpdate();
    }
}
