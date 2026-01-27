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
    public void upsertEmbedding(String messageId, String conversationId, String embedding) {
        entityManager
                .createNativeQuery(
                        "INSERT INTO message_embeddings (message_id, conversation_id, embedding) "
                                + "VALUES (?1, ?2, CAST(?3 AS vector)) "
                                + "ON CONFLICT (message_id) DO UPDATE SET "
                                + "conversation_id = EXCLUDED.conversation_id, "
                                + "embedding = EXCLUDED.embedding, "
                                + "created_at = NOW()")
                .setParameter(1, UUID.fromString(messageId))
                .setParameter(2, UUID.fromString(conversationId))
                .setParameter(3, embedding)
                .executeUpdate();
    }

    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public void deleteByConversationGroupId(String conversationGroupId) {
        // Delete embeddings for all messages in conversations belonging to the group
        // This joins through messages -> conversations -> conversation_groups
        entityManager
                .createNativeQuery(
                        "DELETE FROM message_embeddings "
                                + "WHERE message_id IN ("
                                + "  SELECT m.id FROM messages m "
                                + "  JOIN conversations c ON m.conversation_id = c.id "
                                + "  WHERE c.conversation_group_id = ?1"
                                + ")")
                .setParameter(1, UUID.fromString(conversationGroupId))
                .executeUpdate();
    }
}
