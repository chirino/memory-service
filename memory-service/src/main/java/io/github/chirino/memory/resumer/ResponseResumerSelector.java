package io.github.chirino.memory.resumer;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class ResponseResumerSelector {

    @ConfigProperty(name = "memory-service.response-resumer", defaultValue = "none")
    String resumerType;

    @Inject NoopResponseResumerBackend noopResponseResumerBackend;

    @Inject RedisResponseResumerBackend redisResponseResumerBackend;

    public ResponseResumerBackend getBackend() {
        String type = resumerType == null ? "none" : resumerType.trim().toLowerCase();
        switch (type) {
            case "redis":
                if (redisResponseResumerBackend.enabled()) {
                    return redisResponseResumerBackend;
                }
                // Fall through to noop if Redis is not available
                break;
            case "none":
            default:
                return noopResponseResumerBackend;
        }
        return noopResponseResumerBackend;
    }
}
