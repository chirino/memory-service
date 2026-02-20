package io.github.chirino.memory.vector;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Singleton;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class EmbeddingServiceProducer {

    @ConfigProperty(name = "memory-service.embedding.type", defaultValue = "local")
    String embeddingType;

    @ConfigProperty(name = "memory-service.embedding.openai.api-key")
    Optional<String> openaiApiKey;

    // Fallback: picks up the generic OPENAI_API_KEY env var
    @ConfigProperty(name = "openai.api.key")
    Optional<String> genericOpenaiApiKey;

    @ConfigProperty(
            name = "memory-service.embedding.openai.model-name",
            defaultValue = "text-embedding-3-small")
    String openaiModelName;

    @ConfigProperty(
            name = "memory-service.embedding.openai.base-url",
            defaultValue = "https://api.openai.com/v1")
    String openaiBaseUrl;

    @ConfigProperty(name = "memory-service.embedding.openai.dimensions")
    Optional<Integer> openaiDimensions;

    @Produces
    @Singleton
    public EmbeddingService embeddingService() {
        return switch (embeddingType.trim().toLowerCase()) {
            case "local" -> new LocalEmbeddingService();
            case "openai" ->
                    new OpenAiEmbeddingService(
                            openaiApiKey.or(() -> genericOpenaiApiKey).orElse(null),
                            openaiModelName,
                            openaiBaseUrl,
                            openaiDimensions.orElse(null));
            case "none" -> new DisabledEmbeddingService();
            default ->
                    throw new IllegalStateException(
                            "Unsupported embedding type: "
                                    + embeddingType
                                    + ". Valid values: local, openai, none");
        };
    }
}
