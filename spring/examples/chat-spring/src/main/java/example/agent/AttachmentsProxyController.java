package example.agent;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.time.Duration;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RequestPart;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.multipart.MultipartFile;
import org.springframework.web.reactive.function.BodyInserters;
import org.springframework.web.reactive.function.client.WebClient;
import reactor.core.publisher.Mono;

/**
 * Spring REST controller that proxies attachment requests to the memory-service. Uses WebClient
 * streaming to avoid buffering file content in memory.
 */
@RestController
@RequestMapping("/v1/attachments")
class AttachmentsProxyController {

    private final MemoryServiceClientProperties properties;
    private final WebClient webClient;
    private final OAuth2AuthorizedClientService authorizedClientService;

    AttachmentsProxyController(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        this.properties = properties;
        this.webClient = webClientBuilder.baseUrl(properties.getBaseUrl()).build();
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
    }

    @PostMapping(consumes = MediaType.MULTIPART_FORM_DATA_VALUE)
    public ResponseEntity<?> upload(
            @RequestPart("file") MultipartFile file,
            @RequestParam(value = "expiresIn", required = false) String expiresIn) {
        try {
            String bearer = SecurityHelper.bearerToken(authorizedClientService);

            WebClient.RequestBodySpec req =
                    webClient
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

            // Build multipart body with streaming resource
            var body =
                    BodyInserters.fromMultipartData(
                            "file",
                            new org.springframework.core.io.InputStreamResource(
                                    file.getInputStream()) {
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
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
                    .body(java.util.Map.of("error", "Upload proxy failed: " + e.getMessage()));
        }
    }

    @GetMapping("/{id}")
    public ResponseEntity<?> retrieve(@PathVariable String id) {
        String bearer = SecurityHelper.bearerToken(authorizedClientService);

        var upstream =
                webClient
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

                                    // Handle redirects
                                    if (status == HttpStatus.FOUND) {
                                        String location =
                                                response
                                                        .headers()
                                                        .header(HttpHeaders.LOCATION)
                                                        .stream()
                                                        .findFirst()
                                                        .orElse("");
                                        return Mono.just(
                                                ResponseEntity.status(HttpStatus.FOUND)
                                                        .header(HttpHeaders.LOCATION, location)
                                                        .<Object>build());
                                    }

                                    // Read bytes for success responses.
                                    // For truly large files, S3 redirect (302) is used instead,
                                    // so this path only handles small inline content.
                                    if (status == HttpStatus.OK) {
                                        return response.bodyToMono(byte[].class)
                                                .defaultIfEmpty(new byte[0])
                                                .map(
                                                        bytes -> {
                                                            var builder = ResponseEntity.ok();
                                                            response
                                                                    .headers()
                                                                    .header(
                                                                            HttpHeaders
                                                                                    .CONTENT_TYPE)
                                                                    .stream()
                                                                    .findFirst()
                                                                    .ifPresent(
                                                                            ct ->
                                                                                    builder.header(
                                                                                            HttpHeaders
                                                                                                    .CONTENT_TYPE,
                                                                                            ct));
                                                            response
                                                                    .headers()
                                                                    .header(
                                                                            HttpHeaders
                                                                                    .CONTENT_DISPOSITION)
                                                                    .stream()
                                                                    .findFirst()
                                                                    .ifPresent(
                                                                            cd ->
                                                                                    builder.header(
                                                                                            HttpHeaders
                                                                                                    .CONTENT_DISPOSITION,
                                                                                            cd));
                                                            return (Object) builder.body(bytes);
                                                        });
                                    }

                                    // Forward error responses
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

    @GetMapping("/{id}/download-url")
    public ResponseEntity<?> getDownloadUrl(@PathVariable String id) {
        String bearer = SecurityHelper.bearerToken(authorizedClientService);

        var upstream =
                webClient
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
                                                        body ->
                                                                ResponseEntity.status(
                                                                                response
                                                                                        .statusCode())
                                                                        .contentType(
                                                                                MediaType
                                                                                        .APPLICATION_JSON)
                                                                        .body((Object) body)))
                        .block(resolveTimeout());

        return upstream != null
                ? upstream
                : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
    }

    @DeleteMapping("/{id}")
    public ResponseEntity<?> delete(@PathVariable String id) {
        String bearer = SecurityHelper.bearerToken(authorizedClientService);

        var upstream =
                webClient
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
                                                    body ->
                                                            ResponseEntity.status(
                                                                            response.statusCode())
                                                                    .contentType(
                                                                            MediaType
                                                                                    .APPLICATION_JSON)
                                                                    .body((Object) body));
                                })
                        .block(resolveTimeout());

        return upstream != null
                ? upstream
                : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
    }

    private Duration resolveTimeout() {
        Duration configured = properties.getTimeout();
        return (configured != null) ? configured : Duration.ofSeconds(30);
    }
}
