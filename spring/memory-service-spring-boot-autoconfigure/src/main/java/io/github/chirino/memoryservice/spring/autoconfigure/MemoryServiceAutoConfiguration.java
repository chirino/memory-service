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

    @Bean
    @ConditionalOnMissingBean
    public ApiClient memoryServiceApiClient(
            ObjectProvider<WebClient.Builder> webClientBuilderProvider,
            ObjectProvider<MemoryServiceConnectionDetails> connectionDetailsProvider,
            MemoryServiceClientProperties properties) {

        WebClient.Builder builder =
                webClientBuilderProvider.getIfAvailable(ApiClient::buildWebClientBuilder);
        ApiClient apiClient = (builder != null) ? new ApiClient(builder.build()) : new ApiClient();

        URI baseUri =
                Optional.ofNullable(connectionDetailsProvider.getIfAvailable())
                        .map(MemoryServiceConnectionDetails::getBaseUri)
                        .orElseGet(
                                () ->
                                        Optional.ofNullable(properties.getBaseUrl())
                                                .orElseGet(
                                                        () -> URI.create(apiClient.getBasePath())));

        apiClient.setBasePath(baseUri.toString());
        configureBearer(properties, apiClient);
        return apiClient;
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
