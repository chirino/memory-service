package io.github.chirino.memoryservice.process.binaries;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

import io.github.chirino.memoryservice.process.MemoryServiceProcess;
import java.net.ServerSocket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class PackagedMemoryServiceProcessTest {

    @TempDir Path temporaryDirectory;

    @Test
    void extractsStartsAndStopsCurrentPlatformBinary() {
        assumeTrue(
                Boolean.getBoolean("memory-service.packaged.test"),
                "enabled only when CI stages real native binaries");

        MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("state"))
                        .cacheDirectory(temporaryDirectory.resolve("cache"))
                        .startupTimeout(Duration.ofSeconds(30))
                        .start();
        assertTrue(process.isAlive());
        assertTrue(process.binary().source().startsWith("packaged provider"));
        assertTrue(Files.isRegularFile(temporaryDirectory.resolve("state/memory.db")));

        process.close();
        assertFalse(process.isAlive());
    }

    @Test
    void startsPackagedBinaryWithMainHttpListener() throws Exception {
        assumeTrue(
                Boolean.getBoolean("memory-service.packaged.test"),
                "enabled only when CI stages real native binaries");
        int port;
        try (ServerSocket socket = new ServerSocket(0)) {
            port = socket.getLocalPort();
        }

        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("http-state"))
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
}
