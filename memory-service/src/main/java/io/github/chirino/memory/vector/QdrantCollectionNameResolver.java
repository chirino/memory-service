package io.github.chirino.memory.vector;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.Locale;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class QdrantCollectionNameResolver {

    @ConfigProperty(name = "memory-service.vector.qdrant.collection-name")
    Optional<String> configuredCollectionName;

    @ConfigProperty(
            name = "memory-service.vector.qdrant.collection-prefix",
            defaultValue = "memory-service")
    String collectionPrefix;

    @Inject EmbeddingService embeddingService;

    public String resolveCollectionName() {
        if (configuredCollectionName.isPresent()
                && !configuredCollectionName.orElse("").isBlank()) {
            return configuredCollectionName.get().trim();
        }

        int dimensions = embeddingService.dimensions();
        if (dimensions <= 0) {
            throw new IllegalStateException(
                    "Qdrant collection naming requires a positive embedding dimension. Current"
                            + " value: "
                            + dimensions
                            + ". Check memory-service.embedding.type configuration.");
        }

        String sanitizedPrefix = sanitizeToken(collectionPrefix, "memory-service");
        String sanitizedModel = sanitizeToken(embeddingService.modelId(), "unknown-model");
        return sanitizedPrefix + "_" + sanitizedModel + "-" + dimensions;
    }

    static String sanitizeToken(String value, String fallback) {
        if (value == null || value.isBlank()) {
            return fallback;
        }
        String sanitized =
                value.trim()
                        .toLowerCase(Locale.ROOT)
                        .replaceAll("[^a-z0-9_-]", "-")
                        .replaceAll("[-_]{2,}", "-")
                        .replaceAll("^[^a-z0-9]+", "")
                        .replaceAll("[^a-z0-9]+$", "");
        return sanitized.isBlank() ? fallback : sanitized;
    }
}
