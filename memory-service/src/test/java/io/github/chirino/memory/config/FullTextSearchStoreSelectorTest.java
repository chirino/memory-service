package io.github.chirino.memory.config;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNull;

import io.github.chirino.memory.vector.FullTextSearchStore;
import io.github.chirino.memory.vector.MongoSearchStore;
import io.github.chirino.memory.vector.PgSearchStore;
import org.junit.jupiter.api.Test;

class FullTextSearchStoreSelectorTest {

    private FullTextSearchStoreSelector createSelector() {
        FullTextSearchStoreSelector selector = new FullTextSearchStoreSelector();
        selector.pgSearchStore = TestInstance.of(new PgSearchStore());
        selector.mongoSearchStore = TestInstance.of(new MongoSearchStore());
        return selector;
    }

    @Test
    void selects_postgres_fulltext_store_for_postgres_datastore() {
        FullTextSearchStoreSelector selector = createSelector();
        selector.datastoreType = "postgres";

        FullTextSearchStore selected = selector.getFullTextSearchStore();
        assertInstanceOf(PgSearchStore.class, selected);
    }

    @Test
    void selects_mongo_fulltext_store_for_mongo_datastore() {
        FullTextSearchStoreSelector selector = createSelector();
        selector.datastoreType = "mongodb";

        FullTextSearchStore selected = selector.getFullTextSearchStore();
        assertInstanceOf(MongoSearchStore.class, selected);
    }

    @Test
    void returns_null_for_unknown_datastore() {
        FullTextSearchStoreSelector selector = createSelector();
        selector.datastoreType = "unknown";

        FullTextSearchStore selected = selector.getFullTextSearchStore();
        assertNull(selected);
    }
}
