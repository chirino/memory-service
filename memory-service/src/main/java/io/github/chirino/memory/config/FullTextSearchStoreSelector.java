package io.github.chirino.memory.config;

import io.github.chirino.memory.vector.FullTextSearchStore;
import io.github.chirino.memory.vector.MongoSearchStore;
import io.github.chirino.memory.vector.PgSearchStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class FullTextSearchStoreSelector {

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject Instance<PgSearchStore> pgSearchStore;

    @Inject Instance<MongoSearchStore> mongoSearchStore;

    public FullTextSearchStore getFullTextSearchStore() {
        String ds = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        return switch (ds) {
            case "mongo", "mongodb" -> mongoSearchStore.get();
            case "postgres" -> pgSearchStore.get();
            default -> null;
        };
    }
}
