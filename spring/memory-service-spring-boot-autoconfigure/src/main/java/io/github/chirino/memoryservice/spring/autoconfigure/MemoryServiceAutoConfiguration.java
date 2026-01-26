package io.github.chirino.memoryservice.spring.autoconfigure;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.api.SearchApi;
import io.github.chirino.memoryservice.client.api.SystemApi;
import io.github.chirino.memoryservice.client.api.UserConversationsApi;
import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.github.chirino.memoryservice.client.invoker.auth.Authentication;
import io.github.chirino.memoryservice.client.invoker.auth.HttpBearerAuth;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcClients;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcProperties;
import io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection.MemoryServiceConnectionDetails;
import io.grpc.ManagedChannel;
import java.net.URI;
import java.util.Optional;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Bean;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.WebClient;

@AutoConfiguration
@ConditionalOnClass(ApiClient.class)
@EnableConfigurationProperties({
    MemoryServiceClientProperties.class,
    MemoryServiceGrpcProperties.class
})
public class MemoryServiceAutoConfiguration {

    private static final Logger logger =
            LoggerFactory.getLogger(MemoryServiceAutoConfiguration.class);

    @Bean
    @ConditionalOnMissingBean
    public ApiClient memoryServiceApiClient(
            ObjectProvider<WebClient.Builder> webClientBuilderProvider,
            ObjectProvider<MemoryServiceConnectionDetails> connectionDetailsProvider,
            MemoryServiceClientProperties properties) {

        WebClient.Builder builder =
                webClientBuilderProvider.getIfAvailable(ApiClient::buildWebClientBuilder);
        ApiClient apiClient = (builder != null) ? new ApiClient(builder.build()) : new ApiClient();

        MemoryServiceConnectionDetails connectionDetails =
                connectionDetailsProvider.getIfAvailable();
        if (connectionDetails != null) {
            logger.info(
                    "MemoryService connection details detected: baseUri={}, apiKeyPresent={}",
                    connectionDetails.getBaseUri(),
                    StringUtils.hasText(connectionDetails.getApiKey()));
        } else {
            logger.info(
                    "MemoryService connection details not provided; falling back to properties");
        }

        String basePath =
                Optional.ofNullable(connectionDetails)
                        .map(MemoryServiceConnectionDetails::getBaseUri)
                        .map(URI::toString)
                        .orElseGet(
                                () ->
                                        Optional.ofNullable(properties.getBaseUrl())
                                                .filter(StringUtils::hasText)
                                                .orElseGet(apiClient::getBasePath));

        apiClient.setBasePath(basePath);
        configureApiKey(properties, connectionDetails, apiClient);
        configureBearer(properties, apiClient);
        return apiClient;
    }

    private void configureApiKey(
            MemoryServiceClientProperties properties,
            MemoryServiceConnectionDetails connectionDetails,
            ApiClient apiClient) {
        String apiKey =
                StringUtils.hasText(properties.getApiKey())
                        ? properties.getApiKey()
                        : Optional.ofNullable(connectionDetails)
                                .map(MemoryServiceConnectionDetails::getApiKey)
                                .filter(StringUtils::hasText)
                                .orElse(null);

        if (!StringUtils.hasText(apiKey)) {
            return;
        }
        apiClient.addDefaultHeader("X-API-Key", apiKey);
    }

    private void configureBearer(MemoryServiceClientProperties properties, ApiClient apiClient) {
        if (!StringUtils.hasText(properties.getBearerToken())) {
            return;
        }
        Authentication authentication = apiClient.getAuthentication("BearerAuth");
        if (authentication instanceof HttpBearerAuth bearerAuth) {
            bearerAuth.setBearerToken(properties.getBearerToken());
        }
    }

    @Bean
    @ConditionalOnMissingBean
    public ConversationsApi conversationsApi(ApiClient apiClient) {
        return new ConversationsApi(apiClient);
    }

    @Bean
    @ConditionalOnMissingBean
    public UserConversationsApi userConversationsApi(ApiClient apiClient) {
        return new UserConversationsApi(apiClient);
    }

    @Bean
    @ConditionalOnMissingBean
    public SearchApi searchApi(ApiClient apiClient) {
        return new SearchApi(apiClient);
    }

    @Bean
    @ConditionalOnMissingBean
    public SystemApi systemApi(ApiClient apiClient) {
        return new SystemApi(apiClient);
    }

    @Bean
    @ConditionalOnMissingBean
    @ConditionalOnClass(MemoryServiceProxy.class)
    public MemoryServiceProxy memoryServiceProxy(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider,
            ObjectProvider<MemoryServiceConnectionDetails> connectionDetailsProvider) {

        // Connection details (from Docker Compose/Testcontainers) take precedence over defaults.
        // Only explicit property configuration should override connection details.
        MemoryServiceConnectionDetails connectionDetails =
                connectionDetailsProvider.getIfAvailable();
        if (connectionDetails != null) {
            // Prefer connection details for API key if not explicitly configured
            if (!StringUtils.hasText(properties.getApiKey())
                    && StringUtils.hasText(connectionDetails.getApiKey())) {
                properties.setApiKey(connectionDetails.getApiKey());
            }
            // Prefer connection details for base URL (overrides the default value)
            if (connectionDetails.getBaseUri() != null) {
                properties.setBaseUrl(connectionDetails.getBaseUri().toString());
            }
            logger.info(
                    "MemoryServiceProxy configured from connection details: baseUrl={},"
                            + " apiKeyPresent={}",
                    properties.getBaseUrl(),
                    StringUtils.hasText(properties.getApiKey()));
        } else {
            logger.info(
                    "MemoryServiceProxy configured from properties: baseUrl={}, apiKeyPresent={}",
                    properties.getBaseUrl(),
                    StringUtils.hasText(properties.getApiKey()));
        }

        return new MemoryServiceProxy(
                properties, webClientBuilder, authorizedClientServiceProvider.getIfAvailable());
    }

    // ----- gRPC Client Auto-Configuration -----

    /**
     * Resolves the effective base URI from connection details or properties.
     * This is used by both the REST client and gRPC client configuration.
     */
    private URI resolveBaseUri(
            MemoryServiceConnectionDetails connectionDetails,
            MemoryServiceClientProperties properties) {
        if (connectionDetails != null && connectionDetails.getBaseUri() != null) {
            return connectionDetails.getBaseUri();
        }
        if (StringUtils.hasText(properties.getBaseUrl())) {
            return URI.create(properties.getBaseUrl());
        }
        return null;
    }

    @Bean(destroyMethod = "shutdownNow")
    @ConditionalOnClass(ManagedChannel.class)
    @ConditionalOnMissingBean
    public ManagedChannel memoryServiceChannel(
            MemoryServiceGrpcProperties grpcProperties,
            MemoryServiceClientProperties clientProperties,
            ObjectProvider<MemoryServiceConnectionDetails> connectionDetailsProvider) {

        MemoryServiceConnectionDetails connectionDetails =
                connectionDetailsProvider.getIfAvailable();

        // If gRPC is explicitly configured, use those settings
        if (grpcProperties.isEnabled()) {
            logger.info(
                    "Creating gRPC ManagedChannel (explicit config): target={}, plaintext={}",
                    grpcProperties.getTarget(),
                    grpcProperties.isPlaintext());
            return MemoryServiceGrpcClients.channelBuilder(grpcProperties).build();
        }

        // Otherwise, try to auto-derive from REST client baseUrl or connection details
        URI baseUri = resolveBaseUri(connectionDetails, clientProperties);
        if (baseUri == null) {
            logger.info(
                    "gRPC channel not created: no explicit gRPC config and no baseUrl available");
            return null;
        }

        try {
            String host = baseUri.getHost();
            if (host == null) {
                host = "localhost";
            }
            int port = baseUri.getPort();
            if (port == -1) {
                port = "https".equals(baseUri.getScheme()) ? 443 : 80;
            }
            boolean plaintext = !"https".equals(baseUri.getScheme());

            String target = host + ":" + port;
            logger.info(
                    "Auto-configuring gRPC from baseUri={}: target={}, plaintext={}",
                    baseUri,
                    target,
                    plaintext);

            grpcProperties.setTarget(target);
            grpcProperties.setPlaintext(plaintext);
            return MemoryServiceGrpcClients.channelBuilder(grpcProperties).build();
        } catch (Exception e) {
            logger.warn(
                    "Failed to derive gRPC settings from baseUri={}: {}", baseUri, e.getMessage());
            return null;
        }
    }

    @Bean(destroyMethod = "close")
    @ConditionalOnBean(ManagedChannel.class)
    @ConditionalOnMissingBean(MemoryServiceGrpcClients.MemoryServiceStubs.class)
    public MemoryServiceGrpcClients.MemoryServiceStubs memoryServiceStubs(ManagedChannel channel) {
        if (channel == null) {
            return null;
        }
        logger.info("Creating MemoryServiceStubs for gRPC channel");
        return MemoryServiceGrpcClients.stubs(channel);
    }
}
