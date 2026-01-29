package io.github.chirino.memoryservice.client;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.api.SharingApi;
import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.github.chirino.memoryservice.client.model.Channel;
import io.github.chirino.memoryservice.client.model.ForkFromEntryRequest;
import io.github.chirino.memoryservice.client.model.ShareConversationRequest;
import io.github.chirino.memoryservice.client.model.TransferConversationOwnershipRequest;
import java.time.Duration;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.lang.Nullable;
import org.springframework.security.core.Authentication;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClient;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.security.oauth2.client.authentication.OAuth2AuthenticationToken;
import org.springframework.security.oauth2.core.AbstractOAuth2Token;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.WebClient;
import org.springframework.web.reactive.function.client.WebClientResponseException;
import reactor.core.publisher.Mono;

/**
 * Spring helper that mirrors the Quarkus {@code MemoryServiceProxy}, wrapping the generated REST
 * client so callers can easily forward user requests to the memory-service while injecting the
 * appropriate API key and bearer token.
 */
public class MemoryServiceProxy {

    private static final Logger LOG = LoggerFactory.getLogger(MemoryServiceProxy.class);
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private final MemoryServiceClientProperties properties;
    private final WebClient.Builder webClientBuilder;
    private final OAuth2AuthorizedClientService authorizedClientService;

    public MemoryServiceProxy(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            @Nullable OAuth2AuthorizedClientService authorizedClientService) {
        this.properties = properties;
        this.webClientBuilder = (webClientBuilder != null) ? webClientBuilder : WebClient.builder();
        this.authorizedClientService = authorizedClientService;
    }

    public ResponseEntity<?> listConversations(
            String mode, String after, Integer limit, String query) {
        return execute(
                api -> api.listConversationsWithHttpInfo(mode, after, limit, query), HttpStatus.OK);
    }

    public ResponseEntity<?> getConversation(String conversationId) {
        return execute(api -> api.getConversationWithHttpInfo(conversationId), HttpStatus.OK);
    }

    public ResponseEntity<?> deleteConversation(String conversationId) {
        return execute(
                api -> api.deleteConversationWithHttpInfo(conversationId), HttpStatus.NO_CONTENT);
    }

    public ResponseEntity<?> listConversationEntries(
            String conversationId, String after, Integer limit) {
        return execute(
                api ->
                        api.listConversationEntriesWithHttpInfo(
                                conversationId, after, limit, Channel.HISTORY, null),
                HttpStatus.OK);
    }

    public ResponseEntity<?> listConversationForks(String conversationId) {
        return execute(api -> api.listConversationForksWithHttpInfo(conversationId), HttpStatus.OK);
    }

    public ResponseEntity<?> forkConversationAtEntry(
            String conversationId, String entryId, String body) {
        try {
            ForkFromEntryRequest request =
                    StringUtils.hasText(body)
                            ? OBJECT_MAPPER.readValue(body, ForkFromEntryRequest.class)
                            : new ForkFromEntryRequest();
            return execute(
                    api ->
                            api.forkConversationAtEntryWithHttpInfo(
                                    conversationId, entryId, request),
                    HttpStatus.OK);
        } catch (Exception e) {
            LOG.error("Error parsing fork request body", e);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .body(Map.of("error", "Invalid request body"));
        }
    }

    public ResponseEntity<?> shareConversation(String conversationId, String body) {
        try {
            ShareConversationRequest request =
                    OBJECT_MAPPER.readValue(body, ShareConversationRequest.class);
            return execute(
                    api -> api.shareConversationWithHttpInfo(conversationId, request),
                    HttpStatus.CREATED);
        } catch (Exception e) {
            LOG.error("Error parsing share request body", e);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .body(Map.of("error", "Invalid request body"));
        }
    }

    public ResponseEntity<?> cancelResponse(String conversationId) {
        return execute(
                api -> api.deleteConversationResponseWithHttpInfo(conversationId), HttpStatus.OK);
    }

    public ResponseEntity<?> transferConversationOwnership(String conversationId, String body) {
        try {
            TransferConversationOwnershipRequest request =
                    OBJECT_MAPPER.readValue(body, TransferConversationOwnershipRequest.class);
            return executeSharingApi(
                    api -> api.transferConversationOwnershipWithHttpInfo(conversationId, request),
                    HttpStatus.ACCEPTED);
        } catch (Exception e) {
            LOG.error("Error parsing transfer ownership request", e);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .body(Map.of("error", "Invalid request body"));
        }
    }

    private ConversationsApi conversationsApi(@Nullable String explicitBearerToken) {
        return new ConversationsApi(createConfiguredApiClient(explicitBearerToken));
    }

    private SharingApi sharingApi(@Nullable String explicitBearerToken) {
        return new SharingApi(createConfiguredApiClient(explicitBearerToken));
    }

    private ApiClient createConfiguredApiClient(@Nullable String explicitBearerToken) {
        ApiClient apiClient =
                MemoryServiceClients.createApiClient(properties, webClientBuilder, null);
        String bearer = resolveBearerToken(explicitBearerToken);
        if (StringUtils.hasText(bearer)) {
            apiClient.setBearerToken(bearer);
        } else if (StringUtils.hasText(properties.getBearerToken())) {
            apiClient.setBearerToken(properties.getBearerToken());
        }
        return apiClient;
    }

    private <T> ResponseEntity<?> execute(
            ThrowingFunction<ConversationsApi, Mono<ResponseEntity<T>>> action,
            HttpStatus expectedStatus) {
        try {
            ResponseEntity<T> upstream =
                    action.apply(conversationsApi(null)).block(resolveTimeout());
            return handleUpstreamResponse(upstream);
        } catch (WebClientResponseException e) {
            LOG.warn("memory-service call failed: {}", e.getMessage());
            return ResponseEntity.status(e.getStatusCode()).body(e.getResponseBodyAsString());
        } catch (Exception e) {
            LOG.error("Unexpected error calling memory-service", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
                    .body(Map.of("error", "Internal server error"));
        }
    }

    private <T> ResponseEntity<?> executeSharingApi(
            ThrowingFunction<SharingApi, Mono<ResponseEntity<T>>> action,
            HttpStatus expectedStatus) {
        try {
            ResponseEntity<T> upstream = action.apply(sharingApi(null)).block(resolveTimeout());
            return handleUpstreamResponse(upstream);
        } catch (WebClientResponseException e) {
            LOG.warn("memory-service call failed: {}", e.getMessage());
            return ResponseEntity.status(e.getStatusCode()).body(e.getResponseBodyAsString());
        } catch (Exception e) {
            LOG.error("Unexpected error calling memory-service", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
                    .body(Map.of("error", "Internal server error"));
        }
    }

    private <T> ResponseEntity<?> handleUpstreamResponse(ResponseEntity<T> upstream) {
        if (upstream == null) {
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
        }
        HttpHeaders headers = new HttpHeaders();
        upstream.getHeaders()
                .forEach(
                        (name, values) -> {
                            // Don't forward Content-Length or Transfer-Encoding since we're
                            // re-serializing the body
                            if (!name.equalsIgnoreCase(HttpHeaders.CONTENT_LENGTH)
                                    && !name.equalsIgnoreCase(HttpHeaders.TRANSFER_ENCODING)) {
                                headers.addAll(name, values);
                            }
                        });
        return ResponseEntity.status(upstream.getStatusCode())
                .contentType(MediaType.APPLICATION_JSON)
                .headers(headers)
                .body(upstream.getBody());
    }

    private Duration resolveTimeout() {
        Duration configured = properties.getTimeout();
        return (configured != null) ? configured : Duration.ofSeconds(30);
    }

    private String resolveBearerToken(@Nullable String explicitToken) {
        if (StringUtils.hasText(explicitToken)) {
            return explicitToken;
        }

        Authentication authentication = SecurityContextHolder.getContext().getAuthentication();
        if (authentication == null) {
            return null;
        }

        if (authentication instanceof OAuth2AuthenticationToken oauth2
                && authorizedClientService != null) {
            OAuth2AuthorizedClient client =
                    authorizedClientService.loadAuthorizedClient(
                            oauth2.getAuthorizedClientRegistrationId(), oauth2.getName());
            if (client != null && client.getAccessToken() != null) {
                return client.getAccessToken().getTokenValue();
            }
        }

        Object credentials = authentication.getCredentials();
        if (credentials instanceof AbstractOAuth2Token token) {
            return token.getTokenValue();
        }
        if (credentials instanceof String token && StringUtils.hasText(token)) {
            return token;
        }

        return null;
    }

    @FunctionalInterface
    private interface ThrowingFunction<T, R> {
        R apply(T t) throws Exception;
    }
}
