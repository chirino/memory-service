package io.github.chirino.memory.deployment;

import io.quarkus.builder.item.SimpleBuildItem;
import java.util.Map;

/**
 * Build item that captures the memory-service dev services configuration.
 * This follows the pattern of KeycloakDevServicesConfigBuildItem and allows
 * other extensions to consume the memory-service configuration.
 */
public final class MemoryServiceDevServicesConfigBuildItem extends SimpleBuildItem {

    private final Map<String, String> config;

    public MemoryServiceDevServicesConfigBuildItem(Map<String, String> config) {
        this.config = config;
    }

    /**
     * @return the configuration map containing memory-service related properties
     */
    public Map<String, String> getConfig() {
        return config;
    }

    /**
     * @return the memory-service URL, or null if not available
     */
    public String getUrl() {
        return config != null ? config.get("memory-service-client.url") : null;
    }

    /**
     * @return the memory-service API key, or null if not available
     */
    public String getApiKey() {
        return config != null ? config.get("memory-service-client.api-key") : null;
    }
}
