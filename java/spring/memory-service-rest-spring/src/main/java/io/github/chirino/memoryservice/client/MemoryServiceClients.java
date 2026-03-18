package io.github.chirino.memoryservice.client;

import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.netty.channel.ChannelHandlerContext;
import io.netty.channel.ChannelOutboundHandlerAdapter;
import io.netty.channel.ChannelPromise;
import io.netty.channel.unix.DomainSocketAddress;
import java.net.SocketAddress;
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
import org.springframework.util.Assert;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.ExchangeFilterFunction;
import org.springframework.web.reactive.function.client.WebClient;
import reactor.netty.http.HttpProtocol;
import reactor.netty.http.client.HttpClient;

public final class MemoryServiceClients {

    private static final Logger LOGGER = LoggerFactory.getLogger(MemoryServiceClients.class);

    /**
     * Whether native Netty transport handles {@link DomainSocketAddress} natively.
     * When true (epoll/kqueue on classpath), no address adapter is needed.
     * When false (JDK NIO fallback), we must convert to {@link UnixDomainSocketAddress}.
     */
    private static final boolean NATIVE_UDS_TRANSPORT = probeNativeUdsTransport();

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
                            .remoteAddress(() -> socketAddress(endpoint))
                            .protocol(HttpProtocol.HTTP11);
            if (!NATIVE_UDS_TRANSPORT) {
                // Reactor Netty selects NioDomainSocketChannel when native
                // transport is absent, but NioDomainSocketChannel.doConnect()
                // passes the address straight to the JDK SocketChannel which
                // only accepts java.net.UnixDomainSocketAddress. Intercept the
                // connect call to convert the Netty DomainSocketAddress.
                httpClient =
                        httpClient.doOnChannelInit(
                                (observer, channel, remoteAddress) ->
                                        channel.pipeline()
                                                .addFirst(new DomainSocketAddressAdapter()));
            }
        }
        Duration timeout = properties.getTimeout();
        if (timeout != null) {
            httpClient = httpClient.responseTimeout(timeout);
        }
        return httpClient;
    }

    /**
     * Probes whether Netty's native domain-socket transport (epoll or kqueue) is available.
     * When it is, {@link DomainSocketAddress} works end-to-end without conversion.
     */
    private static boolean probeNativeUdsTransport() {
        for (String className :
                new String[] {"io.netty.channel.epoll.Epoll", "io.netty.channel.kqueue.KQueue"}) {
            try {
                Class<?> cls = Class.forName(className);
                Boolean available = (Boolean) cls.getMethod("isAvailable").invoke(null);
                if (Boolean.TRUE.equals(available)) {
                    LOGGER.debug("Native UDS transport detected: {}", className);
                    return true;
                }
            } catch (ReflectiveOperationException ignored) {
                // class not on classpath — try next
            }
        }
        LOGGER.debug(
                "No native UDS transport found; DomainSocketAddress adapter will be installed");
        return false;
    }

    static SocketAddress socketAddress(MemoryServiceEndpoint endpoint) {
        Assert.isTrue(endpoint.usesUnixSocket(), "endpoint must use a unix socket");
        return new DomainSocketAddress(endpoint.unixSocketPath());
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

    /**
     * Converts Netty {@link DomainSocketAddress} to JDK {@link UnixDomainSocketAddress} before the
     * channel connect. This bridges the gap between Reactor Netty (which uses {@code
     * DomainSocketAddress} for transport selection) and the JDK NIO {@code NioDomainSocketChannel}
     * (which requires {@code UnixDomainSocketAddress}).
     */
    static final class DomainSocketAddressAdapter extends ChannelOutboundHandlerAdapter {
        @Override
        public void connect(
                ChannelHandlerContext ctx,
                SocketAddress remoteAddress,
                SocketAddress localAddress,
                ChannelPromise promise)
                throws Exception {
            if (remoteAddress instanceof DomainSocketAddress dsa) {
                super.connect(ctx, UnixDomainSocketAddress.of(dsa.path()), localAddress, promise);
            } else {
                super.connect(ctx, remoteAddress, localAddress, promise);
            }
        }
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
