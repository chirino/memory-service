package example;

import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import org.jboss.logging.Logger;

/**
 * Client for creating attachments on the memory-service via its REST API. Used by tools (like image
 * generation) that need to store generated content as attachments.
 */
@ApplicationScoped
public class AttachmentClient {

    private static final Logger LOG = Logger.getLogger(AttachmentClient.class);

    @Inject MemoryServiceApiBuilder apiBuilder;

    @Inject SecurityIdentity securityIdentity;

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
            String url = apiBuilder.getBaseUrl() + "/v1/attachments";
            String jsonBody =
                    "{\"sourceUrl\":\""
                            + escapeJson(sourceUrl)
                            + "\",\"contentType\":\""
                            + escapeJson(contentType)
                            + "\",\"name\":\""
                            + escapeJson(name)
                            + "\"}";

            HttpURLConnection conn = (HttpURLConnection) URI.create(url).toURL().openConnection();
            conn.setDoOutput(true);
            conn.setRequestMethod("POST");
            conn.setRequestProperty("Content-Type", "application/json");
            conn.setConnectTimeout(30_000);
            conn.setReadTimeout(30_000);

            String bearer =
                    io.github.chirino.memory.security.SecurityHelper.bearerToken(securityIdentity);
            if (bearer != null) {
                conn.setRequestProperty("Authorization", "Bearer " + bearer);
            }

            try (OutputStream out = conn.getOutputStream()) {
                out.write(jsonBody.getBytes(StandardCharsets.UTF_8));
                out.flush();
            }

            int status = conn.getResponseCode();
            String responseBody;
            try (InputStream respStream =
                    status >= 400 ? conn.getErrorStream() : conn.getInputStream()) {
                responseBody =
                        respStream != null
                                ? new String(respStream.readAllBytes(), StandardCharsets.UTF_8)
                                : "";
            }
            conn.disconnect();

            if (status == 201) {
                var mapper = new com.fasterxml.jackson.databind.ObjectMapper();
                return mapper.readValue(responseBody, Map.class);
            } else {
                LOG.warnf(
                        "Failed to create attachment from URL: HTTP %d - %s", status, responseBody);
                return null;
            }
        } catch (Exception e) {
            LOG.errorf(e, "Error creating attachment from URL: %s", sourceUrl);
            return null;
        }
    }

    private static String escapeJson(String value) {
        if (value == null) {
            return "";
        }
        return value.replace("\\", "\\\\").replace("\"", "\\\"");
    }
}
