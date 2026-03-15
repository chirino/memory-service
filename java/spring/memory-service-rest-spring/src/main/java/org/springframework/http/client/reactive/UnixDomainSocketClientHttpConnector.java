package org.springframework.http.client.reactive;

import io.netty.handler.codec.http.HttpMethod;
import io.netty.handler.codec.http.HttpRequest;
import io.netty.handler.codec.http.HttpVersion;
import java.lang.reflect.Field;
import java.net.URI;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Function;
import org.springframework.util.Assert;
import reactor.core.publisher.Mono;
import reactor.netty.Connection;
import reactor.netty.http.client.HttpClient;
import reactor.netty.http.client.HttpClient.RequestSender;
import reactor.netty.http.client.HttpClientResponse;

/**
 * Reactor Netty connector that preserves the original request URI for Spring while routing the
 * actual HTTP exchange over a Unix domain socket using only the request path and query.
 */
public final class UnixDomainSocketClientHttpConnector implements ClientHttpConnector {

    private final HttpClient httpClient;

    public UnixDomainSocketClientHttpConnector(HttpClient httpClient) {
        Assert.notNull(httpClient, "HttpClient is required");
        this.httpClient = httpClient;
    }

    @Override
    public Mono<ClientHttpResponse> connect(
            org.springframework.http.HttpMethod method,
            URI uri,
            Function<? super ClientHttpRequest, Mono<Void>> requestCallback) {
        RequestSender requestSender = httpClient.request(HttpMethod.valueOf(method.name()));
        RequestSender configuredSender = requestSender.uri(toTransportUri(uri));
        AtomicReference<ReactorClientHttpResponse> responseRef = new AtomicReference<>();

        return configuredSender
                .send(
                        (request, outbound) -> {
                            forceHttp11(request);
                            return requestCallback.apply(
                                    new ReactorClientHttpRequest(method, uri, request, outbound));
                        })
                .responseConnection(
                        (response, connection) ->
                                Mono.just(adaptResponse(responseRef, response, connection)))
                .next()
                .doOnCancel(() -> releaseAfterCancel(responseRef, method));
    }

    private static ClientHttpResponse adaptResponse(
            AtomicReference<ReactorClientHttpResponse> responseRef,
            HttpClientResponse response,
            Connection connection) {
        ReactorClientHttpResponse adapted = new ReactorClientHttpResponse(response, connection);
        responseRef.set(adapted);
        return adapted;
    }

    private static void releaseAfterCancel(
            AtomicReference<ReactorClientHttpResponse> responseRef,
            org.springframework.http.HttpMethod method) {
        ReactorClientHttpResponse response = responseRef.get();
        if (response != null) {
            response.releaseAfterCancel(method);
        }
    }

    private static String toTransportUri(URI uri) {
        String rawPath = uri.getRawPath();
        String path = (rawPath == null || rawPath.isEmpty()) ? "/" : rawPath;
        if (uri.getRawQuery() == null || uri.getRawQuery().isEmpty()) {
            return path;
        }
        return path + "?" + uri.getRawQuery();
    }

    private static void forceHttp11(reactor.netty.http.client.HttpClientRequest request) {
        setField(request, "version", HttpVersion.HTTP_1_1);

        Object nettyRequest = readField(request, "nettyRequest");
        if (nettyRequest instanceof HttpRequest httpRequest) {
            httpRequest.setProtocolVersion(HttpVersion.HTTP_1_1);
        }
    }

    private static Object readField(Object target, String name) {
        try {
            Field field = target.getClass().getDeclaredField(name);
            field.setAccessible(true);
            return field.get(target);
        } catch (ReflectiveOperationException ignored) {
            return null;
        }
    }

    private static void setField(Object target, String name, Object value) {
        try {
            Field field = target.getClass().getDeclaredField(name);
            field.setAccessible(true);
            field.set(target, value);
        } catch (ReflectiveOperationException ignored) {
        }
    }
}
