package io.github.chirino.memory.vector;

import jakarta.enterprise.context.ApplicationScoped;
import java.nio.charset.StandardCharsets;
import java.util.Locale;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class DefaultEmbeddingService implements EmbeddingService {

    @ConfigProperty(name = "memory.embedding.type", defaultValue = "hash")
    String embeddingType;

    @ConfigProperty(name = "memory.embedding.dimension", defaultValue = "256")
    int embeddingDimension;

    @Override
    public boolean isEnabled() {
        return embeddingType != null && !"none".equalsIgnoreCase(embeddingType.trim());
    }

    @Override
    public float[] embed(String text) {
        if (!isEnabled() || text == null || text.isBlank()) {
            return new float[0];
        }
        String type = embeddingType.trim().toLowerCase(Locale.ROOT);
        if (!"hash".equals(type)) {
            return new float[0];
        }
        int dimension = Math.max(8, embeddingDimension);
        return hashEmbedding(text, dimension);
    }

    private float[] hashEmbedding(String text, int dimension) {
        float[] vector = new float[dimension];
        String[] tokens = text.toLowerCase(Locale.ROOT).split("\\s+");
        for (String token : tokens) {
            if (token.isBlank()) {
                continue;
            }
            int hash = stableHash(token);
            int index = Math.floorMod(hash, dimension);
            float sign = (hash & 1) == 0 ? 1.0f : -1.0f;
            vector[index] += sign;
        }
        normalize(vector);
        return vector;
    }

    private int stableHash(String token) {
        byte[] data = token.getBytes(StandardCharsets.UTF_8);
        int hash = 0x811C9DC5;
        for (byte b : data) {
            hash ^= b & 0xff;
            hash *= 0x01000193;
        }
        return hash;
    }

    private void normalize(float[] vector) {
        double sum = 0.0;
        for (float v : vector) {
            sum += v * v;
        }
        if (sum <= 0.0) {
            return;
        }
        float norm = (float) Math.sqrt(sum);
        for (int i = 0; i < vector.length; i++) {
            vector[i] = vector[i] / norm;
        }
    }
}
