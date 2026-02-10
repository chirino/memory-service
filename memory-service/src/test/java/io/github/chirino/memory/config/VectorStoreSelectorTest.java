package io.github.chirino.memory.config;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;

import io.github.chirino.memory.vector.MongoVectorStore;
import io.github.chirino.memory.vector.PgVectorStore;
import io.github.chirino.memory.vector.VectorStore;
import org.junit.jupiter.api.Test;

class VectorStoreSelectorTest {

    @Test
    void selects_pg_and_mongo_vector_stores() {
        PgVectorStore pg = new PgVectorStore();
        MongoVectorStore mongo = new MongoVectorStore();

        VectorStoreSelector selector = new VectorStoreSelector();
        selector.pgVectorStore = pg;
        selector.mongoVectorStore = mongo;

        selector.vectorType = "pgvector";
        VectorStore selected = selector.getVectorStore();
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

    @Test
    void defaults_to_pg_when_vector_type_is_none_and_datastore_is_postgres() {
        PgVectorStore pg = new PgVectorStore();
        MongoVectorStore mongo = new MongoVectorStore();

        VectorStoreSelector selector = new VectorStoreSelector();
        selector.pgVectorStore = pg;
        selector.mongoVectorStore = mongo;
        selector.vectorType = "none";
        selector.datastoreType = "postgres";

        VectorStore selected = selector.getVectorStore();
        assertInstanceOf(PgVectorStore.class, selected);
    }

    @Test
    void defaults_to_mongo_when_vector_type_is_none_and_datastore_is_mongo() {
        PgVectorStore pg = new PgVectorStore();
        MongoVectorStore mongo = new MongoVectorStore();

        VectorStoreSelector selector = new VectorStoreSelector();
        selector.pgVectorStore = pg;
        selector.mongoVectorStore = mongo;
        selector.vectorType = "none";
        selector.datastoreType = "mongodb";

        VectorStore selected = selector.getVectorStore();
        assertInstanceOf(MongoVectorStore.class, selected);
    }
}
