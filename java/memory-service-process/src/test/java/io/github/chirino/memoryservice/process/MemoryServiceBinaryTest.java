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

    @Test
    void downloadsOciProviderOnceThenUsesCacheWithoutNetwork() throws Exception {
        try (FakeRegistry registry = FakeRegistry.https()) {
            byte[] layer = FakeRegistry.gzip(OciBlobFetcherTest.EXECUTABLE);
            registry.handler(exchange -> FakeRegistry.respond(exchange, 200, layer));
            MemoryServiceBinaryProvider provider =
                    ociProvider(
                            registry, layer, FakeRegistry.sha256(OciBlobFetcherTest.EXECUTABLE));
            Path cache = temporaryDirectory.resolve("cache");

            Path first = MemoryServiceBinary.extractProvider(provider, cache, registries());
            assertArrayEquals(OciBlobFetcherTest.EXECUTABLE, Files.readAllBytes(first));
            assertTrue(Files.isExecutable(first));
            assertEquals(1, registry.requests().size());

            Path second = MemoryServiceBinary.extractProvider(provider, cache, registries());
            assertEquals(first, second);
            assertEquals(1, registry.requests().size(), "cached executable needs no request");
        }
    }

    @Test
    void ociDownloadWithWrongExecutableDigestIsTerminal() throws Exception {
        try (FakeRegistry registry = FakeRegistry.https()) {
            byte[] layer = FakeRegistry.gzip(OciBlobFetcherTest.EXECUTABLE);
            registry.handler(exchange -> FakeRegistry.respond(exchange, 200, layer));
            MemoryServiceBinaryProvider provider = ociProvider(registry, layer, "b".repeat(64));

            IOException error =
                    assertThrows(
                            IOException.class,
                            () ->
                                    MemoryServiceBinary.extractProvider(
                                            provider,
                                            temporaryDirectory.resolve("cache"),
                                            registries()));
            assertTrue(error.getMessage().contains("checksum mismatch"));
        }
    }

    @Test
    void concurrentOciExtractionDownloadsOnce() throws Exception {
        try (FakeRegistry registry = FakeRegistry.https()) {
            byte[] layer = FakeRegistry.gzip(OciBlobFetcherTest.EXECUTABLE);
            registry.handler(exchange -> FakeRegistry.respond(exchange, 200, layer));
            MemoryServiceBinaryProvider provider =
                    ociProvider(
                            registry, layer, FakeRegistry.sha256(OciBlobFetcherTest.EXECUTABLE));
            Path cache = temporaryDirectory.resolve("cache");
            MemoryServiceBinaryRegistries settings = registries();
            List<Callable<Path>> work = new ArrayList<>();
            for (int i = 0; i < 8; i++) {
                work.add(() -> MemoryServiceBinary.extractProvider(provider, cache, settings));
            }
            try (var executor = Executors.newFixedThreadPool(work.size())) {
                for (var result : executor.invokeAll(work)) {
                    result.get();
                }
            }
            assertEquals(1, registry.requests().size());
        }
    }

    @Test
    void fallsBackToTheDescriptorBundledInThisJar() throws Exception {
        try (FakeRegistry registry = FakeRegistry.https()) {
            byte[] layer = FakeRegistry.gzip(OciBlobFetcherTest.EXECUTABLE);
            registry.handler(exchange -> FakeRegistry.respond(exchange, 200, layer));
            MemoryServicePlatform platform = new MemoryServicePlatform("bundled", "test");
            ClassLoader bundled = bundledResources(platform, layer);

            MemoryServiceBinary resolved =
                    MemoryServiceBinary.resolve(
                            List.of(
                                    MemoryServiceBinary.packaged(
                                            MemoryServiceBinaryRegistries.builder()
                                                    .mirror(
                                                            "upstream.example",
                                                            registry.authority())
                                                    .trustedCertificates(FakeRegistry.caFile())
                                                    .environment(Map.of())
                                                    .build())),
                            temporaryDirectory.resolve("cache"),
                            platform,
                            getClass().getClassLoader(),
                            bundled,
                            Map.of());

            assertArrayEquals(
                    OciBlobFetcherTest.EXECUTABLE, Files.readAllBytes(resolved.executable()));
            assertEquals(
                    "packaged descriptor bundled in memory-service-process for bundled-test",
                    resolved.source());
            assertEquals(1, registry.requests().size());
        }
    }

    @Test
    void classpathProviderTakesPrecedenceOverBundledDescriptor() throws Exception {
        ClassLoader bundled = bundledResources(TEST_PLATFORM, new byte[] {1, 2, 3});

        MemoryServiceBinary resolved =
                MemoryServiceBinary.resolve(
                        List.of(MemoryServiceBinary.packaged()),
                        temporaryDirectory.resolve("cache"),
                        TEST_PLATFORM,
                        getClass().getClassLoader(),
                        bundled,
                        Map.of());

        assertEquals("packaged provider " + TestBinaryProvider.class.getName(), resolved.source());
        assertArrayEquals(
                "fake-binary\n".getBytes(StandardCharsets.UTF_8),
                Files.readAllBytes(resolved.executable()));
    }

    @Test
    void noProviderAndNoBundledDescriptorFallsThrough() throws Exception {
        Path executable = temporaryDirectory.resolve("memory-service");
        Files.writeString(executable, "fixture");
        assertTrue(executable.toFile().setExecutable(true, true));

        MemoryServiceBinary resolved =
                MemoryServiceBinary.resolve(
                        List.of(
                                MemoryServiceBinary.packaged(),
                                MemoryServiceBinary.file(executable)),
                        temporaryDirectory.resolve("cache"),
                        new MemoryServicePlatform("unpackaged", "test"),
                        getClass().getClassLoader(),
                        bundledResources(
                                new MemoryServicePlatform("other", "test"), new byte[] {1}),
                        Map.of());

        assertEquals(executable.toAbsolutePath(), resolved.executable());
    }

    /** A class loader that serves bundled descriptor resources for one platform. */
    private ClassLoader bundledResources(MemoryServicePlatform platform, byte[] layer)
            throws Exception {
        Path root = Files.createTempDirectory(temporaryDirectory, "bundled");
        Path directory = root.resolve(BundledBinaryProvider.ROOT + platform.id());
        Files.createDirectories(directory);
        Files.writeString(
                directory.resolve(BundledBinaryProvider.DESCRIPTOR),
                "repository=upstream.example/"
                        + FakeRegistry.REPOSITORY_NAME
                        + "\nreference=1.0.0\nlayerDigest=sha256:"
                        + FakeRegistry.sha256(layer)
                        + "\nlayerSize="
                        + layer.length
                        + "\nbinarySize="
                        + OciBlobFetcherTest.EXECUTABLE.length
                        + "\n");
        Files.writeString(
                directory.resolve(BundledBinaryProvider.SHA256),
                FakeRegistry.sha256(OciBlobFetcherTest.EXECUTABLE) + "  memory-service\n");
        return new java.net.URLClassLoader(new java.net.URL[] {root.toUri().toURL()}, null);
    }

    private static MemoryServiceBinaryRegistries registries() {
        return MemoryServiceBinaryRegistries.builder()
                .trustedCertificates(FakeRegistry.caFile())
                .environment(Map.of())
                .build();
    }

    private static MemoryServiceBinaryProvider ociProvider(
            FakeRegistry registry, byte[] layer, String executableSha256) throws Exception {
        String descriptor =
                "repository="
                        + registry.repository()
                        + "\nreference=1.0.0\nlayerDigest=sha256:"
                        + FakeRegistry.sha256(layer)
                        + "\nlayerSize="
                        + layer.length
                        + "\nbinarySize="
                        + OciBlobFetcherTest.EXECUTABLE.length
                        + "\n";
        return new MemoryServiceBinaryProvider() {
            @Override
            public MemoryServicePlatform platform() {
                return TEST_PLATFORM;
            }

            @Override
            public InputStream openSha256() {
                return new ByteArrayInputStream(
                        (executableSha256 + "  memory-service\n")
                                .getBytes(StandardCharsets.US_ASCII));
            }

            @Override
            public InputStream openOciDescriptor() {
                return new ByteArrayInputStream(descriptor.getBytes(StandardCharsets.UTF_8));
            }
        };
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
