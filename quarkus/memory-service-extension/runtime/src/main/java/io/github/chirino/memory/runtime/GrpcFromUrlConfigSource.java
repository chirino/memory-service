package io.github.chirino.memory.runtime;

import java.net.URI;
import java.util.HashMap;
import java.util.Map;
import java.util.Set;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * A ConfigSource that provides gRPC client configuration by deriving values
 * from the memory-service-client.url at runtime.
 *
 * <p>This ConfigSource reads directly from {@link System#getenv()} and
 * {@link System#getProperty(String)} to avoid circular dependencies with
 * SmallRye Config bootstrap. Environment variables are always available
 * at JVM startup, making this approach reliable for container deployments.
 *
 * <p>This source has ordinal 150 - higher than application.properties (100)
 * but lower than environment variables (300), so explicit env var overrides
 * like {@code QUARKUS_GRPC_CLIENTS_RESPONSERESUMER_HOST} still take precedence.
 *
 * <p>Precedence for reading the URL:
 * <ol>
 *   <li>Environment variable: {@code MEMORY_SERVICE_CLIENT_URL}</li>
 *   <li>System property: {@code memory-service-client.url}</li>
 * </ol>
 *
 * @see <a href="https://quarkus.io/guides/grpc-service-consumption">Quarkus gRPC Client Guide</a>
 */
public class GrpcFromUrlConfigSource implements ConfigSource {

    private static final String GRPC_HOST = "quarkus.grpc.clients.responseresumer.host";
    private static final String GRPC_PORT = "quarkus.grpc.clients.responseresumer.port";
    private static final String GRPC_PLAIN_TEXT = "quarkus.grpc.clients.responseresumer.plain-text";
    private static final Set<String> PROPERTY_NAMES = Set.of(GRPC_HOST, GRPC_PORT, GRPC_PLAIN_TEXT);

    private static final String ENV_VAR_NAME = "MEMORY_SERVICE_CLIENT_URL";
    private static final String SYSTEM_PROP_NAME = "memory-service-client.url";

    // Cache parsed values to avoid repeated URL parsing
    private volatile String cachedUrl;
    private volatile ParsedUrl cachedParsed;

    @Override
    public String getValue(String propertyName) {
        if (!PROPERTY_NAMES.contains(propertyName)) {
            return null;
        }

        ParsedUrl parsed = getParsedUrl();
        if (parsed == null) {
            return null;
        }

        return switch (propertyName) {
            case GRPC_HOST -> parsed.host;
            case GRPC_PORT -> parsed.port;
            case GRPC_PLAIN_TEXT -> parsed.plainText;
            default -> null;
        };
    }

    @Override
    public Map<String, String> getProperties() {
        ParsedUrl parsed = getParsedUrl();
        if (parsed == null) {
            return Map.of();
        }
        Map<String, String> props = new HashMap<>();
        props.put(GRPC_HOST, parsed.host);
        props.put(GRPC_PORT, parsed.port);
        props.put(GRPC_PLAIN_TEXT, parsed.plainText);
        return props;
    }

    @Override
    public Set<String> getPropertyNames() {
        ParsedUrl parsed = getParsedUrl();
        if (parsed != null) {
            return PROPERTY_NAMES;
        }
        return Set.of();
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

    private ParsedUrl getParsedUrl() {
        // Read the URL from environment variables first (preferred for containers),
        // then fall back to system properties (useful for local testing with -D flags).
        String url = System.getenv(ENV_VAR_NAME);
        if (url == null || url.isBlank()) {
            url = System.getProperty(SYSTEM_PROP_NAME);
        }
        if (url == null || url.isBlank()) {
            return null;
        }

        // Use cached value if URL hasn't changed
        if (url.equals(cachedUrl) && cachedParsed != null) {
            return cachedParsed;
        }

        // Parse and cache
        cachedUrl = url;
        cachedParsed = parseUrl(url);
        return cachedParsed;
    }

    private ParsedUrl parseUrl(String url) {
        try {
            URI uri = URI.create(url);

            // Validate that we have a proper URL with scheme and host
            String scheme = uri.getScheme();
            if (scheme == null || (!scheme.equals("http") && !scheme.equals("https"))) {
                // Not a valid HTTP/HTTPS URL
                return null;
            }

            String host = uri.getHost();
            if (host == null || host.isBlank()) {
                // URL without a host is invalid
                return null;
            }

            int portNum = uri.getPort();
            if (portNum == -1) {
                // Default port based on scheme
                portNum = "https".equals(scheme) ? 443 : 80;
            }
            String port = String.valueOf(portNum);

            // plain-text=true for http, false for https
            String plainText = "https".equals(scheme) ? "false" : "true";

            return new ParsedUrl(host, port, plainText);
        } catch (Exception e) {
            // If URL parsing fails, return null to let other sources provide values
            return null;
        }
    }

    private record ParsedUrl(String host, String port, String plainText) {}
}
