package io.github.chirino.memoryservice.process;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import com.sun.net.httpserver.HttpsConfigurator;
import com.sun.net.httpserver.HttpsServer;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.URISyntaxException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.KeyStore;
import java.security.MessageDigest;
import java.util.HexFormat;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.Executors;
import java.util.zip.GZIPOutputStream;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;

/**
 * A minimal OCI registry for tests. The TLS keystore under {@code registry-tls/} is a test-only
 * self-signed certificate for {@code localhost} (password {@code changeit}); {@code
 * registry-ca.pem} is its certificate.
 */
final class FakeRegistry implements AutoCloseable {

    static final String REPOSITORY_NAME = "chirino/memory-service-binary";

    /** Handles one request. */
    @FunctionalInterface
    interface Handler {
        void handle(HttpExchange exchange) throws IOException;
    }

    /** One recorded request. */
    record Request(String method, String path, String authorization, String accept) {}

    private final HttpServer server;
    private final boolean https;
    private final List<Request> requests = new CopyOnWriteArrayList<>();
    private volatile Handler handler = exchange -> respond(exchange, 404, new byte[0]);

    private FakeRegistry(HttpServer server, boolean https) {
        this.server = server;
        this.https = https;
        server.createContext(
                "/",
                exchange -> {
                    try {
                        requests.add(
                                new Request(
                                        exchange.getRequestMethod(),
                                        exchange.getRequestURI().toString(),
                                        exchange.getRequestHeaders().getFirst("Authorization"),
                                        exchange.getRequestHeaders().getFirst("Accept")));
                        handler.handle(exchange);
                    } catch (IOException | RuntimeException e) {
                        // The client sees a reset or truncated response.
                    } finally {
                        exchange.close();
                    }
                });
        server.setExecutor(Executors.newCachedThreadPool());
        server.start();
    }

    static FakeRegistry https() throws Exception {
        HttpsServer server =
                HttpsServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
        server.setHttpsConfigurator(new HttpsConfigurator(serverContext()));
        return new FakeRegistry(server, true);
    }

    static FakeRegistry http() throws Exception {
        return new FakeRegistry(
                HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0),
                false);
    }

    static Path caFile() {
        try {
            return Path.of(FakeRegistry.class.getResource("/registry-tls/registry-ca.pem").toURI());
        } catch (URISyntaxException e) {
            throw new IllegalStateException(e);
        }
    }

    private static SSLContext serverContext() throws Exception {
        KeyStore store = KeyStore.getInstance("PKCS12");
        try (InputStream input =
                FakeRegistry.class.getResourceAsStream("/registry-tls/keystore.p12")) {
            store.load(input, "changeit".toCharArray());
        }
        KeyManagerFactory keys =
                KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
        keys.init(store, "changeit".toCharArray());
        SSLContext context = SSLContext.getInstance("TLS");
        context.init(keys.getKeyManagers(), null, null);
        return context;
    }

    void handler(Handler handler) {
        this.handler = handler;
    }

    /** {@code localhost:port}. */
    String authority() {
        return "localhost:" + server.getAddress().getPort();
    }

    /** {@code https://localhost:port} or {@code http://localhost:port}. */
    String origin() {
        return (https ? "https://" : "http://") + authority();
    }

    /** Upstream repository on this registry, as a descriptor names it. */
    String repository() {
        return authority() + "/" + REPOSITORY_NAME;
    }

    List<Request> requests() {
        return requests;
    }

    @Override
    public void close() {
        server.stop(0);
        ((java.util.concurrent.ExecutorService) server.getExecutor()).shutdownNow();
    }

    static void respond(HttpExchange exchange, int status, byte[] body) throws IOException {
        exchange.sendResponseHeaders(status, body.length == 0 ? -1 : body.length);
        if (body.length > 0) {
            try (OutputStream output = exchange.getResponseBody()) {
                output.write(body);
            }
        }
    }

    static void respond(HttpExchange exchange, int status, String body) throws IOException {
        respond(exchange, status, body.getBytes(StandardCharsets.UTF_8));
    }

    static byte[] gzip(byte[] content) throws IOException {
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        try (GZIPOutputStream gzip = new GZIPOutputStream(bytes)) {
            gzip.write(content);
        }
        return bytes.toByteArray();
    }

    static String sha256(byte[] content) throws Exception {
        return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(content));
    }

    static String blobPath(String digest) {
        return "/v2/" + REPOSITORY_NAME + "/blobs/" + digest;
    }
}
