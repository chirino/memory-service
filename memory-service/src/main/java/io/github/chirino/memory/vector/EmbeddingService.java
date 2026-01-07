package io.github.chirino.memory.vector;

public interface EmbeddingService {

    boolean isEnabled();

    float[] embed(String text);
}
