package io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection;

import java.net.URI;
import org.springframework.boot.docker.compose.core.RunningService;
import org.springframework.boot.docker.compose.service.connection.DockerComposeConnectionDetailsFactory;
import org.springframework.boot.docker.compose.service.connection.DockerComposeConnectionSource;

/**
 * Publishes memory-service connection details when the container is started via
 * Spring Boot's Docker Compose support.
 */
public class MemoryServiceDockerComposeConnectionDetailsFactory
        extends DockerComposeConnectionDetailsFactory<MemoryServiceConnectionDetails> {

    private static final String[] SERVICE_NAMES = {"memory-service", "memory-service-service"};

    public MemoryServiceDockerComposeConnectionDetailsFactory() {
        super(SERVICE_NAMES);
    }

    @Override
    protected MemoryServiceConnectionDetails getDockerComposeConnectionDetails(
            DockerComposeConnectionSource source) {
        RunningService service = source.getRunningService();
        int httpPort = service.ports().get(8080);
        URI baseUri = URI.create("http://" + service.host() + ":" + httpPort);
        return new DefaultMemoryServiceConnectionDetails(baseUri);
    }
}
