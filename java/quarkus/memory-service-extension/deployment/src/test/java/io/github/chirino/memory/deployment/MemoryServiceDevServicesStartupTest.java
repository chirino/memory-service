package io.github.chirino.memory.deployment;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.Timeout;
import org.testcontainers.DockerClientFactory;
import org.testcontainers.Testcontainers;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.containers.wait.strategy.Wait;
import org.testcontainers.utility.DockerImageName;

class MemoryServiceDevServicesStartupTest {

    private static final int MEMORY_SERVICE_PORT = 8080;
    private static final String DEVELOPER_FRONTEND_API_KEY = "startup-test-developer-key";

    @Test
    @Timeout(180)
    void containerStartsWithOidcAndNoAudienceAllowlist() throws Exception {
        assumeTrue(
                DockerClientFactory.instance().isDockerAvailable(),
                "Docker is required for the Dev Services startup test");

        HttpServer oidcServer = startOidcDiscoveryServer();
        try {
            int oidcPort = oidcServer.getAddress().getPort();
            Testcontainers.exposeHostPorts(oidcPort);
            String issuer = "http://host.testcontainers.internal:" + oidcPort;
            String configuredTestImage =
                    System.getProperty("memory-service.devservices.test-image");
            String imageName =
                    configuredTestImage != null
                            ? configuredTestImage
                            : MemoryServiceDevServicesProcessor.resolveImageName(
                                    null, MemoryServiceDevServicesProcessor.loadExtensionVersion());

            try (GenericContainer<?> container =
                    new GenericContainer<>(DockerImageName.parse(imageName))
                            .withExposedPorts(MEMORY_SERVICE_PORT)
                            .waitingFor(
                                    Wait.forHttp("/ready")
                                            .forPort(MEMORY_SERVICE_PORT)
                                            .forStatusCode(200)
                                            .withStartupTimeout(Duration.ofMinutes(2)))) {
                MemoryServiceDevServicesProcessor.configureDefaultEnvironment(
                        container, "test-agent-api-key");
                if (configuredTestImage != null) {
                    MemoryServiceDevServicesProcessor.configureFixedPortEnvironment(
                            container, 8082, DEVELOPER_FRONTEND_API_KEY);
                }
                MemoryServiceDevServicesProcessor.configureKeycloakDevService(container, issuer);

                try {
                    container.start();
                } catch (RuntimeException e) {
                    fail(
                            "Memory Service Dev Services container failed to start:\n"
                                    + container.getLogs(),
                            e);
                }

                HttpResponse<Void> ready =
                        HttpClient.newHttpClient()
                                .send(
                                        HttpRequest.newBuilder(
                                                        URI.create(
                                                                "http://"
                                                                        + container.getHost()
                                                                        + ":"
                                                                        + container.getMappedPort(
                                                                                MEMORY_SERVICE_PORT)
                                                                        + "/ready"))
                                                .timeout(Duration.ofSeconds(10))
                                                .GET()
                                                .build(),
                                        HttpResponse.BodyHandlers.discarding());
                assertEquals(200, ready.statusCode());

                if (configuredTestImage != null) {
                    assertNoLoginDeveloperFrontend(container);
                }
            }
        } finally {
            oidcServer.stop(0);
        }
    }

    private static void assertNoLoginDeveloperFrontend(GenericContainer<?> container)
            throws IOException, InterruptedException {
        String baseURL =
                "http://"
                        + container.getHost()
                        + ":"
                        + container.getMappedPort(MEMORY_SERVICE_PORT);
        HttpClient client = HttpClient.newHttpClient();

        HttpResponse<String> configResponse =
                client.send(
                        HttpRequest.newBuilder(URI.create(baseURL + "/developer/config.json"))
                                .timeout(Duration.ofSeconds(10))
                                .GET()
                                .build(),
                        HttpResponse.BodyHandlers.ofString());
        assertEquals(200, configResponse.statusCode());
        assertTrue(configResponse.body().contains("\"mode\":\"api-key\""));
        assertTrue(
                configResponse
                        .body()
                        .contains("\"apiKey\":\"" + DEVELOPER_FRONTEND_API_KEY + "\""));
        assertFalse(configResponse.body().contains("\"authority\""));

        HttpResponse<String> adminResponse =
                client.send(
                        HttpRequest.newBuilder(
                                        URI.create(baseURL + "/v1/admin/conversations?pageSize=1"))
                                .timeout(Duration.ofSeconds(10))
                                .header("X-API-Key", DEVELOPER_FRONTEND_API_KEY)
                                .GET()
                                .build(),
                        HttpResponse.BodyHandlers.ofString());
        assertEquals(200, adminResponse.statusCode(), adminResponse.body());
    }

    private static HttpServer startOidcDiscoveryServer() throws IOException {
        HttpServer server = HttpServer.create(new InetSocketAddress("0.0.0.0", 0), 0);
        int port = server.getAddress().getPort();
        String issuer = "http://host.testcontainers.internal:" + port;
        byte[] body =
                ("{\"issuer\":\""
                                + issuer
                                + "\",\"authorization_endpoint\":\""
                                + issuer
                                + "/authorize\",\"token_endpoint\":\""
                                + issuer
                                + "/token\",\"jwks_uri\":\""
                                + issuer
                                + "/jwks\",\"id_token_signing_alg_values_supported\":[\"RS256\"]}")
                        .getBytes(StandardCharsets.UTF_8);
        server.createContext(
                "/.well-known/openid-configuration",
                exchange -> {
                    exchange.getResponseHeaders().set("Content-Type", "application/json");
                    exchange.sendResponseHeaders(200, body.length);
                    exchange.getResponseBody().write(body);
                    exchange.close();
                });
        server.start();
        return server;
    }
}
