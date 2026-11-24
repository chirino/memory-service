package io.github.chirino.memoryservice.process;

import static io.github.chirino.memoryservice.process.FakeRegistry.blobPath;
import static io.github.chirino.memoryservice.process.FakeRegistry.gzip;
import static io.github.chirino.memoryservice.process.FakeRegistry.respond;
import static io.github.chirino.memoryservice.process.FakeRegistry.sha256;
import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sun.net.httpserver.HttpExchange;
import io.github.chirino.memoryservice.process.FakeRegistry.Request;
import io.github.chirino.memoryservice.process.OciBlobFetcher.Descriptor;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.Proxy;
import java.net.ProxySelector;
import java.net.ServerSocket;
import java.net.SocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Base64;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.logging.Handler;
import java.util.logging.Level;
import java.util.logging.LogRecord;
import java.util.logging.Logger;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class OciBlobFetcherTest {

    static final byte[] EXECUTABLE =
            "fake-memory-service-executable\n".repeat(2000).getBytes(StandardCharsets.UTF_8);

    @TempDir Path temporaryDirectory;

    private final List<FakeRegistry> registries = new ArrayList<>();
    private final List<LogRecord> warnings = new CopyOnWriteArrayList<>();
    private final Handler logHandler =
            new Handler() {
                @Override
                public void publish(LogRecord record) {
                    if (record.getLevel().intValue() >= Level.WARNING.intValue()) {
                        warnings.add(record);
                    }
                }

                @Override
                public void flush() {}

                @Override
                public void close() {}
            };
    private byte[] layer;
    private String digest;

    @BeforeEach
    void setUp() throws Exception {
        layer = gzip(EXECUTABLE);
        digest = "sha256:" + sha256(layer);
        Logger.getLogger(OciBlobFetcher.class.getName()).addHandler(logHandler);
    }

    @AfterEach
    void tearDown() {
        Logger.getLogger(OciBlobFetcher.class.getName()).removeHandler(logHandler);
        registries.forEach(FakeRegistry::close);
    }

    @Test
    void downloadsAnonymouslyWithoutChallenge() throws Exception {
        FakeRegistry registry = https();
        registry.handler(serveBlob(layer));

        Path target = fetch(settings().build(), descriptor(registry));

        assertArrayEquals(EXECUTABLE, Files.readAllBytes(target));
        assertEquals(1, registry.requests().size());
        assertNull(registry.requests().get(0).authorization());
    }

    @Test
    void exchangesAnonymousBearerTokenAfterChallenge() throws Exception {
        FakeRegistry registry = https();
        registry.handler(tokenProtected(registry, registry, layer, "t-1", null));

        Path target = fetch(settings().build(), descriptor(registry));

        assertArrayEquals(EXECUTABLE, Files.readAllBytes(target));
        List<Request> requests = registry.requests();
        assertEquals(3, requests.size());
        assertNull(requests.get(0).authorization());
        assertTrue(requests.get(1).path().startsWith("/token?service=fake&scope=repository"));
        assertNull(requests.get(1).authorization());
        assertEquals("Bearer t-1", requests.get(2).authorization());
    }

    @Test
    void redirectTargetOnAnotherHostReceivesNoAuthorization() throws Exception {
        FakeRegistry registry = https();
        FakeRegistry storage = https();
        storage.handler(exchange -> respond(exchange, 200, layer));
        registry.handler(
                tokenProtectedThen(
                        registry,
                        "t-1",
                        exchange -> redirect(exchange, storage.origin() + "/blob?signature=x")));

        Path target = fetch(settings().build(), descriptor(registry));

        assertArrayEquals(EXECUTABLE, Files.readAllBytes(target));
        assertEquals("Bearer t-1", registry.requests().get(2).authorization());
        assertEquals(1, storage.requests().size());
        assertNull(storage.requests().get(0).authorization());
    }

    @Test
    void refusesRedirectFromHttpsToHttp() throws Exception {
        FakeRegistry registry = https();
        FakeRegistry plain = http();
        plain.handler(exchange -> respond(exchange, 200, layer));
        registry.handler(exchange -> redirect(exchange, plain.origin() + "/blob"));

        IOException error = assertFetchFails(settings().build(), descriptor(registry));

        assertTrue(error.getMessage().contains("refused redirect from HTTPS to http"));
        assertTrue(plain.requests().isEmpty());
    }

    @Test
    void rejectsDigestMismatch() throws Exception {
        FakeRegistry registry = https();
        registry.handler(exchange -> respond(exchange, 200, layer));
        Descriptor wrong =
                new Descriptor(
                        registry.repository(),
                        "1.0.0",
                        "sha256:" + "0".repeat(64),
                        layer.length,
                        EXECUTABLE.length);

        IOException error = assertFetchFails(settings().build(), wrong);

        assertTrue(error.getMessage().contains("layer digest mismatch"), error.getMessage());
        assertEquals(1, registry.requests().size(), "integrity failures are not retried");
    }

    @Test
    void rejectsLayerSizeMismatch() throws Exception {
        FakeRegistry registry = https();
        registry.handler(serveBlob(layer));
        Descriptor larger =
                new Descriptor(
                        registry.repository(),
                        "1.0.0",
                        digest,
                        layer.length + 10,
                        EXECUTABLE.length);

        IOException error = assertFetchFails(settings().build(), larger);

        assertTrue(error.getMessage().contains("layer size mismatch"), error.getMessage());
    }

    @Test
    void rejectsExecutableSizeMismatch() throws Exception {
        FakeRegistry registry = https();
        registry.handler(serveBlob(layer));
        Descriptor larger =
                new Descriptor(
                        registry.repository(),
                        "1.0.0",
                        digest,
                        layer.length,
                        EXECUTABLE.length + 1);

        IOException error = assertFetchFails(settings().build(), larger);

        assertTrue(error.getMessage().contains("executable size mismatch"), error.getMessage());
    }

    @Test
    void rejectsCorruptGzipStream() throws Exception {
        byte[] corrupt = "this is not gzip".getBytes(StandardCharsets.UTF_8);
        String corruptDigest = "sha256:" + sha256(corrupt);
        FakeRegistry registry = https();
        registry.handler(exchange -> respond(exchange, 200, corrupt));
        Descriptor descriptor =
                new Descriptor(registry.repository(), "1.0.0", corruptDigest, corrupt.length, 100);

        IOException error = assertFetchFails(settings().build(), descriptor);

        assertTrue(error.getMessage().contains("corrupt layer"), error.getMessage());
    }

    @Test
    void cutsOffBodyLongerThanLayerSize() throws Exception {
        FakeRegistry registry = https();
        registry.handler(serveBlob(layer));
        Descriptor smaller =
                new Descriptor(
                        registry.repository(),
                        "1.0.0",
                        digest,
                        layer.length - 5,
                        EXECUTABLE.length);

        IOException error = assertFetchFails(settings().build(), smaller);

        assertTrue(error.getMessage().contains("larger than layerSize"), error.getMessage());
    }

    @Test
    void cutsOffGzipThatExpandsPastBinarySize() throws Exception {
        FakeRegistry registry = https();
        registry.handler(serveBlob(layer));
        Descriptor smaller =
                new Descriptor(registry.repository(), "1.0.0", digest, layer.length, 1024);

        IOException error = assertFetchFails(settings().build(), smaller);

        assertTrue(error.getMessage().contains("expands past binarySize"), error.getMessage());
    }

    @Test
    void rejectsMalformedDescriptors() {
        String valid =
                "repository=ghcr.io/chirino/memory-service-binary\n"
                        + "reference=0.7.0\n"
                        + "layerDigest=sha256:"
                        + "a".repeat(64)
                        + "\nlayerSize=10\nbinarySize=20\n";
        assertParseFails(valid.replace("layerSize=10\n", ""), "missing layerSize");
        assertParseFails(valid.replace("sha256:", "md5:"), "malformed layerDigest");
        assertParseFails(valid.replace("binarySize=20", "binarySize=0"), "must be positive");
        assertParseFails(valid.replace("binarySize=20", "binarySize=-3"), "malformed binarySize");
        assertParseFails(
                valid.replace("ghcr.io/chirino/memory-service-binary", "memory-service-binary"),
                "malformed repository");
        assertParseFails(valid.replace("reference=0.7.0", "reference=../x"), "malformed reference");
    }

    @Test
    void retriesServerErrorsAndRateLimitsWithRetryAfter() throws Exception {
        FakeRegistry registry = https();
        AtomicInteger calls = new AtomicInteger();
        registry.handler(
                exchange -> {
                    int call = calls.incrementAndGet();
                    if (call == 1) {
                        respond(exchange, 503, "");
                    } else if (call == 2) {
                        exchange.getResponseHeaders().add("Retry-After", "0");
                        respond(exchange, 429, "");
                    } else {
                        respond(exchange, 200, layer);
                    }
                });

        Path target = fetch(settings().build(), descriptor(registry));

        assertArrayEquals(EXECUTABLE, Files.readAllBytes(target));
        assertEquals(3, registry.requests().size());
    }

    @Test
    void movesToNextLocationAfterRetriesAreExhausted() throws Exception {
        FakeRegistry failing = https();
        failing.handler(exchange -> respond(exchange, 502, ""));
        FakeRegistry upstream = https();
        upstream.handler(serveBlob(layer));
        int closedPort;
        try (ServerSocket socket = new ServerSocket(0, 1, InetAddress.getLoopbackAddress())) {
            closedPort = socket.getLocalPort();
        }

        MemoryServiceBinaryRegistries settings =
                settings()
                        .mirror(upstream.authority(), failing.authority())
                        .mirror(upstream.authority(), "http://localhost:" + closedPort)
                        .upstreamFallback(true)
                        .build();
        Path target = fetch(settings, descriptor(upstream));

        assertArrayEquals(EXECUTABLE, Files.readAllBytes(target));
        assertEquals(3, failing.requests().size());
        assertEquals(1, upstream.requests().size());
        assertEquals(2, warnings.size(), "one warning per failed location");
    }

    @Test
    void failsWhenEveryLocationFailsAndListsEachOne() throws Exception {
        FakeRegistry mirror = https();
        mirror.handler(exchange -> respond(exchange, 403, ""));
        FakeRegistry upstream = https();
        upstream.handler(exchange -> respond(exchange, 404, ""));

        MemoryServiceBinaryRegistries settings =
                settings()
                        .mirror(upstream.authority(), mirror.authority())
                        .upstreamFallback(true)
                        .build();
        IOException error = assertFetchFails(settings, descriptor(upstream));

        assertTrue(
                error.getMessage()
                        .contains(mirror.authority() + "/" + FakeRegistry.REPOSITORY_NAME));
        assertTrue(error.getMessage().contains(upstream.repository()));
    }

    @Test
    void stalledBodyFailsAtDownloadTimeout() throws Exception {
        FakeRegistry registry = https();
        registry.handler(
                exchange -> {
                    exchange.sendResponseHeaders(200, layer.length);
                    OutputStream output = exchange.getResponseBody();
                    output.write(layer, 0, 16);
                    output.flush();
                    try {
                        Thread.sleep(10_000);
                    } catch (InterruptedException e) {
                        Thread.currentThread().interrupt();
                    }
                });

        long started = System.nanoTime();
        IOException error =
                assertFetchFails(
                        settings().downloadTimeout(Duration.ofSeconds(1)).build(),
                        descriptor(registry));

        assertTrue(error.getMessage().contains("deadline"), error.getMessage());
        assertTrue(Duration.ofNanos(System.nanoTime() - started).toSeconds() < 8);
    }

    @Test
    void stalledTokenResponseFailsAtDownloadTimeout() throws Exception {
        FakeRegistry registry = https();
        FakeRegistry.Handler blob = tokenProtected(registry, registry, layer, "t-1", null);
        registry.handler(
                exchange -> {
                    if (!exchange.getRequestURI().getPath().equals("/token")) {
                        blob.handle(exchange);
                        return;
                    }
                    exchange.sendResponseHeaders(200, 1000);
                    OutputStream output = exchange.getResponseBody();
                    output.write("{\"tok".getBytes(StandardCharsets.UTF_8));
                    output.flush();
                    try {
                        Thread.sleep(10_000);
                    } catch (InterruptedException e) {
                        Thread.currentThread().interrupt();
                    }
                });

        long started = System.nanoTime();
        IOException error =
                assertFetchFails(
                        settings().downloadTimeout(Duration.ofSeconds(1)).build(),
                        descriptor(registry));

        assertTrue(error.getMessage().contains("deadline"), error.getMessage());
        assertTrue(Duration.ofNanos(System.nanoTime() - started).toSeconds() < 8);
    }

    @Test
    void transientManifestWarmUpFailureIsRetried() throws Exception {
        FakeRegistry mirror = https();
        AtomicInteger manifestCalls = new AtomicInteger();
        AtomicBoolean warmed = new AtomicBoolean();
        mirror.handler(
                exchange -> {
                    String path = exchange.getRequestURI().getPath();
                    if (path.endsWith("/manifests/1.0.0")) {
                        if (manifestCalls.incrementAndGet() == 1) {
                            respond(exchange, 503, "");
                        } else {
                            warmed.set(true);
                            respond(exchange, 200, "{}");
                        }
                    } else if (path.equals(blobPath(digest)) && warmed.get()) {
                        respond(exchange, 200, layer);
                    } else {
                        respond(exchange, 404, "");
                    }
                });

        Path target =
                fetch(
                        settings().mirror("upstream.example", mirror.authority()).build(),
                        new Descriptor(
                                "upstream.example/" + FakeRegistry.REPOSITORY_NAME,
                                "1.0.0",
                                digest,
                                layer.length,
                                EXECUTABLE.length));

        assertArrayEquals(EXECUTABLE, Files.readAllBytes(target));
        assertEquals(2, manifestCalls.get());
    }

    @Test
    void malformedRedirectMovesToTheNextLocation() throws Exception {
        FakeRegistry broken = https();
        broken.handler(exchange -> redirect(exchange, "%"));
        FakeRegistry healthy = https();
        healthy.handler(serveBlob(layer));

        Path target =
                fetch(
                        settings()
                                .mirror("upstream.example", broken.authority())
                                .mirror("upstream.example", healthy.authority())
                                .build(),
                        new Descriptor(
                                "upstream.example/" + FakeRegistry.REPOSITORY_NAME,
                                "1.0.0",
                                digest,
                                layer.length,
                                EXECUTABLE.length));

        assertArrayEquals(EXECUTABLE, Files.readAllBytes(target));
        assertEquals(1, broken.requests().size());
        assertTrue(
                warnings.stream()
                        .anyMatch(record -> record.getMessage().contains("malformed Location")));
    }

    @Test
    void tokenRealmOnAnotherHostReceivesExplicitCredentialsOverHttpsOnly() throws Exception {
        FakeRegistry registry = https();
        FakeRegistry realm = https();
        registry.handler(tokenProtected(registry, realm, layer, "t-1", null));
        realm.handler(tokenEndpoint("t-1", null));

        fetch(
                settings().credentials(registry.authority(), "robot", "s3cret").build(),
                descriptor(registry));

        assertEquals(basic("robot", "s3cret"), realm.requests().get(0).authorization());
        assertNull(registry.requests().get(0).authorization());

        FakeRegistry plainRealm = http();
        FakeRegistry second = https();
        second.handler(tokenProtected(second, plainRealm, layer, "t-1", null));
        plainRealm.handler(tokenEndpoint("t-1", null));
        IOException error =
                assertFetchFails(
                        settings().credentials(second.authority(), "robot", "s3cret").build(),
                        descriptor(second));
        assertTrue(error.getMessage().contains("downgrades HTTPS"), error.getMessage());
        assertTrue(plainRealm.requests().isEmpty());
    }

    @Test
    void sendsGithubTokenOnlyToTokenRealmOnGithubRegistry() throws Exception {
        FakeRegistry registry = https();
        registry.handler(tokenProtected(registry, registry, layer, "t-1", null));

        fetch(
                settings()
                        .githubRegistry(registry.authority())
                        .environment(Map.of("GH_TOKEN", "gh-first", "GITHUB_TOKEN", "gh-second"))
                        .build(),
                descriptor(registry));
        assertEquals(basic("token", "gh-first"), registry.requests().get(1).authorization());

        FakeRegistry other = https();
        FakeRegistry realm = https();
        other.handler(tokenProtected(other, realm, layer, "t-1", null));
        realm.handler(tokenEndpoint("t-1", null));
        fetch(
                settings()
                        .githubRegistry(other.authority())
                        .environment(Map.of("GITHUB_TOKEN", "gh-second"))
                        .build(),
                descriptor(other));
        assertNull(realm.requests().get(0).authorization(), "realm on another host");
    }

    @Test
    void githubTokenIsNeverSentToMirrors() throws Exception {
        FakeRegistry mirror = https();
        mirror.handler(tokenProtected(mirror, mirror, layer, "t-1", null));

        fetch(
                settings()
                        .githubRegistry("upstream.example")
                        .mirror("upstream.example", mirror.authority())
                        .environment(Map.of("GH_TOKEN", "gh-first"))
                        .build(),
                new Descriptor(
                        "upstream.example/" + FakeRegistry.REPOSITORY_NAME,
                        "1.0.0",
                        digest,
                        layer.length,
                        EXECUTABLE.length));

        for (Request request : mirror.requests()) {
            assertTrue(
                    request.authorization() == null || request.authorization().startsWith("Bearer"),
                    request.toString());
        }
    }

    @Test
    void rejectedImplicitTokenFallsBackToAnonymousWithWarning() throws Exception {
        FakeRegistry registry = https();
        registry.handler(tokenProtected(registry, registry, layer, "t-1", "reject-basic"));

        Path target =
                fetch(
                        settings()
                                .githubRegistry(registry.authority())
                                .environment(Map.of("GITHUB_TOKEN", "expired"))
                                .build(),
                        descriptor(registry));

        assertArrayEquals(EXECUTABLE, Files.readAllBytes(target));
        assertEquals(basic("token", "expired"), registry.requests().get(1).authorization());
        assertNull(registry.requests().get(2).authorization());
        assertTrue(
                warnings.stream().anyMatch(record -> record.getMessage().contains("GITHUB_TOKEN")),
                "warning names the variable");
        assertTrue(warnings.stream().noneMatch(record -> record.getMessage().contains("expired")));
    }

    @Test
    void rejectedExplicitCredentialsFailTheLocation() throws Exception {
        FakeRegistry registry = https();
        registry.handler(tokenProtected(registry, registry, layer, "t-1", "reject-basic"));

        IOException error =
                assertFetchFails(
                        settings().credentials(registry.authority(), "robot", "wrong").build(),
                        descriptor(registry));

        assertTrue(
                error.getMessage().contains("token exchange rejected credentials("),
                error.getMessage());
        assertTrue(!error.getMessage().contains("wrong"), "no secrets in errors");
    }

    @Test
    void disabledGithubTokenPickupSendsNoToken() throws Exception {
        FakeRegistry registry = https();
        registry.handler(tokenProtected(registry, registry, layer, "t-1", null));

        fetch(
                settings()
                        .githubRegistry(registry.authority())
                        .githubTokenFromEnvironment(false)
                        .environment(Map.of("GH_TOKEN", "gh-first", "GITHUB_TOKEN", "gh-second"))
                        .build(),
                descriptor(registry));

        assertNull(registry.requests().get(1).authorization());
    }

    @Test
    void explicitCredentialsStayOnTheirHost() throws Exception {
        FakeRegistry mirror = https();
        mirror.handler(tokenProtected(mirror, mirror, layer, "t-1", null));
        FakeRegistry plainMirror = http();
        plainMirror.handler(serveBlob(layer));

        fetch(
                settings()
                        .mirror("upstream.example", mirror.authority())
                        .credentials("upstream.example", "robot", "s3cret")
                        .build(),
                new Descriptor(
                        "upstream.example/" + FakeRegistry.REPOSITORY_NAME,
                        "1.0.0",
                        digest,
                        layer.length,
                        EXECUTABLE.length));
        assertNull(mirror.requests().get(1).authorization(), "credentials for another host");

        IllegalArgumentException invalid =
                assertThrows(
                        IllegalArgumentException.class,
                        () ->
                                settings()
                                        .mirror(
                                                "upstream.example",
                                                "http://" + plainMirror.authority())
                                        .credentials(plainMirror.authority(), "robot", "s3cret")
                                        .build());
        assertTrue(invalid.getMessage().contains("http://"));
    }

    @Test
    void mirror404PullsManifestOnceThenRetriesBlob() throws Exception {
        FakeRegistry mirror = https();
        AtomicBoolean warmed = new AtomicBoolean();
        mirror.handler(
                exchange -> {
                    String path = exchange.getRequestURI().getPath();
                    if (path.equals("/v2/" + FakeRegistry.REPOSITORY_NAME + "/manifests/1.0.0")) {
                        warmed.set(true);
                        respond(exchange, 200, "{}");
                    } else if (path.equals(blobPath(digest)) && warmed.get()) {
                        respond(exchange, 200, layer);
                    } else {
                        respond(exchange, 404, "");
                    }
                });

        fetch(
                settings().mirror("upstream.example", mirror.authority()).build(),
                new Descriptor(
                        "upstream.example/" + FakeRegistry.REPOSITORY_NAME,
                        "1.0.0",
                        digest,
                        layer.length,
                        EXECUTABLE.length));

        List<Request> requests = mirror.requests();
        assertEquals(3, requests.size());
        assertEquals(OciBlobFetcher.MANIFEST_MEDIA_TYPE, requests.get(1).accept());
    }

    @Test
    void untrustedCertificateFailsWithoutTrustedCertificates() throws Exception {
        FakeRegistry registry = https();
        registry.handler(serveBlob(layer));

        assertFetchFails(
                MemoryServiceBinaryRegistries.builder()
                        .environment(Map.of())
                        .retryBackoff(Duration.ZERO)
                        .build(),
                descriptor(registry));
        assertTrue(registry.requests().isEmpty());
    }

    @Test
    void usesConfiguredProxySelectorForRegistryAndRedirectRequests() throws Exception {
        FakeRegistry registry = https();
        FakeRegistry storage = https();
        storage.handler(exchange -> respond(exchange, 200, layer));
        registry.handler(exchange -> redirect(exchange, storage.origin() + "/blob"));
        List<URI> selected = new CopyOnWriteArrayList<>();
        ProxySelector recording =
                new ProxySelector() {
                    @Override
                    public List<Proxy> select(URI uri) {
                        selected.add(uri);
                        return List.of(Proxy.NO_PROXY);
                    }

                    @Override
                    public void connectFailed(URI uri, SocketAddress address, IOException e) {}
                };

        fetch(settings().proxy(recording).build(), descriptor(registry));

        assertTrue(selected.stream().anyMatch(uri -> uri.getPort() == port(registry)));
        assertTrue(selected.stream().anyMatch(uri -> uri.getPort() == port(storage)));
    }

    private static int port(FakeRegistry registry) {
        return Integer.parseInt(registry.authority().substring("localhost:".length()));
    }

    private FakeRegistry https() throws Exception {
        FakeRegistry registry = FakeRegistry.https();
        registries.add(registry);
        return registry;
    }

    private FakeRegistry http() throws Exception {
        FakeRegistry registry = FakeRegistry.http();
        registries.add(registry);
        return registry;
    }

    private MemoryServiceBinaryRegistries.Builder settings() {
        return MemoryServiceBinaryRegistries.builder()
                .trustedCertificates(FakeRegistry.caFile())
                .environment(Map.of())
                .retryBackoff(Duration.ZERO);
    }

    private Descriptor descriptor(FakeRegistry registry) {
        return new Descriptor(
                registry.repository(), "1.0.0", digest, layer.length, EXECUTABLE.length);
    }

    private Path fetch(MemoryServiceBinaryRegistries settings, Descriptor descriptor)
            throws IOException {
        Path target = Files.createTempFile(temporaryDirectory, "download", ".bin");
        new OciBlobFetcher(settings).fetch(descriptor, target, "test-test");
        return target;
    }

    private IOException assertFetchFails(
            MemoryServiceBinaryRegistries settings, Descriptor descriptor) {
        return assertThrows(IOException.class, () -> fetch(settings, descriptor));
    }

    private static void assertParseFails(String properties, String message) {
        IOException error =
                assertThrows(
                        IOException.class,
                        () ->
                                Descriptor.parse(
                                        new ByteArrayInputStream(
                                                properties.getBytes(StandardCharsets.UTF_8)),
                                        "test"));
        assertTrue(error.getMessage().contains(message), error.getMessage());
    }

    private FakeRegistry.Handler serveBlob(byte[] body) {
        return exchange -> {
            if (exchange.getRequestURI().getPath().equals(blobPath(digest))) {
                respond(exchange, 200, body);
            } else {
                respond(exchange, 404, "");
            }
        };
    }

    /**
     * A registry that requires {@code Bearer token} for blobs and names {@code realm}'s {@code
     * /token} endpoint. With {@code reject} set, the token endpoint refuses Basic credentials.
     */
    private FakeRegistry.Handler tokenProtected(
            FakeRegistry registry, FakeRegistry realm, byte[] body, String token, String reject) {
        FakeRegistry.Handler tokens = tokenEndpoint(token, reject);
        return exchange -> {
            String path = exchange.getRequestURI().getPath();
            if (path.equals("/token")) {
                tokens.handle(exchange);
                return;
            }
            String authorization = exchange.getRequestHeaders().getFirst("Authorization");
            if (!("Bearer " + token).equals(authorization)) {
                exchange.getResponseHeaders()
                        .add(
                                "WWW-Authenticate",
                                "Bearer realm=\""
                                        + realm.origin()
                                        + "/token\",service=\"fake\",scope=\"repository:"
                                        + FakeRegistry.REPOSITORY_NAME
                                        + ":pull\"");
                respond(exchange, 401, "");
                return;
            }
            if (body == null) {
                return;
            }
            respond(exchange, 200, body);
        };
    }

    private FakeRegistry.Handler tokenProtectedThen(
            FakeRegistry registry, String token, FakeRegistry.Handler onAuthorized) {
        FakeRegistry.Handler base = tokenProtected(registry, registry, null, token, null);
        return exchange -> {
            String authorization = exchange.getRequestHeaders().getFirst("Authorization");
            if (!exchange.getRequestURI().getPath().equals("/token")
                    && ("Bearer " + token).equals(authorization)) {
                onAuthorized.handle(exchange);
            } else {
                base.handle(exchange);
            }
        };
    }

    private static FakeRegistry.Handler tokenEndpoint(String token, String reject) {
        return exchange -> {
            if (reject != null && exchange.getRequestHeaders().getFirst("Authorization") != null) {
                respond(exchange, 403, "");
                return;
            }
            exchange.getResponseHeaders().add("Content-Type", "application/json");
            respond(exchange, 200, "{\"token\":\"" + token + "\",\"expires_in\":300}");
        };
    }

    private static void redirect(HttpExchange exchange, String location) throws IOException {
        exchange.getResponseHeaders().add("Location", location);
        respond(exchange, 307, "");
    }

    private static String basic(String username, String secret) {
        return "Basic "
                + Base64.getEncoder()
                        .encodeToString((username + ":" + secret).getBytes(StandardCharsets.UTF_8));
    }
}
