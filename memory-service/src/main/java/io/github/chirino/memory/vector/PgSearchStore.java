package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.store.SearchTypeUnavailableException;
import io.github.chirino.memory.vector.FullTextSearchRepository.FullTextSearchResult;
import io.github.chirino.memory.vector.PgVectorEmbeddingRepository.VectorSearchResult;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * PgVector-backed search store implementation.
 *
 * <p>Performs semantic search using pgvector's cosine similarity operator on stored embeddings.
 * Access control is enforced via JOIN with conversation_memberships table.
 */
@ApplicationScoped
public class PgSearchStore implements VectorSearchStore, FullTextSearchStore {

    private static final Logger LOG = Logger.getLogger(PgSearchStore.class);

    @ConfigProperty(name = "memory-service.search.semantic.enabled", defaultValue = "true")
    boolean semanticSearchEnabled;

    @ConfigProperty(name = "memory-service.search.fulltext.enabled", defaultValue = "true")
    boolean fullTextSearchEnabled;

    @Inject PgVectorEmbeddingRepository embeddingRepository;
    @Inject FullTextSearchRepository fullTextSearchRepository;
    @Inject EmbeddingService embeddingService;
    @Inject SearchResultDtoBuilder resultBuilder;

    @Override
    public boolean isEnabled() {
        // Vector store is available when this implementation is selected.
        return true;
    }

    @Override
    public boolean isSemanticSearchAvailable() {
        return semanticSearchEnabled && embeddingService.isEnabled();
    }

    @Override
    public boolean isFullTextSearchAvailable() {
        return fullTextSearchEnabled;
    }

    @Override
    public SearchResultsDto search(String userId, SearchEntriesRequest request) {
        if (request.getQuery() == null || request.getQuery().isBlank()) {
            return resultBuilder.emptyResults();
        }

        String searchType = request.getSearchType() != null ? request.getSearchType() : "auto";

        return switch (searchType) {
            case "semantic" -> {
                validateSemanticSearchAvailable();
                yield semanticSearch(userId, request);
            }
            case "fulltext" -> {
                validateFullTextSearchAvailable();
                yield fullTextSearch(userId, request);
            }
            case "auto" ->
                    throw new IllegalArgumentException(
                            "searchType 'auto' must be resolved by the caller");
            default ->
                    throw new IllegalArgumentException(
                            "Invalid searchType: "
                                    + searchType
                                    + ". Valid values: auto, semantic, fulltext");
        };
    }

    private void validateSemanticSearchAvailable() {
        if (!isSemanticSearchAvailable()) {
            List<String> available = fullTextSearchEnabled ? List.of("fulltext") : List.of();
            throw new SearchTypeUnavailableException(
                    "Semantic search is not available. The embedding service is disabled on this"
                            + " server.",
                    available);
        }
    }

    private void validateFullTextSearchAvailable() {
        if (!isFullTextSearchAvailable()) {
            List<String> available = isSemanticSearchAvailable() ? List.of("semantic") : List.of();
            throw new SearchTypeUnavailableException(
                    "Full-text search is not available. The server is not configured with"
                            + " PostgreSQL full-text search support.",
                    available);
        }
    }

    private SearchResultsDto semanticSearch(String userId, SearchEntriesRequest request) {
        // Embed the query
        float[] queryEmbedding = embeddingService.embed(request.getQuery());
        if (queryEmbedding == null || queryEmbedding.length == 0) {
            LOG.warn("semanticSearch: failed to embed query");
            return resultBuilder.emptyResults();
        }

        int limit = request.getLimit() != null ? request.getLimit() : 20;
        boolean groupByConversation =
                request.getGroupByConversation() == null || request.getGroupByConversation();
        boolean includeEntry = request.getIncludeEntry() == null || request.getIncludeEntry();

        LOG.infof(
                "semanticSearch: query='%s', limit=%d, groupByConversation=%s",
                request.getQuery(), limit, groupByConversation);

        // Perform vector search - fetch one extra to determine if there's a next page
        List<VectorSearchResult> vectorResults;
        try {
            vectorResults =
                    embeddingRepository.searchSimilar(
                            userId,
                            toPgVectorLiteral(queryEmbedding),
                            limit + 1,
                            groupByConversation,
                            embeddingService.modelId());
            LOG.infof("semanticSearch: vector query returned %d raw results", vectorResults.size());
        } catch (Exception e) {
            LOG.warnf("semanticSearch: vector search failed: %s", e.getMessage());
            return resultBuilder.emptyResults();
        }

        if (vectorResults.isEmpty()) {
            return resultBuilder.emptyResults();
        }

        // Build result DTOs with entry details
        List<SearchResultDto> resultsList = new ArrayList<>();
        for (VectorSearchResult vr : vectorResults) {
            if (resultsList.size() >= limit) {
                break;
            }

            SearchResultDto dto =
                    resultBuilder.buildFromVectorResult(
                            vr.entryId(), vr.conversationId(), vr.score(), includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        // Determine next cursor
        String nextCursor = null;
        if (vectorResults.size() > limit && !resultsList.isEmpty()) {
            nextCursor = resultsList.get(resultsList.size() - 1).getEntryId();
        }

        return resultBuilder.buildResultsFromList(resultsList, nextCursor);
    }

    private SearchResultsDto fullTextSearch(String userId, SearchEntriesRequest request) {
        int limit = request.getLimit() != null ? request.getLimit() : 20;
        boolean groupByConversation =
                request.getGroupByConversation() == null || request.getGroupByConversation();
        boolean includeEntry = request.getIncludeEntry() == null || request.getIncludeEntry();

        LOG.infof(
                "fullTextSearch: query='%s', limit=%d, groupByConversation=%s",
                request.getQuery(), limit, groupByConversation);

        List<FullTextSearchResult> ftsResults;
        try {
            ftsResults =
                    fullTextSearchRepository.search(
                            userId, request.getQuery(), limit + 1, groupByConversation);
            LOG.infof("fullTextSearch: query returned %d raw results", ftsResults.size());
        } catch (Exception e) {
            LOG.warnf("fullTextSearch: search failed: %s", e.getMessage());
            return resultBuilder.emptyResults();
        }

        if (ftsResults.isEmpty()) {
            return resultBuilder.emptyResults();
        }

        List<SearchResultDto> resultsList = new ArrayList<>();
        for (FullTextSearchResult fts : ftsResults) {
            if (resultsList.size() >= limit) {
                break;
            }

            SearchResultDto dto =
                    resultBuilder.buildFromFullTextResult(
                            fts.entryId(),
                            fts.conversationId(),
                            fts.score(),
                            fts.highlight(),
                            includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        String nextCursor = null;
        if (ftsResults.size() > limit && !resultsList.isEmpty()) {
            nextCursor = resultsList.get(resultsList.size() - 1).getEntryId();
        }

        return resultBuilder.buildResultsFromList(resultsList, nextCursor);
    }

    @Override
    public void upsertTranscriptEmbedding(
            String conversationGroupId, String conversationId, String entryId, float[] embedding) {
        if (embedding == null || embedding.length == 0) {
            return;
        }
        embeddingRepository.upsertEmbedding(
                entryId,
                conversationId,
                conversationGroupId,
                toPgVectorLiteral(embedding),
                embeddingService.modelId());
        LOG.infof(
                "Embedding stored: entryId=%s, conversationId=%s, model=%s, dimensions=%d",
                entryId, conversationId, embeddingService.modelId(), embedding.length);
    }

    @Override
    public void deleteByConversationGroupId(String conversationGroupId) {
        try {
            embeddingRepository.deleteByConversationGroupId(conversationGroupId);
        } catch (Exception e) {
            // May fail if entry_embeddings table does not exist yet
            LOG.debugf(
                    "Could not delete embeddings for group %s: %s",
                    conversationGroupId, e.getMessage());
        }
    }

    @Override
    public SearchResultsDto adminSearch(AdminSearchQuery query) {
        if (query.getQuery() == null || query.getQuery().isBlank()) {
            return resultBuilder.emptyResults();
        }

        String searchType = query.getSearchType() != null ? query.getSearchType() : "auto";

        return switch (searchType) {
            case "semantic" -> {
                validateSemanticSearchAvailable();
                yield adminSemanticSearch(query);
            }
            case "fulltext" -> {
                validateFullTextSearchAvailable();
                yield adminFullTextSearch(query);
            }
            case "auto" ->
                    throw new IllegalArgumentException(
                            "searchType 'auto' must be resolved by the caller");
            default ->
                    throw new IllegalArgumentException(
                            "Invalid searchType: "
                                    + searchType
                                    + ". Valid values: auto, semantic, fulltext");
        };
    }

    private SearchResultsDto adminSemanticSearch(AdminSearchQuery query) {
        float[] queryEmbedding = embeddingService.embed(query.getQuery());
        if (queryEmbedding == null || queryEmbedding.length == 0) {
            LOG.warn("adminSemanticSearch: failed to embed query");
            return resultBuilder.emptyResults();
        }

        int limit = query.getLimit() != null ? query.getLimit() : 20;
        boolean groupByConversation =
                query.getGroupByConversation() == null || query.getGroupByConversation();
        boolean includeEntry = query.getIncludeEntry() == null || query.getIncludeEntry();

        LOG.infof(
                "adminSemanticSearch: query='%s', limit=%d, groupByConversation=%s, userId=%s",
                query.getQuery(), limit, groupByConversation, query.getUserId());

        List<VectorSearchResult> vectorResults;
        try {
            vectorResults =
                    embeddingRepository.adminSearchSimilar(
                            toPgVectorLiteral(queryEmbedding),
                            limit + 1,
                            groupByConversation,
                            query.getUserId(),
                            query.isIncludeDeleted(),
                            embeddingService.modelId());
            LOG.infof(
                    "adminSemanticSearch: vector query returned %d raw results",
                    vectorResults.size());
        } catch (Exception e) {
            LOG.warnf("adminSemanticSearch: vector search failed: %s", e.getMessage());
            return resultBuilder.emptyResults();
        }

        if (vectorResults.isEmpty()) {
            return resultBuilder.emptyResults();
        }

        List<SearchResultDto> resultsList = new ArrayList<>();
        for (VectorSearchResult vr : vectorResults) {
            if (resultsList.size() >= limit) {
                break;
            }

            SearchResultDto dto =
                    resultBuilder.buildFromVectorResult(
                            vr.entryId(), vr.conversationId(), vr.score(), includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        String nextCursor = null;
        if (vectorResults.size() > limit && !resultsList.isEmpty()) {
            nextCursor = resultsList.get(resultsList.size() - 1).getEntryId();
        }

        return resultBuilder.buildResultsFromList(resultsList, nextCursor);
    }

    private SearchResultsDto adminFullTextSearch(AdminSearchQuery query) {
        int limit = query.getLimit() != null ? query.getLimit() : 20;
        boolean groupByConversation =
                query.getGroupByConversation() == null || query.getGroupByConversation();
        boolean includeEntry = query.getIncludeEntry() == null || query.getIncludeEntry();

        LOG.infof(
                "adminFullTextSearch: query='%s', limit=%d, groupByConversation=%s, userId=%s",
                query.getQuery(), limit, groupByConversation, query.getUserId());

        List<FullTextSearchResult> ftsResults;
        try {
            ftsResults =
                    fullTextSearchRepository.adminSearch(
                            query.getQuery(),
                            limit + 1,
                            groupByConversation,
                            query.getUserId(),
                            query.isIncludeDeleted());
            LOG.infof("adminFullTextSearch: query returned %d raw results", ftsResults.size());
        } catch (Exception e) {
            LOG.warnf("adminFullTextSearch: search failed: %s", e.getMessage());
            return resultBuilder.emptyResults();
        }

        if (ftsResults.isEmpty()) {
            return resultBuilder.emptyResults();
        }

        List<SearchResultDto> resultsList = new ArrayList<>();
        for (FullTextSearchResult fts : ftsResults) {
            if (resultsList.size() >= limit) {
                break;
            }

            SearchResultDto dto =
                    resultBuilder.buildFromFullTextResult(
                            fts.entryId(),
                            fts.conversationId(),
                            fts.score(),
                            fts.highlight(),
                            includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        String nextCursor = null;
        if (ftsResults.size() > limit && !resultsList.isEmpty()) {
            nextCursor = resultsList.get(resultsList.size() - 1).getEntryId();
        }

        return resultBuilder.buildResultsFromList(resultsList, nextCursor);
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
