package io.github.chirino.memoryservice.client;

import static org.assertj.core.api.Assertions.assertThat;

import io.github.chirino.memoryservice.client.invoker.ApiClient;
import org.junit.jupiter.api.Test;
import org.springframework.http.HttpHeaders;
import org.springframework.test.util.ReflectionTestUtils;
import org.springframework.web.reactive.function.client.WebClient;

class MemoryServiceClientsTest {

    @Test
    void buildsApiClientWithDefaults() {
        MemoryServiceClientProperties properties = new MemoryServiceClientProperties();
        properties.setBaseUrl("https://memory.local");
        properties.setApiKey("abc");

        ApiClient client =
                MemoryServiceClients.createApiClient(properties, WebClient.builder(), null);

        assertThat(client.getBasePath()).isEqualTo("https://memory.local");
        HttpHeaders headers = (HttpHeaders) ReflectionTestUtils.getField(client, "defaultHeaders");
        assertThat(headers.getFirst("X-API-Key")).isEqualTo("abc");
    }
}
