package io.github.chirino.memory.config;

import io.github.chirino.memory.vector.MongoSearchStore;
import io.github.chirino.memory.vector.PgSearchStore;
import io.github.chirino.memory.vector.SearchStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class SearchStoreSelector {

    @ConfigProperty(name = "memory-service.vector.store.type", defaultValue = "none")
    String vectorStoreType;

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject Instance<PgSearchStore> pgSearchStore;

    @Inject Instance<MongoSearchStore> mongoSearchStore;

    public SearchStore getSearchStore() {
        String type = vectorStoreType == null ? "none" : vectorStoreType.trim().toLowerCase();
        switch (type) {
            case "pgvector":
                return pgSearchStore.get();
            case "mongo":
                return mongoSearchStore.get();
            case "none":
            default:
                return defaultForDatastore();
        }
    }

    /**
     * When no explicit search store type is configured, select the store matching the datastore
     * type. Both PgSearchStore and MongoSearchStore gracefully fall back to full-text search when
     * semantic/embedding search is unavailable.
     */
    private SearchStore defaultForDatastore() {
        String ds = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        return switch (ds) {
            case "mongo", "mongodb" -> mongoSearchStore.get();
            default -> pgSearchStore.get();
        };
    }
}
