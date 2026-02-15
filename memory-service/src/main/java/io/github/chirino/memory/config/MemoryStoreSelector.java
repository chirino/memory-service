package io.github.chirino.memory.config;

import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.MeteredMemoryStore;
import io.github.chirino.memory.store.impl.MongoMemoryStore;
import io.github.chirino.memory.store.impl.PostgresMemoryStore;
import io.micrometer.core.instrument.MeterRegistry;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class MemoryStoreSelector {

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject Instance<PostgresMemoryStore> postgresMemoryStore;

    @Inject Instance<MongoMemoryStore> mongoMemoryStore;

    @Inject MeterRegistry meterRegistry;

    private MemoryStore meteredStore;

    @PostConstruct
    void init() {
        MemoryStore delegate = selectDelegate();
        meteredStore = new MeteredMemoryStore(meterRegistry, delegate);
    }

    public MemoryStore getStore() {
        return meteredStore;
    }

    private MemoryStore selectDelegate() {
        String type = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        if ("postgres".equals(type)) {
            return postgresMemoryStore.get();
        }
        if ("mongo".equals(type) || "mongodb".equals(type)) {
            return mongoMemoryStore.get();
        }
        throw new IllegalStateException(
                "Unsupported memory-service.datastore.type: " + datastoreType);
    }
}
