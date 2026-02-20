package io.github.chirino.memory.vector;

import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.Optional;
import org.junit.jupiter.api.Test;

class EmbeddingStoreProducerTest {

    private EmbeddingStoreProducer createProducer(String storeType) {
        EmbeddingStoreProducer producer = new EmbeddingStoreProducer();
        producer.storeType = storeType;
        producer.qdrantHost = "localhost";
        producer.qdrantPort = 6334;
        producer.qdrantCollectionName = "memory_segments";
        producer.qdrantApiKey = Optional.empty();
        producer.qdrantUseTls = false;
        return producer;
    }

    @Test
    void produces_qdrant_store_when_type_is_qdrant() {
        EmbeddingStoreProducer producer = createProducer("qdrant");

        // QdrantEmbeddingStore is created eagerly (connection is lazy),
        // so this should succeed without a running Qdrant instance
        var store = producer.embeddingStore();
        assertNotNull(store);
    }

    @Test
    void throws_for_unsupported_type() {
        EmbeddingStoreProducer producer = createProducer("pgvector");

        IllegalStateException ex =
                assertThrows(IllegalStateException.class, producer::embeddingStore);
        assertTrue(ex.getMessage().contains("pgvector"));
    }

    @Test
    void throws_for_none_type() {
        EmbeddingStoreProducer producer = createProducer("none");

        IllegalStateException ex =
                assertThrows(IllegalStateException.class, producer::embeddingStore);
        assertTrue(ex.getMessage().contains("none"));
    }

    @Test
    void produces_qdrant_store_with_api_key() {
        EmbeddingStoreProducer producer = createProducer("qdrant");
        producer.qdrantApiKey = Optional.of("test-api-key");

        var store = producer.embeddingStore();
        assertNotNull(store);
    }
}
