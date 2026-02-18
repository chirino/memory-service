package io.github.chirino.memory.config;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;

import io.github.chirino.memory.vector.MongoSearchStore;
import io.github.chirino.memory.vector.PgSearchStore;
import io.github.chirino.memory.vector.SearchStore;
import org.junit.jupiter.api.Test;

class SearchStoreSelectorTest {

    private SearchStoreSelector createSelector() {
        SearchStoreSelector selector = new SearchStoreSelector();
        selector.pgSearchStore = TestInstance.of(new PgSearchStore());
        selector.mongoSearchStore = TestInstance.of(new MongoSearchStore());
        return selector;
    }

    @Test
    void selects_pg_and_mongo_search_stores() {
        SearchStoreSelector selector = createSelector();

        selector.vectorStoreType = "pgvector";
        SearchStore selected = selector.getSearchStore();
        assertInstanceOf(PgSearchStore.class, selected);

        selector.vectorStoreType = "mongo";
        selected = selector.getSearchStore();
        assertInstanceOf(MongoSearchStore.class, selected);
    }

    @Test
    void defaults_to_pg_when_vector_store_type_is_none_and_datastore_is_postgres() {
        SearchStoreSelector selector = createSelector();
        selector.vectorStoreType = "none";
        selector.datastoreType = "postgres";

        SearchStore selected = selector.getSearchStore();
        assertInstanceOf(PgSearchStore.class, selected);
    }

    @Test
    void defaults_to_mongo_when_vector_store_type_is_none_and_datastore_is_mongo() {
        SearchStoreSelector selector = createSelector();
        selector.vectorStoreType = "none";
        selector.datastoreType = "mongodb";

        SearchStore selected = selector.getSearchStore();
        assertInstanceOf(MongoSearchStore.class, selected);
    }
}
