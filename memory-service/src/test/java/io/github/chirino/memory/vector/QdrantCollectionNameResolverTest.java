package io.github.chirino.memory.vector;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.Optional;
import org.junit.jupiter.api.Test;

class QdrantCollectionNameResolverTest {

    @Test
    void builds_dynamic_name_with_model_and_dimensions() {
        QdrantCollectionNameResolver resolver =
                createResolver("openai/text-embedding-3-small", 1536);

        String collectionName = resolver.resolveCollectionName();

        assertEquals("memory-service_openai-text-embedding-3-small-1536", collectionName);
    }

    @Test
    void sanitizes_local_model_name() {
        QdrantCollectionNameResolver resolver = createResolver("local/all-MiniLM-L6-v2", 384);

        String collectionName = resolver.resolveCollectionName();

        assertEquals("memory-service_local-all-minilm-l6-v2-384", collectionName);
    }

    @Test
    void uses_override_collection_name_when_configured() {
        QdrantCollectionNameResolver resolver =
                createResolver("openai/text-embedding-3-small", 1536);
        resolver.configuredCollectionName = Optional.of("my-explicit-collection");

        String collectionName = resolver.resolveCollectionName();

        assertEquals("my-explicit-collection", collectionName);
    }

    @Test
    void fails_when_dimensions_are_invalid_for_dynamic_naming() {
        QdrantCollectionNameResolver resolver = createResolver("openai/text-embedding-3-small", 0);

        assertThrows(IllegalStateException.class, resolver::resolveCollectionName);
    }

    private static QdrantCollectionNameResolver createResolver(String modelId, int dimensions) {
        QdrantCollectionNameResolver resolver = new QdrantCollectionNameResolver();
        resolver.collectionPrefix = "memory-service";
        resolver.configuredCollectionName = Optional.empty();
        resolver.embeddingService = mock(EmbeddingService.class);
        when(resolver.embeddingService.modelId()).thenReturn(modelId);
        when(resolver.embeddingService.dimensions()).thenReturn(dimensions);
        return resolver;
    }
}
