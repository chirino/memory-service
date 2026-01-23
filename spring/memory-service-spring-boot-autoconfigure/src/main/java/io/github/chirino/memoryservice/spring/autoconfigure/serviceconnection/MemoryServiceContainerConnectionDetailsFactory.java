package io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection;

import java.net.URI;
import java.util.List;
import org.springframework.boot.testcontainers.service.connection.ContainerConnectionDetailsFactory;
import org.springframework.boot.testcontainers.service.connection.ContainerConnectionSource;
import org.testcontainers.containers.GenericContainer;

/**
 * Publishes memory-service connection details for Testcontainers based tests.
 */
public class MemoryServiceContainerConnectionDetailsFactory
        extends ContainerConnectionDetailsFactory<
                GenericContainer<?>, MemoryServiceConnectionDetails> {

    private static final List<String> CONNECTION_NAMES =
            List.of(
                    ContainerConnectionDetailsFactory.ANY_CONNECTION_NAME,
                    "memory-service",
                    "memory-service-service");

    public MemoryServiceContainerConnectionDetailsFactory() {
        super(CONNECTION_NAMES);
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
    }
}
