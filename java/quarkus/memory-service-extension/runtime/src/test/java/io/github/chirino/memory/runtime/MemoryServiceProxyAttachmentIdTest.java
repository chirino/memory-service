package io.github.chirino.memory.runtime;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import jakarta.ws.rs.core.MultivaluedHashMap;
import jakarta.ws.rs.core.MultivaluedMap;
import jakarta.ws.rs.core.Response;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.InetSocketAddress;
import java.net.StandardProtocolFamily;
import java.net.UnixDomainSocketAddress;
import java.nio.channels.Channels;
import java.nio.channels.ServerSocketChannel;
import java.nio.channels.SocketChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Collection;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.CopyOnWriteArrayList;
import org.jboss.resteasy.reactive.server.multipart.FileItem;
import org.jboss.resteasy.reactive.server.multipart.FormValue;
import org.jboss.resteasy.reactive.server.multipart.MultipartFormDataInput;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

/**
 * Verifies the requests {@link MemoryServiceProxy} sends for caller-supplied attachment IDs over
 * both transports: plain HTTP and a unix domain socket.
 */
class MemoryServiceProxyAttachmentIdTest {

    private static final String ID = "6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10";

    private record Captured(String method, String path, String contentType, String body) {}

    private final List<Captured> captured = new CopyOnWriteArrayList<>();
    private volatile int responseStatus = 201;
    private HttpServer httpServer;
    private ServerSocketChannel unixServer;
    private Path socketPath;
    private Thread unixThread;

    @AfterEach
    void stopServers() throws IOException {
        if (httpServer != null) {
            httpServer.stop(0);
        }
        if (unixServer != null) {
            unixServer.close();
        }
        if (unixThread != null) {
            unixThread.interrupt();
        }
        if (socketPath != null) {
            Files.deleteIfExists(socketPath);
        }
    }

    // ---- HTTP transport ----

    private MemoryServiceProxy httpProxy() throws IOException {
        httpServer = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        httpServer.createContext(
                "/",
                exchange -> {
                    ByteArrayOutputStream body = new ByteArrayOutputStream();
                    exchange.getRequestBody().transferTo(body);
                    captured.add(
                            new Captured(
                                    exchange.getRequestMethod(),
                                    exchange.getRequestURI().getPath(),
                                    exchange.getRequestHeaders().getFirst("Content-Type"),
                                    body.toString(StandardCharsets.UTF_8)));
                    byte[] response = "{}".getBytes(StandardCharsets.UTF_8);
                    exchange.getResponseHeaders().add("Content-Type", "application/json");
                    exchange.sendResponseHeaders(responseStatus, response.length);
                    exchange.getResponseBody().write(response);
                    exchange.close();
                });
        httpServer.start();
        return proxyFor("http://127.0.0.1:" + httpServer.getAddress().getPort());
    }

    // ---- Unix domain socket transport ----

    private MemoryServiceProxy unixProxy() throws IOException {
        // A short path: unix socket paths are limited to about 100 characters.
        socketPath =
                Path.of("/tmp", "ms-" + UUID.randomUUID().toString().substring(0, 8) + ".sock");
        unixServer = ServerSocketChannel.open(StandardProtocolFamily.UNIX);
        unixServer.bind(UnixDomainSocketAddress.of(socketPath));
        unixThread =
                new Thread(
                        () -> {
                            while (unixServer.isOpen()) {
                                try (SocketChannel channel = unixServer.accept()) {
                                    serveOne(channel);
                                } catch (IOException e) {
                                    return;
                                }
                            }
                        });
        unixThread.setDaemon(true);
        unixThread.start();
        return proxyFor("unix://" + socketPath);
    }

    /** Reads one HTTP request from the socket, records it, and answers with responseStatus. */
    private void serveOne(SocketChannel channel) throws IOException {
        InputStream in = Channels.newInputStream(channel);
        ByteArrayOutputStream head = new ByteArrayOutputStream();
        int matched = 0;
        while (matched < 4) {
            int b = in.read();
            if (b < 0) {
                return;
            }
            head.write(b);
            matched = (b == (matched % 2 == 0 ? '\r' : '\n')) ? matched + 1 : (b == '\r' ? 1 : 0);
        }
        String[] lines = head.toString(StandardCharsets.ISO_8859_1).split("\r\n");
        String[] requestLine = lines[0].split(" ");
        int contentLength = 0;
        String contentType = null;
        for (String line : lines) {
            String lower = line.toLowerCase();
            if (lower.startsWith("content-length:")) {
                contentLength = Integer.parseInt(line.substring(15).trim());
            } else if (lower.startsWith("content-type:")) {
                contentType = line.substring(13).trim();
            }
        }
        String body = new String(in.readNBytes(contentLength), StandardCharsets.UTF_8);
        captured.add(new Captured(requestLine[0], requestLine[1], contentType, body));

        String response =
                "HTTP/1.1 "
                        + responseStatus
                        + " X\r\nContent-Type: application/json\r\nContent-Length: 2\r\n"
                        + "Connection: close\r\n\r\n{}";
        channel.write(java.nio.ByteBuffer.wrap(response.getBytes(StandardCharsets.UTF_8)));
    }

    private static MemoryServiceProxy proxyFor(String url) {
        MemoryServiceApiBuilder builder =
                new MemoryServiceApiBuilder(
                        Optional.of(url), Optional.of("test"), new ObjectMapper());
        MemoryServiceProxy proxy = new MemoryServiceProxy();
        proxy.memoryServiceApiBuilder = builder;
        return proxy;
    }

    // ---- Fake multipart input ----

    private static MultipartFormDataInput input(String filename, String content) {
        byte[] bytes = content.getBytes(StandardCharsets.UTF_8);
        FormValue file =
                new FormValue() {
                    @Override
                    public String getValue() {
                        return null;
                    }

                    @Override
                    public String getCharset() {
                        return null;
                    }

                    @Override
                    public FileItem getFileItem() {
                        return new FileItem() {
                            @Override
                            public boolean isInMemory() {
                                return true;
                            }

                            @Override
                            public Path getFile() {
                                return null;
                            }

                            @Override
                            public long getFileSize() {
                                return bytes.length;
                            }

                            @Override
                            public InputStream getInputStream() {
                                return new ByteArrayInputStream(bytes);
                            }

                            @Override
                            public void delete() {}

                            @Override
                            public void write(Path target) {}
                        };
                    }

                    @Override
                    public boolean isFileItem() {
                        return true;
                    }

                    @Override
                    public String getFileName() {
                        return filename;
                    }

                    @Override
                    public MultivaluedMap<String, String> getHeaders() {
                        MultivaluedMap<String, String> headers = new MultivaluedHashMap<>();
                        headers.add("Content-Type", "text/plain");
                        return headers;
                    }
                };
        Map<String, Collection<FormValue>> values = Map.of("file", List.of(file));
        return () -> values;
    }

    /** The value of the {@code id} form part, or null when the request has no such part. */
    private static String idPartValue(String multipartBody) {
        int part = multipartBody.indexOf("name=\"id\"");
        if (part < 0) {
            return null;
        }
        int headersEnd = multipartBody.indexOf("\r\n\r\n", part);
        int boundary = multipartBody.indexOf("\r\n--", headersEnd + 4);
        return multipartBody.substring(headersEnd + 4, boundary);
    }

    // ---- Tests, run once per transport ----

    @Test
    void httpUploadWithIdSendsIdFieldBeforeFilePart() throws Exception {
        assertUploadWithIdSendsIdFieldBeforeFilePart(httpProxy());
    }

    @Test
    void unixSocketUploadWithIdSendsIdFieldBeforeFilePart() throws Exception {
        assertUploadWithIdSendsIdFieldBeforeFilePart(unixProxy());
    }

    private void assertUploadWithIdSendsIdFieldBeforeFilePart(MemoryServiceProxy proxy) {
        proxy.uploadAttachment(input("a.txt", "hello"), null, ID);

        assertThat(captured).hasSize(1);
        Captured request = captured.get(0);
        assertThat(request.method()).isEqualTo("POST");
        assertThat(request.path()).isEqualTo("/v1/attachments");
        assertThat(request.contentType()).startsWith("multipart/form-data; boundary=");
        assertThat(idPartValue(request.body())).isEqualTo(ID);
        assertThat(request.body().indexOf("name=\"id\""))
                .isLessThan(request.body().indexOf("name=\"file\""));
        assertThat(request.body()).contains("hello");
    }

    @Test
    void httpUploadWithoutIdSendsNoIdField() throws Exception {
        assertUploadWithoutIdSendsNoIdField(httpProxy());
    }

    @Test
    void unixSocketUploadWithoutIdSendsNoIdField() throws Exception {
        assertUploadWithoutIdSendsNoIdField(unixProxy());
    }

    private void assertUploadWithoutIdSendsNoIdField(MemoryServiceProxy proxy) {
        proxy.uploadAttachment(input("a.txt", "hello"), null);

        assertThat(captured).hasSize(1);
        assertThat(captured.get(0).body()).doesNotContain("name=\"id\"");
        assertThat(captured.get(0).body()).contains("name=\"file\"").contains("hello");
    }

    @Test
    void httpUploadSendsABlankIdUnchangedInsteadOfDroppingIt() throws Exception {
        assertBlankIdIsSent(httpProxy());
    }

    @Test
    void unixSocketUploadSendsABlankIdUnchangedInsteadOfDroppingIt() throws Exception {
        assertBlankIdIsSent(unixProxy());
    }

    private void assertBlankIdIsSent(MemoryServiceProxy proxy) {
        proxy.uploadAttachment(input("a.txt", "hello"), null, "");

        // A blank ID must reach the memory service, which rejects it with 400. Dropping it would
        // silently turn the request into a non-idempotent upload.
        assertThat(idPartValue(captured.get(0).body())).isEqualTo("");
    }

    @Test
    void retryingWithTheSameIdSendsTheSameIdEveryTime() throws Exception {
        MemoryServiceProxy proxy = httpProxy();

        proxy.uploadAttachment(input("a.txt", "hello"), null, ID);
        proxy.uploadAttachment(input("a.txt", "hello"), null, ID);

        assertThat(captured).hasSize(2);
        for (Captured request : captured) {
            assertThat(idPartValue(request.body())).isEqualTo(ID);
        }
    }

    @Test
    void conflictStatusIsReturnedNotHidden() throws Exception {
        MemoryServiceProxy proxy = httpProxy();
        responseStatus = 409;

        Response response = proxy.uploadAttachment(input("a.txt", "hello"), null, ID);

        assertThat(response.getStatus()).isEqualTo(409);
    }

    @Test
    void httpCreateFromUrlForwardsTheIdInTheJsonBody() throws Exception {
        assertCreateFromUrlForwardsId(httpProxy());
    }

    @Test
    void unixSocketCreateFromUrlForwardsTheIdInTheJsonBody() throws Exception {
        assertCreateFromUrlForwardsId(unixProxy());
    }

    private void assertCreateFromUrlForwardsId(MemoryServiceProxy proxy) {
        proxy.createAttachmentFromUrl(
                Map.of(
                        "sourceUrl",
                        "https://example.com/a.png",
                        "contentType",
                        "image/png",
                        "id",
                        ID));

        assertThat(captured).hasSize(1);
        assertThat(captured.get(0).method()).isEqualTo("POST");
        assertThat(captured.get(0).path()).isEqualTo("/v1/attachments");
        assertThat(captured.get(0).body()).contains("\"id\":\"" + ID + "\"");
    }

    @Test
    void httpGetAttachmentMetadataRequestsTheMetadataRoute() throws Exception {
        assertMetadataRoute(httpProxy());
    }

    @Test
    void unixSocketGetAttachmentMetadataRequestsTheMetadataRoute() throws Exception {
        assertMetadataRoute(unixProxy());
    }

    private void assertMetadataRoute(MemoryServiceProxy proxy) {
        responseStatus = 200;

        Response response = proxy.getAttachmentMetadata(ID);

        assertThat(response.getStatus()).isEqualTo(200);
        assertThat(captured).hasSize(1);
        assertThat(captured.get(0).method()).isEqualTo("GET");
        assertThat(captured.get(0).path()).isEqualTo("/v1/attachments/" + ID + "/metadata");
    }

    @Test
    void getAttachmentMetadataReturnsNotFoundUnchanged() throws Exception {
        MemoryServiceProxy proxy = httpProxy();
        responseStatus = 404;

        assertThat(proxy.getAttachmentMetadata(ID).getStatus()).isEqualTo(404);
    }
}
