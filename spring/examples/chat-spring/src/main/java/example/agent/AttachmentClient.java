package example.agent;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
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

    private final WebClient webClient;
    private final OAuth2AuthorizedClientService authorizedClientService;

    public AttachmentClient(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            org.springframework.beans.factory.ObjectProvider<OAuth2AuthorizedClientService>
                    authorizedClientServiceProvider) {
        this.webClient = webClientBuilder.baseUrl(properties.getBaseUrl()).build();
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
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
            String bearer = SecurityHelper.bearerToken(authorizedClientService);

            Map<String, String> body =
                    Map.of(
                            "sourceUrl", sourceUrl,
                            "contentType", contentType,
                            "name", name);

            WebClient.RequestBodySpec req =
                    webClient.post().uri("/v1/attachments").contentType(MediaType.APPLICATION_JSON);

            if (StringUtils.hasText(bearer)) {
                req = req.header(HttpHeaders.AUTHORIZATION, "Bearer " + bearer);
            }

            String responseBody =
                    req.bodyValue(body)
                            .retrieve()
                            .bodyToMono(String.class)
                            .block(java.time.Duration.ofSeconds(30));

            if (responseBody != null) {
                return MAPPER.readValue(responseBody, Map.class);
            }
            return null;
        } catch (Exception e) {
            LOG.error("Error creating attachment from URL: {}", sourceUrl, e);
            return null;
        }
    }
}
