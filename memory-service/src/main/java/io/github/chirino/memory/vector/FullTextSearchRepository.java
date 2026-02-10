package io.github.chirino.memory.vector;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import java.util.Arrays;
import java.util.List;
import java.util.stream.Collectors;
import org.jboss.logging.Logger;

/**
 * Repository for PostgreSQL full-text search using tsvector and GIN indexes.
 *
 * <p>Provides fast keyword search with stemming, ranking, and highlighting as a fallback when
 * vector search is unavailable or returns no results. Uses prefix matching with the :* operator
 * to match partial words (e.g., "Jav" matches "JavaScript").
 */
@ApplicationScoped
public class FullTextSearchRepository {

    private static final Logger LOG = Logger.getLogger(FullTextSearchRepository.class);

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

        // Convert query to prefix tsquery format (e.g., "Jav script" -> "Jav:* & script:*")
        String prefixQuery = toPrefixTsQuery(query);
        if (prefixQuery.isEmpty()) {
            LOG.infof("fullTextSearch: empty query after conversion, original='%s'", query);
            return List.of();
        }
        LOG.infof(
                "fullTextSearch: converted query '%s' to prefix tsquery '%s'", query, prefixQuery);

        String sql;
        if (groupByConversation) {
            sql =
                    """
                    WITH accessible_ranked AS (
                        SELECT
                            e.id AS entry_id,
                            e.conversation_id,
                            ts_rank(e.indexed_content_tsv, to_tsquery('english', ?1)) AS score,
                            ts_headline('english', e.indexed_content, to_tsquery('english', ?1),
                                'StartSel=**, StopSel=**, MaxWords=50, MinWords=20') AS highlight,
                            ROW_NUMBER() OVER (
                                PARTITION BY e.conversation_id
                                ORDER BY ts_rank(e.indexed_content_tsv, to_tsquery('english', ?1)) DESC
                            ) AS rank_in_conversation
                        FROM entries e
                        JOIN conversations c ON c.id = e.conversation_id AND c.deleted_at IS NULL
                        JOIN conversation_groups cg ON cg.id = c.conversation_group_id AND cg.deleted_at IS NULL
                        JOIN conversation_memberships cm ON cm.conversation_group_id = cg.id AND cm.user_id = ?2
                        WHERE e.indexed_content_tsv @@ to_tsquery('english', ?1)
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
                        ts_rank(e.indexed_content_tsv, to_tsquery('english', ?1)) AS score,
                        ts_headline('english', e.indexed_content, to_tsquery('english', ?1),
                            'StartSel=**, StopSel=**, MaxWords=50, MinWords=20') AS highlight
                    FROM entries e
                    JOIN conversations c ON c.id = e.conversation_id AND c.deleted_at IS NULL
                    JOIN conversation_groups cg ON cg.id = c.conversation_group_id AND cg.deleted_at IS NULL
                    JOIN conversation_memberships cm ON cm.conversation_group_id = cg.id AND cm.user_id = ?2
                    WHERE e.indexed_content_tsv @@ to_tsquery('english', ?1)
                    ORDER BY score DESC
                    LIMIT ?3
                    """;
        }

        @SuppressWarnings("unchecked")
        List<Object[]> rows =
                entityManager
                        .createNativeQuery(sql)
                        .setParameter(1, prefixQuery)
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

    /**
     * Converts a plain text query to a PostgreSQL tsquery with prefix matching.
     *
     * <p>Example: "Jav script" becomes "Jav:* & script:*"
     *
     * @param query the plain text search query
     * @return the tsquery string with prefix operators
     */
    private String toPrefixTsQuery(String query) {
        if (query == null || query.isBlank()) {
            return "";
        }

        return Arrays.stream(query.trim().split("\\s+"))
                .filter(word -> !word.isEmpty())
                .map(this::escapeTsQueryWord)
                .filter(word -> !word.isEmpty())
                .map(word -> word + ":*")
                .collect(Collectors.joining(" & "));
    }

    /**
     * Escapes special characters in a word for use in to_tsquery.
     *
     * <p>Removes characters that have special meaning in tsquery syntax.
     */
    private String escapeTsQueryWord(String word) {
        // Remove tsquery special characters: & | ! ( ) : * \ '
        return word.replaceAll("[&|!():'\\\\*]", "");
    }

    /**
     * Admin full-text search on indexed_content without membership filtering.
     *
     * @param query the search query
     * @param limit maximum results
     * @param groupByConversation when true, returns best match per conversation
     * @param userId optional filter by conversation owner
     * @param includeDeleted whether to include soft-deleted conversations
     * @return search results with scores and highlights
     */
    @Transactional
    public List<FullTextSearchResult> adminSearch(
            String query,
            int limit,
            boolean groupByConversation,
            String userId,
            boolean includeDeleted) {

        // Convert query to prefix tsquery format (e.g., "Jav script" -> "Jav:* & script:*")
        String prefixQuery = toPrefixTsQuery(query);
        if (prefixQuery.isEmpty()) {
            LOG.infof("adminSearch: empty query after conversion, original='%s'", query);
            return List.of();
        }
        LOG.infof(
                "adminSearch: converted query '%s' to prefix tsquery '%s', userId=%s,"
                        + " includeDeleted=%s",
                query, prefixQuery, userId, includeDeleted);

        // Build dynamic WHERE clauses
        StringBuilder deletedFilter = new StringBuilder();
        if (!includeDeleted) {
            deletedFilter.append(" AND c.deleted_at IS NULL AND cg.deleted_at IS NULL");
        }

        StringBuilder userFilter = new StringBuilder();
        if (userId != null && !userId.isBlank()) {
            userFilter.append(" AND c.owner_user_id = ?4");
        }

        String sql;
        if (groupByConversation) {
            sql =
                    """
                    WITH ranked AS (
                        SELECT
                            e.id AS entry_id,
                            e.conversation_id,
                            ts_rank(e.indexed_content_tsv, to_tsquery('english', ?1)) AS score,
                            ts_headline('english', e.indexed_content, to_tsquery('english', ?1),
                                'StartSel=**, StopSel=**, MaxWords=50, MinWords=20') AS highlight,
                            ROW_NUMBER() OVER (
                                PARTITION BY e.conversation_id
                                ORDER BY ts_rank(e.indexed_content_tsv, to_tsquery('english', ?1)) DESC
                            ) AS rank_in_conversation
                        FROM entries e
                        JOIN conversations c ON c.id = e.conversation_id
                        JOIN conversation_groups cg ON cg.id = c.conversation_group_id
                        WHERE e.indexed_content_tsv @@ to_tsquery('english', ?1)
                    """
                            + deletedFilter
                            + userFilter
                            + """
                            )
                            SELECT entry_id, conversation_id, score, highlight
                            FROM ranked
                            WHERE rank_in_conversation = 1
                            ORDER BY score DESC
                            LIMIT ?2
                            """;
        } else {
            sql =
                    """
                    SELECT
                        e.id AS entry_id,
                        e.conversation_id,
                        ts_rank(e.indexed_content_tsv, to_tsquery('english', ?1)) AS score,
                        ts_headline('english', e.indexed_content, to_tsquery('english', ?1),
                            'StartSel=**, StopSel=**, MaxWords=50, MinWords=20') AS highlight
                    FROM entries e
                    JOIN conversations c ON c.id = e.conversation_id
                    JOIN conversation_groups cg ON cg.id = c.conversation_group_id
                    WHERE e.indexed_content_tsv @@ to_tsquery('english', ?1)
                    """
                            + deletedFilter
                            + userFilter
                            + """
                            ORDER BY score DESC
                            LIMIT ?2
                            """;
        }

        var nativeQuery =
                entityManager
                        .createNativeQuery(sql)
                        .setParameter(1, prefixQuery)
                        .setParameter(2, limit);

        if (userId != null && !userId.isBlank()) {
            nativeQuery.setParameter(4, userId);
        }

        @SuppressWarnings("unchecked")
        List<Object[]> rows = nativeQuery.getResultList();

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
