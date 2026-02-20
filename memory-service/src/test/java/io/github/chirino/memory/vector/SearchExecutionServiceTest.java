package io.github.chirino.memory.vector;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.store.SearchTypeUnavailableException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class SearchExecutionServiceTest {

    @Test
    void auto_uses_semantic_when_it_has_results() {
        ScriptedVectorStore vectorStore = new ScriptedVectorStore();
        vectorStore.resultsByType.put("semantic", resultsWithCount(1));

        SearchExecutionService service = serviceFor(vectorStore, null);
        SearchEntriesRequest request = request("auto");

        SearchResultsDto result = service.search("alice", request);

        assertEquals(1, result.getResults().size());
        assertEquals(List.of("semantic"), vectorStore.agentCalls);
    }

    @Test
    void auto_falls_back_to_fulltext_when_semantic_is_empty() {
        ScriptedVectorStore vectorStore = new ScriptedVectorStore();
        vectorStore.resultsByType.put("semantic", resultsWithCount(0));

        ScriptedFullTextStore fullTextStore = new ScriptedFullTextStore();
        fullTextStore.resultsByType.put("fulltext", resultsWithCount(2));

        SearchExecutionService service = serviceFor(vectorStore, fullTextStore);
        SearchEntriesRequest request = request(null);

        SearchResultsDto result = service.search("alice", request);

        assertEquals(2, result.getResults().size());
        assertEquals(List.of("semantic"), vectorStore.agentCalls);
        assertEquals(List.of("fulltext"), fullTextStore.agentCalls);
    }

    @Test
    void auto_falls_back_when_semantic_is_unavailable() {
        ScriptedVectorStore vectorStore = new ScriptedVectorStore();
        vectorStore.semanticAvailable = false;

        ScriptedFullTextStore fullTextStore = new ScriptedFullTextStore();
        fullTextStore.resultsByType.put("fulltext", resultsWithCount(1));

        SearchExecutionService service = serviceFor(vectorStore, fullTextStore);
        SearchEntriesRequest request = request("auto");

        SearchResultsDto result = service.search("alice", request);

        assertEquals(1, result.getResults().size());
        assertTrue(vectorStore.agentCalls.isEmpty());
        assertEquals(List.of("fulltext"), fullTextStore.agentCalls);
    }

    @Test
    void auto_returns_empty_when_both_types_are_unavailable() {
        ScriptedVectorStore vectorStore = new ScriptedVectorStore();
        vectorStore.semanticAvailable = false;

        ScriptedFullTextStore fullTextStore = new ScriptedFullTextStore();
        fullTextStore.fullTextAvailable = false;

        SearchExecutionService service = serviceFor(vectorStore, fullTextStore);
        SearchEntriesRequest request = request("auto");

        SearchResultsDto result = service.search("alice", request);

        assertTrue(result.getResults().isEmpty());
        assertTrue(vectorStore.agentCalls.isEmpty());
        assertTrue(fullTextStore.agentCalls.isEmpty());
    }

    @Test
    void explicit_search_type_is_passed_through_without_fallback() {
        ScriptedVectorStore vectorStore = new ScriptedVectorStore();
        vectorStore.resultsByType.put("semantic", resultsWithCount(1));

        ScriptedFullTextStore fullTextStore = new ScriptedFullTextStore();
        fullTextStore.resultsByType.put("fulltext", resultsWithCount(4));

        SearchExecutionService service = serviceFor(vectorStore, fullTextStore);
        SearchEntriesRequest request = request("semantic");

        SearchResultsDto result = service.search("alice", request);

        assertEquals(1, result.getResults().size());
        assertEquals(List.of("semantic"), vectorStore.agentCalls);
        assertTrue(fullTextStore.agentCalls.isEmpty());
    }

    @Test
    void admin_auto_uses_same_fallback_flow() {
        ScriptedVectorStore vectorStore = new ScriptedVectorStore();
        vectorStore.adminResultsByType.put("semantic", resultsWithCount(0));

        ScriptedFullTextStore fullTextStore = new ScriptedFullTextStore();
        fullTextStore.adminResultsByType.put("fulltext", resultsWithCount(3));

        SearchExecutionService service = serviceFor(vectorStore, fullTextStore);
        AdminSearchQuery query = adminQuery("auto");

        SearchResultsDto result = service.adminSearch(query);

        assertEquals(3, result.getResults().size());
        assertEquals(List.of("semantic"), vectorStore.adminCalls);
        assertEquals(List.of("fulltext"), fullTextStore.adminCalls);
    }

    @Test
    void invalid_search_type_fails_fast() {
        SearchExecutionService service = serviceFor(new ScriptedVectorStore(), null);
        SearchEntriesRequest request = request("bogus");

        IllegalArgumentException error =
                assertThrows(
                        IllegalArgumentException.class, () -> service.search("alice", request));
        assertTrue(error.getMessage().contains("Invalid searchType"));
    }

    @Test
    void explicit_semantic_throws_unavailable_when_no_vector_store() {
        SearchExecutionService service = serviceFor(null, new ScriptedFullTextStore());
        SearchEntriesRequest request = request("semantic");

        SearchTypeUnavailableException error =
                assertThrows(
                        SearchTypeUnavailableException.class,
                        () -> service.search("alice", request));
        assertEquals(List.of("fulltext"), error.getAvailableTypes());
    }

    private static SearchExecutionService serviceFor(
            VectorSearchStore vectorStore, FullTextSearchStore fullTextStore) {
        return new SearchExecutionService() {
            @Override
            public VectorSearchStore vectorSearchStore() {
                return vectorStore;
            }

            @Override
            public FullTextSearchStore fullTextSearchStore() {
                return fullTextStore;
            }
        };
    }

    private static SearchEntriesRequest request(String searchType) {
        SearchEntriesRequest request = new SearchEntriesRequest();
        request.setQuery("customers");
        request.setSearchType(searchType);
        request.setLimit(20);
        return request;
    }

    private static AdminSearchQuery adminQuery(String searchType) {
        AdminSearchQuery query = new AdminSearchQuery();
        query.setQuery("customers");
        query.setSearchType(searchType);
        query.setLimit(20);
        return query;
    }

    private static SearchResultsDto resultsWithCount(int count) {
        SearchResultsDto results = new SearchResultsDto();
        List<SearchResultDto> entries = new ArrayList<>();
        for (int i = 0; i < count; i++) {
            SearchResultDto dto = new SearchResultDto();
            dto.setEntryId("00000000-0000-0000-0000-00000000000" + i);
            entries.add(dto);
        }
        results.setResults(entries);
        results.setAfterCursor(null);
        return results;
    }

    private static final class ScriptedVectorStore implements VectorSearchStore {
        private final Map<String, SearchResultsDto> resultsByType = new HashMap<>();
        private final Map<String, SearchResultsDto> adminResultsByType = new HashMap<>();
        private final List<String> agentCalls = new ArrayList<>();
        private final List<String> adminCalls = new ArrayList<>();
        private boolean semanticAvailable = true;

        @Override
        public boolean isEnabled() {
            return true;
        }

        @Override
        public boolean isSemanticSearchAvailable() {
            return semanticAvailable;
        }

        @Override
        public SearchResultsDto search(String userId, SearchEntriesRequest request) {
            String type = request.getSearchType();
            agentCalls.add(type);
            if (!semanticAvailable) {
                throw new SearchTypeUnavailableException(type + " unavailable", List.of());
            }
            return resultsByType.getOrDefault(type, resultsWithCount(0));
        }

        @Override
        public SearchResultsDto adminSearch(AdminSearchQuery query) {
            String type = query.getSearchType();
            adminCalls.add(type);
            if (!semanticAvailable) {
                throw new SearchTypeUnavailableException(type + " unavailable", List.of());
            }
            return adminResultsByType.getOrDefault(type, resultsWithCount(0));
        }

        @Override
        public void upsertTranscriptEmbedding(
                String conversationGroupId,
                String conversationId,
                String entryId,
                float[] embedding) {}

        @Override
        public void deleteByConversationGroupId(String conversationGroupId) {}
    }

    private static final class ScriptedFullTextStore implements FullTextSearchStore {
        private final Map<String, SearchResultsDto> resultsByType = new HashMap<>();
        private final Map<String, SearchResultsDto> adminResultsByType = new HashMap<>();
        private final List<String> agentCalls = new ArrayList<>();
        private final List<String> adminCalls = new ArrayList<>();
        private boolean fullTextAvailable = true;

        @Override
        public boolean isFullTextSearchAvailable() {
            return fullTextAvailable;
        }

        @Override
        public SearchResultsDto search(String userId, SearchEntriesRequest request) {
            String type = request.getSearchType();
            agentCalls.add(type);
            if (!fullTextAvailable) {
                throw new SearchTypeUnavailableException(
                        type + " unavailable", Collections.emptyList());
            }
            return resultsByType.getOrDefault(type, resultsWithCount(0));
        }

        @Override
        public SearchResultsDto adminSearch(AdminSearchQuery query) {
            String type = query.getSearchType();
            adminCalls.add(type);
            if (!fullTextAvailable) {
                throw new SearchTypeUnavailableException(
                        type + " unavailable", Collections.emptyList());
            }
            return adminResultsByType.getOrDefault(type, resultsWithCount(0));
        }
    }
}
