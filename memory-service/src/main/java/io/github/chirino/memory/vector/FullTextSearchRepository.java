package io.github.chirino.memory.vector;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import java.util.List;

/**
 * Repository for PostgreSQL full-text search using tsvector and GIN indexes.
 *
 * <p>Provides fast keyword search with stemming, ranking, and highlighting as a fallback when
 * vector search is unavailable or returns no results.
 */
@ApplicationScoped
public class FullTextSearchRepository {

    @Inject EntityManager entityManager;

    /**
     * Full-text search on indexed_content with access control.
     *
     * @param userId the user ID for access control
     * @param query the search query
     * @param limit maximum results
     * @param groupByConversation when true, returns best match per conversation
     * @return search results with scores and highlights
     */
    @Transactional
    public List<FullTextSearchResult> search(
            String userId, String query, int limit, boolean groupByConversation) {

        String sql;
        if (groupByConversation) {
            sql =
                    """
                    WITH accessible_ranked AS (
                        SELECT
                            e.id AS entry_id,
                            e.conversation_id,
                            ts_rank(e.indexed_content_tsv, plainto_tsquery('english', ?1)) AS score,
                            ts_headline('english', e.indexed_content, plainto_tsquery('english', ?1),
                                'StartSel=<mark>, StopSel=</mark>, MaxWords=50, MinWords=20') AS highlight,
                            ROW_NUMBER() OVER (
                                PARTITION BY e.conversation_id
                                ORDER BY ts_rank(e.indexed_content_tsv, plainto_tsquery('english', ?1)) DESC
                            ) AS rank_in_conversation
                        FROM entries e
                        JOIN conversations c ON c.id = e.conversation_id AND c.deleted_at IS NULL
                        JOIN conversation_groups cg ON cg.id = c.conversation_group_id AND cg.deleted_at IS NULL
                        JOIN conversation_memberships cm ON cm.conversation_group_id = cg.id AND cm.user_id = ?2
                        WHERE e.indexed_content_tsv @@ plainto_tsquery('english', ?1)
                    )
                    SELECT entry_id, conversation_id, score, highlight
                    FROM accessible_ranked
                    WHERE rank_in_conversation = 1
                    ORDER BY score DESC
                    LIMIT ?3
                    """;
        } else {
            sql =
                    """
                    SELECT
                        e.id AS entry_id,
                        e.conversation_id,
                        ts_rank(e.indexed_content_tsv, plainto_tsquery('english', ?1)) AS score,
                        ts_headline('english', e.indexed_content, plainto_tsquery('english', ?1),
                            'StartSel=<mark>, StopSel=</mark>, MaxWords=50, MinWords=20') AS highlight
                    FROM entries e
                    JOIN conversations c ON c.id = e.conversation_id AND c.deleted_at IS NULL
                    JOIN conversation_groups cg ON cg.id = c.conversation_group_id AND cg.deleted_at IS NULL
                    JOIN conversation_memberships cm ON cm.conversation_group_id = cg.id AND cm.user_id = ?2
                    WHERE e.indexed_content_tsv @@ plainto_tsquery('english', ?1)
                    ORDER BY score DESC
                    LIMIT ?3
                    """;
        }

        @SuppressWarnings("unchecked")
        List<Object[]> rows =
                entityManager
                        .createNativeQuery(sql)
                        .setParameter(1, query)
                        .setParameter(2, userId)
                        .setParameter(3, limit)
                        .getResultList();

        return rows.stream()
                .map(
                        row ->
                                new FullTextSearchResult(
                                        row[0].toString(), // entry_id
                                        row[1].toString(), // conversation_id
                                        ((Number) row[2]).doubleValue(), // score
                                        (String) row[3] // highlight
                                        ))
                .toList();
    }

    /** Result from full-text search. */
    public record FullTextSearchResult(
            String entryId, String conversationId, double score, String highlight) {}
}
