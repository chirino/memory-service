package io.github.chirino.memory.runtime;

import io.smallrye.config.ConfigSourceContext;
import io.smallrye.config.ConfigSourceFactory;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * ConfigSourceFactory that aliases {@code memory-service-client.*} properties
 * to {@code quarkus.rest-client.memory-service-client.*}.
 *
 * <p>This allows users to configure the REST client using the simpler
 * {@code memory-service-client.url} property instead of the verbose
 * {@code quarkus.rest-client.memory-service-client.url}.
 *
 * <p>Note: gRPC client configuration is handled separately by
 * {@link GrpcFromUrlConfigSource}, which reads directly from environment
 * variables to work correctly in container deployments.
 */
public class MemoryServiceClientAliasConfigSourceFactory implements ConfigSourceFactory {

    private static final String TARGET_PREFIX = "quarkus.rest-client.memory-service-client.";
    private static final String ALIAS_PREFIX = "memory-service-client.";

    @Override
    public Iterable<ConfigSource> getConfigSources(ConfigSourceContext context) {
        Map<String, String> props = new HashMap<>();

        // Alias memory-service-client.* to quarkus.rest-client.memory-service-client.*
        for (var it = context.iterateNames(); it.hasNext(); ) {
            String name = it.next();
            if (name.startsWith(ALIAS_PREFIX)) {
                var value = context.getValue(name);
                if (value != null && value.getValue() != null) {
                    String suffix = name.substring(ALIAS_PREFIX.length());
                    // api-key is not a REST client property, skip it
                    if ("api-key".equals(suffix)) {
                        continue;
                    }
                    String targetName = TARGET_PREFIX + suffix;
                    props.put(targetName, value.getValue());
                }
            }
        }

        if (props.isEmpty()) {
            return List.of();
        }
        return List.of(new MemoryServiceClientAliasConfigSource(props));
    }
}
