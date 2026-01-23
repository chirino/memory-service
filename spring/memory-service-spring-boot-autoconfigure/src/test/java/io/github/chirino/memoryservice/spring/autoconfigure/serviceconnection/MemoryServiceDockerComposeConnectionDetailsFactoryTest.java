package io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection;

import static org.assertj.core.api.Assertions.assertThat;

import java.lang.reflect.Constructor;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.boot.docker.compose.core.ConnectionPorts;
import org.springframework.boot.docker.compose.core.ImageReference;
import org.springframework.boot.docker.compose.core.RunningService;
import org.springframework.boot.docker.compose.service.connection.DockerComposeConnectionSource;

class MemoryServiceDockerComposeConnectionDetailsFactoryTest {

    @Test
    void exposesFirstApiKeyFromEnv() throws Exception {
        RunningService service =
                new StubRunningService(
                        Map.of("MEMORY_SERVICE_API_KEYS_AGENT", "first-key,second-key"), 18080);
        Constructor<DockerComposeConnectionSource> constructor =
                DockerComposeConnectionSource.class.getDeclaredConstructor(RunningService.class);
        constructor.setAccessible(true);
        DockerComposeConnectionSource source = constructor.newInstance(service);

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
        public java.util.List<Integer> getAll() {
            return java.util.List.of(mappedPort);
        }

        @Override
        public java.util.List<Integer> getAll(String name) {
            return java.util.List.of(mappedPort);
        }
    }
}
