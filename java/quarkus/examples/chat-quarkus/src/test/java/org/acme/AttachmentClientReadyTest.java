package org.acme;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * An entry can link an attachment only when it is {@code ready}, and a URL attachment is created
 * {@code downloading}. The image tool hands the returned ID to the history recorder, so the client
 * must not return it until the download has finished.
 */
class AttachmentClientReadyTest {

    private static final String ID = "11111111-1111-4111-8111-111111111111";

    private HttpServer server;
    private final List<String> requests = new CopyOnWriteArrayList<>();
    private final AtomicInteger metadataCalls = new AtomicInteger();
    private volatile String[] metadataStatuses = {"ready"};

    @BeforeEach
    void startServer() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/v1/attachments",
                exchange -> {
                    requests.add(exchange.getRequestMethod() + " " + exchange.getRequestURI());
                    String body;
                    int status = 200;
                    if ("POST".equals(exchange.getRequestMethod())) {
                        status = 201;
                        body = json("downloading");
                    } else {
                        int call = metadataCalls.getAndIncrement();
                        String[] statuses = metadataStatuses;
                        body = json(statuses[Math.min(call, statuses.length - 1)]);
                    }
                    byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
                    exchange.getResponseHeaders().add("Content-Type", "application/json");
                    exchange.sendResponseHeaders(status, bytes.length);
                    exchange.getResponseBody().write(bytes);
                    exchange.close();
                });
        server.start();
    }

    @AfterEach
    void stopServer() {
        server.stop(0);
    }

    private static String json(String status) {
        return "{\"id\":\"" + ID + "\",\"status\":\"" + status + "\"}";
    }

    private AttachmentClient client(long timeoutMillis) {
        AttachmentClient client = new AttachmentClient();
        client.apiBuilder =
                new MemoryServiceApiBuilder(
                        Optional.of("http://127.0.0.1:" + server.getAddress().getPort()),
                        Optional.empty(),
                        new ObjectMapper());
        client.readyTimeoutMillis = timeoutMillis;
        return client;
    }

    @Test
    void createFromUrlReturnsOnlyOnceTheAttachmentIsReady() {
        metadataStatuses = new String[] {"downloading", "downloading", "ready"};

        Map<String, Object> attachment =
                client(10_000).createFromUrl("https://example.test/a.png", "image/png", "a.png");

        assertEquals("ready", attachment.get("status"));
        assertEquals(ID, attachment.get("id"));
        assertEquals(3, metadataCalls.get());
        assertEquals("POST /v1/attachments", requests.get(0));
        assertEquals("GET /v1/attachments/" + ID + "/metadata", requests.get(1));
    }

    @Test
    void createFromUrlReturnsNullWhenTheDownloadFails() {
        metadataStatuses = new String[] {"downloading", "failed"};

        assertNull(
                client(10_000).createFromUrl("https://example.test/a.png", "image/png", "a.png"));
    }

    @Test
    void createFromUrlReturnsNullWhenTheDownloadDoesNotFinishInTime() {
        metadataStatuses = new String[] {"downloading"};

        assertNull(client(800).createFromUrl("https://example.test/a.png", "image/png", "a.png"));
    }
}
