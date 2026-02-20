package io.github.chirino.memory.vector;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import java.util.List;
import java.util.UUID;

@ApplicationScoped
public class PgVectorEmbeddingRepository {

    @Inject EntityManager entityManager;

    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public void upsertEmbedding(
            String entryId,
            String conversationId,
            String conversationGroupId,
            String embedding,
            String model) {
        entityManager
                .createNativeQuery(
                        "INSERT INTO entry_embeddings (entry_id, conversation_id,"
                            + " conversation_group_id, embedding, model) VALUES (?1, ?2, ?3,"
                            + " CAST(?4 AS vector), ?5) ON CONFLICT (entry_id) DO UPDATE SET"
                            + " conversation_id = EXCLUDED.conversation_id, conversation_group_id ="
                            + " EXCLUDED.conversation_group_id, embedding = EXCLUDED.embedding,"
                            + " model = EXCLUDED.model, created_at = NOW()")
                .setParameter(1, UUID.fromString(entryId))
                .setParameter(2, UUID.fromString(conversationId))
                .setParameter(3, UUID.fromString(conversationGroupId))
                .setParameter(4, embedding)
                .setParameter(5, model)
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

    /**
     * Search for similar entries using vector similarity, filtered by model.
     *
     * @param userId the user ID for access control filtering
     * @param embeddingLiteral the query embedding in pgvector literal format (e.g.,
     *     "[0.1,0.2,...]")
     * @param limit maximum number of results to return
     * @param groupByConversation when true, returns only the highest-scoring entry per conversation
     * @param model the embedding model ID to filter by (only search embeddings from this model)
     * @return list of search results ordered by similarity score (descending)
     */
    @Transactional
    public List<VectorSearchResult> searchSimilar(
            String userId,
            String embeddingLiteral,
            int limit,
            boolean groupByConversation,
            String model) {

        String sql;
        if (groupByConversation) {
            // Grouped by conversation - returns best match per conversation
            sql =
                    """
                    WITH accessible_ranked AS (
                        SELECT
                            ee.entry_id,
                            ee.conversation_id,
                            1 - (ee.embedding <=> CAST(?1 AS vector)) AS score,
                            ROW_NUMBER() OVER (
                                PARTITION BY ee.conversation_id
                                ORDER BY ee.embedding <=> CAST(?1 AS vector)
                            ) AS rank_in_conversation
                        FROM entry_embeddings ee
                        JOIN conversation_memberships cm
                            ON cm.conversation_group_id = ee.conversation_group_id
                            AND cm.user_id = ?2
                        JOIN conversations c
                            ON c.id = ee.conversation_id
                            AND c.deleted_at IS NULL
                        JOIN conversation_groups cg
                            ON cg.id = ee.conversation_group_id
                            AND cg.deleted_at IS NULL
                        WHERE ee.model = ?4
                    )
                    SELECT entry_id, conversation_id, score
                    FROM accessible_ranked
                    WHERE rank_in_conversation = 1
                    ORDER BY score DESC
                    LIMIT ?3
                    """;
        } else {
            // No grouping - returns all entries ordered by score
            sql =
                    """
                    SELECT
                        ee.entry_id,
                        ee.conversation_id,
                        1 - (ee.embedding <=> CAST(?1 AS vector)) AS score
                    FROM entry_embeddings ee
                    JOIN conversation_memberships cm
                        ON cm.conversation_group_id = ee.conversation_group_id
                        AND cm.user_id = ?2
                    JOIN conversations c
                        ON c.id = ee.conversation_id
                        AND c.deleted_at IS NULL
                    JOIN conversation_groups cg
                        ON cg.id = ee.conversation_group_id
                        AND cg.deleted_at IS NULL
                    WHERE ee.model = ?4
                    ORDER BY ee.embedding <=> CAST(?1 AS vector)
                    LIMIT ?3
                    """;
        }

        @SuppressWarnings("unchecked")
        List<Object[]> rows =
                entityManager
                        .createNativeQuery(sql)
                        .setParameter(1, embeddingLiteral)
                        .setParameter(2, userId)
                        .setParameter(3, limit)
                        .setParameter(4, model)
                        .getResultList();

        return rows.stream()
                .map(
                        row ->
                                new VectorSearchResult(
                                        row[0].toString(), // entry_id
                                        row[1].toString(), // conversation_id
                                        ((Number) row[2]).doubleValue() // score
                                        ))
                .toList();
    }

    /**
     * Admin search for similar entries without membership filtering.
     *
     * @param embeddingLiteral the query embedding in pgvector literal format
     * @param limit maximum number of results to return
     * @param groupByConversation when true, returns only the highest-scoring entry per conversation
     * @param userId optional filter by conversation owner
     * @param includeDeleted whether to include soft-deleted conversations
     * @param model the embedding model ID to filter by
     * @return list of search results ordered by similarity score (descending)
     */
    @Transactional
    public List<VectorSearchResult> adminSearchSimilar(
            String embeddingLiteral,
            int limit,
            boolean groupByConversation,
            String userId,
            boolean includeDeleted,
            String model) {

        // Build dynamic WHERE clauses
        StringBuilder deletedFilter = new StringBuilder();
        if (!includeDeleted) {
            deletedFilter.append(" AND c.deleted_at IS NULL AND cg.deleted_at IS NULL");
        }

        StringBuilder userFilter = new StringBuilder();
        if (userId != null && !userId.isBlank()) {
            userFilter.append(" AND c.owner_user_id = ?3");
        }

        String modelFilter = " AND ee.model = ?4";
        // Compute the model parameter position
        int modelParamIdx = (userId != null && !userId.isBlank()) ? 5 : 4;
        modelFilter = " AND ee.model = ?" + modelParamIdx;

        String sql;
        if (groupByConversation) {
            sql =
                    """
                    WITH ranked AS (
                        SELECT
                            ee.entry_id,
                            ee.conversation_id,
                            1 - (ee.embedding <=> CAST(?1 AS vector)) AS score,
                            ROW_NUMBER() OVER (
                                PARTITION BY ee.conversation_id
                                ORDER BY ee.embedding <=> CAST(?1 AS vector)
                            ) AS rank_in_conversation
                        FROM entry_embeddings ee
                        JOIN conversations c ON c.id = ee.conversation_id
                        JOIN conversation_groups cg ON cg.id = ee.conversation_group_id
                        WHERE 1=1
                    """
                            + deletedFilter
                            + userFilter
                            + modelFilter
                            + """
                            )
                            SELECT entry_id, conversation_id, score
                            FROM ranked
                            WHERE rank_in_conversation = 1
                            ORDER BY score DESC
                            LIMIT ?2
                            """;
        } else {
            sql =
                    """
                    SELECT
                        ee.entry_id,
                        ee.conversation_id,
                        1 - (ee.embedding <=> CAST(?1 AS vector)) AS score
                    FROM entry_embeddings ee
                    JOIN conversations c ON c.id = ee.conversation_id
                    JOIN conversation_groups cg ON cg.id = ee.conversation_group_id
                    WHERE 1=1
                    """
                            + deletedFilter
                            + userFilter
                            + modelFilter
                            + """
                            ORDER BY ee.embedding <=> CAST(?1 AS vector)
                            LIMIT ?2
                            """;
        }

        var nativeQuery =
                entityManager
                        .createNativeQuery(sql)
                        .setParameter(1, embeddingLiteral)
                        .setParameter(2, limit);

        if (userId != null && !userId.isBlank()) {
            nativeQuery.setParameter(3, userId);
        }

        nativeQuery.setParameter(modelParamIdx, model);

        @SuppressWarnings("unchecked")
        List<Object[]> rows = nativeQuery.getResultList();

        return rows.stream()
                .map(
                        row ->
                                new VectorSearchResult(
                                        row[0].toString(), // entry_id
                                        row[1].toString(), // conversation_id
                                        ((Number) row[2]).doubleValue() // score
                                        ))
                .toList();
    }

    /** Result from vector similarity search. */
    public record VectorSearchResult(String entryId, String conversationId, double score) {}
}
