package io.github.chirino.memoryservice.client;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.api.SearchApi;
import io.github.chirino.memoryservice.client.api.SharingApi;
import io.github.chirino.memoryservice.client.invoker.ApiClient;
import io.github.chirino.memoryservice.client.model.Channel;
import io.github.chirino.memoryservice.client.model.CreateOwnershipTransferRequest;
import io.github.chirino.memoryservice.client.model.SearchConversationsRequest;
import io.github.chirino.memoryservice.client.model.ShareConversationRequest;
import io.github.chirino.memoryservice.client.model.UpdateConversationMembershipRequest;
import io.github.chirino.memoryservice.client.model.UpdateConversationRequest;
import java.io.IOException;
import java.io.PipedInputStream;
import java.io.PipedOutputStream;
import java.io.UncheckedIOException;
import java.time.Duration;
import java.util.Map;
import java.util.UUID;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.core.io.InputStreamResource;
import org.springframework.core.io.buffer.DataBuffer;
import org.springframework.core.io.buffer.DataBufferUtils;
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
import org.springframework.web.multipart.MultipartFile;
import org.springframework.web.reactive.function.BodyInserters;
import org.springframework.web.reactive.function.client.ClientResponse;
import org.springframework.web.reactive.function.client.WebClient;
import org.springframework.web.reactive.function.client.WebClientResponseException;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;
import reactor.core.scheduler.Schedulers;

/**
 * Spring helper that mirrors the Quarkus {@code MemoryServiceProxy}, wrapping the generated REST
 * client so callers can easily forward user requests to the memory-service while injecting the
 * appropriate API key and bearer token.
 */
public class MemoryServiceProxy {

    private static final Logger LOG = LoggerFactory.getLogger(MemoryServiceProxy.class);
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private static UUID toUuid(String s) {
        return s == null || s.isBlank() ? null : UUID.fromString(s);
    }

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

    private WebClient createWebClient() {
        var builder = MemoryServiceClients.createWebClient(properties, webClientBuilder, null);
        builder.baseUrl(properties.getBaseUrl());
        return builder.build();
    }

    /**
     * Streams a reactive body flux into an {@link InputStreamResource} suitable for Spring MVC.
     * Uses a pipe so data flows from the WebClient's Netty I/O thread (via a bounded-elastic
     * scheduler to avoid blocking the event loop) to the servlet thread without buffering the
     * entire response in memory.
     */
    private ResponseEntity<?> streamBinaryResponse(ClientResponse response, String... headerNames) {
        try {
            PipedOutputStream pos = new PipedOutputStream();
            PipedInputStream pis = new PipedInputStream(pos, 65536);

            Flux<DataBuffer> bodyFlux = response.bodyToFlux(DataBuffer.class);
            bodyFlux.publishOn(Schedulers.boundedElastic())
                    .doOnNext(
                            dataBuffer -> {
                                try {
                                    byte[] bytes = new byte[dataBuffer.readableByteCount()];
                                    dataBuffer.read(bytes);
                                    pos.write(bytes);
                                } catch (IOException e) {
                                    throw new UncheckedIOException(e);
                                } finally {
                                    DataBufferUtils.release(dataBuffer);
                                }
                            })
                    .doFinally(
                            signal -> {
                                try {
                                    pos.close();
                                } catch (IOException ignored) {
                                }
                            })
                    .subscribe();

            var builder = ResponseEntity.ok();
            for (String name : headerNames) {
                response.headers().header(name).stream()
                        .findFirst()
                        .ifPresent(value -> builder.header(name, value));
            }
            return builder.body(new InputStreamResource(pis));
        } catch (IOException e) {
            LOG.error("Failed to create streaming pipe", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
        }
    }

    public ResponseEntity<?> listConversations(
            String mode, String after, Integer limit, String query) {
        return execute(
                api -> api.listConversationsWithHttpInfo(mode, toUuid(after), limit, query),
                HttpStatus.OK);
    }

    public ResponseEntity<?> getConversation(String conversationId) {
        return execute(
                api -> api.getConversationWithHttpInfo(toUuid(conversationId)), HttpStatus.OK);
    }

    public ResponseEntity<?> updateConversation(String conversationId, String body) {
        try {
            UpdateConversationRequest request =
                    OBJECT_MAPPER.readValue(body, UpdateConversationRequest.class);
            return execute(
                    api -> api.updateConversationWithHttpInfo(toUuid(conversationId), request),
                    HttpStatus.OK);
        } catch (Exception e) {
            LOG.error("Error parsing update conversation request body", e);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .body(Map.of("error", "Invalid request body"));
        }
    }

    public ResponseEntity<?> deleteConversation(String conversationId) {
        return execute(
                api -> api.deleteConversationWithHttpInfo(toUuid(conversationId)),
                HttpStatus.NO_CONTENT);
    }

    public ResponseEntity<?> listConversationEntries(
            String conversationId,
            String after,
            Integer limit,
            Channel channel,
            String epoch,
            String forks) {
        return execute(
                api ->
                        api.listConversationEntriesWithHttpInfo(
                                toUuid(conversationId),
                                toUuid(after),
                                limit,
                                channel,
                                epoch,
                                forks),
                HttpStatus.OK);
    }

    public ResponseEntity<?> listConversationForks(
            String conversationId, String afterCursor, Integer limit) {
        return execute(
                api ->
                        api.listConversationForksWithHttpInfo(
                                toUuid(conversationId), toUuid(afterCursor), limit),
                HttpStatus.OK);
    }

    public ResponseEntity<?> shareConversation(String conversationId, String body) {
        try {
            ShareConversationRequest request =
                    OBJECT_MAPPER.readValue(body, ShareConversationRequest.class);
            return executeSharingApi(
                    api -> api.shareConversationWithHttpInfo(toUuid(conversationId), request),
                    HttpStatus.CREATED);
        } catch (Exception e) {
            LOG.error("Error parsing share request body", e);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .body(Map.of("error", "Invalid request body"));
        }
    }

    public ResponseEntity<?> cancelResponse(String conversationId) {
        return execute(
                api -> api.deleteConversationResponseWithHttpInfo(toUuid(conversationId)),
                HttpStatus.OK);
    }

    public ResponseEntity<?> listConversationMemberships(
            String conversationId, String afterCursor, Integer limit) {
        return executeSharingApi(
                api ->
                        api.listConversationMembershipsWithHttpInfo(
                                toUuid(conversationId), afterCursor, limit),
                HttpStatus.OK);
    }

    public ResponseEntity<?> updateConversationMembership(
            String conversationId, String userId, String body) {
        try {
            UpdateConversationMembershipRequest request =
                    OBJECT_MAPPER.readValue(body, UpdateConversationMembershipRequest.class);
            return executeSharingApi(
                    api ->
                            api.updateConversationMembershipWithHttpInfo(
                                    toUuid(conversationId), userId, request),
                    HttpStatus.OK);
        } catch (Exception e) {
            LOG.error("Error parsing update membership request body", e);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .body(Map.of("error", "Invalid request body"));
        }
    }

    public ResponseEntity<?> deleteConversationMembership(String conversationId, String userId) {
        return executeSharingApiVoid(
                api -> api.deleteConversationMembershipWithHttpInfo(toUuid(conversationId), userId),
                HttpStatus.NO_CONTENT);
    }

    public ResponseEntity<?> listPendingTransfers(String role, String afterCursor, Integer limit) {
        return executeSharingApi(
                api -> api.listPendingTransfersWithHttpInfo(role, toUuid(afterCursor), limit),
                HttpStatus.OK);
    }

    public ResponseEntity<?> createOwnershipTransfer(String body) {
        try {
            CreateOwnershipTransferRequest request =
                    OBJECT_MAPPER.readValue(body, CreateOwnershipTransferRequest.class);
            return executeSharingApi(
                    api -> api.createOwnershipTransferWithHttpInfo(request), HttpStatus.CREATED);
        } catch (Exception e) {
            LOG.error("Error parsing create transfer request body", e);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .body(Map.of("error", "Invalid request body"));
        }
    }

    public ResponseEntity<?> getTransfer(String transferId) {
        return executeSharingApi(
                api -> api.getTransferWithHttpInfo(toUuid(transferId)), HttpStatus.OK);
    }

    public ResponseEntity<?> acceptTransfer(String transferId) {
        return executeSharingApi(
                api -> api.acceptTransferWithHttpInfo(toUuid(transferId)), HttpStatus.OK);
    }

    public ResponseEntity<?> deleteTransfer(String transferId) {
        return executeSharingApiVoid(
                api -> api.deleteTransferWithHttpInfo(toUuid(transferId)), HttpStatus.NO_CONTENT);
    }

    public ResponseEntity<?> searchConversations(String body) {
        try {
            SearchConversationsRequest request =
                    OBJECT_MAPPER.readValue(body, SearchConversationsRequest.class);
            return executeSearchApi(
                    api -> api.searchConversationsWithHttpInfo(request), HttpStatus.OK);
        } catch (Exception e) {
            LOG.error("Error parsing search request body", e);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .body(Map.of("error", "Invalid request body"));
        }
    }

    // ---- Attachment operations (raw HTTP, no generated client) ----

    /**
     * Creates an attachment from a source URL by forwarding the JSON request to the memory service.
     */
    public ResponseEntity<?> createAttachmentFromUrl(Map<String, Object> request) {
        try {
            String bearer = resolveBearerToken(null);

            WebClient.RequestBodySpec req =
                    createWebClient()
                            .post()
                            .uri("/v1/attachments")
                            .contentType(MediaType.APPLICATION_JSON);

            if (StringUtils.hasText(bearer)) {
                req = req.header(HttpHeaders.AUTHORIZATION, "Bearer " + bearer);
            }

            var upstream =
                    req.bodyValue(request)
                            .exchangeToMono(
                                    response ->
                                            response.bodyToMono(String.class)
                                                    .defaultIfEmpty("")
                                                    .map(
                                                            responseBody ->
                                                                    ResponseEntity.status(
                                                                                    response
                                                                                            .statusCode())
                                                                            .contentType(
                                                                                    MediaType
                                                                                            .APPLICATION_JSON)
                                                                            .body(
                                                                                    (Object)
                                                                                            responseBody)))
                            .block(resolveTimeout());

            return upstream != null
                    ? upstream
                    : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
        } catch (Exception e) {
            LOG.error("Error creating attachment from URL", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
                    .body(Map.of("error", "Create from URL proxy failed: " + e.getMessage()));
        }
    }

    /**
     * Uploads an attachment to the memory service using streaming multipart.
     *
     * @param file      the multipart file from the request
     * @param expiresIn optional expiration duration (e.g. "300s")
     * @return the upstream JSON response with attachment metadata
     */
    public ResponseEntity<?> uploadAttachment(MultipartFile file, String expiresIn) {
        try {
            String bearer = resolveBearerToken(null);
            WebClient.RequestBodySpec req =
                    createWebClient()
                            .post()
                            .uri(
                                    uriBuilder -> {
                                        uriBuilder.path("/v1/attachments");
                                        if (StringUtils.hasText(expiresIn)) {
                                            uriBuilder.queryParam("expiresIn", expiresIn);
                                        }
                                        return uriBuilder.build();
                                    })
                            .contentType(MediaType.MULTIPART_FORM_DATA);

            if (StringUtils.hasText(bearer)) {
                req = req.header(HttpHeaders.AUTHORIZATION, "Bearer " + bearer);
            }

            var body =
                    BodyInserters.fromMultipartData(
                            "file",
                            new InputStreamResource(file.getInputStream()) {
                                @Override
                                public String getFilename() {
                                    return file.getOriginalFilename();
                                }

                                @Override
                                public long contentLength() {
                                    return file.getSize();
                                }
                            });

            var upstream =
                    req.body(body)
                            .exchangeToMono(
                                    response ->
                                            response.bodyToMono(String.class)
                                                    .defaultIfEmpty("")
                                                    .map(
                                                            responseBody ->
                                                                    ResponseEntity.status(
                                                                                    response
                                                                                            .statusCode())
                                                                            .contentType(
                                                                                    MediaType
                                                                                            .APPLICATION_JSON)
                                                                            .body(
                                                                                    (Object)
                                                                                            responseBody)))
                            .block(resolveTimeout());

            return upstream != null
                    ? upstream
                    : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();

        } catch (Exception e) {
            LOG.error("Error uploading attachment", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
                    .body(Map.of("error", "Upload proxy failed: " + e.getMessage()));
        }
    }

    /**
     * Retrieves an attachment by ID. Handles 302 redirects (e.g. S3 presigned URLs).
     * Binary content is streamed through a pipe without buffering in memory.
     */
    public ResponseEntity<?> retrieveAttachment(String id) {
        String bearer = resolveBearerToken(null);

        var upstream =
                createWebClient()
                        .get()
                        .uri("/v1/attachments/{id}", id)
                        .headers(
                                h -> {
                                    if (StringUtils.hasText(bearer)) {
                                        h.setBearerAuth(bearer);
                                    }
                                })
                        .exchangeToMono(
                                response -> {
                                    HttpStatus status =
                                            HttpStatus.resolve(response.statusCode().value());

                                    if (status == HttpStatus.FOUND) {
                                        String location =
                                                response
                                                        .headers()
                                                        .header(HttpHeaders.LOCATION)
                                                        .stream()
                                                        .findFirst()
                                                        .orElse("");
                                        return response.releaseBody()
                                                .thenReturn(
                                                        (Object)
                                                                ResponseEntity.status(
                                                                                HttpStatus.FOUND)
                                                                        .header(
                                                                                HttpHeaders
                                                                                        .LOCATION,
                                                                                location)
                                                                        .build());
                                    }

                                    if (status == HttpStatus.OK) {
                                        return Mono.just(
                                                (Object)
                                                        streamBinaryResponse(
                                                                response,
                                                                HttpHeaders.CONTENT_TYPE,
                                                                HttpHeaders.CONTENT_DISPOSITION));
                                    }

                                    return response.bodyToMono(String.class)
                                            .defaultIfEmpty("")
                                            .map(
                                                    errorBody ->
                                                            (Object)
                                                                    ResponseEntity.status(
                                                                                    response
                                                                                            .statusCode())
                                                                            .contentType(
                                                                                    MediaType
                                                                                            .APPLICATION_JSON)
                                                                            .body(errorBody));
                                })
                        .block(resolveTimeout());

        return upstream != null
                ? (ResponseEntity<?>) upstream
                : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
    }

    /**
     * Gets a signed download URL for an attachment.
     */
    public ResponseEntity<?> getAttachmentDownloadUrl(String id) {
        String bearer = resolveBearerToken(null);

        var upstream =
                createWebClient()
                        .get()
                        .uri("/v1/attachments/{id}/download-url", id)
                        .headers(
                                h -> {
                                    if (StringUtils.hasText(bearer)) {
                                        h.setBearerAuth(bearer);
                                    }
                                })
                        .exchangeToMono(
                                response ->
                                        response.bodyToMono(String.class)
                                                .defaultIfEmpty("")
                                                .map(
                                                        responseBody ->
                                                                ResponseEntity.status(
                                                                                response
                                                                                        .statusCode())
                                                                        .contentType(
                                                                                MediaType
                                                                                        .APPLICATION_JSON)
                                                                        .body(
                                                                                (Object)
                                                                                        responseBody)))
                        .block(resolveTimeout());

        return upstream != null
                ? upstream
                : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
    }

    /**
     * Deletes an attachment by ID.
     */
    public ResponseEntity<?> deleteAttachment(String id) {
        String bearer = resolveBearerToken(null);

        var upstream =
                createWebClient()
                        .delete()
                        .uri("/v1/attachments/{id}", id)
                        .headers(
                                h -> {
                                    if (StringUtils.hasText(bearer)) {
                                        h.setBearerAuth(bearer);
                                    }
                                })
                        .exchangeToMono(
                                response -> {
                                    if (response.statusCode().value() == 204) {
                                        return Mono.just(
                                                ResponseEntity.noContent().<Object>build());
                                    }
                                    return response.bodyToMono(String.class)
                                            .defaultIfEmpty("")
                                            .map(
                                                    responseBody ->
                                                            ResponseEntity.status(
                                                                            response.statusCode())
                                                                    .contentType(
                                                                            MediaType
                                                                                    .APPLICATION_JSON)
                                                                    .body((Object) responseBody));
                                })
                        .block(resolveTimeout());

        return upstream != null
                ? upstream
                : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
    }

    /**
     * Downloads an attachment using a signed token. No authentication required.
     * Binary content is streamed through a pipe without buffering in memory.
     */
    public ResponseEntity<?> downloadAttachmentByToken(String token, String filename) {
        var upstream =
                createWebClient()
                        .get()
                        .uri("/v1/attachments/download/{token}/{filename}", token, filename)
                        .exchangeToMono(
                                response -> {
                                    HttpStatus status =
                                            HttpStatus.resolve(response.statusCode().value());

                                    if (status == HttpStatus.OK) {
                                        return Mono.just(
                                                (Object)
                                                        streamBinaryResponse(
                                                                response,
                                                                HttpHeaders.CONTENT_TYPE,
                                                                HttpHeaders.CONTENT_DISPOSITION,
                                                                HttpHeaders.CACHE_CONTROL));
                                    }

                                    return response.bodyToMono(String.class)
                                            .defaultIfEmpty("")
                                            .map(
                                                    errorBody ->
                                                            (Object)
                                                                    ResponseEntity.status(
                                                                                    response
                                                                                            .statusCode())
                                                                            .body(errorBody));
                                })
                        .block(resolveTimeout());

        return upstream != null
                ? (ResponseEntity<?>) upstream
                : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
    }

    private ConversationsApi conversationsApi(@Nullable String explicitBearerToken) {
        return new ConversationsApi(createConfiguredApiClient(explicitBearerToken));
    }

    private SharingApi sharingApi(@Nullable String explicitBearerToken) {
        return new SharingApi(createConfiguredApiClient(explicitBearerToken));
    }

    private SearchApi searchApi(@Nullable String explicitBearerToken) {
        return new SearchApi(createConfiguredApiClient(explicitBearerToken));
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
            LOG.warn(
                    "memory-service call failed: {} {}",
                    e.getStatusCode(),
                    e.getResponseBodyAsString());
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
            LOG.warn(
                    "memory-service call failed: {} {}",
                    e.getStatusCode(),
                    e.getResponseBodyAsString());
            return ResponseEntity.status(e.getStatusCode()).body(e.getResponseBodyAsString());
        } catch (Exception e) {
            LOG.error("Unexpected error calling memory-service", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
                    .body(Map.of("error", "Internal server error"));
        }
    }

    private ResponseEntity<?> executeSharingApiVoid(
            ThrowingFunction<SharingApi, Mono<ResponseEntity<Void>>> action,
            HttpStatus expectedStatus) {
        try {
            ResponseEntity<Void> upstream = action.apply(sharingApi(null)).block(resolveTimeout());
            if (upstream == null) {
                return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
            }
            return ResponseEntity.status(upstream.getStatusCode()).build();
        } catch (WebClientResponseException e) {
            LOG.warn(
                    "memory-service call failed: {} {}",
                    e.getStatusCode(),
                    e.getResponseBodyAsString());
            return ResponseEntity.status(e.getStatusCode()).body(e.getResponseBodyAsString());
        } catch (Exception e) {
            LOG.error("Unexpected error calling memory-service", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
                    .body(Map.of("error", "Internal server error"));
        }
    }

    private <T> ResponseEntity<?> executeSearchApi(
            ThrowingFunction<SearchApi, Mono<ResponseEntity<T>>> action,
            HttpStatus expectedStatus) {
        try {
            ResponseEntity<T> upstream = action.apply(searchApi(null)).block(resolveTimeout());
            return handleUpstreamResponse(upstream);
        } catch (WebClientResponseException e) {
            LOG.warn(
                    "memory-service call failed: {} {}",
                    e.getStatusCode(),
                    e.getResponseBodyAsString());
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
