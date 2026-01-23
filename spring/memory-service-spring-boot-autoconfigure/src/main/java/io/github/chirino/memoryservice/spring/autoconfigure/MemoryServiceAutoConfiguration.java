package io.github.chirino.memoryservice.spring.autoconfigure;

import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.api.SearchApi;
import io.github.chirino.memoryservice.client.api.SystemApi;
import io.github.chirino.memoryservice.client.api.UserConversationsApi;
import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.github.chirino.memoryservice.client.invoker.auth.Authentication;
import io.github.chirino.memoryservice.client.invoker.auth.HttpBearerAuth;
import io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection.MemoryServiceConnectionDetails;
import java.net.URI;
import java.util.Optional;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Bean;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.WebClient;

@AutoConfiguration
@ConditionalOnClass(ApiClient.class)
@EnableConfigurationProperties(MemoryServiceClientProperties.class)
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

        URI baseUri =
                Optional.ofNullable(connectionDetails)
                        .map(MemoryServiceConnectionDetails::getBaseUri)
                        .orElseGet(
                                () ->
                                        Optional.ofNullable(properties.getBaseUrl())
                                                .orElseGet(
                                                        () -> URI.create(apiClient.getBasePath())));

        apiClient.setBasePath(baseUri.toString());
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
}
