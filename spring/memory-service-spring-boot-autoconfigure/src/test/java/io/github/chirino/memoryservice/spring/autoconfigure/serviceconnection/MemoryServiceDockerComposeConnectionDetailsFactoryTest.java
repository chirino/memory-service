package io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.boot.docker.compose.core.ConnectionPorts;
import org.springframework.boot.docker.compose.core.ImageReference;
import org.springframework.boot.docker.compose.core.RunningService;
import org.springframework.boot.docker.compose.service.connection.DockerComposeConnectionSource;

class MemoryServiceDockerComposeConnectionDetailsFactoryTest {

    @Test
    void exposesFirstApiKeyFromEnv() {
        RunningService service =
                new StubRunningService(
                        Map.of("MEMORY_SERVICE_API_KEYS_AGENT", "first-key,second-key"), 18080);

        // Use Mockito to mock DockerComposeConnectionSource since its constructor is not public
        DockerComposeConnectionSource source = mock(DockerComposeConnectionSource.class);
        when(source.getRunningService()).thenReturn(service);

        MemoryServiceDockerComposeConnectionDetailsFactory factory =
                new MemoryServiceDockerComposeConnectionDetailsFactory();
        MemoryServiceConnectionDetails details = factory.getDockerComposeConnectionDetails(source);

        assertThat(details.getApiKey()).isEqualTo("first-key");
        assertThat(details.getBaseUrl()).isEqualTo("http://localhost:18080");
    }

    private static final class StubRunningService implements RunningService {

        private final Map<String, String> env;
        private final ConnectionPorts ports;

        private StubRunningService(Map<String, String> env, int mappedPort) {
            this.env = env;
            this.ports = new StubPorts(mappedPort);
        }

        @Override
        public String name() {
            return "memory-service";
        }

        @Override
        public ImageReference image() {
            return ImageReference.of("ghcr.io/chirino/memory-service:latest");
        }

        @Override
        public String host() {
            return "localhost";
        }

        @Override
        public ConnectionPorts ports() {
            return ports;
        }

        @Override
        public Map<String, String> env() {
            return env;
        }

        @Override
        public Map<String, String> labels() {
            return Map.of();
        }
    }

    private static final class StubPorts implements ConnectionPorts {

        private final int mappedPort;

        private StubPorts(int mappedPort) {
            this.mappedPort = mappedPort;
        }

        @Override
        public int get(int port) {
            return mappedPort;
        }

        @Override
        public List<Integer> getAll() {
            return List.of(mappedPort);
        }

        @Override
        public List<Integer> getAll(String name) {
            return List.of(mappedPort);
        }
    }
}
