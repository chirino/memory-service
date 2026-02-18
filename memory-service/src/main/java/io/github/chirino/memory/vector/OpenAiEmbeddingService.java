package io.github.chirino.memory.vector;

import dev.langchain4j.model.openai.OpenAiEmbeddingModel;

public class OpenAiEmbeddingService implements EmbeddingService {

    private final OpenAiEmbeddingModel model;
    private final String modelName;
    private final int dimensions;

    public OpenAiEmbeddingService(
            String apiKey, String modelName, String baseUrl, Integer dimensions) {
        if (apiKey == null || apiKey.isBlank()) {
            throw new IllegalStateException(
                    "memory-service.embedding.openai.api-key is required when embedding type is"
                            + " openai");
        }
        this.modelName = modelName;

        var builder =
                OpenAiEmbeddingModel.builder().apiKey(apiKey).modelName(modelName).baseUrl(baseUrl);

        if (dimensions != null && dimensions > 0) {
            builder.dimensions(dimensions);
            this.dimensions = dimensions;
        } else {
            // Use known defaults for common models
            this.dimensions = inferDimensions(modelName);
        }

        this.model = builder.build();
    }

    @Override
    public boolean isEnabled() {
        return true;
    }

    @Override
    public float[] embed(String text) {
        if (text == null || text.isBlank()) {
            return new float[0];
        }
        return model.embed(text).content().vector();
    }

    @Override
    public int dimensions() {
        return dimensions;
    }

    @Override
    public String modelId() {
        return "openai/" + modelName;
    }

    private static int inferDimensions(String modelName) {
        return switch (modelName) {
            case "text-embedding-3-small" -> 1536;
            case "text-embedding-3-large" -> 3072;
            case "text-embedding-ada-002" -> 1536;
            default -> 1536;
        };
    }
}
