package io.github.chirino.memoryservice.client;

import io.github.chirino.memoryservice.client.invoker.ApiClient;
import java.time.Duration;
import java.util.Map;
import java.util.Objects;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.http.client.reactive.ReactorClientHttpConnector;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.ReactiveOAuth2AuthorizedClientManager;
import org.springframework.security.oauth2.client.web.reactive.function.client.ServerOAuth2AuthorizedClientExchangeFilterFunction;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.ExchangeFilterFunction;
import org.springframework.web.reactive.function.client.WebClient;
import reactor.netty.http.client.HttpClient;

public final class MemoryServiceClients {

    private static final Logger LOGGER = LoggerFactory.getLogger(MemoryServiceClients.class);

    private MemoryServiceClients() {}

    public static ApiClient createApiClient(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            @Nullable ReactiveOAuth2AuthorizedClientManager authorizedClientManager) {

        WebClient.Builder builder = webClientBuilder.clone();

        if (properties.getTimeout() != null) {
            builder.clientConnector(new ReactorClientHttpConnector(buildHttpClient(properties)));
        }

        if (properties.isLogRequests()) {
            builder.filter(logRequests(properties));
        }

        if (authorizedClientManager != null
                && StringUtils.hasText(properties.getOidcClientRegistration())) {
            builder.filter(oauth(properties, authorizedClientManager));
        }

        builder.defaultHeaders(
                headers -> {
                    if (StringUtils.hasText(properties.getApiKey())) {
                        headers.set("X-API-Key", Objects.requireNonNull(properties.getApiKey()));
                    }
                    for (Map.Entry<String, String> entry :
                            properties.getDefaultHeaders().entrySet()) {
                        headers.set(entry.getKey(), entry.getValue());
                    }
                });

        ApiClient apiClient = new ApiClient(builder.build());
        apiClient.setBasePath(properties.getBaseUrl());
        if (StringUtils.hasText(properties.getApiKey())) {
            apiClient.addDefaultHeader("X-API-Key", properties.getApiKey());
        }
        if (StringUtils.hasText(properties.getBearerToken())) {
            apiClient.setBearerToken(properties.getBearerToken());
        }
        properties
                .getDefaultHeaders()
                .forEach((name, value) -> apiClient.addDefaultHeader(name, value));
        return apiClient;
    }

    private static HttpClient buildHttpClient(MemoryServiceClientProperties properties) {
        HttpClient httpClient = HttpClient.create();
        Duration timeout = properties.getTimeout();
        if (timeout != null) {
            httpClient = httpClient.responseTimeout(timeout);
        }
        return httpClient;
    }

    private static ExchangeFilterFunction logRequests(MemoryServiceClientProperties properties) {
        return (request, next) -> {
            String url = request.url().toString();
            if (StringUtils.hasText(properties.getBaseUrl())
                    && !url.startsWith(properties.getBaseUrl())) {
                return next.exchange(request);
            }
            boolean hasAuthorization = request.headers().containsKey(HttpHeaders.AUTHORIZATION);
            boolean hasApiKey = request.headers().containsKey("X-API-Key");
            LOGGER.info(
                    "memory-service client request: {} {}, sent Authorization header: {}, sent"
                            + " X-API-Key header: {}",
                    request.method(),
                    url,
                    hasAuthorization,
                    hasApiKey);
            return next.exchange(request);
        };
    }

    private static ExchangeFilterFunction oauth(
            MemoryServiceClientProperties properties,
            ReactiveOAuth2AuthorizedClientManager authorizedClientManager) {

        ServerOAuth2AuthorizedClientExchangeFilterFunction oauth =
                new ServerOAuth2AuthorizedClientExchangeFilterFunction(authorizedClientManager);
        oauth.setDefaultClientRegistrationId(properties.getOidcClientRegistration());
        return oauth;
    }
}
