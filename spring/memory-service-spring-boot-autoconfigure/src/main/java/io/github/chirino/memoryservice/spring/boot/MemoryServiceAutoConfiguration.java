package io.github.chirino.memoryservice.spring.boot;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.client.MemoryServiceClients;
import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcClients;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcProperties;
import io.grpc.ManagedChannel;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.oauth2.client.ReactiveOAuth2AuthorizedClientManager;
import org.springframework.web.reactive.function.client.WebClient;

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties({
    MemoryServiceClientProperties.class,
    MemoryServiceGrpcProperties.class
})
public class MemoryServiceAutoConfiguration {

    @Bean
    @ConditionalOnMissingBean
    public ApiClient memoryServiceApiClient(
            MemoryServiceClientProperties properties,
            ObjectProvider<WebClient.Builder> builderProvider,
            ObjectProvider<ReactiveOAuth2AuthorizedClientManager> authorizedClientManagerProvider) {

        WebClient.Builder builder = builderProvider.getIfAvailable(WebClient::builder);
        ReactiveOAuth2AuthorizedClientManager manager =
                authorizedClientManagerProvider.getIfAvailable();
        return MemoryServiceClients.createApiClient(properties, builder, manager);
    }

    @Bean(destroyMethod = "shutdownNow")
    @ConditionalOnClass(ManagedChannel.class)
    @ConditionalOnProperty(prefix = "memory-service.grpc", name = "enabled", havingValue = "true")
    @ConditionalOnMissingBean
    public ManagedChannel memoryServiceChannel(MemoryServiceGrpcProperties properties) {
        return MemoryServiceGrpcClients.channelBuilder(properties).build();
    }

    @Bean(destroyMethod = "close")
    @ConditionalOnBean(ManagedChannel.class)
    @ConditionalOnMissingBean(MemoryServiceGrpcClients.MemoryServiceStubs.class)
    public MemoryServiceGrpcClients.MemoryServiceStubs memoryServiceStubs(ManagedChannel channel) {
        return MemoryServiceGrpcClients.stubs(channel);
    }
}
