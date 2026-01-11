package io.github.chirino.memory.config;

import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.impl.MongoMemoryStore;
import io.github.chirino.memory.store.impl.PostgresMemoryStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class MemoryStoreSelector {

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject PostgresMemoryStore postgresMemoryStore;

    @Inject MongoMemoryStore mongoMemoryStore;

    public MemoryStore getStore() {
        String type = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        // For now, only postgres is implemented. Other types can be wired here later.
        if ("postgres".equals(type)) {
            return postgresMemoryStore;
        }
        if ("mongo".equals(type) || "mongodb".equals(type)) {
            return mongoMemoryStore;
        }
        throw new IllegalStateException(
                "Unsupported memory-service.datastore.type: " + datastoreType);
    }
}
