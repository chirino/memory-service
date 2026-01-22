package io.github.chirino.memory.runtime;

import java.net.URI;
import java.util.HashMap;
import java.util.Map;
import java.util.Set;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * Runtime ConfigSource that automatically configures the gRPC client
 * from the memory-service HTTP URL. This ConfigSource is created by
 * MemoryServiceClientAliasConfigSourceFactory which has access to the ConfigSourceContext
 * and can read values from other config sources (including dev services).
 *
 * According to Quarkus gRPC client configuration, we need to set:
 * - quarkus.grpc.clients.responseresumer.host
 * - quarkus.grpc.clients.responseresumer.port
 * - quarkus.grpc.clients.responseresumer.plain-text (based on URL scheme: true for http, false for https)
 */
public class GrpcClientUrlConfigSource implements ConfigSource {

    private final String host;
    private final String port;
    private final String plainText;
    private static final String GRPC_CLIENT_HOST = "quarkus.grpc.clients.responseresumer.host";
    private static final String GRPC_CLIENT_PORT = "quarkus.grpc.clients.responseresumer.port";
    private static final String GRPC_CLIENT_PLAIN_TEXT =
            "quarkus.grpc.clients.responseresumer.plain-text";

    GrpcClientUrlConfigSource(String httpUrl) {
        String parsedHost;
        String parsedPort;
        String parsedPlainText;

        if (httpUrl != null && !httpUrl.isBlank()) {
            try {
                URI uri = URI.create(httpUrl);
                parsedHost = uri.getHost() != null ? uri.getHost() : "localhost";
                // If port is not specified in URI, use default HTTP port based on scheme
                int portNum = uri.getPort();
                if (portNum == -1) {
                    portNum = "https".equals(uri.getScheme()) ? 443 : 80;
                }
                parsedPort = String.valueOf(portNum);
                // Configure plain-text based on URL scheme
                // If HTTPS, disable plain-text (enable TLS). If HTTP, enable plain-text.
                parsedPlainText = "https".equals(uri.getScheme()) ? "false" : "true";
            } catch (Exception e) {
                // If URL parsing fails, set defaults (assume HTTP/plain-text)
                parsedHost = "localhost";
                parsedPort = "8080";
                parsedPlainText = "true";
            }
        } else {
            parsedHost = null;
            parsedPort = null;
            parsedPlainText = null;
        }

        // Assign to final fields once
        this.host = parsedHost;
        this.port = parsedPort;
        this.plainText = parsedPlainText;
    }

    @Override
    public Map<String, String> getProperties() {
        if (host != null && port != null) {
            Map<String, String> props = new HashMap<>();
            props.put(GRPC_CLIENT_HOST, host);
            props.put(GRPC_CLIENT_PORT, port);
            if (plainText != null) {
                props.put(GRPC_CLIENT_PLAIN_TEXT, plainText);
            }
            return props;
        }
        return Map.of();
    }

    @Override
    public Set<String> getPropertyNames() {
        if (host != null && port != null) {
            Set<String> names = new java.util.HashSet<>();
            names.add(GRPC_CLIENT_HOST);
            names.add(GRPC_CLIENT_PORT);
            if (plainText != null) {
                names.add(GRPC_CLIENT_PLAIN_TEXT);
            }
            return names;
        }
        return Set.of();
    }

    @Override
    public String getValue(String propertyName) {
        if (GRPC_CLIENT_HOST.equals(propertyName) && host != null) {
            return host;
        }
        if (GRPC_CLIENT_PORT.equals(propertyName) && port != null) {
            return port;
        }
        if (GRPC_CLIENT_PLAIN_TEXT.equals(propertyName) && plainText != null) {
            return plainText;
        }
        return null;
    }

    @Override
    public String getName() {
        return "memory-service-grpc-client-url";
    }

    @Override
    public int getOrdinal() {
        // Higher ordinal than application.properties (100) so it can provide defaults
        // but lower than system properties (400) and environment variables (300)
        // This allows it to see dev services config (which typically has ordinal 100)
        return 250;
    }
}
