package example;

import io.github.chirino.memory.client.api.ConversationsApi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.client.ClientRequestFilter;
import java.net.URI;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.rest.client.RestClientBuilder;

@ApplicationScoped
public final class ConversationsApiBuilder {

    private final String baseUrl;
    private final String apiKey;
    private final String bearerToken;

    @Inject
    public ConversationsApiBuilder(
            @ConfigProperty(name = "memory-service-client.url") Optional<String> clientUrl,
            @ConfigProperty(name = "memory-service.url") Optional<String> legacyUrl,
            @ConfigProperty(name = "memory-service-client.api-key") Optional<String> apiKey) {
        this(resolveBaseUrl(clientUrl, legacyUrl), apiKey.orElse(null), null);
    }

    private ConversationsApiBuilder(String baseUrl, String apiKey, String bearerToken) {
        this.baseUrl = baseUrl;
        this.apiKey = apiKey;
        this.bearerToken = bearerToken;
    }

    public ConversationsApiBuilder withBearerAuth(String token) {
        if (token == null || token.isBlank()) {
            return new ConversationsApiBuilder(baseUrl, apiKey, null);
        }
        return new ConversationsApiBuilder(baseUrl, apiKey, token);
    }

    public ConversationsApiBuilder withBaseUrl(String baseUrl) {
        if (baseUrl == null || baseUrl.isBlank()) {
            baseUrl = resolveBaseUrl(Optional.empty(), Optional.empty());
        }
        return new ConversationsApiBuilder(baseUrl, apiKey, bearerToken);
    }

    public ConversationsApi build() {
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
        return builder.build(ConversationsApi.class);
    }

    private static String resolveBaseUrl(Optional<String> clientUrl, Optional<String> legacyUrl) {
        return clientUrl.orElseGet(() -> legacyUrl.orElse("http://localhost:8080"));
    }
}
