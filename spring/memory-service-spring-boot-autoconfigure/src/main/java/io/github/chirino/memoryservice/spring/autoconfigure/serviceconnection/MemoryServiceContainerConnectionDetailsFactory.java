package io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection;

import java.net.URI;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.testcontainers.service.connection.ContainerConnectionDetailsFactory;
import org.springframework.boot.testcontainers.service.connection.ContainerConnectionSource;
import org.springframework.util.StringUtils;
import org.testcontainers.containers.GenericContainer;

/**
 * Publishes memory-service connection details for Testcontainers based tests.
 */
public class MemoryServiceContainerConnectionDetailsFactory
        extends ContainerConnectionDetailsFactory<
                GenericContainer<?>, MemoryServiceConnectionDetails> {

    private static final Logger LOG =
            LoggerFactory.getLogger(MemoryServiceContainerConnectionDetailsFactory.class);

    public MemoryServiceContainerConnectionDetailsFactory() {
        super(
                List.of(
                        "ghcr.io/chirino/memory-service:latest",
                        "ghcr.io/chirino/memory-service",
                        "memory-service"));
    }

    @Override
    protected MemoryServiceConnectionDetails getContainerConnectionDetails(
            ContainerConnectionSource<GenericContainer<?>> source) {
        return new MemoryServiceContainerConnectionDetails(source);
    }

    private static final class MemoryServiceContainerConnectionDetails
            extends ContainerConnectionDetailsFactory.ContainerConnectionDetails<
                    GenericContainer<?>>
            implements MemoryServiceConnectionDetails {

        private MemoryServiceContainerConnectionDetails(
                ContainerConnectionSource<GenericContainer<?>> source) {
            super(source);
        }

        @Override
        public URI getBaseUri() {
            GenericContainer<?> container = getContainer();
            Integer httpPort = container.getMappedPort(8080);
            return URI.create("http://" + container.getHost() + ":" + httpPort);
        }

        @Override
        public String getApiKey() {
            GenericContainer<?> container = getContainer();
            Map<String, String> env = container.getEnvMap();
            String apiKey = firstApiKey(env);
            LOG.info(
                    "MemoryService Testcontainers env detected: apiKeyPresent={}, envKeys={},"
                            + " MEMORY_SERVICE_API_KEYS_AGENT={}",
                    StringUtils.hasText(apiKey),
                    env.keySet(),
                    env.get("MEMORY_SERVICE_API_KEYS_AGENT"));
            return apiKey;
        }
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
