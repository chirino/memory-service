package io.github.chirino.memoryservice.history;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.client.MemoryServiceClients;
import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.invoker.ApiClient;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.ReactiveOAuth2AuthorizedClientManager;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.WebClient;

public class ConversationsApiFactory {

    private final MemoryServiceClientProperties properties;
    private final WebClient.Builder webClientBuilder;
    private final ReactiveOAuth2AuthorizedClientManager authorizedClientManager;

    public ConversationsApiFactory(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            @Nullable ReactiveOAuth2AuthorizedClientManager authorizedClientManager) {
        this.properties = properties;
        this.webClientBuilder = webClientBuilder;
        this.authorizedClientManager = authorizedClientManager;
    }

    public ConversationsApi create(@Nullable String explicitBearerToken) {
        ApiClient apiClient =
                MemoryServiceClients.createApiClient(
                        properties, webClientBuilder, authorizedClientManager);
        String bearerToken = resolveBearerToken(explicitBearerToken);
        if (StringUtils.hasText(bearerToken)) {
            apiClient.setBearerToken(bearerToken);
        }
        return new ConversationsApi(apiClient);
    }

    private String resolveBearerToken(@Nullable String explicitBearerToken) {
        if (StringUtils.hasText(explicitBearerToken)) {
            return explicitBearerToken;
        }
        return properties.getBearerToken();
    }
}
