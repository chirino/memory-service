package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.config.MemoryStoreSelector;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.Collections;
import java.util.List;

/**
 * Shared logic for building {@link SearchResultDto} instances from search results.
 *
 * <p>Used by both {@link PgSearchStore} and {@link LangChain4jSearchStore} to avoid duplicating
 * entity fetching, decryption, and DTO assembly.
 */
@ApplicationScoped
public class SearchResultDtoBuilder {

    @Inject MemoryStoreSelector storeSelector;

    public SearchResultDto buildFromVectorResult(
            String entryId, String conversationId, double score, boolean includeEntry) {
        return storeSelector
                .getStore()
                .buildFromVectorResult(entryId, conversationId, score, includeEntry);
    }

    public SearchResultDto buildFromFullTextResult(
            String entryId,
            String conversationId,
            double score,
            String highlight,
            boolean includeEntry) {
        return storeSelector
                .getStore()
                .buildFromFullTextResult(entryId, conversationId, score, highlight, includeEntry);
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
}
