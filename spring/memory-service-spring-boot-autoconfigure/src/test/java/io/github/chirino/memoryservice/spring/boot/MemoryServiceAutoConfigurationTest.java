package io.github.chirino.memoryservice.spring.boot;

import static org.assertj.core.api.Assertions.assertThat;

import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcClients;
import io.grpc.ManagedChannel;
import org.junit.jupiter.api.Test;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.autoconfigure.web.reactive.function.client.WebClientAutoConfiguration;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.http.HttpHeaders;
import org.springframework.test.util.ReflectionTestUtils;

class MemoryServiceAutoConfigurationTest {

    private final ApplicationContextRunner contextRunner =
            new ApplicationContextRunner()
                    .withConfiguration(
                            AutoConfigurations.of(
                                    WebClientAutoConfiguration.class,
                                    MemoryServiceAutoConfiguration.class));

    @Test
    void createsApiClientWithApiKey() {
        contextRunner
                .withPropertyValues(
                        "memory-service.client.base-url=https://example.test",
                        "memory-service.client.api-key=xyz")
                .run(
                        context -> {
                            ApiClient client = context.getBean(ApiClient.class);
                            assertThat(client.getBasePath()).isEqualTo("https://example.test");
                            HttpHeaders headers =
                                    (HttpHeaders)
                                            ReflectionTestUtils.getField(client, "defaultHeaders");
                            assertThat(headers.getFirst("X-API-Key")).isEqualTo("xyz");
                        });
    }

    @Test
    void createsGrpcBeansWhenEnabled() {
        contextRunner
                .withPropertyValues(
                        "memory-service.grpc.enabled=true",
                        "memory-service.grpc.target=localhost:6565")
                .run(
                        context -> {
                            ManagedChannel channel = context.getBean(ManagedChannel.class);
                            MemoryServiceGrpcClients.MemoryServiceStubs stubs =
                                    context.getBean(
                                            MemoryServiceGrpcClients.MemoryServiceStubs.class);

                            assertThat(channel.isShutdown()).isFalse();
                            assertThat(stubs.systemService()).isNotNull();
                        });
    }
}
