package io.github.chirino.memoryservice.client;

import static org.assertj.core.api.Assertions.assertThat;

import com.sun.net.httpserver.HttpServer;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.http.ResponseEntity;
import org.springframework.mock.web.MockMultipartFile;
import org.springframework.web.reactive.function.client.WebClient;

/** Verifies the requests {@link MemoryServiceProxy} sends for caller-supplied attachment IDs. */
class MemoryServiceProxyAttachmentIdTest {

    private static final String ID = "6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10";

    private record Captured(String method, String path, String contentType, String body) {}

    private HttpServer server;
    private final List<Captured> captured = new CopyOnWriteArrayList<>();
    private volatile int responseStatus = 201;
    private MemoryServiceProxy proxy;

    @BeforeEach
    void startServer() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
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
        server.start();

        MemoryServiceClientProperties properties = new MemoryServiceClientProperties();
        properties.setUrl("http://127.0.0.1:" + server.getAddress().getPort());
        properties.setApiKey("test");
        proxy = new MemoryServiceProxy(properties, WebClient.builder(), null);
    }

    @AfterEach
    void stopServer() {
        server.stop(0);
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

    private static MockMultipartFile file() {
        return new MockMultipartFile("file", "a.txt", "text/plain", "hello".getBytes());
    }

    @Test
    void uploadWithIdSendsIdFieldBeforeFilePart() {
        proxy.uploadAttachment(file(), null, ID);

        assertThat(captured).hasSize(1);
        Captured request = captured.get(0);
        assertThat(request.method()).isEqualTo("POST");
        assertThat(request.path()).isEqualTo("/v1/attachments");
        assertThat(request.contentType()).startsWith("multipart/form-data");
        assertThat(idPartValue(request.body())).isEqualTo(ID);
        assertThat(request.body().indexOf("name=\"id\""))
                .isLessThan(request.body().indexOf("name=\"file\""));
    }

    @Test
    void uploadWithoutIdSendsNoIdField() {
        proxy.uploadAttachment(file(), null);

        assertThat(captured).hasSize(1);
        assertThat(captured.get(0).body()).doesNotContain("name=\"id\"");
        assertThat(captured.get(0).body()).contains("name=\"file\"");
    }

    @Test
    void uploadSendsABlankIdUnchangedInsteadOfDroppingIt() {
        proxy.uploadAttachment(file(), null, "");

        // A blank ID must reach the memory service, which rejects it with 400. Dropping it would
        // silently turn the request into a non-idempotent upload.
        assertThat(idPartValue(captured.get(0).body())).isEqualTo("");
    }

    @Test
    void retryingWithTheSameIdSendsTheSameIdEveryTime() {
        proxy.uploadAttachment(file(), null, ID);
        proxy.uploadAttachment(file(), null, ID);

        assertThat(captured).hasSize(2);
        for (Captured request : captured) {
            assertThat(idPartValue(request.body())).isEqualTo(ID);
        }
    }

    @Test
    void conflictStatusIsReturnedNotHidden() {
        responseStatus = 409;

        ResponseEntity<?> response = proxy.uploadAttachment(file(), null, ID);

        assertThat(response.getStatusCode().value()).isEqualTo(409);
    }

    @Test
    void createFromUrlForwardsTheIdInTheJsonBody() {
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
    void getAttachmentMetadataRequestsTheMetadataRoute() {
        responseStatus = 200;

        proxy.getAttachmentMetadata(ID);

        assertThat(captured).hasSize(1);
        assertThat(captured.get(0).method()).isEqualTo("GET");
        assertThat(captured.get(0).path()).isEqualTo("/v1/attachments/" + ID + "/metadata");
    }

    @Test
    void getAttachmentMetadataReturnsNotFoundUnchanged() {
        responseStatus = 404;

        ResponseEntity<?> response = proxy.getAttachmentMetadata(ID);

        assertThat(response.getStatusCode().value()).isEqualTo(404);
    }
}
