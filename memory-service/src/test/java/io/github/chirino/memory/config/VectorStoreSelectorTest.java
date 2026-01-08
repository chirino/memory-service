package io.github.chirino.memory.config;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;

import io.github.chirino.memory.vector.MongoVectorStore;
import io.github.chirino.memory.vector.NoopVectorStore;
import io.github.chirino.memory.vector.PgVectorStore;
import io.github.chirino.memory.vector.VectorStore;
import org.junit.jupiter.api.Test;

class VectorStoreSelectorTest {

    @Test
    void selects_noop_pg_and_mongo_vector_stores() {
        NoopVectorStore noop = new NoopVectorStore();
        PgVectorStore pg = new PgVectorStore();
        MongoVectorStore mongo = new MongoVectorStore();

        VectorStoreSelector selector = new VectorStoreSelector();
        selector.noopVectorStore = noop;
        selector.pgVectorStore = pg;
        selector.mongoVectorStore = mongo;

        selector.vectorType = "none";
        VectorStore selected = selector.getVectorStore();
        assertInstanceOf(NoopVectorStore.class, selected);

        selector.vectorType = "pgvector";
        selected = selector.getVectorStore();
        assertInstanceOf(PgVectorStore.class, selected);

        selector.vectorType = "postgres";
        selected = selector.getVectorStore();
        assertInstanceOf(PgVectorStore.class, selected);

        selector.vectorType = "mongo";
        selected = selector.getVectorStore();
        assertInstanceOf(MongoVectorStore.class, selected);

        selector.vectorType = "mongodb";
        selected = selector.getVectorStore();
        assertInstanceOf(MongoVectorStore.class, selected);
    }
}
