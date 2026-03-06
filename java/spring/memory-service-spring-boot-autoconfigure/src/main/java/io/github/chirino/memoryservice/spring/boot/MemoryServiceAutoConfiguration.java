package io.github.chirino.memoryservice.spring.boot;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.client.MemoryServiceClients;
import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcClients;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcProperties;
import io.grpc.ManagedChannel;
import java.net.URI;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Conditional;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.oauth2.client.ReactiveOAuth2AuthorizedClientManager;
import org.springframework.web.reactive.function.client.WebClient;

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties({
    MemoryServiceClientProperties.class,
    MemoryServiceGrpcProperties.class
})
public class MemoryServiceAutoConfiguration {

    private static final Logger LOG = LoggerFactory.getLogger(MemoryServiceAutoConfiguration.class);

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
    @Conditional(OnGrpcEnabledCondition.class)
    @ConditionalOnMissingBean
    public ManagedChannel memoryServiceChannel(
            MemoryServiceGrpcProperties grpcProperties,
            MemoryServiceClientProperties clientProperties) {

        // If gRPC target is not explicitly configured, derive it from REST client baseUrl
        if (!grpcProperties.isEnabled()) {
            String baseUrl = clientProperties.getBaseUrl();
            try {
                URI uri = URI.create(baseUrl);
                String host = uri.getHost();
                if (host == null) {
                    host = "localhost";
                }
                int port = uri.getPort();
                if (port == -1) {
                    port = "https".equals(uri.getScheme()) ? 443 : 80;
                }
                boolean plaintext = !"https".equals(uri.getScheme());

                String target = host + ":" + port;
                LOG.info(
                        "Auto-configuring gRPC from REST client baseUrl={}: target={},"
                                + " plaintext={}",
                        baseUrl,
                        target,
                        plaintext);

                grpcProperties.setTarget(target);
                grpcProperties.setPlaintext(plaintext);
            } catch (Exception e) {
                LOG.warn(
                        "Failed to derive gRPC settings from baseUrl={}: {}",
                        baseUrl,
                        e.getMessage());
                throw new IllegalStateException(
                        "Cannot configure gRPC channel from baseUrl: " + baseUrl, e);
            }
        }

        return MemoryServiceGrpcClients.channelBuilder(grpcProperties).build();
    }

    @Bean(destroyMethod = "close")
    @ConditionalOnBean(ManagedChannel.class)
    @ConditionalOnMissingBean(MemoryServiceGrpcClients.MemoryServiceStubs.class)
    public MemoryServiceGrpcClients.MemoryServiceStubs memoryServiceStubs(ManagedChannel channel) {
        return MemoryServiceGrpcClients.stubs(channel);
    }
}
