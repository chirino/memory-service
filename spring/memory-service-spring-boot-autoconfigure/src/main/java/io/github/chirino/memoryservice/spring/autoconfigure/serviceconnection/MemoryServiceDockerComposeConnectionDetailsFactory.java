package io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection;

import java.net.URI;
import java.util.Arrays;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.docker.compose.core.RunningService;
import org.springframework.boot.docker.compose.service.connection.DockerComposeConnectionDetailsFactory;
import org.springframework.boot.docker.compose.service.connection.DockerComposeConnectionSource;
import org.springframework.util.StringUtils;

/**
 * Publishes memory-service connection details when the container is started via
 * Spring Boot's Docker Compose support.
 */
public class MemoryServiceDockerComposeConnectionDetailsFactory
        extends DockerComposeConnectionDetailsFactory<MemoryServiceConnectionDetails> {

    private static final Logger LOG =
            LoggerFactory.getLogger(MemoryServiceDockerComposeConnectionDetailsFactory.class);

    public MemoryServiceDockerComposeConnectionDetailsFactory() {
        super(
                new String[] {
                    "ghcr.io/chirino/memory-service:latest",
                    "ghcr.io/chirino/memory-service",
                    "memory-service"
                });
    }

    @Override
    protected MemoryServiceConnectionDetails getDockerComposeConnectionDetails(
            DockerComposeConnectionSource source) {
        RunningService service = source.getRunningService();
        Integer httpPort;
        try {
            httpPort = service.ports().get(8080);
        } catch (IllegalStateException e) {
            LOG.warn(
                    "Skipping service '{}' for memory-service connection: no host port mapping for"
                            + " container port 8080. Available ports: {}",
                    service.name(),
                    service.ports().getAll());
            throw e;
        }
        URI baseUri = URI.create("http://" + service.host() + ":" + httpPort);

        LOG.info(
                "MemoryService Docker Compose connection detected for service '{}': envKeys={}",
                service.name(),
                service.env());
        String apiKey = firstApiKey(service.env());
        LOG.info(
                "MemoryService Docker Compose connection detected for service '{}': baseUri={},"
                        + " apiKeyPresent={}, envKeys={}",
                service.name(),
                baseUri,
                StringUtils.hasText(apiKey),
                service.env().keySet());
        return new DefaultMemoryServiceConnectionDetails(baseUri, apiKey);
    }

    private static String firstApiKey(Map<String, String> env) {
        String csv = env.get("MEMORY_SERVICE_API_KEYS_AGENT");
        if (!StringUtils.hasText(csv)) {
            return null;
        }
        return Arrays.stream(csv.split(","))
                .map(String::trim)
                .filter(StringUtils::hasText)
                .findFirst()
                .orElse(null);
    }
}
