package io.github.chirino.memory.vector;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.persistence.entity.ConversationEntity;
import io.github.chirino.memory.persistence.entity.EntryEntity;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.EntryRepository;
import io.github.chirino.memory.store.SearchTypeUnavailableException;
import io.github.chirino.memory.vector.FullTextSearchRepository.FullTextSearchResult;
import io.github.chirino.memory.vector.PgVectorEmbeddingRepository.VectorSearchResult;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.nio.charset.StandardCharsets;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Locale;
import java.util.UUID;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * PgVector-backed search store implementation.
 *
 * <p>Performs semantic search using pgvector's cosine similarity operator on stored embeddings.
 * Access control is enforced via JOIN with conversation_memberships table.
 */
@ApplicationScoped
public class PgSearchStore implements SearchStore {

    private static final Logger LOG = Logger.getLogger(PgSearchStore.class);
    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @ConfigProperty(name = "memory-service.search.semantic.enabled", defaultValue = "true")
    boolean semanticSearchEnabled;

    @ConfigProperty(name = "memory-service.search.fulltext.enabled", defaultValue = "true")
    boolean fullTextSearchEnabled;

    @Inject PgVectorEmbeddingRepository embeddingRepository;
    @Inject FullTextSearchRepository fullTextSearchRepository;
    @Inject EmbeddingService embeddingService;
    @Inject EntryRepository entryRepository;
    @Inject ConversationRepository conversationRepository;
    @Inject DataEncryptionService dataEncryptionService;
    @Inject ObjectMapper objectMapper;

    @Override
    public boolean isEnabled() {
        // Always enabled - falls back to keyword search when embeddings are not available
        return true;
    }

    @Override
    public SearchResultsDto search(String userId, SearchEntriesRequest request) {
        if (request.getQuery() == null || request.getQuery().isBlank()) {
            return emptyResults();
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
            case "auto" -> autoSearch(userId, request);
            default ->
                    throw new IllegalArgumentException(
                            "Invalid searchType: "
                                    + searchType
                                    + ". Valid values: auto, semantic, fulltext");
        };
    }

    private void validateSemanticSearchAvailable() {
        if (!semanticSearchEnabled || !embeddingService.isEnabled()) {
            List<String> available = fullTextSearchEnabled ? List.of("fulltext") : List.of();
            throw new SearchTypeUnavailableException(
                    "Semantic search is not available. The embedding service is disabled on this"
                            + " server.",
                    available);
        }
    }

    private void validateFullTextSearchAvailable() {
        if (!fullTextSearchEnabled) {
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

    private SearchResultsDto autoSearch(String userId, SearchEntriesRequest request) {
        // Try semantic first if available
        if (semanticSearchEnabled && embeddingService.isEnabled()) {
            LOG.infof("autoSearch: attempting semantic search for query '%s'", request.getQuery());
            SearchResultsDto results = semanticSearch(userId, request);
            if (!results.getResults().isEmpty()) {
                LOG.infof(
                        "autoSearch: semantic search returned %d results",
                        results.getResults().size());
                return results;
            }
            LOG.info("autoSearch: semantic search returned no results");
        } else {
            LOG.infof(
                    "autoSearch: semantic search skipped (enabled=%s, embeddingService.enabled=%s)",
                    semanticSearchEnabled, embeddingService.isEnabled());
        }

        // Fall back to full-text if available
        if (fullTextSearchEnabled) {
            LOG.infof("autoSearch: attempting full-text search for query '%s'", request.getQuery());
            SearchResultsDto results = fullTextSearch(userId, request);
            if (!results.getResults().isEmpty()) {
                LOG.infof(
                        "autoSearch: full-text search returned %d results",
                        results.getResults().size());
                return results;
            }
            LOG.info("autoSearch: full-text search returned no results");
        } else {
            LOG.info("autoSearch: full-text search skipped (disabled)");
        }

        LOG.info("autoSearch: no results from any search method");
        return emptyResults();
    }

    private SearchResultsDto semanticSearch(String userId, SearchEntriesRequest request) {
        // Embed the query
        float[] queryEmbedding = embeddingService.embed(request.getQuery());
        if (queryEmbedding == null || queryEmbedding.length == 0) {
            LOG.warn("semanticSearch: failed to embed query");
            return emptyResults();
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
            return emptyResults();
        }

        if (vectorResults.isEmpty()) {
            return emptyResults();
        }

        // Build result DTOs with entry details
        List<SearchResultDto> resultsList = new ArrayList<>();
        for (VectorSearchResult vr : vectorResults) {
            if (resultsList.size() >= limit) {
                break;
            }

            SearchResultDto dto = buildSearchResultDto(vr, includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        SearchResultsDto result = new SearchResultsDto();
        result.setResults(resultsList);

        // Determine next cursor
        if (vectorResults.size() > limit && !resultsList.isEmpty()) {
            result.setAfterCursor(resultsList.get(resultsList.size() - 1).getEntryId());
        }

        return result;
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
            return emptyResults();
        }

        if (ftsResults.isEmpty()) {
            return emptyResults();
        }

        List<SearchResultDto> resultsList = new ArrayList<>();
        for (FullTextSearchResult fts : ftsResults) {
            if (resultsList.size() >= limit) {
                break;
            }

            SearchResultDto dto = buildSearchResultDtoFromFts(fts, includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        SearchResultsDto result = new SearchResultsDto();
        result.setResults(resultsList);

        if (ftsResults.size() > limit && !resultsList.isEmpty()) {
            result.setAfterCursor(resultsList.get(resultsList.size() - 1).getEntryId());
        }

        return result;
    }

    private SearchResultsDto emptyResults() {
        SearchResultsDto result = new SearchResultsDto();
        result.setResults(Collections.emptyList());
        result.setAfterCursor(null);
        return result;
    }

    private SearchResultDto buildSearchResultDto(VectorSearchResult vr, boolean includeEntry) {
        // Fetch entry
        EntryEntity entry = entryRepository.findById(UUID.fromString(vr.entryId()));
        if (entry == null) {
            return null;
        }

        // Fetch conversation for title
        ConversationEntity conversation =
                conversationRepository.findById(UUID.fromString(vr.conversationId()));

        SearchResultDto dto = new SearchResultDto();
        dto.setEntryId(vr.entryId());
        dto.setConversationId(vr.conversationId());
        dto.setScore(vr.score());

        // Decrypt and set conversation title
        if (conversation != null && conversation.getTitle() != null) {
            dto.setConversationTitle(decryptTitle(conversation.getTitle()));
        }

        // Generate highlights from indexed content
        String indexedContent = entry.getIndexedContent();
        if (indexedContent != null && !indexedContent.isBlank()) {
            dto.setHighlights(extractHighlight(indexedContent));
        }

        // Include full entry if requested
        if (includeEntry) {
            dto.setEntry(toEntryDto(entry));
        }

        return dto;
    }

    private SearchResultDto buildSearchResultDtoFromFts(
            FullTextSearchResult fts, boolean includeEntry) {
        // Fetch entry
        EntryEntity entry = entryRepository.findById(UUID.fromString(fts.entryId()));
        if (entry == null) {
            return null;
        }

        // Fetch conversation for title
        ConversationEntity conversation =
                conversationRepository.findById(UUID.fromString(fts.conversationId()));

        SearchResultDto dto = new SearchResultDto();
        dto.setEntryId(fts.entryId());
        dto.setConversationId(fts.conversationId());
        dto.setScore(fts.score());

        // Decrypt and set conversation title
        if (conversation != null && conversation.getTitle() != null) {
            dto.setConversationTitle(decryptTitle(conversation.getTitle()));
        }

        // Use ts_headline highlight from full-text search
        if (fts.highlight() != null && !fts.highlight().isBlank()) {
            dto.setHighlights(fts.highlight());
        }

        // Include full entry if requested
        if (includeEntry) {
            dto.setEntry(toEntryDto(entry));
        }

        return dto;
    }

    private String decryptTitle(byte[] encryptedTitle) {
        if (encryptedTitle == null) {
            return null;
        }
        byte[] plain = dataEncryptionService.decrypt(encryptedTitle);
        return new String(plain, StandardCharsets.UTF_8);
    }

    @SuppressWarnings("unchecked")
    private List<Object> decryptContent(byte[] content) {
        if (content == null) {
            return null;
        }
        byte[] plain = dataEncryptionService.decrypt(content);
        try {
            return objectMapper.readValue(plain, List.class);
        } catch (Exception e) {
            LOG.warnf("Failed to deserialize entry content: %s", e.getMessage());
            return null;
        }
    }

    private EntryDto toEntryDto(EntryEntity entry) {
        EntryDto dto = new EntryDto();
        dto.setId(entry.getId().toString());
        dto.setConversationId(entry.getConversation().getId().toString());
        dto.setUserId(entry.getUserId());
        dto.setChannel(entry.getChannel());
        dto.setEpoch(entry.getEpoch());
        dto.setContentType(entry.getContentType());

        List<Object> content = decryptContent(entry.getContent());
        if (content != null) {
            dto.setContent(content);
        }

        if (entry.getCreatedAt() != null) {
            dto.setCreatedAt(ISO_FORMATTER.format(entry.getCreatedAt()));
        }

        return dto;
    }

    private String extractHighlight(String text) {
        if (text == null || text.isBlank()) {
            return null;
        }
        // Return first 200 characters as highlight
        int maxLength = 200;
        if (text.length() <= maxLength) {
            return text;
        }
        return text.substring(0, maxLength) + "...";
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
            return emptyResults();
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
            case "auto" -> adminAutoSearch(query);
            default ->
                    throw new IllegalArgumentException(
                            "Invalid searchType: "
                                    + searchType
                                    + ". Valid values: auto, semantic, fulltext");
        };
    }

    private SearchResultsDto adminAutoSearch(AdminSearchQuery query) {
        // Try semantic first if available
        if (semanticSearchEnabled && embeddingService.isEnabled()) {
            LOG.infof(
                    "adminAutoSearch: attempting semantic search for query '%s'", query.getQuery());
            SearchResultsDto results = adminSemanticSearch(query);
            if (!results.getResults().isEmpty()) {
                LOG.infof(
                        "adminAutoSearch: semantic search returned %d results",
                        results.getResults().size());
                return results;
            }
            LOG.info("adminAutoSearch: semantic search returned no results");
        } else {
            LOG.infof(
                    "adminAutoSearch: semantic search skipped (enabled=%s,"
                            + " embeddingService.enabled=%s)",
                    semanticSearchEnabled, embeddingService.isEnabled());
        }

        // Fall back to full-text if available
        if (fullTextSearchEnabled) {
            LOG.infof(
                    "adminAutoSearch: attempting full-text search for query '%s'",
                    query.getQuery());
            SearchResultsDto results = adminFullTextSearch(query);
            if (!results.getResults().isEmpty()) {
                LOG.infof(
                        "adminAutoSearch: full-text search returned %d results",
                        results.getResults().size());
                return results;
            }
            LOG.info("adminAutoSearch: full-text search returned no results");
        } else {
            LOG.info("adminAutoSearch: full-text search skipped (disabled)");
        }

        LOG.info("adminAutoSearch: no results from any search method");
        return emptyResults();
    }

    private SearchResultsDto adminSemanticSearch(AdminSearchQuery query) {
        float[] queryEmbedding = embeddingService.embed(query.getQuery());
        if (queryEmbedding == null || queryEmbedding.length == 0) {
            LOG.warn("adminSemanticSearch: failed to embed query");
            return emptyResults();
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
            return emptyResults();
        }

        if (vectorResults.isEmpty()) {
            return emptyResults();
        }

        List<SearchResultDto> resultsList = new ArrayList<>();
        for (VectorSearchResult vr : vectorResults) {
            if (resultsList.size() >= limit) {
                break;
            }

            SearchResultDto dto = buildSearchResultDto(vr, includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        SearchResultsDto result = new SearchResultsDto();
        result.setResults(resultsList);

        if (vectorResults.size() > limit && !resultsList.isEmpty()) {
            result.setAfterCursor(resultsList.get(resultsList.size() - 1).getEntryId());
        }

        return result;
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
            return emptyResults();
        }

        if (ftsResults.isEmpty()) {
            return emptyResults();
        }

        List<SearchResultDto> resultsList = new ArrayList<>();
        for (FullTextSearchResult fts : ftsResults) {
            if (resultsList.size() >= limit) {
                break;
            }

            SearchResultDto dto = buildSearchResultDtoFromFts(fts, includeEntry);
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        SearchResultsDto result = new SearchResultsDto();
        result.setResults(resultsList);

        if (ftsResults.size() > limit && !resultsList.isEmpty()) {
            result.setAfterCursor(resultsList.get(resultsList.size() - 1).getEntryId());
        }

        return result;
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
