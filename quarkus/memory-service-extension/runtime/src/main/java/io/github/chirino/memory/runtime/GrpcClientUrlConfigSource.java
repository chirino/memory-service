package io.github.chirino.memory.runtime;

import java.net.URI;
import java.util.Map;
import java.util.Set;
import org.eclipse.microprofile.config.ConfigProvider;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * Runtime ConfigSource that automatically configures the gRPC client
 * from the memory-service HTTP URL. This ConfigSource lazily reads the URL
 * at runtime to support environment variable configuration in containers.
 *
 * According to Quarkus gRPC client configuration, we need to set:
 * - quarkus.grpc.clients.responseresumer.host
 * - quarkus.grpc.clients.responseresumer.port
 * - quarkus.grpc.clients.responseresumer.plain-text (based on URL scheme: true for http, false for https)
 */
public class GrpcClientUrlConfigSource implements ConfigSource {

    private static final String GRPC_CLIENT_HOST = "quarkus.grpc.clients.responseresumer.host";
    private static final String GRPC_CLIENT_PORT = "quarkus.grpc.clients.responseresumer.port";
    private static final String GRPC_CLIENT_PLAIN_TEXT =
            "quarkus.grpc.clients.responseresumer.plain-text";

    // URL sources to check (in order of preference)
    private static final String MEMORY_SERVICE_CLIENT_URL = "memory-service-client.url";
    private static final String REST_CLIENT_URL = "quarkus.rest-client.memory-service-client.url";

    // Cache parsed values to avoid repeated parsing
    private volatile ParsedUrl cachedParsedUrl;
    private volatile String cachedSourceUrl;

    private static class ParsedUrl {
        final String host;
        final String port;
        final String plainText;

        ParsedUrl(String host, String port, String plainText) {
            this.host = host;
            this.port = port;
            this.plainText = plainText;
        }
    }

    GrpcClientUrlConfigSource(String buildTimeUrl) {
        // If a URL was available at build time, cache it
        if (buildTimeUrl != null && !buildTimeUrl.isBlank()) {
            this.cachedSourceUrl = buildTimeUrl;
            this.cachedParsedUrl = parseUrl(buildTimeUrl);
        }
    }

    private ParsedUrl getParsedUrl() {
        // First check if we have a cached value
        ParsedUrl cached = cachedParsedUrl;
        if (cached != null) {
            return cached;
        }

        // Try to read the URL from config at runtime
        String httpUrl = null;
        try {
            var config = ConfigProvider.getConfig();
            httpUrl =
                    config.getOptionalValue(MEMORY_SERVICE_CLIENT_URL, String.class)
                            .or(() -> config.getOptionalValue(REST_CLIENT_URL, String.class))
                            .orElse(null);
        } catch (Exception e) {
            // Config not available yet, return null
            return null;
        }

        if (httpUrl == null || httpUrl.isBlank()) {
            return null;
        }

        // Cache the parsed result
        if (!httpUrl.equals(cachedSourceUrl)) {
            cachedSourceUrl = httpUrl;
            cachedParsedUrl = parseUrl(httpUrl);
        }

        return cachedParsedUrl;
    }

    private ParsedUrl parseUrl(String httpUrl) {
        try {
            URI uri = URI.create(httpUrl);
            String host = uri.getHost() != null ? uri.getHost() : "localhost";
            int portNum = uri.getPort();
            if (portNum == -1) {
                portNum = "https".equals(uri.getScheme()) ? 443 : 80;
            }
            String port = String.valueOf(portNum);
            String plainText = "https".equals(uri.getScheme()) ? "false" : "true";
            return new ParsedUrl(host, port, plainText);
        } catch (Exception e) {
            // If URL parsing fails, return defaults
            return new ParsedUrl("localhost", "8080", "true");
        }
    }

    @Override
    public Map<String, String> getProperties() {
        ParsedUrl parsed = getParsedUrl();
        if (parsed != null) {
            return Map.of(
                    GRPC_CLIENT_HOST, parsed.host,
                    GRPC_CLIENT_PORT, parsed.port,
                    GRPC_CLIENT_PLAIN_TEXT, parsed.plainText);
        }
        return Map.of();
    }

    @Override
    public Set<String> getPropertyNames() {
        ParsedUrl parsed = getParsedUrl();
        if (parsed != null) {
            return Set.of(GRPC_CLIENT_HOST, GRPC_CLIENT_PORT, GRPC_CLIENT_PLAIN_TEXT);
        }
        return Set.of();
    }

    @Override
    public String getValue(String propertyName) {
        ParsedUrl parsed = getParsedUrl();
        if (parsed == null) {
            return null;
        }
        if (GRPC_CLIENT_HOST.equals(propertyName)) {
            return parsed.host;
        }
        if (GRPC_CLIENT_PORT.equals(propertyName)) {
            return parsed.port;
        }
        if (GRPC_CLIENT_PLAIN_TEXT.equals(propertyName)) {
            return parsed.plainText;
        }
        return null;
    }

    @Override
    public String getName() {
        return "memory-service-grpc-client-url";
    }

    @Override
    public int getOrdinal() {
        // Lower ordinal than environment variables (300) so explicit config takes precedence
        // but higher than application.properties (100) to provide computed defaults
        return 250;
    }
}
