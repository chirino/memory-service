package io.github.chirino.memory.config;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNull;

import io.github.chirino.memory.vector.LangChain4jSearchStore;
import io.github.chirino.memory.vector.PgSearchStore;
import io.github.chirino.memory.vector.VectorSearchStore;
import org.junit.jupiter.api.Test;

class SearchStoreSelectorTest {

    private SearchStoreSelector createSelector() {
        SearchStoreSelector selector = new SearchStoreSelector();
        selector.pgSearchStore = TestInstance.of(new PgSearchStore());
        selector.langChain4jSearchStore = TestInstance.of(new LangChain4jSearchStore());
        return selector;
    }

    @Test
    void selects_pg_search_store_for_pgvector() {
        SearchStoreSelector selector = createSelector();

        selector.vectorStoreType = "pgvector";
        VectorSearchStore selected = selector.getSearchStore();
        assertInstanceOf(PgSearchStore.class, selected);
    }

    @Test
    void routes_qdrant_to_langchain4j_search_store() {
        SearchStoreSelector selector = createSelector();
        selector.vectorStoreType = "qdrant";

        VectorSearchStore selected = selector.getSearchStore();
        assertInstanceOf(LangChain4jSearchStore.class, selected);
    }

    @Test
    void returns_null_when_vector_store_type_is_none() {
        SearchStoreSelector selector = createSelector();
        selector.vectorStoreType = "none";

        VectorSearchStore selected = selector.getSearchStore();
        assertNull(selected);
    }

    @Test
    void returns_null_for_unknown_vector_store_type() {
        SearchStoreSelector selector = createSelector();
        selector.vectorStoreType = "mongo";

        VectorSearchStore selected = selector.getSearchStore();
        assertNull(selected);
    }
}
