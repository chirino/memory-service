package io.github.chirino.memory.vector;

public class DisabledEmbeddingService implements EmbeddingService {

    @Override
    public boolean isEnabled() {
        return false;
    }

    @Override
    public float[] embed(String text) {
        return new float[0];
    }

    @Override
    public int dimensions() {
        return 0;
    }

    @Override
    public String modelId() {
        return "none";
    }
}
