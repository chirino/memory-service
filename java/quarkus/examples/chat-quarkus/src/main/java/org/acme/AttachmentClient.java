package org.acme;

import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.github.chirino.memory.runtime.UnixSocketHttpClient;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.io.ByteArrayOutputStream;
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
            if (apiBuilder.usesUnixSocket()) {
                UnixSocketHttpClient.HttpResponseData response =
                        new UnixSocketHttpClient(
                                        apiBuilder.getUnixSocketPath(),
                                        new com.fasterxml.jackson.databind.ObjectMapper(),
                                        apiBuilder.getApiKey(),
                                        io.github.chirino.memory.security.SecurityHelper
                                                .bearerToken(securityIdentity))
                                .exchange(
                                        "POST",
                                        "/v1/attachments",
                                        null,
                                        Map.of(
                                                "sourceUrl", sourceUrl,
                                                "contentType", contentType,
                                                "name", name));
                if (response.statusCode() == 201) {
                    var mapper = new com.fasterxml.jackson.databind.ObjectMapper();
                    return mapper.readValue(response.body(), Map.class);
                }
                LOG.warnf(
                        "Failed to create attachment from URL: HTTP %d - %s",
                        response.statusCode(), new String(response.body(), StandardCharsets.UTF_8));
                return null;
            }
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

    /**
     * Upload an attachment from in-memory content.
     *
     * @param content the attachment bytes
     * @param contentType the MIME type
     * @param name display name for the attachment
     * @return a map with the attachment metadata (id, href, status, etc.) or null on failure
     */
    @SuppressWarnings("unchecked")
    public Map<String, Object> upload(byte[] content, String contentType, String name) {
        try {
            String boundary =
                    "----Boundary" + java.util.UUID.randomUUID().toString().replace("-", "");
            byte[] body = multipartBody(content, contentType, name, boundary);
            String multipartContentType = "multipart/form-data; boundary=" + boundary;

            if (apiBuilder.usesUnixSocket()) {
                UnixSocketHttpClient.HttpResponseData response =
                        new UnixSocketHttpClient(
                                        apiBuilder.getUnixSocketPath(),
                                        new com.fasterxml.jackson.databind.ObjectMapper(),
                                        apiBuilder.getApiKey(),
                                        io.github.chirino.memory.security.SecurityHelper
                                                .bearerToken(securityIdentity))
                                .exchange(
                                        "POST",
                                        "/v1/attachments",
                                        null,
                                        body,
                                        multipartContentType,
                                        Map.of());
                if (response.statusCode() == 201) {
                    var mapper = new com.fasterxml.jackson.databind.ObjectMapper();
                    return mapper.readValue(response.body(), Map.class);
                }
                LOG.warnf(
                        "Failed to upload attachment: HTTP %d - %s",
                        response.statusCode(), new String(response.body(), StandardCharsets.UTF_8));
                return null;
            }

            HttpURLConnection conn =
                    (HttpURLConnection)
                            URI.create(apiBuilder.getBaseUrl() + "/v1/attachments")
                                    .toURL()
                                    .openConnection();
            conn.setDoOutput(true);
            conn.setRequestMethod("POST");
            conn.setRequestProperty("Content-Type", multipartContentType);
            conn.setConnectTimeout(30_000);
            conn.setReadTimeout(30_000);

            String bearer =
                    io.github.chirino.memory.security.SecurityHelper.bearerToken(securityIdentity);
            if (bearer != null) {
                conn.setRequestProperty("Authorization", "Bearer " + bearer);
            }

            try (OutputStream out = conn.getOutputStream()) {
                out.write(body);
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
            }
            LOG.warnf("Failed to upload attachment: HTTP %d - %s", status, responseBody);
            return null;
        } catch (Exception e) {
            LOG.errorf(e, "Error uploading attachment: %s", name);
            return null;
        }
    }

    private static byte[] multipartBody(
            byte[] content, String contentType, String name, String boundary)
            throws java.io.IOException {
        ByteArrayOutputStream body = new ByteArrayOutputStream();
        body.write(
                ("--"
                                + boundary
                                + "\r\n"
                                + "Content-Disposition: form-data; name=\"file\"; filename=\""
                                + escapeMultipartFilename(name)
                                + "\"\r\n"
                                + "Content-Type: "
                                + (contentType == null || contentType.isBlank()
                                        ? "application/octet-stream"
                                        : contentType)
                                + "\r\n\r\n")
                        .getBytes(StandardCharsets.UTF_8));
        body.write(content);
        body.write(("\r\n--" + boundary + "--\r\n").getBytes(StandardCharsets.UTF_8));
        return body.toByteArray();
    }

    private static String escapeMultipartFilename(String value) {
        if (value == null || value.isBlank()) {
            return "upload";
        }
        return value.replace("\\", "\\\\")
                .replace("\"", "\\\"")
                .replace("\r", "")
                .replace("\n", "");
    }

    private static String escapeJson(String value) {
        if (value == null) {
            return "";
        }
        return value.replace("\\", "\\\\").replace("\"", "\\\"");
    }
}
