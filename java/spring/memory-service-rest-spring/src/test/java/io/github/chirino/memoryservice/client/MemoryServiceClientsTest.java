package io.github.chirino.memoryservice.client;

import static org.assertj.core.api.Assertions.assertThat;

import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.netty.channel.unix.DomainSocketAddress;
import org.junit.jupiter.api.Test;
import org.springframework.http.HttpHeaders;
import org.springframework.test.util.ReflectionTestUtils;
import org.springframework.web.reactive.function.client.WebClient;

class MemoryServiceClientsTest {

    @Test
    void buildsApiClientWithDefaults() {
        MemoryServiceClientProperties properties = new MemoryServiceClientProperties();
        properties.setUrl("https://memory.local");
        properties.setApiKey("abc");

        ApiClient client =
                MemoryServiceClients.createApiClient(properties, WebClient.builder(), null);

        assertThat(client.getBasePath()).isEqualTo("https://memory.local");
        HttpHeaders headers = (HttpHeaders) ReflectionTestUtils.getField(client, "defaultHeaders");
        assertThat(headers.getFirst("X-API-Key")).isEqualTo("abc");
    }

    @Test
    void usesLogicalLocalhostBasePathForUnixSocketUrl() {
        MemoryServiceClientProperties properties = new MemoryServiceClientProperties();
        properties.setUrl("unix:///tmp/memory-service.sock");

        ApiClient client =
                MemoryServiceClients.createApiClient(properties, WebClient.builder(), null);

        assertThat(client.getBasePath()).isEqualTo("http://localhost");
    }

    @Test
    void usesNettyDomainSocketAddressForUnixSocketTransport() {
        MemoryServiceEndpoint endpoint =
                MemoryServiceEndpoint.parse("unix:///tmp/memory-service.sock");

        assertThat(MemoryServiceClients.socketAddress(endpoint))
                .isInstanceOf(DomainSocketAddress.class)
                .hasToString("/tmp/memory-service.sock");
    }
}
