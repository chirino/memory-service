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
     * Create an attachment from a source URL.
     *
     * @param sourceUrl the URL of the content to store
     * @param contentType the MIME type
     * @param name display name for the attachment
     * @return a map with the attachment metadata (id, href, status, etc.) or null on failure
     */
    @SuppressWarnings("unchecked")
    public Map<String, Object> createFromUrl(String sourceUrl, String contentType, String name) {
        try {
            Map<String, String> body =
                    Map.of("sourceUrl", sourceUrl, "contentType", contentType, "name", name);

            WebClient webClient = createWebClient();
            WebClient.RequestBodySpec req =
                    webClient.post().uri("/v1/attachments").contentType(MediaType.APPLICATION_JSON);

            String bearer = cachedBearerToken.get();
            if (StringUtils.hasText(bearer)) {
                req = req.header(HttpHeaders.AUTHORIZATION, "Bearer " + bearer);
            }

            Duration timeout =
                    properties.getTimeout() != null
                            ? properties.getTimeout()
                            : Duration.ofSeconds(30);

            String responseBody =
                    req.bodyValue(body).retrieve().bodyToMono(String.class).block(timeout);

            if (responseBody != null) {
                return MAPPER.readValue(responseBody, Map.class);
            }
            return null;
        } catch (Exception e) {
            LOG.error("Error creating attachment from URL: {}", sourceUrl, e);
            return null;
        }
    }

    private WebClient createWebClient() {
        var builder = MemoryServiceClients.createWebClient(properties, webClientBuilder, null);
        builder.baseUrl(properties.getBaseUrl());
        return builder.build();
    }
}
