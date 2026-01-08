package io.github.chirino.memory.runtime;

import io.smallrye.config.ConfigSourceContext;
import io.smallrye.config.ConfigSourceFactory;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * ConfigSourceFactory that:
 * 1. Aliases {@code memory-service-client.*} properties to {@code quarkus.rest-client.memory-service-client.*}
 * 2. Automatically configures the gRPC client URL to match the memory-service HTTP URL
 *
 * Note: This factory runs at build time, so it can only see static configuration.
 * For runtime configuration (e.g., from dev services), see {@link GrpcClientUrlConfigSource}.
 */
public class MemoryServiceClientAliasConfigSourceFactory implements ConfigSourceFactory {

    private static final String TARGET_PREFIX = "quarkus.rest-client.memory-service-client.";
    private static final String ALIAS_PREFIX = "memory-service-client.";
    private static final String GRPC_CLIENT_HOST = "quarkus.grpc.clients.responseresumer.host";
    private static final String GRPC_CLIENT_PORT = "quarkus.grpc.clients.responseresumer.port";
    private static final String REST_CLIENT_URL = "quarkus.rest-client.memory-service-client.url";

    @Override
    public Iterable<ConfigSource> getConfigSources(ConfigSourceContext context) {
        Map<String, String> props = new HashMap<>();

        // 1. Alias memory-service-client.* to quarkus.rest-client.memory-service-client.*
        for (var it = context.iterateNames(); it.hasNext(); ) {
            String name = it.next();
            if (name.startsWith(ALIAS_PREFIX)) {
                var value = context.getValue(name);
                if (value != null && value.getValue() != null) {
                    String suffix = name.substring(ALIAS_PREFIX.length());
                    if ("api-key".equals(suffix)) {
                        continue;
                    }
                    String targetName = TARGET_PREFIX + suffix;
                    props.put(targetName, value.getValue());
                }
            }
        }

        // 2. Auto-configure gRPC client URL from memory-service HTTP URL
        // Try to get the HTTP URL from either the alias or the target property
        // This can come from static config or from dev services (via context)
        String httpUrl = null;

        // Check if we already aliased the URL in step 1
        String aliasedUrl = props.get(REST_CLIENT_URL);
        if (aliasedUrl != null && !aliasedUrl.isBlank()) {
            httpUrl = aliasedUrl;
        } else {
            // Check memory-service-client.url alias from context (may include dev services config)
            var aliasUrlValue = context.getValue(ALIAS_PREFIX + "url");
            if (aliasUrlValue != null && aliasUrlValue.getValue() != null) {
                httpUrl = aliasUrlValue.getValue();
            } else {
                // Check the target REST client URL property from context (may include dev services
                // config)
                var restUrlValue = context.getValue(REST_CLIENT_URL);
                if (restUrlValue != null && restUrlValue.getValue() != null) {
                    httpUrl = restUrlValue.getValue();
                }
            }
        }

        // Check if gRPC client is already explicitly configured - don't override
        var grpcHostValue = context.getValue(GRPC_CLIENT_HOST);
        var grpcPortValue = context.getValue(GRPC_CLIENT_PORT);
        boolean grpcAlreadyConfigured =
                (grpcHostValue != null && grpcHostValue.getValue() != null)
                        || (grpcPortValue != null && grpcPortValue.getValue() != null);

        // Always add the runtime ConfigSource for gRPC client configuration
        // This will handle cases where the URL is set at runtime (e.g., from dev services)
        // Pass the HTTP URL we found (or null if not found) so it can provide the gRPC host/port
        // Only provide values if gRPC client is not already explicitly configured
        String grpcUrlForConfigSource = grpcAlreadyConfigured ? null : httpUrl;
        GrpcClientUrlConfigSource grpcConfigSource =
                new GrpcClientUrlConfigSource(grpcUrlForConfigSource);

        if (props.isEmpty()) {
            // Even if no props, we still need the runtime ConfigSource for gRPC URL mapping
            return List.of(grpcConfigSource);
        }
        return List.of(new MemoryServiceClientAliasConfigSource(props), grpcConfigSource);
    }
}
