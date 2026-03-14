package io.github.chirino.memoryservice.client;

import io.github.chirino.memoryservice.client.invoker.ApiClient;
import java.net.UnixDomainSocketAddress;
import java.time.Duration;
import java.util.Map;
import java.util.Objects;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.http.client.reactive.ReactorClientHttpConnector;
import org.springframework.http.client.reactive.UnixDomainSocketClientHttpConnector;
import org.springframework.lang.NonNull;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.ReactiveOAuth2AuthorizedClientManager;
import org.springframework.security.oauth2.client.web.reactive.function.client.ServerOAuth2AuthorizedClientExchangeFilterFunction;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.ExchangeFilterFunction;
import org.springframework.web.reactive.function.client.WebClient;
import reactor.netty.http.HttpProtocol;
import reactor.netty.http.client.HttpClient;

public final class MemoryServiceClients {

    private static final Logger LOGGER = LoggerFactory.getLogger(MemoryServiceClients.class);

    private MemoryServiceClients() {}

    public static ApiClient createApiClient(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            @Nullable ReactiveOAuth2AuthorizedClientManager authorizedClientManager) {
        MemoryServiceEndpoint endpoint = resolveEndpoint(properties);

        WebClient build =
                createWebClient(properties, webClientBuilder, authorizedClientManager).build();
        ApiClient apiClient = new ApiClient(build);
        apiClient.setBasePath(endpoint.logicalBaseUrl());
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

    public static String resolveBaseUrl(MemoryServiceClientProperties properties) {
        return resolveEndpoint(properties).logicalBaseUrl();
    }

    public static MemoryServiceEndpoint resolveEndpoint(MemoryServiceClientProperties properties) {
        return MemoryServiceEndpoint.parse(properties.getUrl());
    }

    @NonNull
    public static WebClient.Builder createWebClient(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            @Nullable ReactiveOAuth2AuthorizedClientManager authorizedClientManager) {
        WebClient.Builder builder = webClientBuilder.clone();
        MemoryServiceEndpoint endpoint = resolveEndpoint(properties);

        if (endpoint.usesUnixSocket()) {
            builder.clientConnector(
                    new UnixDomainSocketClientHttpConnector(buildHttpClient(endpoint, properties)));
        } else if (properties.getTimeout() != null) {
            builder.clientConnector(
                    new ReactorClientHttpConnector(buildHttpClient(endpoint, properties)));
        }

        if (properties.isLogRequests()) {
            builder.filter(logRequests(properties));
        }

        if (authorizedClientManager != null
                && StringUtils.hasText(properties.getOidcClientRegistration())) {
            builder.filter(oauth(properties, authorizedClientManager));
        }

        builder.baseUrl(endpoint.logicalBaseUrl());

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

        return builder;
    }

    private static HttpClient buildHttpClient(
            MemoryServiceEndpoint endpoint, MemoryServiceClientProperties properties) {
        HttpClient httpClient = HttpClient.create();
        if (endpoint.usesUnixSocket()) {
            httpClient =
                    httpClient
                            .remoteAddress(
                                    () -> UnixDomainSocketAddress.of(endpoint.unixSocketPath()))
                            .protocol(HttpProtocol.HTTP11);
        }
        Duration timeout = properties.getTimeout();
        if (timeout != null) {
            httpClient = httpClient.responseTimeout(timeout);
        }
        return httpClient;
    }

    private static ExchangeFilterFunction logRequests(MemoryServiceClientProperties properties) {
        return (request, next) -> {
            String url = request.url().toString();
            String baseUrl = resolveEndpoint(properties).logicalBaseUrl();
            if (StringUtils.hasText(baseUrl) && !url.startsWith(baseUrl)) {
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
