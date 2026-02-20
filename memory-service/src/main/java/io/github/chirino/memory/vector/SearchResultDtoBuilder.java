package io.github.chirino.memory.vector;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.persistence.entity.ConversationEntity;
import io.github.chirino.memory.persistence.entity.EntryEntity;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.EntryRepository;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.nio.charset.StandardCharsets;
import java.time.format.DateTimeFormatter;
import java.util.Collections;
import java.util.List;
import java.util.UUID;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import org.jboss.logging.Logger;

/**
 * Shared logic for building {@link SearchResultDto} instances from search results.
 *
 * <p>Used by both {@link PgSearchStore} and {@link LangChain4jSearchStore} to avoid duplicating
 * entity fetching, decryption, and DTO assembly.
 */
@ApplicationScoped
public class SearchResultDtoBuilder {

    private static final Logger LOG = Logger.getLogger(SearchResultDtoBuilder.class);
    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @Inject EntryRepository entryRepository;
    @Inject ConversationRepository conversationRepository;
    @Inject DataEncryptionService dataEncryptionService;
    @Inject ObjectMapper objectMapper;

    public SearchResultDto buildFromVectorResult(
            String entryId, String conversationId, double score, boolean includeEntry) {
        EntryEntity entry = entryRepository.findById(UUID.fromString(entryId));
        if (entry == null) {
            return null;
        }

        ConversationEntity conversation =
                conversationRepository.findById(UUID.fromString(conversationId));

        SearchResultDto dto = new SearchResultDto();
        dto.setEntryId(entryId);
        dto.setConversationId(conversationId);
        dto.setScore(score);

        if (conversation != null && conversation.getTitle() != null) {
            dto.setConversationTitle(decryptTitle(conversation.getTitle()));
        }

        String indexedContent = entry.getIndexedContent();
        if (indexedContent != null && !indexedContent.isBlank()) {
            dto.setHighlights(extractHighlight(indexedContent));
        }

        if (includeEntry) {
            dto.setEntry(toEntryDto(entry));
        }

        return dto;
    }

    public SearchResultDto buildFromFullTextResult(
            String entryId,
            String conversationId,
            double score,
            String highlight,
            boolean includeEntry) {
        EntryEntity entry = entryRepository.findById(UUID.fromString(entryId));
        if (entry == null) {
            return null;
        }

        ConversationEntity conversation =
                conversationRepository.findById(UUID.fromString(conversationId));

        SearchResultDto dto = new SearchResultDto();
        dto.setEntryId(entryId);
        dto.setConversationId(conversationId);
        dto.setScore(score);

        if (conversation != null && conversation.getTitle() != null) {
            dto.setConversationTitle(decryptTitle(conversation.getTitle()));
        }

        if (highlight != null && !highlight.isBlank()) {
            dto.setHighlights(highlight);
        }

        if (includeEntry) {
            dto.setEntry(toEntryDto(entry));
        }

        return dto;
    }

    public SearchResultsDto buildResultsFromList(List<SearchResultDto> results, String nextCursor) {
        SearchResultsDto dto = new SearchResultsDto();
        dto.setResults(results);
        dto.setAfterCursor(nextCursor);
        return dto;
    }

    public SearchResultsDto emptyResults() {
        SearchResultsDto result = new SearchResultsDto();
        result.setResults(Collections.emptyList());
        result.setAfterCursor(null);
        return result;
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
        int maxLength = 200;
        if (text.length() <= maxLength) {
            return text;
        }
        return text.substring(0, maxLength) + "...";
    }
}
