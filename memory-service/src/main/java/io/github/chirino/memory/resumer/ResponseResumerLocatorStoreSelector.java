package io.github.chirino.memory.resumer;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class ResponseResumerLocatorStoreSelector {

    @ConfigProperty(name = "memory-service.response-resumer", defaultValue = "none")
    String resumerType;

    @Inject RedisResponseResumerLocatorStore redisStore;

    @Inject InfinispanResponseResumerLocatorStore infinispanStore;

    @Inject NoopResponseResumerLocatorStore noopStore;

    public ResponseResumerLocatorStore select() {
        String type = resumerType == null ? "none" : resumerType.trim().toLowerCase();
        return switch (type) {
            case "redis" -> requireAvailable(redisStore, "redis");
            case "infinispan" -> requireAvailable(infinispanStore, "infinispan");
            case "none" -> noopStore;
            default ->
                    throw new IllegalStateException(
                            "Unsupported memory-service.response-resumer value: " + resumerType);
        };
    }

    private ResponseResumerLocatorStore requireAvailable(
            ResponseResumerLocatorStore store, String type) {
        if (!store.available()) {
            throw new IllegalStateException(
                    "Response resumer is enabled (memory-service.response-resumer="
                            + type
                            + ") but the "
                            + type
                            + " client is not available.");
        }
        return store;
    }
}
