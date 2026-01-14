package io.github.chirino.memory.resumer;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Disposes;
import jakarta.enterprise.inject.Produces;
import java.time.Duration;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class RedisTempFileResumerBackendProducer {
    private static final Duration RESPONSE_TTL = Duration.ofSeconds(10);
    private static final Duration RESPONSE_REFRESH = Duration.ofSeconds(5);

    @ConfigProperty(name = "memory-service.response-resumer.temp-dir")
    Optional<String> tempDir;

    @ConfigProperty(
            name = "memory-service.response-resumer.temp-file-retention",
            defaultValue = "PT30M")
    Duration tempFileRetention;

    @ConfigProperty(name = "memory-service.grpc-advertised-address")
    Optional<String> advertisedAddress;

    @Produces
    @ApplicationScoped
    TempFileResumerBackend produceTempFileResumerBackend(
            RedisResponseResumerLocatorStore locatorStore) {
        TempFileResumerBackend backend =
                new TempFileResumerBackend(
                        locatorStore,
                        RESPONSE_TTL,
                        RESPONSE_REFRESH,
                        tempDir,
                        tempFileRetention,
                        advertisedAddress);
        backend.start();
        return backend;
    }

    void disposeTempFileResumerBackend(@Disposes TempFileResumerBackend backend) {
        backend.stop();
    }
}
