package io.github.chirino.memory.vector;

import dev.langchain4j.model.embedding.onnx.allminilml6v2q.AllMiniLmL6V2QuantizedEmbeddingModel;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class DefaultEmbeddingService implements EmbeddingService {

    @ConfigProperty(name = "memory-service.embedding.enabled", defaultValue = "true")
    boolean embeddingEnabled;

    // Use Instance for lazy loading - model is only instantiated when needed
    @Inject Instance<AllMiniLmL6V2QuantizedEmbeddingModel> embeddingModelInstance;

    private AllMiniLmL6V2QuantizedEmbeddingModel embeddingModel;

    @Override
    public boolean isEnabled() {
        return embeddingEnabled;
    }

    @Override
    public float[] embed(String text) {
        if (!isEnabled() || text == null || text.isBlank()) {
            return new float[0];
        }
        // Lazy load the model only when needed
        if (embeddingModel == null) {
            embeddingModel = embeddingModelInstance.get();
        }
        return embeddingModel.embed(text).content().vector();
    }
}
