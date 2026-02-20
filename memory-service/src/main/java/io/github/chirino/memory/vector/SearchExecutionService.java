package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.config.FullTextSearchStoreSelector;
import io.github.chirino.memory.config.SearchStoreSelector;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.store.SearchTypeUnavailableException;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Locale;

/**
 * Resolves "auto" search type at the caller layer by orchestrating explicit semantic/full-text
 * calls against the active {@link VectorSearchStore}.
 */
@ApplicationScoped
public class SearchExecutionService {

    @Inject SearchStoreSelector searchStoreSelector;
    @Inject FullTextSearchStoreSelector fullTextSearchStoreSelector;

    public VectorSearchStore vectorSearchStore() {
        return searchStoreSelector.getSearchStore();
    }

    public FullTextSearchStore fullTextSearchStore() {
        return fullTextSearchStoreSelector.getFullTextSearchStore();
    }

    public boolean isSearchAvailable() {
        VectorSearchStore vectorStore = vectorSearchStore();
        FullTextSearchStore fullTextStore = fullTextSearchStore();
        return isSemanticAvailable(vectorStore) || isFullTextAvailable(fullTextStore);
    }

    public SearchResultsDto search(String userId, SearchEntriesRequest request) {
        VectorSearchStore vectorStore = vectorSearchStore();
        FullTextSearchStore fullTextStore = fullTextSearchStore();
        String searchType = normalizeSearchType(request.getSearchType());

        if ("semantic".equals(searchType)) {
            return searchSemantic(userId, request, vectorStore, fullTextStore);
        }
        if ("fulltext".equals(searchType)) {
            return searchFullText(userId, request, vectorStore, fullTextStore);
        }

        SearchResultsDto semantic =
                tryVectorAgentSearch(vectorStore, userId, copyWithSearchType(request, "semantic"));
        if (semantic != null && semantic.getResults() != null && !semantic.getResults().isEmpty()) {
            return semantic;
        }

        SearchResultsDto fullText =
                tryFullTextAgentSearch(
                        fullTextStore, userId, copyWithSearchType(request, "fulltext"));
        if (fullText != null && fullText.getResults() != null && !fullText.getResults().isEmpty()) {
            return fullText;
        }

        return emptyResults();
    }

    public SearchResultsDto adminSearch(AdminSearchQuery query) {
        VectorSearchStore vectorStore = vectorSearchStore();
        FullTextSearchStore fullTextStore = fullTextSearchStore();
        String searchType = normalizeSearchType(query.getSearchType());

        if ("semantic".equals(searchType)) {
            return adminSemanticSearch(query, vectorStore, fullTextStore);
        }
        if ("fulltext".equals(searchType)) {
            return adminFullTextSearch(query, vectorStore, fullTextStore);
        }

        SearchResultsDto semantic =
                tryVectorAdminSearch(vectorStore, copyWithSearchType(query, "semantic"));
        if (semantic != null && semantic.getResults() != null && !semantic.getResults().isEmpty()) {
            return semantic;
        }

        SearchResultsDto fullText =
                tryFullTextAdminSearch(fullTextStore, copyWithSearchType(query, "fulltext"));
        if (fullText != null && fullText.getResults() != null && !fullText.getResults().isEmpty()) {
            return fullText;
        }

        return emptyResults();
    }

    private SearchResultsDto searchSemantic(
            String userId,
            SearchEntriesRequest request,
            VectorSearchStore vectorStore,
            FullTextSearchStore fullTextStore) {
        if (!isSemanticAvailable(vectorStore)) {
            throw new SearchTypeUnavailableException(
                    "Semantic search is not available on this server.",
                    availableTypes(vectorStore, fullTextStore));
        }
        return vectorStore.search(userId, copyWithSearchType(request, "semantic"));
    }

    private SearchResultsDto searchFullText(
            String userId,
            SearchEntriesRequest request,
            VectorSearchStore vectorStore,
            FullTextSearchStore fullTextStore) {
        if (!isFullTextAvailable(fullTextStore)) {
            throw new SearchTypeUnavailableException(
                    "Full-text search is not available on this server.",
                    availableTypes(vectorStore, fullTextStore));
        }
        return fullTextStore.search(userId, copyWithSearchType(request, "fulltext"));
    }

    private SearchResultsDto adminSemanticSearch(
            AdminSearchQuery query,
            VectorSearchStore vectorStore,
            FullTextSearchStore fullTextStore) {
        if (!isSemanticAvailable(vectorStore)) {
            throw new SearchTypeUnavailableException(
                    "Semantic search is not available on this server.",
                    availableTypes(vectorStore, fullTextStore));
        }
        return vectorStore.adminSearch(copyWithSearchType(query, "semantic"));
    }

    private SearchResultsDto adminFullTextSearch(
            AdminSearchQuery query,
            VectorSearchStore vectorStore,
            FullTextSearchStore fullTextStore) {
        if (!isFullTextAvailable(fullTextStore)) {
            throw new SearchTypeUnavailableException(
                    "Full-text search is not available on this server.",
                    availableTypes(vectorStore, fullTextStore));
        }
        return fullTextStore.adminSearch(copyWithSearchType(query, "fulltext"));
    }

    private static boolean isSemanticAvailable(VectorSearchStore vectorStore) {
        return vectorStore != null
                && vectorStore.isEnabled()
                && vectorStore.isSemanticSearchAvailable();
    }

    private static boolean isFullTextAvailable(FullTextSearchStore fullTextStore) {
        return fullTextStore != null && fullTextStore.isFullTextSearchAvailable();
    }

    private static List<String> availableTypes(
            VectorSearchStore vectorStore, FullTextSearchStore fullTextStore) {
        List<String> available = new ArrayList<>(2);
        if (isSemanticAvailable(vectorStore)) {
            available.add("semantic");
        }
        if (isFullTextAvailable(fullTextStore)) {
            available.add("fulltext");
        }
        return available;
    }

    private static SearchResultsDto tryVectorAgentSearch(
            VectorSearchStore store, String userId, SearchEntriesRequest request) {
        if (!isSemanticAvailable(store)) {
            return null;
        }
        try {
            return store.search(userId, request);
        } catch (SearchTypeUnavailableException e) {
            return null;
        }
    }

    private static SearchResultsDto tryFullTextAgentSearch(
            FullTextSearchStore store, String userId, SearchEntriesRequest request) {
        if (!isFullTextAvailable(store)) {
            return null;
        }
        try {
            return store.search(userId, request);
        } catch (SearchTypeUnavailableException e) {
            return null;
        }
    }

    private static SearchResultsDto tryVectorAdminSearch(
            VectorSearchStore store, AdminSearchQuery query) {
        if (!isSemanticAvailable(store)) {
            return null;
        }
        try {
            return store.adminSearch(query);
        } catch (SearchTypeUnavailableException e) {
            return null;
        }
    }

    private static SearchResultsDto tryFullTextAdminSearch(
            FullTextSearchStore store, AdminSearchQuery query) {
        if (!isFullTextAvailable(store)) {
            return null;
        }
        try {
            return store.adminSearch(query);
        } catch (SearchTypeUnavailableException e) {
            return null;
        }
    }

    private static String normalizeSearchType(String raw) {
        if (raw == null || raw.isBlank()) {
            return "auto";
        }
        String value = raw.trim().toLowerCase(Locale.ROOT);
        if ("auto".equals(value) || "semantic".equals(value) || "fulltext".equals(value)) {
            return value;
        }
        throw new IllegalArgumentException(
                "Invalid searchType: " + raw + ". Valid values: auto, semantic, fulltext");
    }

    private static SearchEntriesRequest copyWithSearchType(
            SearchEntriesRequest request, String type) {
        SearchEntriesRequest copy = new SearchEntriesRequest();
        copy.setQuery(request.getQuery());
        copy.setSearchType(type);
        copy.setLimit(request.getLimit());
        copy.setAfterCursor(request.getAfterCursor());
        copy.setIncludeEntry(request.getIncludeEntry());
        copy.setGroupByConversation(request.getGroupByConversation());
        return copy;
    }

    private static AdminSearchQuery copyWithSearchType(AdminSearchQuery query, String type) {
        AdminSearchQuery copy = new AdminSearchQuery();
        copy.setQuery(query.getQuery());
        copy.setSearchType(type);
        copy.setLimit(query.getLimit());
        copy.setAfterCursor(query.getAfterCursor());
        copy.setIncludeEntry(query.getIncludeEntry());
        copy.setGroupByConversation(query.getGroupByConversation());
        copy.setUserId(query.getUserId());
        copy.setIncludeDeleted(query.isIncludeDeleted());
        return copy;
    }

    private static SearchResultsDto emptyResults() {
        SearchResultsDto empty = new SearchResultsDto();
        empty.setResults(Collections.emptyList());
        empty.setAfterCursor(null);
        return empty;
    }
}
