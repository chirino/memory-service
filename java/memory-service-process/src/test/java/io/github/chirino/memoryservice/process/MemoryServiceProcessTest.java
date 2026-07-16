package io.github.chirino.memoryservice.process;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeFalse;

import java.io.InputStream;
import java.io.OutputStream;
import java.net.ServerSocket;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Duration;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;
import java.util.function.Consumer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class MemoryServiceProcessTest {

    @TempDir Path temporaryDirectory;

    @BeforeEach
    void requireUnixSockets() {
        assumeFalse(
                MemoryServicePlatform.current().operatingSystem().equals("windows"),
                "managed local process currently requires Unix sockets");
    }

    @Test
    void startsWaitsForReadinessBridgesOutputAndCloses() throws Exception {
        Path executable = fakeExecutable();
        List<String> stdout = new CopyOnWriteArrayList<>();
        List<String> stderr = new CopyOnWriteArrayList<>();

        MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("state"))
                        .binaryResolvers(MemoryServiceBinary.file(executable))
                        .startupTimeout(Duration.ofSeconds(5))
                        .standardOutput(stdout::add)
                        .standardError(stderr::add)
                        .start();

        assertTrue(process.isAlive());
        assertTrue(process.target().startsWith("unix:///"));
        awaitContains(stdout, "fake-memory-service-stdout");
        awaitContains(stderr, "fake-memory-service-stderr");
        awaitContains(stdout, "fake-memory-service-args=[serve]");
        awaitContains(stdout, "fake-memory-service-db-kind=sqlite");
        awaitContains(stdout, "fake-memory-service-embedding-kind=none");

        process.close();
        process.close();
        assertFalse(process.isAlive());
    }

    @Test
    void environmentOverridesDefaultsAndControlsTheEffectiveSocket() throws Exception {
        Path socketDirectory = Files.createTempDirectory(Path.of("/tmp"), "msp-");
        Files.setPosixFilePermissions(
                socketDirectory, PosixFilePermissions.fromString("rwx------"));
        Path socket = socketDirectory.resolve("memory.sock");
        List<String> stdout = new CopyOnWriteArrayList<>();

        try {
            try (MemoryServiceProcess process =
                    MemoryServiceProcess.builder(temporaryDirectory.resolve("custom-state"))
                            .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                            .environment(
                                    Map.of(
                                            "MEMORY_SERVICE_UNIX_SOCKET",
                                            socket.toString(),
                                            "MEMORY_SERVICE_EMBEDDING_KIND",
                                            "local"))
                            .standardOutput(stdout::add)
                            .start()) {
                assertEquals(socket, process.socketPath());
                awaitContains(stdout, "fake-memory-service-embedding-kind=local");
            }
        } finally {
            Files.deleteIfExists(socket);
            Files.deleteIfExists(socketDirectory);
        }
    }

    @Test
    void disablesUnixSocketAndUsesTheMainHttpListener() throws Exception {
        int port = availablePort();

        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("http"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .disableUnixSocket()
                        .environment("MEMORY_SERVICE_HOST", "127.0.0.1")
                        .environment("MEMORY_SERVICE_PORT", Integer.toString(port))
                        .start()) {
            assertEquals("http://127.0.0.1:" + port, process.target());
            assertTrue(process.unixSocketPath().isEmpty());
            assertThrows(IllegalStateException.class, process::socketPath);
        }
    }

    @Test
    void unixSocketListenerOverridesPreviousHttpListener() throws Exception {
        int port = availablePort();
        Path socket = temporaryDirectory.resolve("uds-after-http/memory.sock");

        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("uds-after-http-state"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .httpListener(port)
                        .unixSocketListener(socket)
                        .start()) {
            assertEquals(socket.toAbsolutePath(), process.socketPath());
            assertEquals("unix://" + socket.toAbsolutePath(), process.target());
        }
    }

    @Test
    void normalizesBlankMainAndManagementHosts() throws Exception {
        int mainPort = availablePort();
        int managementPort = availablePort();

        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("blank-hosts"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .httpListener(mainPort)
                        .environment("MEMORY_SERVICE_HOST", " ")
                        .managementHttpListener(managementPort)
                        .environment("MEMORY_SERVICE_MANAGEMENT_HOST", "")
                        .start()) {
            assertEquals("http://127.0.0.1:" + mainPort, process.target());
            assertTrue(process.isAlive());
        }
    }

    @Test
    void usesDedicatedManagementHttpListenerForReadiness() throws Exception {
        int mainPort = availablePort();
        int managementPort = availablePort();

        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("management"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .httpListener(mainPort)
                        .managementHttpListener(managementPort)
                        .start()) {
            assertEquals("http://127.0.0.1:" + mainPort, process.target());
            assertTrue(process.isAlive());
        }
    }

    @Test
    void skipsReadinessWhenManagementEndpointIsUnavailable() throws Exception {
        int port = availablePort();
        long started = System.nanoTime();

        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("no-ready"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .httpListener(port)
                        .environment("MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER", "false")
                        .startupTimeout(Duration.ofSeconds(5))
                        .start()) {
            assertTrue(process.isAlive());
            assertTrue(
                    Duration.ofNanos(System.nanoTime() - started).compareTo(Duration.ofSeconds(2))
                            < 0);
        }
    }

    @Test
    void discardsNullOutputConsumersWithoutBlockingTheChild() throws Exception {
        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("discard-state"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .environment("FAKE_MEMORY_SERVICE_MODE", "flood-output")
                        .standardOutput(null)
                        .standardError(null)
                        .startupTimeout(Duration.ofSeconds(10))
                        .start()) {
            assertTrue(process.isAlive());
        }
    }

    @Test
    void canDiscardEitherOutputStreamIndependently() throws Exception {
        List<String> stderr = new CopyOnWriteArrayList<>();
        try (MemoryServiceProcess ignored =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("e"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .standardOutput(null)
                        .standardError(stderr::add)
                        .start()) {
            awaitContains(stderr, "fake-memory-service-stderr");
        }

        List<String> stdout = new CopyOnWriteArrayList<>();
        try (MemoryServiceProcess ignored =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("o"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .standardOutput(stdout::add)
                        .standardError(null)
                        .start()) {
            awaitContains(stdout, "fake-memory-service-stdout");
        }
    }

    @Test
    void mergesOutputWhenConsumersAreTheSameInstance() throws Exception {
        List<String> output = new CopyOnWriteArrayList<>();
        Consumer<String> consumer = output::add;

        try (MemoryServiceProcess process =
                MemoryServiceProcess.builder(temporaryDirectory.resolve("merged-state"))
                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                        .standardOutput(consumer)
                        .standardError(consumer)
                        .start()) {
            awaitContains(output, "fake-memory-service-stdout");
            awaitContains(output, "fake-memory-service-stderr");
            String suffix = "-" + process.processHandle().pid();
            assertTrue(hasThreadNamed("memory-service-output" + suffix));
            assertFalse(hasThreadNamed("memory-service-stdout" + suffix));
            assertFalse(hasThreadNamed("memory-service-stderr" + suffix));
        }
    }

    @Test
    void isolatesMemoryServiceEnvironmentUnlessInheritanceIsEnabled() {
        Map<String, String> defaults =
                Map.of(
                        "MEMORY_SERVICE_DB_KIND", "sqlite",
                        "MEMORY_SERVICE_TLS", "false");
        Map<String, String> overrides = Map.of("MEMORY_SERVICE_TLS", "true");
        Map<String, String> isolated =
                new HashMap<>(
                        Map.of(
                                "PATH", "/bin",
                                "MEMORY_SERVICE_DB_KIND", "postgres",
                                "MEMORY_SERVICE_AMBIENT_ONLY", "value"));

        MemoryServiceProcess.configureProcessEnvironment(
                isolated, defaults, java.util.Set.of(), overrides, false);

        assertEquals("/bin", isolated.get("PATH"));
        assertEquals("sqlite", isolated.get("MEMORY_SERVICE_DB_KIND"));
        assertEquals("true", isolated.get("MEMORY_SERVICE_TLS"));
        assertFalse(isolated.containsKey("MEMORY_SERVICE_AMBIENT_ONLY"));

        Map<String, String> inherited =
                new HashMap<>(
                        Map.of(
                                "MEMORY_SERVICE_DB_KIND", "postgres",
                                "MEMORY_SERVICE_AMBIENT_ONLY", "value"));
        MemoryServiceProcess.configureProcessEnvironment(
                inherited, defaults, java.util.Set.of(), Map.of(), true);

        assertEquals("postgres", inherited.get("MEMORY_SERVICE_DB_KIND"));
        assertEquals("false", inherited.get("MEMORY_SERVICE_TLS"));
        assertEquals("value", inherited.get("MEMORY_SERVICE_AMBIENT_ONLY"));

        MemoryServiceProcess.configureProcessEnvironment(
                inherited, defaults, java.util.Set.of("MEMORY_SERVICE_DB_KIND"), Map.of(), true);
        assertFalse(inherited.containsKey("MEMORY_SERVICE_DB_KIND"));
    }

    @Test
    void failsLoudlyOnEarlyExitAndIncludesRecentOutput() throws Exception {
        MemoryServiceStartException error =
                assertThrows(
                        MemoryServiceStartException.class,
                        () ->
                                MemoryServiceProcess.builder(
                                                temporaryDirectory.resolve("early-state"))
                                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                                        .environment("FAKE_MEMORY_SERVICE_MODE", "early-exit")
                                        .startupTimeout(Duration.ofSeconds(5))
                                        .start());
        assertTrue(error.getMessage().contains("exited with code 23"));
        assertTrue(error.getMessage().contains("fake-memory-service"));
    }

    @Test
    void timesOutAndTerminatesUnreadyProcess() throws Exception {
        MemoryServiceStartException error =
                assertThrows(
                        MemoryServiceStartException.class,
                        () ->
                                MemoryServiceProcess.builder(
                                                temporaryDirectory.resolve("timeout-state"))
                                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                                        .environment("FAKE_MEMORY_SERVICE_MODE", "never-ready")
                                        .startupTimeout(Duration.ofMillis(600))
                                        .shutdownTimeout(Duration.ofMillis(200))
                                        .start());
        assertTrue(error.getMessage().contains("did not become ready"));
    }

    @Test
    void forcesProcessThatIgnoresGracefulTermination() {
        UncooperativeProcess process = new UncooperativeProcess();

        MemoryServiceProcess.terminateProcess(process, Duration.ofMillis(1));

        assertTrue(process.destroyCalled);
        assertTrue(process.destroyForciblyCalled);
        assertFalse(process.isAlive());
    }

    @Test
    void rejectsInsecureSocketDirectory() throws Exception {
        Path insecure = temporaryDirectory.resolve("insecure");
        Files.createDirectory(insecure);
        if (!Files.getFileStore(insecure).supportsFileAttributeView("posix")) {
            return;
        }
        Files.setPosixFilePermissions(insecure, PosixFilePermissions.fromString("rwxr-xr-x"));

        MemoryServiceStartException error =
                assertThrows(
                        MemoryServiceStartException.class,
                        () ->
                                MemoryServiceProcess.builder(
                                                temporaryDirectory.resolve("secure-state"))
                                        .socketPath(insecure.resolve("memory.sock"))
                                        .binaryResolvers(MemoryServiceBinary.file(fakeExecutable()))
                                        .start());

        assertTrue(error.getCause().getMessage().contains("not owner-only"));
    }

    private Path fakeExecutable() throws Exception {
        URL testClasses =
                FakeMemoryServiceMain.class.getProtectionDomain().getCodeSource().getLocation();
        Path executable = temporaryDirectory.resolve("fake-memory-service-" + System.nanoTime());
        String java = Path.of(System.getProperty("java.home"), "bin", "java").toString();
        String script =
                "#!/bin/sh\nexec "
                        + shellQuote(java)
                        + " -cp "
                        + shellQuote(Path.of(testClasses.toURI()).toString())
                        + " "
                        + FakeMemoryServiceMain.class.getName()
                        + " \"$@\"\n";
        Files.writeString(executable, script, StandardCharsets.UTF_8);
        Files.setPosixFilePermissions(executable, PosixFilePermissions.fromString("rwx------"));
        return executable;
    }

    private static String shellQuote(String value) {
        return "'" + value.replace("'", "'\\''") + "'";
    }

    private static void awaitContains(List<String> lines, String expected) throws Exception {
        long deadline = System.nanoTime() + Duration.ofSeconds(2).toNanos();
        while (System.nanoTime() < deadline) {
            if (lines.contains(expected)) {
                return;
            }
            Thread.sleep(20);
        }
        assertTrue(lines.contains(expected), "missing output " + expected + " in " + lines);
    }

    private static boolean hasThreadNamed(String name) {
        return Thread.getAllStackTraces().keySet().stream()
                .anyMatch(thread -> thread.getName().equals(name));
    }

    private static int availablePort() throws Exception {
        try (ServerSocket socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private static final class UncooperativeProcess extends Process {

        private boolean destroyCalled;
        private boolean destroyForciblyCalled;

        @Override
        public OutputStream getOutputStream() {
            return OutputStream.nullOutputStream();
        }

        @Override
        public InputStream getInputStream() {
            return InputStream.nullInputStream();
        }

        @Override
        public InputStream getErrorStream() {
            return InputStream.nullInputStream();
        }

        @Override
        public int waitFor() {
            return 0;
        }

        @Override
        public boolean waitFor(long timeout, TimeUnit unit) {
            return destroyForciblyCalled;
        }

        @Override
        public int exitValue() {
            if (isAlive()) {
                throw new IllegalThreadStateException();
            }
            return 0;
        }

        @Override
        public void destroy() {
            destroyCalled = true;
        }

        @Override
        public Process destroyForcibly() {
            destroyForciblyCalled = true;
            return this;
        }

        @Override
        public boolean isAlive() {
            return !destroyForciblyCalled;
        }
    }
}
