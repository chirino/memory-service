package io.github.chirino.memory.vector;

import dev.langchain4j.data.embedding.Embedding;
import dev.langchain4j.data.segment.TextSegment;
import dev.langchain4j.store.embedding.EmbeddingSearchRequest;
import dev.langchain4j.store.embedding.EmbeddingSearchResult;
import dev.langchain4j.store.embedding.EmbeddingStore;
import dev.langchain4j.store.embedding.filter.Filter;
import dev.langchain4j.store.embedding.filter.comparison.IsEqualTo;
import dev.langchain4j.store.embedding.filter.comparison.IsIn;
import dev.langchain4j.store.embedding.filter.logical.And;
import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.store.SearchTypeUnavailableException;
import io.github.chirino.memory.vector.FullTextSearchRepository.FullTextSearchResult;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Generic LangChain4j {@link EmbeddingStore}-backed search store.
 *
 * <p>Adapts any LangChain4j {@code EmbeddingStore<TextSegment>} to the {@link VectorSearchStore}
 * interface. The concrete {@code EmbeddingStore} (Qdrant, Chroma, Milvus, etc.) is produced by
 * {@link EmbeddingStoreProducer} and injected via CDI.
 *
 * <p>Access control is enforced via a two-step process: first, the user's allowed conversation
 * group IDs are fetched from the active datastore, then these IDs are passed as a metadata filter
 * to the vector store.
 */
@ApplicationScoped
public class LangChain4jSearchStore implements VectorSearchStore {

    private static final Logger LOG = Logger.getLogger(LangChain4jSearchStore.class);
    private static final String SEGMENT_PLACEHOLDER_TEXT = "[indexed-content]";

    @Inject EmbeddingStore<TextSegment> embeddingStore;
    @Inject EmbeddingService embeddingService;
    @Inject SearchResultDtoBuilder resultBuilder;
    @Inject ConversationMembershipRepository membershipRepository;
    @Inject MongoConversationMembershipRepository mongoMembershipRepository;

    // Optional full-text search (available when datastore is Postgres)
    @Inject Instance<FullTextSearchRepository> fullTextSearchRepository;

    @ConfigProperty(name = "memory-service.search.semantic.enabled", defaultValue = "true")
    boolean semanticSearchEnabled;

    @ConfigProperty(name = "memory-service.search.fulltext.enabled", defaultValue = "true")
    boolean fullTextSearchEnabled;

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Override
    public boolean isEnabled() {
        return true;
    }

    @Override
    public boolean isSemanticSearchAvailable() {
        return semanticSearchEnabled && embeddingService.isEnabled();
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

    @Override
    public void upsertTranscriptEmbedding(
            String conversationGroupId, String conversationId, String entryId, float[] embedding) {
        if (embedding == null || embedding.length == 0) {
            return;
        }

        try {
            TextSegment segment =
                    TextSegment.from(
                            SEGMENT_PLACEHOLDER_TEXT,
                            dev.langchain4j.data.document.Metadata.from(
                                    Map.of(
                                            "conversation_id",
                                            conversationId,
                                            "conversation_group_id",
                                            conversationGroupId,
                                            "model",
                                            embeddingService.modelId())));

            embeddingStore.addAll(
                    List.of(entryId), List.of(Embedding.from(embedding)), List.of(segment));
        } catch (Exception e) {
            LOG.warnf(
                    e,
                    "LangChain4j embedding upsert failed: entryId=%s, conversationId=%s,"
                            + " conversationGroupId=%s, model=%s, dimensions=%d",
                    entryId,
                    conversationId,
                    conversationGroupId,
                    embeddingService.modelId(),
                    embedding.length);
            throw e;
        }

        LOG.infof(
                "Embedding stored via LangChain4j: entryId=%s, conversationId=%s, model=%s,"
                        + " dimensions=%d",
                entryId, conversationId, embeddingService.modelId(), embedding.length);
    }

    @Override
    public void deleteByConversationGroupId(String conversationGroupId) {
        try {
            embeddingStore.removeAll(new IsEqualTo("conversation_group_id", conversationGroupId));
        } catch (Exception e) {
            LOG.debugf(
                    "Could not delete embeddings for group %s: %s",
                    conversationGroupId, e.getMessage());
        }
    }

    // -- Private helpers --

    private void validateSemanticSearchAvailable() {
        if (!isSemanticSearchAvailable()) {
            List<String> available = isFullTextAvailable() ? List.of("fulltext") : List.of();
            throw new SearchTypeUnavailableException(
                    "Semantic search is not available. The embedding service is disabled on this"
                            + " server.",
                    available);
        }
    }

    private void validateFullTextSearchAvailable() {
        if (!isFullTextAvailable()) {
            List<String> available =
                    (semanticSearchEnabled && embeddingService.isEnabled())
                            ? List.of("semantic")
                            : List.of();
            throw new SearchTypeUnavailableException(
                    "Full-text search is not available. The server is not configured with"
                            + " PostgreSQL full-text search support.",
                    available);
        }
    }

    private boolean isFullTextAvailable() {
        return fullTextSearchEnabled
                && isPostgresDatastore()
                && fullTextSearchRepository.isResolvable();
    }

    private SearchResultsDto semanticSearch(String userId, SearchEntriesRequest request) {
        float[] queryVector = embeddingService.embed(request.getQuery());
        if (queryVector == null || queryVector.length == 0) {
            return resultBuilder.emptyResults();
        }

        List<String> allowedGroupIds = getAllowedGroupIds(userId);
        if (allowedGroupIds.isEmpty()) {
            return resultBuilder.emptyResults();
        }

        int limit = request.getLimit() != null ? request.getLimit() : 20;
        boolean groupByConversation =
                request.getGroupByConversation() == null || request.getGroupByConversation();
        boolean includeEntry = request.getIncludeEntry() == null || request.getIncludeEntry();

        Filter filter =
                new And(
                        new IsIn("conversation_group_id", allowedGroupIds),
                        new IsEqualTo("model", embeddingService.modelId()));

        int fetchLimit = groupByConversation ? limit * 3 : limit + 1;

        EmbeddingSearchRequest searchRequest =
                EmbeddingSearchRequest.builder()
                        .queryEmbedding(Embedding.from(queryVector))
                        .filter(filter)
                        .maxResults(fetchLimit)
                        .build();

        EmbeddingSearchResult<TextSegment> searchResult = embeddingStore.search(searchRequest);

        return buildSearchResults(searchResult.matches(), limit, groupByConversation, includeEntry);
    }

    private SearchResultsDto fullTextSearch(String userId, SearchEntriesRequest request) {
        if (!fullTextSearchRepository.isResolvable()) {
            return resultBuilder.emptyResults();
        }

        int limit = request.getLimit() != null ? request.getLimit() : 20;
        boolean groupByConversation =
                request.getGroupByConversation() == null || request.getGroupByConversation();
        boolean includeEntry = request.getIncludeEntry() == null || request.getIncludeEntry();

        List<FullTextSearchResult> ftsResults;
        try {
            ftsResults =
                    fullTextSearchRepository
                            .get()
                            .search(userId, request.getQuery(), limit + 1, groupByConversation);
        } catch (Exception e) {
            LOG.warnf("fullTextSearch: search failed: %s", e.getMessage());
            return resultBuilder.emptyResults();
        }

        return buildFullTextResults(ftsResults, limit, includeEntry);
    }

    private SearchResultsDto adminSemanticSearch(AdminSearchQuery query) {
        float[] queryVector = embeddingService.embed(query.getQuery());
        if (queryVector == null || queryVector.length == 0) {
            return resultBuilder.emptyResults();
        }

        int limit = query.getLimit() != null ? query.getLimit() : 20;
        boolean groupByConversation =
                query.getGroupByConversation() == null || query.getGroupByConversation();
        boolean includeEntry = query.getIncludeEntry() == null || query.getIncludeEntry();

        // Build filter: model is always required
        Filter filter;
        if (query.getUserId() != null && !query.getUserId().isBlank()) {
            // With userId filter: pre-query the user's conversation group IDs
            List<String> userGroupIds = getAllowedGroupIds(query.getUserId());
            if (userGroupIds.isEmpty()) {
                return resultBuilder.emptyResults();
            }
            filter =
                    new And(
                            new IsIn("conversation_group_id", userGroupIds),
                            new IsEqualTo("model", embeddingService.modelId()));
        } else {
            // No userId filter: search with model filter only
            filter = new IsEqualTo("model", embeddingService.modelId());
        }

        int fetchLimit = groupByConversation ? limit * 3 : limit + 1;

        EmbeddingSearchRequest searchRequest =
                EmbeddingSearchRequest.builder()
                        .queryEmbedding(Embedding.from(queryVector))
                        .filter(filter)
                        .maxResults(fetchLimit)
                        .build();

        EmbeddingSearchResult<TextSegment> searchResult = embeddingStore.search(searchRequest);

        return buildSearchResults(searchResult.matches(), limit, groupByConversation, includeEntry);
    }

    private SearchResultsDto adminFullTextSearch(AdminSearchQuery query) {
        if (!fullTextSearchRepository.isResolvable()) {
            return resultBuilder.emptyResults();
        }

        int limit = query.getLimit() != null ? query.getLimit() : 20;
        boolean groupByConversation =
                query.getGroupByConversation() == null || query.getGroupByConversation();
        boolean includeEntry = query.getIncludeEntry() == null || query.getIncludeEntry();

        List<FullTextSearchResult> ftsResults;
        try {
            ftsResults =
                    fullTextSearchRepository
                            .get()
                            .adminSearch(
                                    query.getQuery(),
                                    limit + 1,
                                    groupByConversation,
                                    query.getUserId(),
                                    query.isIncludeDeleted());
        } catch (Exception e) {
            LOG.warnf("adminFullTextSearch: search failed: %s", e.getMessage());
            return resultBuilder.emptyResults();
        }

        return buildFullTextResults(ftsResults, limit, includeEntry);
    }

    private List<String> getAllowedGroupIds(String userId) {
        String ds = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        if ("mongo".equals(ds) || "mongodb".equals(ds)) {
            return mongoMembershipRepository.listConversationGroupIdsForUser(userId);
        }
        return membershipRepository.listConversationGroupIdsForUser(userId);
    }

    private boolean isPostgresDatastore() {
        String ds = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        return !("mongo".equals(ds) || "mongodb".equals(ds));
    }

    private SearchResultsDto buildSearchResults(
            List<dev.langchain4j.store.embedding.EmbeddingMatch<TextSegment>> matches,
            int limit,
            boolean groupByConversation,
            boolean includeEntry) {

        List<dev.langchain4j.store.embedding.EmbeddingMatch<TextSegment>> effective = matches;
        if (groupByConversation) {
            effective = groupByConversation(matches, limit);
        }

        List<SearchResultDto> resultsList = new ArrayList<>();
        for (var match : effective) {
            if (resultsList.size() >= limit) {
                break;
            }

            String entryId = match.embeddingId();
            String conversationId = match.embedded().metadata().getString("conversation_id");
            double score = match.score();

            SearchResultDto dto =
                    resultBuilder.buildFromVectorResult(
                            entryId, conversationId, score, includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        // Next cursor: if we got more matches than limit (non-grouped) or enough grouped results
        String nextCursor = null;
        if (!groupByConversation && matches.size() > limit && !resultsList.isEmpty()) {
            nextCursor = resultsList.get(resultsList.size() - 1).getEntryId();
        }

        return resultBuilder.buildResultsFromList(resultsList, nextCursor);
    }

    private List<dev.langchain4j.store.embedding.EmbeddingMatch<TextSegment>> groupByConversation(
            List<dev.langchain4j.store.embedding.EmbeddingMatch<TextSegment>> matches, int limit) {
        Map<String, dev.langchain4j.store.embedding.EmbeddingMatch<TextSegment>>
                bestPerConversation = new LinkedHashMap<>();
        for (var match : matches) {
            String convId = match.embedded().metadata().getString("conversation_id");
            bestPerConversation.putIfAbsent(convId, match); // first = highest score
        }
        return bestPerConversation.values().stream().limit(limit).toList();
    }

    private SearchResultsDto buildFullTextResults(
            List<FullTextSearchResult> ftsResults, int limit, boolean includeEntry) {
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
}
