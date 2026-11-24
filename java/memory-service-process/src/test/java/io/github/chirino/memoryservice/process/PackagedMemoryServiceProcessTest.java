package io.github.chirino.memoryservice.process;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

import java.io.IOException;
import java.io.InputStream;
import java.net.ServerSocket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/**
 * Runs the real native executable described by the descriptors a release build bundles. Enabled
 * with {@code -Dmemory-service.packaged.test=true}, which CI sets after staging descriptors with
 * {@code -Pbinary-descriptors}.
 */
class PackagedMemoryServiceProcessTest {

    private static final List<String> RELEASED_PLATFORMS =
            List.of("linux-amd64", "linux-arm64", "macos-arm64");

    @TempDir Path temporaryDirectory;

    @BeforeEach
    void requireStagedDescriptors() {
        assumeTrue(
                Boolean.getBoolean("memory-service.packaged.test"),
                "enabled only when CI stages real native binary descriptors");
    }

    @Test
    void bundlesDescriptorsForEveryReleasedPlatform() throws IOException {
        ClassLoader resources = MemoryServiceBinary.class.getClassLoader();
        for (String platform : RELEASED_PLATFORMS) {
            String root = BundledBinaryProvider.ROOT + platform + "/";
            for (String name :
                    List.of(BundledBinaryProvider.DESCRIPTOR, BundledBinaryProvider.SHA256)) {
                try (InputStream input = resources.getResourceAsStream(root + name)) {
                    assertTrue(input != null, "missing " + root + name);
                }
            }
            assertEquals(null, resources.getResource(root + "memory-service"), "no executable");
        }
    }

    @Test
    void downloadsStartsAndStopsCurrentPlatformBinary() {
        MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("state"))
                        .binaryResolvers(packaged())
                        .cacheDirectory(temporaryDirectory.resolve("cache"))
                        .startupTimeout(Duration.ofSeconds(30))
                        .start();
        assertTrue(process.isAlive());
        assertTrue(
                process.binary().source().startsWith("packaged descriptor bundled"),
                process.binary().source());
        assertTrue(Files.isRegularFile(temporaryDirectory.resolve("state/memory.db")));

        process.close();
        assertFalse(process.isAlive());
    }

    @Test
    void startsPackagedBinaryWithMainHttpListener() throws Exception {
        int port;
        try (ServerSocket socket = new ServerSocket(0)) {
            port = socket.getLocalPort();
        }

        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("http-state"))
                        .binaryResolvers(packaged())
                        .cacheDirectory(temporaryDirectory.resolve("http-cache"))
                        .httpListener(port)
                        .environment("MEMORY_SERVICE_API_KEYS_AGENT", "test-api-key")
                        .startupTimeout(Duration.ofSeconds(30))
                        .start()) {
            assertTrue(process.isAlive());
            assertEquals("http://127.0.0.1:" + port, process.target());
            assertTrue(process.unixSocketPath().isEmpty());
            assertTrue(Files.isRegularFile(temporaryDirectory.resolve("http-state/memory.db")));
        }
    }

    /**
     * CI pushes the native binaries to a throwaway local registry and sets {@code
     * memory-service.packaged.test.mirror} to it, which also exercises the mirror path.
     */
    private static MemoryServiceBinary.Resolver packaged() {
        String mirror = System.getProperty("memory-service.packaged.test.mirror", "");
        if (mirror.isBlank()) {
            return MemoryServiceBinary.packaged();
        }
        return MemoryServiceBinary.packaged(
                MemoryServiceBinaryRegistries.builder()
                        .mirror("ghcr.io", mirror)
                        .githubTokenFromEnvironment(false)
                        .build());
    }
}
