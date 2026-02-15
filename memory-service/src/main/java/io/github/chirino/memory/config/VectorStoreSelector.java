package io.github.chirino.memory.config;

import io.github.chirino.memory.vector.MongoVectorStore;
import io.github.chirino.memory.vector.PgVectorStore;
import io.github.chirino.memory.vector.VectorStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class VectorStoreSelector {

    @ConfigProperty(name = "memory-service.vector.type", defaultValue = "none")
    String vectorType;

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject Instance<PgVectorStore> pgVectorStore;

    @Inject Instance<MongoVectorStore> mongoVectorStore;

    public VectorStore getVectorStore() {
        String type = vectorType == null ? "none" : vectorType.trim().toLowerCase();
        switch (type) {
            case "pgvector":
            case "postgres":
                return pgVectorStore.get();
            case "mongo":
            case "mongodb":
                return mongoVectorStore.get();
            case "none":
            default:
                return defaultForDatastore();
        }
    }

    /**
     * When no explicit vector type is configured, select the store matching the datastore type.
     * Both PgVectorStore and MongoVectorStore gracefully fall back to full-text search when
     * semantic/embedding search is unavailable.
     */
    private VectorStore defaultForDatastore() {
        String ds = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        return switch (ds) {
            case "mongo", "mongodb" -> mongoVectorStore.get();
            default -> pgVectorStore.get();
        };
    }
}
