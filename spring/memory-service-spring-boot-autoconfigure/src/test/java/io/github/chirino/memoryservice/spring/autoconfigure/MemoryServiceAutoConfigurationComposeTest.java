package io.github.chirino.memoryservice.spring.autoconfigure;

import static org.assertj.core.api.Assertions.assertThat;

import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection.DefaultMemoryServiceConnectionDetails;
import io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection.MemoryServiceConnectionDetails;
import java.net.URI;
import org.junit.jupiter.api.Test;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.autoconfigure.web.reactive.function.client.WebClientAutoConfiguration;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.http.HttpHeaders;
import org.springframework.test.util.ReflectionTestUtils;

class MemoryServiceAutoConfigurationComposeTest {

    private final ApplicationContextRunner contextRunner =
            new ApplicationContextRunner()
                    .withConfiguration(
                            AutoConfigurations.of(
                                    WebClientAutoConfiguration.class,
                                    MemoryServiceAutoConfiguration.class));

    @Test
    void apiKeyDefaultsFromServiceConnectionWhenPropertyMissing() {
        MemoryServiceConnectionDetails connectionDetails =
                new DefaultMemoryServiceConnectionDetails(
                        URI.create("https://compose.test"), "derived-key");

        contextRunner
                .withBean(MemoryServiceConnectionDetails.class, () -> connectionDetails)
                .run(
                        context -> {
                            ApiClient client = context.getBean(ApiClient.class);
                            assertThat(client.getBasePath()).isEqualTo("https://compose.test");
                            HttpHeaders headers =
                                    (HttpHeaders)
                                            ReflectionTestUtils.getField(client, "defaultHeaders");
                            assertThat(headers.getFirst("X-API-Key")).isEqualTo("derived-key");
                        });
    }
}
