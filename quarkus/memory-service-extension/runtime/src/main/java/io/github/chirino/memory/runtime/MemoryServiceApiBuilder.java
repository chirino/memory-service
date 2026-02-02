package io.github.chirino.memory.runtime;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.client.ClientRequestFilter;
import java.net.URI;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.rest.client.RestClientBuilder;

@ApplicationScoped
public class MemoryServiceApiBuilder {

    private final String baseUrl;
    private final String apiKey;
    private final String bearerToken;

    @Inject
    public MemoryServiceApiBuilder(
            @ConfigProperty(name = "memory-service.client.url") Optional<String> clientUrl,
            @ConfigProperty(name = "quarkus.rest-client.memory-service-client.url")
                    Optional<String> quarkusRestClientUrl,
            @ConfigProperty(name = "memory-service.client.api-key") Optional<String> apiKey) {
        this(resolveBaseUrl(clientUrl, quarkusRestClientUrl), apiKey.orElse(null), null);
    }

    private MemoryServiceApiBuilder(String baseUrl, String apiKey, String bearerToken) {
        this.baseUrl = baseUrl;
        this.apiKey = apiKey;
        this.bearerToken = bearerToken;
    }

    public MemoryServiceApiBuilder withBearerAuth(String token) {
        if (token == null || token.isBlank()) {
            token = null;
        }
        return new MemoryServiceApiBuilder(baseUrl, apiKey, token);
    }

    public MemoryServiceApiBuilder withApiKey(String apiKey) {
        if (apiKey == null || apiKey.isBlank()) {
            apiKey = null;
        }
        return new MemoryServiceApiBuilder(baseUrl, apiKey, bearerToken);
    }

    public MemoryServiceApiBuilder withBaseUrl(String baseUrl) {
        if (baseUrl == null || baseUrl.isBlank()) {
            baseUrl = resolveBaseUrl(Optional.empty(), Optional.empty());
        }
        return new MemoryServiceApiBuilder(baseUrl, apiKey, bearerToken);
    }

    public <T> T build(Class<T> clazz) {
        RestClientBuilder builder = RestClientBuilder.newBuilder().baseUri(URI.create(baseUrl));
        if (apiKey != null && !apiKey.isBlank()) {
            builder.register(
                    (ClientRequestFilter) ctx -> ctx.getHeaders().putSingle("X-API-Key", apiKey));
        }
        if (bearerToken != null && !bearerToken.isBlank()) {
            builder.register(
                    (ClientRequestFilter)
                            ctx ->
                                    ctx.getHeaders()
                                            .putSingle("Authorization", "Bearer " + bearerToken));
        }
        return builder.build(clazz);
    }

    private static String resolveBaseUrl(Optional<String> clientUrl, Optional<String> legacyUrl) {
        return clientUrl.orElseGet(() -> legacyUrl.orElse("http://localhost:8080"));
    }
}
