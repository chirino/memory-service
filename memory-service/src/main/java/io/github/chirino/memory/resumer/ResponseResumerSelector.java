package io.github.chirino.memory.resumer;

import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class ResponseResumerSelector {

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @ConfigProperty(name = "memory-service.response-resumer.enabled")
    Optional<Boolean> resumerEnabled;

    @Inject NoopResponseResumerBackend noopResponseResumerBackend;

    @Inject TempFileResumerBackend tempFileResumerBackend;

    @PostConstruct
    void validateConfiguration() {
        String type = cacheType == null ? "none" : cacheType.trim().toLowerCase();
        if (!"redis".equals(type) && !"infinispan".equals(type) && !"none".equals(type)) {
            throw new IllegalStateException(
                    "Unsupported memory-service.cache.type value: " + cacheType);
        }
    }

    public ResponseResumerBackend getBackend() {
        String type = cacheType == null ? "none" : cacheType.trim().toLowerCase();

        // If explicitly disabled, return noop
        if (resumerEnabled.isPresent() && !resumerEnabled.get()) {
            return noopResponseResumerBackend;
        }

        if ("redis".equals(type) || "infinispan".equals(type)) {
            if (tempFileResumerBackend.enabled()) {
                return tempFileResumerBackend;
            }
        }
        return noopResponseResumerBackend;
    }
}
