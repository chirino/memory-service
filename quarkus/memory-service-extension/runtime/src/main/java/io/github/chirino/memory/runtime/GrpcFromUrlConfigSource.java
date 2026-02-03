package io.github.chirino.memory.runtime;

import java.net.URI;
import java.util.Map;
import java.util.Set;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * A ConfigSource that provides gRPC client configuration by deriving values
 * from the memory-service.client.url.
 *
 * <p>This ConfigSource is created by {@link GrpcFromUrlConfigSourceFactory}
 * which reads the URL from the config context (including application.properties).
 *
 * <p>This source has ordinal 150 - higher than application.properties (100)
 * but lower than environment variables (300), so explicit env var overrides
 * like {@code QUARKUS_GRPC_CLIENTS_RESPONSERESUMER_HOST} still take precedence.
 *
 * @see GrpcFromUrlConfigSourceFactory
 * @see <a href="https://quarkus.io/guides/grpc-service-consumption">Quarkus gRPC Client Guide</a>
 */
public class GrpcFromUrlConfigSource implements ConfigSource {

    static final String GRPC_HOST = "quarkus.grpc.clients.responseresumer.host";
    static final String GRPC_PORT = "quarkus.grpc.clients.responseresumer.port";
    static final String GRPC_PLAIN_TEXT = "quarkus.grpc.clients.responseresumer.plain-text";
    static final Set<String> PROPERTY_NAMES = Set.of(GRPC_HOST, GRPC_PORT, GRPC_PLAIN_TEXT);

    private final Map<String, String> properties;

    /**
     * Creates a new config source with the given pre-computed properties.
     *
     * @param properties the gRPC client properties derived from the URL
     */
    GrpcFromUrlConfigSource(Map<String, String> properties) {
        this.properties = properties;
    }

    @Override
    public String getValue(String propertyName) {
        return properties.get(propertyName);
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
    public String getName() {
        return "memory-service-grpc-url-derived";
    }

    @Override
    public int getOrdinal() {
        // Higher than application.properties (100) but lower than env vars (300)
        // This allows explicit QUARKUS_GRPC_CLIENTS_* to override derived values
        return 150;
    }

    /**
     * Parses a URL and extracts gRPC client configuration properties.
     *
     * @param url the URL to parse
     * @return the gRPC client properties, or an empty map if the URL is invalid
     */
    static Map<String, String> parseUrl(String url) {
        if (url == null || url.isBlank()) {
            return Map.of();
        }

        try {
            URI uri = URI.create(url);

            // Validate that we have a proper URL with scheme and host
            String scheme = uri.getScheme();
            if (scheme == null || (!scheme.equals("http") && !scheme.equals("https"))) {
                // Not a valid HTTP/HTTPS URL
                return Map.of();
            }

            String host = uri.getHost();
            if (host == null || host.isBlank()) {
                // URL without a host is invalid
                return Map.of();
            }

            int portNum = uri.getPort();
            if (portNum == -1) {
                // Default port based on scheme
                portNum = "https".equals(scheme) ? 443 : 80;
            }
            String port = String.valueOf(portNum);

            // plain-text=true for http, false for https
            String plainText = "https".equals(scheme) ? "false" : "true";

            return Map.of(
                    GRPC_HOST, host,
                    GRPC_PORT, port,
                    GRPC_PLAIN_TEXT, plainText);
        } catch (Exception e) {
            // If URL parsing fails, return empty map to let other sources provide values
            return Map.of();
        }
    }
}
