package io.github.chirino.memory;

import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;
import java.util.Map;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.utility.DockerImageName;

public class QdrantTestResource implements QuarkusTestResourceLifecycleManager {

    private GenericContainer<?> qdrant;

    @Override
    public Map<String, String> start() {
        // Docker Engine v29+ can reject the default API version negotiated by docker-java.
        System.setProperty("api.version", System.getProperty("api.version", "1.44"));

        qdrant =
                new GenericContainer<>(DockerImageName.parse("qdrant/qdrant:v1.15.4"))
                        .withExposedPorts(6333, 6334);
        qdrant.start();

        return Map.of(
                "memory-service.vector.qdrant.host",
                qdrant.getHost(),
                "memory-service.vector.qdrant.port",
                String.valueOf(qdrant.getMappedPort(6334)),
                "memory-service.vector.qdrant.use-tls",
                "false");
    }

    @Override
    public void stop() {
        if (qdrant != null) {
            qdrant.stop();
        }
    }
}
