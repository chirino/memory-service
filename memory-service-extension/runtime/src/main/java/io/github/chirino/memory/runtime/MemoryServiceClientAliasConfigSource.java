package io.github.chirino.memory.runtime;

import java.util.Map;
import java.util.Set;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * Simple ConfigSource that exposes a fixed map of aliased properties.
 * Instances are created by {@link MemoryServiceClientAliasConfigSourceFactory}.
 */
public class MemoryServiceClientAliasConfigSource implements ConfigSource {

    private final Map<String, String> properties;

    MemoryServiceClientAliasConfigSource(Map<String, String> properties) {
        this.properties = Map.copyOf(properties);
    }

    @Override
    public Map<String, String> getProperties() {
        return properties;
    }

    @Override
    public Set<String> getPropertyNames() {
        return properties.keySet();
    }

    @Override
    public String getValue(String propertyName) {
        return properties.get(propertyName);
    }

    @Override
    public String getName() {
        return "memory-service-client-alias";
    }

    @Override
    public int getOrdinal() {
        // Lower than application.properties (100) so explicit quarkus.rest-client.*
        // entries still win if present.
        return 90;
    }
}
