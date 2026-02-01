package io.github.chirino.memory.vector;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.persistence.entity.ConversationEntity;
import io.github.chirino.memory.persistence.entity.EntryEntity;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.EntryRepository;
import io.github.chirino.memory.store.impl.PostgresMemoryStore;
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
import org.jboss.logging.Logger;

/**
 * PgVector-backed vector store implementation.
 *
 * <p>Performs semantic search using pgvector's cosine similarity operator on stored embeddings.
 * Access control is enforced via JOIN with conversation_memberships table.
 */
@ApplicationScoped
public class PgVectorStore implements VectorStore {

    private static final Logger LOG = Logger.getLogger(PgVectorStore.class);
    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @Inject PostgresMemoryStore postgresMemoryStore;
    @Inject PgVectorEmbeddingRepository embeddingRepository;
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
        SearchResultsDto result = new SearchResultsDto();
        result.setResults(Collections.emptyList());
        result.setNextCursor(null);

        if (request.getQuery() == null || request.getQuery().isBlank()) {
            return result;
        }

        // Embed the query
        float[] queryEmbedding = embeddingService.embed(request.getQuery());
        if (queryEmbedding == null || queryEmbedding.length == 0) {
            LOG.warn("Failed to embed query, falling back to keyword search");
            return postgresMemoryStore.searchEntries(userId, request);
        }

        int limit = request.getLimit() != null ? request.getLimit() : 20;
        boolean groupByConversation =
                request.getGroupByConversation() == null || request.getGroupByConversation();
        boolean includeEntry = request.getIncludeEntry() == null || request.getIncludeEntry();

        // Perform vector search - fetch one extra to determine if there's a next page
        List<VectorSearchResult> vectorResults;
        try {
            vectorResults =
                    embeddingRepository.searchSimilar(
                            userId,
                            toPgVectorLiteral(queryEmbedding),
                            limit + 1,
                            groupByConversation);
        } catch (Exception e) {
            LOG.warnf("Vector search failed, falling back to keyword search: %s", e.getMessage());
            return postgresMemoryStore.searchEntries(userId, request);
        }

        // Fall back to keyword search if vector search returns no results
        // This handles cases where embeddings haven't been stored yet (e.g., inline indexedContent)
        if (vectorResults.isEmpty()) {
            LOG.debug("Vector search returned no results, falling back to keyword search");
            return postgresMemoryStore.searchEntries(userId, request);
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

        // Determine next cursor
        if (vectorResults.size() > limit && !resultsList.isEmpty()) {
            result.setNextCursor(resultsList.get(resultsList.size() - 1).getEntryId());
        }

        result.setResults(resultsList);
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
                entryId, conversationId, conversationGroupId, toPgVectorLiteral(embedding));
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
