package io.github.chirino.memoryservice.client;

import java.net.URI;
import org.springframework.util.Assert;
import org.springframework.util.StringUtils;

public final class MemoryServiceEndpoint {

    private static final String DEFAULT_URL = "http://localhost:8080";
    private static final String LOGICAL_UNIX_BASE_URL = "http://localhost";

    private final String configuredUrl;
    private final String logicalBaseUrl;
    private final String unixSocketPath;
    private final URI tcpUri;

    private MemoryServiceEndpoint(
            String configuredUrl, String logicalBaseUrl, String unixSocketPath, URI tcpUri) {
        this.configuredUrl = configuredUrl;
        this.logicalBaseUrl = logicalBaseUrl;
        this.unixSocketPath = unixSocketPath;
        this.tcpUri = tcpUri;
    }

    public static MemoryServiceEndpoint parse(String url) {
        String configuredUrl = StringUtils.hasText(url) ? url.trim() : DEFAULT_URL;
        URI uri = URI.create(configuredUrl);
        String scheme = uri.getScheme();
        Assert.hasText(
                scheme,
                "memory-service.client.url must use http://, https://, or unix:///absolute/path");
        if ("unix".equalsIgnoreCase(scheme)) {
            String path = uri.getPath();
            Assert.hasText(path, "memory-service.client.url must use unix:///absolute/path syntax");
            Assert.isTrue(
                    path.startsWith("/"),
                    "memory-service.client.url must use unix:///absolute/path syntax");
            return new MemoryServiceEndpoint(
                    configuredUrl, LOGICAL_UNIX_BASE_URL, path, URI.create(LOGICAL_UNIX_BASE_URL));
        }
        Assert.isTrue(
                "http".equalsIgnoreCase(scheme) || "https".equalsIgnoreCase(scheme),
                "memory-service.client.url must use http://, https://, or unix:///absolute/path");
        return new MemoryServiceEndpoint(configuredUrl, configuredUrl, null, uri);
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
