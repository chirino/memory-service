package io.github.chirino.memory.vector;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.mongo.model.MongoConversation;
import io.github.chirino.memory.mongo.model.MongoEntry;
import io.github.chirino.memory.mongo.repo.MongoConversationRepository;
import io.github.chirino.memory.mongo.repo.MongoEntryRepository;
import io.github.chirino.memory.mongo.repo.MongoFullTextSearchRepository;
import io.github.chirino.memory.mongo.repo.MongoFullTextSearchRepository.FullTextSearchResult;
import io.github.chirino.memory.store.SearchTypeUnavailableException;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.nio.charset.StandardCharsets;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * MongoDB-backed search store implementation.
 *
 * <p>Currently supports full-text search using MongoDB text indexes. Vector search (semantic) is
 * not yet implemented for MongoDB - it requires MongoDB Atlas Vector Search or similar.
 */
@ApplicationScoped
public class MongoSearchStore implements SearchStore {

    private static final Logger LOG = Logger.getLogger(MongoSearchStore.class);
    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @ConfigProperty(name = "memory-service.search.semantic.enabled", defaultValue = "true")
    boolean semanticSearchEnabled;

    @ConfigProperty(name = "memory-service.search.fulltext.enabled", defaultValue = "true")
    boolean fullTextSearchEnabled;

    @Inject MongoFullTextSearchRepository fullTextSearchRepository;
    @Inject MongoEntryRepository entryRepository;
    @Inject MongoConversationRepository conversationRepository;
    @Inject DataEncryptionService dataEncryptionService;
    @Inject ObjectMapper objectMapper;

    @Override
    public boolean isEnabled() {
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
                // MongoDB vector search not implemented yet - this will throw
                yield emptyResults();
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
        // MongoDB vector search is not implemented yet
        List<String> available = fullTextSearchEnabled ? List.of("fulltext") : List.of();
        throw new SearchTypeUnavailableException(
                "Semantic search is not available for MongoDB. MongoDB Atlas Vector Search is not"
                        + " yet implemented.",
                available);
    }

    private void validateFullTextSearchAvailable() {
        if (!fullTextSearchEnabled) {
            // Semantic search is also not available for MongoDB
            throw new SearchTypeUnavailableException(
                    "Full-text search is not available. The server is not configured with MongoDB"
                            + " text search support.",
                    List.of());
        }
    }

    private SearchResultsDto autoSearch(String userId, SearchEntriesRequest request) {
        // For MongoDB, semantic search is not available, go directly to full-text
        if (fullTextSearchEnabled) {
            SearchResultsDto results = fullTextSearch(userId, request);
            if (!results.getResults().isEmpty()) {
                return results;
            }
        }

        return emptyResults();
    }

    private SearchResultsDto fullTextSearch(String userId, SearchEntriesRequest request) {
        int limit = request.getLimit() != null ? request.getLimit() : 20;
        boolean groupByConversation =
                request.getGroupByConversation() == null || request.getGroupByConversation();
        boolean includeEntry = request.getIncludeEntry() == null || request.getIncludeEntry();

        List<FullTextSearchResult> ftsResults;
        try {
            ftsResults =
                    fullTextSearchRepository.search(
                            userId, request.getQuery(), limit + 1, groupByConversation);
        } catch (Exception e) {
            LOG.warnf("MongoDB full-text search failed: %s", e.getMessage());
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
            result.setNextCursor(resultsList.get(resultsList.size() - 1).getEntryId());
        }

        return result;
    }

    private SearchResultsDto emptyResults() {
        SearchResultsDto result = new SearchResultsDto();
        result.setResults(Collections.emptyList());
        result.setNextCursor(null);
        return result;
    }

    private SearchResultDto buildSearchResultDtoFromFts(
            FullTextSearchResult fts, boolean includeEntry) {
        // Fetch entry
        MongoEntry entry = entryRepository.findById(fts.entryId());
        if (entry == null) {
            return null;
        }

        // Fetch conversation for title
        MongoConversation conversation = conversationRepository.findById(fts.conversationId());

        SearchResultDto dto = new SearchResultDto();
        dto.setEntryId(fts.entryId());
        dto.setConversationId(fts.conversationId());
        dto.setScore(fts.score());

        // Decrypt and set conversation title
        if (conversation != null && conversation.title != null) {
            dto.setConversationTitle(decryptTitle(conversation.title));
        }

        // Use highlight from full-text search
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

    private EntryDto toEntryDto(MongoEntry entry) {
        EntryDto dto = new EntryDto();
        dto.setId(entry.id);
        dto.setConversationId(entry.conversationId);
        dto.setUserId(entry.userId);
        dto.setChannel(entry.channel);
        dto.setEpoch(entry.epoch);
        dto.setContentType(entry.contentType);

        List<Object> content = decryptContent(entry.content);
        if (content != null) {
            dto.setContent(content);
        }

        if (entry.createdAt != null) {
            dto.setCreatedAt(
                    ISO_FORMATTER.format(entry.createdAt.atOffset(java.time.ZoneOffset.UTC)));
        }

        return dto;
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
                // MongoDB vector search not implemented yet - this will throw
                yield emptyResults();
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
        // For MongoDB, semantic search is not available, go directly to full-text
        if (fullTextSearchEnabled) {
            SearchResultsDto results = adminFullTextSearch(query);
            if (!results.getResults().isEmpty()) {
                return results;
            }
        }

        return emptyResults();
    }

    private SearchResultsDto adminFullTextSearch(AdminSearchQuery query) {
        int limit = query.getLimit() != null ? query.getLimit() : 20;
        boolean groupByConversation =
                query.getGroupByConversation() == null || query.getGroupByConversation();
        boolean includeEntry = query.getIncludeEntry() == null || query.getIncludeEntry();

        List<FullTextSearchResult> ftsResults;
        try {
            ftsResults =
                    fullTextSearchRepository.adminSearch(
                            query.getQuery(),
                            limit + 1,
                            groupByConversation,
                            query.getUserId(),
                            query.isIncludeDeleted());
        } catch (Exception e) {
            LOG.warnf("MongoDB admin full-text search failed: %s", e.getMessage());
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
            result.setNextCursor(resultsList.get(resultsList.size() - 1).getEntryId());
        }

        return result;
    }

    @Override
    public void upsertTranscriptEmbedding(
            String conversationGroupId, String conversationId, String entryId, float[] embedding) {
        // no-op until MongoDB Atlas Vector Search is implemented
    }

    @Override
    public void deleteByConversationGroupId(String conversationGroupId) {
        // no-op until MongoDB Atlas Vector Search is implemented
    }
}
