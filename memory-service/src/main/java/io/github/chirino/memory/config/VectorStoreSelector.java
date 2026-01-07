package io.github.chirino.memory.config;

import io.github.chirino.memory.vector.MongoVectorStore;
import io.github.chirino.memory.vector.NoopVectorStore;
import io.github.chirino.memory.vector.PgVectorStore;
import io.github.chirino.memory.vector.VectorStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class VectorStoreSelector {

    @ConfigProperty(name = "memory.vector.type", defaultValue = "none")
    String vectorType;

    @Inject NoopVectorStore noopVectorStore;

    @Inject PgVectorStore pgVectorStore;

    @Inject MongoVectorStore mongoVectorStore;

    public VectorStore getVectorStore() {
        String type = vectorType == null ? "none" : vectorType.trim().toLowerCase();
        switch (type) {
            case "pgvector":
            case "postgres":
                return pgVectorStore;
            case "mongo":
            case "mongodb":
                return mongoVectorStore;
            case "none":
            default:
                return noopVectorStore;
        }
    }
}
