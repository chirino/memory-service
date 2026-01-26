package io.github.chirino.memory.resumer;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class ResponseResumerLocatorStoreSelector {

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @ConfigProperty(name = "memory-service.response-resumer.enabled")
    Optional<Boolean> resumerEnabled;

    @Inject RedisResponseResumerLocatorStore redisStore;

    @Inject InfinispanResponseResumerLocatorStore infinispanStore;

    @Inject NoopResponseResumerLocatorStore noopStore;

    public ResponseResumerLocatorStore select() {
        String type = cacheType == null ? "none" : cacheType.trim().toLowerCase();

        // If explicitly disabled, return noop
        if (resumerEnabled.isPresent() && !resumerEnabled.get()) {
            return noopStore;
        }

        return switch (type) {
            case "redis" -> requireAvailable(redisStore, "redis");
            case "infinispan" -> requireAvailable(infinispanStore, "infinispan");
            case "none" -> {
                // Error if explicitly enabled but no cache configured
                if (resumerEnabled.orElse(false)) {
                    throw new IllegalStateException(
                            "Response resumer is enabled but memory-service.cache.type=none. "
                                    + "Configure a cache backend (redis or infinispan).");
                }
                yield noopStore;
            }
            default ->
                    throw new IllegalStateException(
                            "Unsupported memory-service.cache.type value: " + cacheType);
        };
    }

    private ResponseResumerLocatorStore requireAvailable(
            ResponseResumerLocatorStore store, String type) {
        if (!store.available()) {
            throw new IllegalStateException(
                    "Response resumer is enabled (memory-service.cache.type="
                            + type
                            + ") but the "
                            + type
                            + " client is not available.");
        }
        return store;
    }
}
