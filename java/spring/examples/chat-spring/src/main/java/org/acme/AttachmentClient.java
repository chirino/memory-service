package org.acme;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.client.MemoryServiceClients;
import java.time.Duration;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.stereotype.Component;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.WebClient;

/**
 * Client for creating attachments on the memory-service via its REST API. Used by tools (like image
 * generation) that need to store generated content as attachments.
 */
@Component
public class AttachmentClient {

    private static final Logger LOG = LoggerFactory.getLogger(AttachmentClient.class);
    private static final long POLL_INTERVAL_MILLIS = 250;
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final MemoryServiceClientProperties properties;
    private final WebClient.Builder webClientBuilder;

    /**
     * Holds a bearer token captured on the HTTP request thread so it can be used later on background
     * threads (e.g. Reactor's boundedElastic) where SecurityContextHolder is empty.
     */
    private final AtomicReference<String> cachedBearerToken = new AtomicReference<>();

    public AttachmentClient(
            MemoryServiceClientProperties properties, WebClient.Builder webClientBuilder) {
        this.properties = properties;
        this.webClientBuilder = webClientBuilder;
    }

    /**
     * Capture the bearer token on the HTTP request thread so that background tool invocations can
     * authenticate with the memory-service.
     */
    public void setBearerToken(String token) {
        cachedBearerToken.set(token);
    }

    /**
     * Create an attachment from a source URL and wait until it is {@code ready}.
     *
     * <p>The Memory Service returns a URL attachment as {@code downloading}, and an entry can link
     * an attachment only once it is {@code ready}. Callers hand the returned ID to the history
     * advisor, so this method does not return until the download has finished.
     *
     * @param sourceUrl the URL of the content to store
     * @param contentType the MIME type
     * @param name display name for the attachment
     * @return a map with the attachment metadata (id, href, status, etc.), or null on failure, if
     *     the download fails, or if it does not finish within the configured timeout
     */
    @SuppressWarnings("unchecked")
    public Map<String, Object> createFromUrl(String sourceUrl, String contentType, String name) {
        try {
            Map<String, String> body =
                    Map.of("sourceUrl", sourceUrl, "contentType", contentType, "name", name);

            Duration timeout =
                    properties.getTimeout() != null
                            ? properties.getTimeout()
                            : Duration.ofSeconds(30);
            long deadline = System.nanoTime() + timeout.toNanos();

            WebClient webClient = createWebClient();
            WebClient.RequestBodySpec req =
                    webClient.post().uri("/v1/attachments").contentType(MediaType.APPLICATION_JSON);
            String bearer = cachedBearerToken.get();
            if (StringUtils.hasText(bearer)) {
                req = req.header(HttpHeaders.AUTHORIZATION, "Bearer " + bearer);
            }

            String responseBody =
                    req.bodyValue(body).retrieve().bodyToMono(String.class).block(timeout);
            if (responseBody == null) {
                return null;
            }
            Map<String, Object> attachment = MAPPER.readValue(responseBody, Map.class);

            while (true) {
                Object status = attachment.get("status");
                if ("ready".equals(status)) {
                    return attachment;
                }
                if (!"downloading".equals(status)) {
                    LOG.warn(
                            "Attachment {} created from URL {} ended as {}",
                            attachment.get("id"),
                            sourceUrl,
                            status);
                    return null;
                }
                Thread.sleep(POLL_INTERVAL_MILLIS);
                if (System.nanoTime() >= deadline) {
                    LOG.warn(
                            "Attachment {} created from URL {} is still downloading after {}",
                            attachment.get("id"),
                            sourceUrl,
                            timeout);
                    return null;
                }

                WebClient.RequestHeadersSpec<?> lookup =
                        webClient.get().uri("/v1/attachments/{id}/metadata", attachment.get("id"));
                if (StringUtils.hasText(bearer)) {
                    lookup = lookup.header(HttpHeaders.AUTHORIZATION, "Bearer " + bearer);
                }
                String metadata =
                        lookup.retrieve()
                                .bodyToMono(String.class)
                                .block(Duration.ofNanos(Math.max(1, deadline - System.nanoTime())));
                if (metadata == null) {
                    return null;
                }
                attachment = MAPPER.readValue(metadata, Map.class);
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return null;
        } catch (Exception e) {
            LOG.error("Error creating attachment from URL: {}", sourceUrl, e);
            return null;
        }
    }

    private WebClient createWebClient() {
        var builder = MemoryServiceClients.createWebClient(properties, webClientBuilder, null);
        builder.baseUrl(MemoryServiceClients.resolveBaseUrl(properties));
        return builder.build();
    }
}
