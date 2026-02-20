package io.github.chirino.memory.vector;

import dev.langchain4j.model.embedding.onnx.allminilml6v2q.AllMiniLmL6V2QuantizedEmbeddingModel;

public class LocalEmbeddingService implements EmbeddingService {

    private volatile AllMiniLmL6V2QuantizedEmbeddingModel model;

    @Override
    public boolean isEnabled() {
        return true;
    }

    @Override
    public float[] embed(String text) {
        if (text == null || text.isBlank()) {
            return new float[0];
        }
        return getModel().embed(text).content().vector();
    }

    @Override
    public int dimensions() {
        return 384;
    }

    @Override
    public String modelId() {
        return "local/all-MiniLM-L6-v2";
    }

    private AllMiniLmL6V2QuantizedEmbeddingModel getModel() {
        if (model == null) {
            synchronized (this) {
                if (model == null) {
                    model = new AllMiniLmL6V2QuantizedEmbeddingModel();
                }
            }
        }
        return model;
    }
}
