package io.github.chirino.memory.vector;

import dev.langchain4j.data.segment.TextSegment;
import dev.langchain4j.store.embedding.EmbeddingStore;
import dev.langchain4j.store.embedding.qdrant.QdrantEmbeddingStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * CDI producer that creates the appropriate LangChain4j {@link EmbeddingStore} based on the
 * configured vector store type.
 *
 * <p>This producer is only resolved when {@link
 * io.github.chirino.memory.config.SearchStoreSelector} routes to {@link LangChain4jSearchStore}
 * (via {@code Instance<>} lazy resolution). When the store type is {@code pgvector}, {@code mongo},
 * or {@code none}, this producer is never called.
 */
@ApplicationScoped
public class EmbeddingStoreProducer {

    @ConfigProperty(name = "memory-service.vector.store.type", defaultValue = "none")
    String storeType;

    @ConfigProperty(name = "memory-service.vector.qdrant.host", defaultValue = "localhost")
    String qdrantHost;

    @ConfigProperty(name = "memory-service.vector.qdrant.port", defaultValue = "6334")
    int qdrantPort;

    @ConfigProperty(name = "memory-service.vector.qdrant.api-key")
    Optional<String> qdrantApiKey;

    @ConfigProperty(name = "memory-service.vector.qdrant.use-tls", defaultValue = "false")
    boolean qdrantUseTls;

    @Inject QdrantCollectionNameResolver collectionNameResolver;

    @Produces
    @Singleton
    public EmbeddingStore<TextSegment> embeddingStore() {
        return switch (storeType.trim().toLowerCase()) {
            case "qdrant" -> buildQdrantStore();
            default ->
                    throw new IllegalStateException(
                            "No LangChain4j EmbeddingStore configured for store type: "
                                    + storeType
                                    + ". This producer is only called when the SearchStoreSelector"
                                    + " routes to LangChain4jSearchStore.");
        };
    }

    private EmbeddingStore<TextSegment> buildQdrantStore() {
        String collectionName = collectionNameResolver.resolveCollectionName();
        var builder =
                QdrantEmbeddingStore.builder()
                        .host(qdrantHost)
                        .port(qdrantPort)
                        .collectionName(collectionName)
                        .useTls(qdrantUseTls);

        qdrantApiKey.filter(k -> !k.isBlank()).ifPresent(builder::apiKey);

        return builder.build();
    }
}
