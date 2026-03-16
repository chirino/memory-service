package io.github.chirino.memory.runtime;

import java.net.URI;

public final class MemoryServiceClientUrl {

    private static final String DEFAULT_URL = "http://localhost:8080";
    private static final String LOGICAL_UNIX_BASE_URL = "http://localhost";

    private final String configuredUrl;
    private final String logicalBaseUrl;
    private final String unixSocketPath;
    private final URI tcpUri;

    private MemoryServiceClientUrl(
            String configuredUrl, String logicalBaseUrl, String unixSocketPath, URI tcpUri) {
        this.configuredUrl = configuredUrl;
        this.logicalBaseUrl = logicalBaseUrl;
        this.unixSocketPath = unixSocketPath;
        this.tcpUri = tcpUri;
    }

    public static MemoryServiceClientUrl parse(String url) {
        String configuredUrl = url == null || url.isBlank() ? DEFAULT_URL : url.trim();
        URI uri = URI.create(configuredUrl);
        String scheme = uri.getScheme();
        if (scheme == null || scheme.isBlank()) {
            throw new IllegalArgumentException(
                    "memory-service.client.url must use http://, https://, or"
                            + " unix:///absolute/path");
        }
        if ("unix".equalsIgnoreCase(scheme)) {
            String path = uri.getPath();
            if (path == null || path.isBlank() || !path.startsWith("/")) {
                throw new IllegalArgumentException(
                        "memory-service.client.url must use unix:///absolute/path syntax");
            }
            return new MemoryServiceClientUrl(
                    configuredUrl, LOGICAL_UNIX_BASE_URL, path, URI.create(LOGICAL_UNIX_BASE_URL));
        }
        if (!"http".equalsIgnoreCase(scheme) && !"https".equalsIgnoreCase(scheme)) {
            throw new IllegalArgumentException(
                    "memory-service.client.url must use http://, https://, or"
                            + " unix:///absolute/path");
        }
        return new MemoryServiceClientUrl(configuredUrl, configuredUrl, null, uri);
    }

    public String configuredUrl() {
        return configuredUrl;
    }

    public String logicalBaseUrl() {
        return logicalBaseUrl;
    }

    public String unixSocketPath() {
        return unixSocketPath;
    }

    public URI tcpUri() {
        return tcpUri;
    }

    public boolean usesUnixSocket() {
        return unixSocketPath != null;
    }
}
