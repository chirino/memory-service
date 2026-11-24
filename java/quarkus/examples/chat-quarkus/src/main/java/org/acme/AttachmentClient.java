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

    /** How long {@link #createFromUrl} waits for the download to finish. Visible for tests. */
    long readyTimeoutMillis = 30_000;

    private static final long POLL_INTERVAL_MILLIS = 250;

    /**
     * Create an attachment from a source URL and wait until it is {@code ready}.
     *
     * <p>The Memory Service returns a URL attachment as {@code downloading}, and an entry can link
     * an attachment only once it is {@code ready}. Callers hand the returned ID to the history
     * recorder, so this method does not return until the download has finished.
     *
     * @param sourceUrl the URL of the content to store
     * @param contentType the MIME type
     * @param name display name for the attachment
     * @return a map with the attachment metadata (id, href, status, etc.), or null on failure, if
     *     the download fails, or if it does not finish within the timeout
     */
    @SuppressWarnings("unchecked")
    public Map<String, Object> createFromUrl(String sourceUrl, String contentType, String name) {
        try {
            long deadline = System.nanoTime() + readyTimeoutMillis * 1_000_000L;
            var mapper = new com.fasterxml.jackson.databind.ObjectMapper();

            UnixSocketHttpClient.HttpResponseData response =
                    send(
                            "POST",
                            "/v1/attachments",
                            mapper.writeValueAsBytes(
                                    Map.of(
                                            "sourceUrl", sourceUrl,
                                            "contentType", contentType,
                                            "name", name)));
            if (response.statusCode() != 201) {
                LOG.warnf(
                        "Failed to create attachment from URL: HTTP %d - %s",
                        response.statusCode(), new String(response.body(), StandardCharsets.UTF_8));
                return null;
            }
            Map<String, Object> attachment = mapper.readValue(response.body(), Map.class);

            while (true) {
                Object status = attachment.get("status");
                if ("ready".equals(status)) {
                    return attachment;
                }
                if (!"downloading".equals(status)) {
                    LOG.warnf(
                            "Attachment %s created from URL %s ended as %s",
                            attachment.get("id"), sourceUrl, status);
                    return null;
                }
                Thread.sleep(POLL_INTERVAL_MILLIS);
                if (System.nanoTime() >= deadline) {
                    LOG.warnf(
                            "Attachment %s created from URL %s is still downloading after %d ms",
                            attachment.get("id"), sourceUrl, readyTimeoutMillis);
                    return null;
                }
                response =
                        send("GET", "/v1/attachments/" + attachment.get("id") + "/metadata", null);
                if (response.statusCode() != 200) {
                    LOG.warnf(
                            "Failed to look up attachment %s: HTTP %d - %s",
                            attachment.get("id"),
                            response.statusCode(),
                            new String(response.body(), StandardCharsets.UTF_8));
                    return null;
                }
                attachment = mapper.readValue(response.body(), Map.class);
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return null;
        } catch (Exception e) {
            LOG.errorf(e, "Error creating attachment from URL: %s", sourceUrl);
            return null;
        }
    }

    /** Sends a JSON request to the Memory Service over its Unix socket or HTTP endpoint. */
    private UnixSocketHttpClient.HttpResponseData send(String method, String path, byte[] jsonBody)
            throws java.io.IOException {
        String bearer =
                io.github.chirino.memory.security.SecurityHelper.bearerToken(securityIdentity);
        if (apiBuilder.usesUnixSocket()) {
            return new UnixSocketHttpClient(
                            apiBuilder.getUnixSocketPath(),
                            new com.fasterxml.jackson.databind.ObjectMapper(),
                            apiBuilder.getApiKey(),
                            bearer)
                    .exchange(
                            method,
                            path,
                            null,
                            jsonBody,
                            jsonBody == null ? null : "application/json",
                            Map.of());
        }

        HttpURLConnection conn =
                (HttpURLConnection)
                        URI.create(apiBuilder.getBaseUrl() + path).toURL().openConnection();
        try {
            conn.setRequestMethod(method);
            conn.setConnectTimeout(30_000);
            conn.setReadTimeout(30_000);
            if (bearer != null) {
                conn.setRequestProperty("Authorization", "Bearer " + bearer);
            }
            if (jsonBody != null) {
                conn.setDoOutput(true);
                conn.setRequestProperty("Content-Type", "application/json");
                try (OutputStream out = conn.getOutputStream()) {
                    out.write(jsonBody);
                    out.flush();
                }
            }
            int status = conn.getResponseCode();
            try (InputStream respStream =
                    status >= 400 ? conn.getErrorStream() : conn.getInputStream()) {
                return new UnixSocketHttpClient.HttpResponseData(
                        status,
                        Map.of(),
                        respStream != null ? respStream.readAllBytes() : new byte[0]);
            }
        } finally {
            conn.disconnect();
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
}
