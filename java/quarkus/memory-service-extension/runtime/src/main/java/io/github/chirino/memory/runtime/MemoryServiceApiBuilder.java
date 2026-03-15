package io.github.chirino.memory.runtime;

import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.client.ClientRequestFilter;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.rest.client.RestClientBuilder;

@ApplicationScoped
public class MemoryServiceApiBuilder {

    private final MemoryServiceClientUrl clientUrl;
    private final String apiKey;
    private final String bearerToken;
    private final ObjectMapper objectMapper;

    @Inject
    public MemoryServiceApiBuilder(
            @ConfigProperty(name = "memory-service.client.url") Optional<String> clientUrl,
            @ConfigProperty(name = "quarkus.rest-client.memory-service-client.url")
                    Optional<String> quarkusRestClientUrl,
            @ConfigProperty(name = "memory-service.client.api-key") Optional<String> apiKey,
            ObjectMapper objectMapper) {
        this(
                resolveClientUrl(clientUrl, quarkusRestClientUrl),
                apiKey.orElse(null),
                null,
                objectMapper);
    }

    private MemoryServiceApiBuilder(
            MemoryServiceClientUrl clientUrl,
            String apiKey,
            String bearerToken,
            ObjectMapper objectMapper) {
        this.clientUrl = clientUrl;
        this.apiKey = apiKey;
        this.bearerToken = bearerToken;
        this.objectMapper = objectMapper;
    }

    public MemoryServiceApiBuilder withBearerAuth(String token) {
        if (token == null || token.isBlank()) {
            token = null;
        }
        return new MemoryServiceApiBuilder(clientUrl, apiKey, token, objectMapper);
    }

    public MemoryServiceApiBuilder withApiKey(String apiKey) {
        if (apiKey == null || apiKey.isBlank()) {
            apiKey = null;
        }
        return new MemoryServiceApiBuilder(clientUrl, apiKey, bearerToken, objectMapper);
    }

    public MemoryServiceApiBuilder withUrl(String url) {
        return new MemoryServiceApiBuilder(
                resolveClientUrl(Optional.ofNullable(url), Optional.empty()),
                apiKey,
                bearerToken,
                objectMapper);
    }

    public MemoryServiceApiBuilder withBaseUrl(String baseUrl) {
        return withUrl(baseUrl);
    }

    public String getBaseUrl() {
        return clientUrl.logicalBaseUrl();
    }

    public String getUrl() {
        return clientUrl.configuredUrl();
    }

    public String getApiKey() {
        return apiKey;
    }

    public boolean usesUnixSocket() {
        return clientUrl.usesUnixSocket();
    }

    public String getUnixSocketPath() {
        return clientUrl.unixSocketPath();
    }

    public <T> T build(Class<T> clazz) {
        if (clientUrl.usesUnixSocket()) {
            return UnixSocketRestClientFactory.create(
                    clazz, clientUrl.unixSocketPath(), objectMapper, apiKey, bearerToken);
        }
        RestClientBuilder builder = RestClientBuilder.newBuilder().baseUri(clientUrl.tcpUri());
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

    private static MemoryServiceClientUrl resolveClientUrl(
            Optional<String> clientUrl, Optional<String> legacyUrl) {
        return MemoryServiceClientUrl.parse(
                clientUrl.orElseGet(() -> legacyUrl.orElse("http://localhost:8080")));
    }
}
