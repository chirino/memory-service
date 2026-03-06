package io.github.chirino.memory.runtime;

import io.smallrye.config.ConfigSourceContext;
import io.smallrye.config.ConfigSourceFactory;
import java.util.List;
import java.util.Map;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * ConfigSourceFactory that creates a {@link GrpcFromUrlConfigSource} by reading
 * the memory-service.client.url from the config context.
 *
 * <p>Using a ConfigSourceFactory allows reading from all config sources including
 * application.properties, environment variables, and system properties.
 *
 * <p>Precedence for reading the URL:
 * <ol>
 *   <li>Environment variable: {@code MEMORY_SERVICE_CLIENT_URL}</li>
 *   <li>Config property: {@code memory-service.client.url} (from any source)</li>
 * </ol>
 */
public class GrpcFromUrlConfigSourceFactory implements ConfigSourceFactory {

    private static final String ENV_VAR_NAME = "MEMORY_SERVICE_CLIENT_URL";
    private static final String CONFIG_PROP_NAME = "memory-service.client.url";

    @Override
    public Iterable<ConfigSource> getConfigSources(ConfigSourceContext context) {
        // First check environment variable (highest priority for URL source)
        String url = System.getenv(ENV_VAR_NAME);

        // Then check the config context (includes application.properties, system props, etc.)
        if (url == null || url.isBlank()) {
            var configValue = context.getValue(CONFIG_PROP_NAME);
            if (configValue != null && configValue.getValue() != null) {
                url = configValue.getValue();
            }
        }

        Map<String, String> props = GrpcFromUrlConfigSource.parseUrl(url);
        if (props.isEmpty()) {
            return List.of();
        }

        return List.of(new GrpcFromUrlConfigSource(props));
    }
}
