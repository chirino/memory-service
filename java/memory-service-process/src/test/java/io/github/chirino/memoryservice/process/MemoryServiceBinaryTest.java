package io.github.chirino.memoryservice.process;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermission;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.Callable;
import java.util.concurrent.Executors;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class MemoryServiceBinaryTest {

    private static final MemoryServicePlatform TEST_PLATFORM =
            new MemoryServicePlatform("test", "test");

    @TempDir Path temporaryDirectory;

    @Test
    void discoversExtractsAndRepairsPackagedBinary() throws Exception {
        Path cache = temporaryDirectory.resolve("cache");
        MemoryServiceBinary first = packaged(cache);

        assertEquals(TEST_PLATFORM, first.platform());
        assertArrayEquals(
                "fake-binary\n".getBytes(StandardCharsets.UTF_8),
                Files.readAllBytes(first.executable()));
        assertTrue(Files.isExecutable(first.executable()));
        if (Files.getFileStore(first.executable()).supportsFileAttributeView("posix")) {
            Set<PosixFilePermission> ownerOnly =
                    Set.of(
                            PosixFilePermission.OWNER_READ,
                            PosixFilePermission.OWNER_WRITE,
                            PosixFilePermission.OWNER_EXECUTE);
            assertEquals(ownerOnly, Files.getPosixFilePermissions(first.executable()));
            assertEquals(ownerOnly, Files.getPosixFilePermissions(cache));
            assertEquals(ownerOnly, Files.getPosixFilePermissions(first.executable().getParent()));
        }

        Files.writeString(first.executable(), "corrupt", StandardCharsets.UTF_8);
        MemoryServiceBinary repaired = packaged(cache);
        assertEquals(first.executable(), repaired.executable());
        assertArrayEquals(
                "fake-binary\n".getBytes(StandardCharsets.UTF_8),
                Files.readAllBytes(repaired.executable()));
    }

    @Test
    void concurrentExtractionReturnsOneVerifiedPath() throws Exception {
        Path cache = temporaryDirectory.resolve("cache");
        List<Callable<Path>> work = new ArrayList<>();
        for (int i = 0; i < 8; i++) {
            work.add(() -> packaged(cache).executable());
        }
        try (var executor = Executors.newFixedThreadPool(work.size())) {
            var results = executor.invokeAll(work);
            Path expected = results.get(0).get();
            for (var result : results) {
                assertEquals(expected, result.get());
            }
        }
    }

    @Test
    void orderedResolversSkipOnlyUnavailableCandidates() throws Exception {
        Path executable = temporaryDirectory.resolve("memory-service");
        Files.writeString(executable, "fixture");
        assertTrue(executable.toFile().setExecutable(true, true));

        MemoryServiceBinary resolved =
                MemoryServiceBinary.resolve(
                        List.of(
                                MemoryServiceBinary.file(temporaryDirectory.resolve("missing")),
                                MemoryServiceBinary.file(executable)),
                        temporaryDirectory.resolve("cache"),
                        TEST_PLATFORM,
                        getClass().getClassLoader(),
                        Map.of());
        assertEquals(executable.toAbsolutePath(), resolved.executable());
    }

    @Test
    void pathResolverUsesProvidedSearchOrder() throws Exception {
        Path first = temporaryDirectory.resolve("first");
        Path second = temporaryDirectory.resolve("second");
        Files.createDirectories(first);
        Files.createDirectories(second);
        Path executable = second.resolve("memory-service");
        Files.writeString(executable, "fixture");
        assertTrue(executable.toFile().setExecutable(true, true));

        MemoryServiceBinary resolved =
                MemoryServiceBinary.resolve(
                        List.of(MemoryServiceBinary.onPath("memory-service")),
                        temporaryDirectory.resolve("cache"),
                        TEST_PLATFORM,
                        getClass().getClassLoader(),
                        Map.of("PATH", first + java.io.File.pathSeparator + second));
        assertEquals(executable.toAbsolutePath(), resolved.executable());
    }

    @Test
    void checksumFailureIsTerminal() {
        MemoryServiceBinaryProvider invalid =
                new MemoryServiceBinaryProvider() {
                    @Override
                    public MemoryServicePlatform platform() {
                        return TEST_PLATFORM;
                    }

                    @Override
                    public InputStream openBinary() {
                        return new ByteArrayInputStream("wrong".getBytes(StandardCharsets.UTF_8));
                    }

                    @Override
                    public InputStream openSha256() {
                        return new ByteArrayInputStream(
                                ("a23de3623db1cff0644e2de7c621a30742872393d67dc8941f42e87f7c1dbf50\n")
                                        .getBytes(StandardCharsets.US_ASCII));
                    }
                };

        IOException error =
                assertThrows(
                        IOException.class,
                        () ->
                                MemoryServiceBinary.extractProvider(
                                        invalid, temporaryDirectory.resolve("cache")));
        assertTrue(error.getMessage().contains("checksum mismatch"));
    }

    @Test
    void reportsAllUnavailableResolvers() {
        IOException error =
                assertThrows(
                        IOException.class,
                        () ->
                                MemoryServiceBinary.resolve(
                                        List.of(
                                                MemoryServiceBinary.file(
                                                        temporaryDirectory.resolve("missing")),
                                                MemoryServiceBinary.onPath("also-missing")),
                                        temporaryDirectory.resolve("cache"),
                                        TEST_PLATFORM,
                                        getClass().getClassLoader(),
                                        Map.of("PATH", temporaryDirectory.toString())));
        assertTrue(error.getMessage().contains("attempted"));
    }

    private MemoryServiceBinary packaged(Path cache) throws IOException {
        return MemoryServiceBinary.resolve(
                List.of(MemoryServiceBinary.packaged()),
                cache,
                TEST_PLATFORM,
                getClass().getClassLoader(),
                Map.of());
    }
}
