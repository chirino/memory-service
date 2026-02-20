package io.github.chirino.memory.config;

import io.github.chirino.memory.vector.LangChain4jSearchStore;
import io.github.chirino.memory.vector.PgSearchStore;
import io.github.chirino.memory.vector.VectorSearchStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class SearchStoreSelector {

    @ConfigProperty(name = "memory-service.vector.store.type", defaultValue = "none")
    String vectorStoreType;

    @Inject Instance<PgSearchStore> pgSearchStore;

    @Inject Instance<LangChain4jSearchStore> langChain4jSearchStore;

    public VectorSearchStore getSearchStore() {
        String type = vectorStoreType == null ? "none" : vectorStoreType.trim().toLowerCase();
        return switch (type) {
            case "pgvector":
                yield pgSearchStore.get();
            case "qdrant":
                yield langChain4jSearchStore.get();
            case "none":
            default:
                yield null;
        };
    }
}
