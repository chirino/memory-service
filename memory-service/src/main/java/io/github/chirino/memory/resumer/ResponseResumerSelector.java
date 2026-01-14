package io.github.chirino.memory.resumer;

import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class ResponseResumerSelector {

    @ConfigProperty(name = "memory-service.response-resumer", defaultValue = "none")
    String resumerType;

    @Inject NoopResponseResumerBackend noopResponseResumerBackend;

    @Inject TempFileResumerBackend tempFileResumerBackend;

    @PostConstruct
    void validateConfiguration() {
        String type = resumerType == null ? "none" : resumerType.trim().toLowerCase();
        if (!"redis".equals(type) && !"infinispan".equals(type) && !"none".equals(type)) {
            throw new IllegalStateException(
                    "Unsupported memory-service.response-resumer value: " + resumerType);
        }
    }

    public ResponseResumerBackend getBackend() {
        String type = resumerType == null ? "none" : resumerType.trim().toLowerCase();
        switch (type) {
            case "redis":
            case "infinispan":
                if (tempFileResumerBackend.enabled()) {
                    return tempFileResumerBackend;
                }
                // Fall through to noop if the resumer store is not available
                break;
            case "none":
            default:
                return noopResponseResumerBackend;
        }
        return noopResponseResumerBackend;
    }
}
