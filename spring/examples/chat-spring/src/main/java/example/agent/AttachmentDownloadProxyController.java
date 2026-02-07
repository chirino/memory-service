package example.agent;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import java.time.Duration;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.reactive.function.client.WebClient;

/**
 * Unauthenticated proxy that forwards signed download requests to the memory-service. The signed
 * token in the URL path provides authorization — no bearer token is required.
 */
@RestController
@RequestMapping("/v1/attachments/download")
class AttachmentDownloadProxyController {

    private final MemoryServiceClientProperties properties;
    private final WebClient webClient;

    AttachmentDownloadProxyController(
            MemoryServiceClientProperties properties, WebClient.Builder webClientBuilder) {
        this.properties = properties;
        this.webClient = webClientBuilder.baseUrl(properties.getBaseUrl()).build();
    }

    @GetMapping("/{token}/{filename}")
    public ResponseEntity<?> download(@PathVariable String token, @PathVariable String filename) {
        var upstream =
                webClient
                        .get()
                        .uri("/v1/attachments/download/{token}/{filename}", token, filename)
                        .exchangeToMono(
                                response -> {
                                    HttpStatus status =
                                            HttpStatus.resolve(response.statusCode().value());

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
                                                            response
                                                                    .headers()
                                                                    .header(
                                                                            HttpHeaders
                                                                                    .CACHE_CONTROL)
                                                                    .stream()
                                                                    .findFirst()
                                                                    .ifPresent(
                                                                            cc ->
                                                                                    builder.header(
                                                                                            HttpHeaders
                                                                                                    .CACHE_CONTROL,
                                                                                            cc));
                                                            return (Object) builder.body(bytes);
                                                        });
                                    }

                                    return response.bodyToMono(String.class)
                                            .defaultIfEmpty("")
                                            .map(
                                                    body ->
                                                            (Object)
                                                                    ResponseEntity.status(
                                                                                    response
                                                                                            .statusCode())
                                                                            .body(body));
                                })
                        .block(resolveTimeout());

        return upstream != null
                ? (ResponseEntity<?>) upstream
                : ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).build();
    }

    private Duration resolveTimeout() {
        Duration configured = properties.getTimeout();
        return (configured != null) ? configured : Duration.ofSeconds(30);
    }
}
