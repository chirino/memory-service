package io.github.chirino.memory.history.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import dev.langchain4j.data.message.AudioContent;
import dev.langchain4j.data.message.Content;
import dev.langchain4j.data.message.ImageContent;
import dev.langchain4j.data.message.PdfFileContent;
import dev.langchain4j.data.message.VideoContent;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.client.Client;
import jakarta.ws.rs.client.ClientBuilder;
import jakarta.ws.rs.core.Response;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Resolves {@link AttachmentRef} references into {@link Attachments} by downloading each attachment
 * from the memory-service and converting it to the appropriate LangChain4j {@link Content} type
 * based on MIME type. Downloads are streamed to a temp file to avoid buffering large attachments in
 * memory.
 */
@ApplicationScoped
public class AttachmentResolver {

    private static final Logger LOG = Logger.getLogger(AttachmentResolver.class);

    @Inject SecurityIdentity securityIdentity;

    @ConfigProperty(name = "memory-service.client.url")
    Optional<String> clientUrl;

    @ConfigProperty(name = "quarkus.rest-client.memory-service-client.url")
    Optional<String> quarkusRestClientUrl;

    @ConfigProperty(name = "memory-service.client.temp-dir")
    Optional<String> tempDir;

    /**
     * Resolves a list of attachment references into an {@link Attachments} object containing both
     * metadata (for history recording) and LangChain4j Content objects (for LLM delivery).
     */
    public Attachments resolve(List<AttachmentRef> refs) {
        if (refs == null || refs.isEmpty()) {
            return Attachments.empty();
        }

        List<Map<String, Object>> metadata = new ArrayList<>();
        List<Content> contents = new ArrayList<>();

        for (AttachmentRef ref : refs) {
            if (ref.id() == null || ref.id().isBlank()) {
                continue;
            }

            // Build metadata for history recording
            Map<String, Object> meta = new LinkedHashMap<>();
            meta.put("attachmentId", ref.id());
            if (ref.contentType() != null && !ref.contentType().isBlank()) {
                meta.put("contentType", ref.contentType());
            }
            if (ref.name() != null && !ref.name().isBlank()) {
                meta.put("name", ref.name());
            }
            metadata.add(meta);

            // Download and convert to LangChain4j Content
            try {
                Content content = downloadAndConvert(ref);
                if (content != null) {
                    contents.add(content);
                }
            } catch (Exception e) {
                LOG.warnf(e, "Failed to resolve attachment %s, skipping", ref.id());
            }
        }

        return new Attachments(metadata, contents);
    }

    private Content downloadAndConvert(AttachmentRef ref) throws IOException {
        String baseUrl =
                clientUrl.orElseGet(() -> quarkusRestClientUrl.orElse("http://localhost:8080"));
        String url = baseUrl + "/v1/attachments/" + ref.id();
        String bearer = bearerToken(securityIdentity);
        Client client = ClientBuilder.newClient();
        try {
            var req = client.target(url).request();
            if (bearer != null) {
                req = req.header("Authorization", "Bearer " + bearer);
            }
            Response response = req.get();
            if (response.getStatus() == 302) {
                // S3 redirect — use the signed URL directly
                String signedUrl = response.getHeaderString("Location");
                return toContentFromUrl(ref.contentType(), signedUrl);
            }
            if (response.getStatus() == 200) {
                String contentType = response.getHeaderString("Content-Type");
                if (contentType == null) {
                    contentType = ref.contentType();
                }
                if (contentType == null) {
                    contentType = "application/octet-stream";
                }
                return streamToTempFileAndConvert(
                        response.readEntity(InputStream.class), contentType);
            }
            LOG.warnf(
                    "Unexpected status %d downloading attachment %s",
                    response.getStatus(), ref.id());
        } finally {
            client.close();
        }
        return null;
    }

    /**
     * Streams response body to a temp file, then base64-encodes from the file. This avoids
     * buffering the entire attachment in memory.
     */
    private Content streamToTempFileAndConvert(InputStream body, String contentType)
            throws IOException {
        Path dir = resolveTempDir();
        Path tempFile = Files.createTempFile(dir, "attachment-", ".tmp");
        try {
            try (OutputStream out = Files.newOutputStream(tempFile)) {
                body.transferTo(out);
            }
            byte[] bytes = Files.readAllBytes(tempFile);
            String base64 = Base64.getEncoder().encodeToString(bytes);
            return toContentFromBase64(contentType, base64);
        } finally {
            Files.deleteIfExists(tempFile);
        }
    }

    private Path resolveTempDir() {
        if (tempDir.isPresent()) {
            return Paths.get(tempDir.get());
        }
        return Paths.get(System.getProperty("java.io.tmpdir"));
    }

    static Content toContentFromUrl(String contentType, String url) {
        if (contentType == null) {
            // Default to image for URL-based content (most common case for S3 redirects)
            return ImageContent.from(url);
        }
        if (contentType.startsWith("image/")) {
            return ImageContent.from(url);
        }
        if (contentType.startsWith("audio/")) {
            return AudioContent.from(url);
        }
        if (contentType.startsWith("video/")) {
            return VideoContent.from(url);
        }
        if (contentType.equals("application/pdf")) {
            return PdfFileContent.from(url);
        }
        LOG.debugf("Unsupported content type for LLM via URL: %s", contentType);
        return null;
    }

    static Content toContentFromBase64(String contentType, String base64) {
        if (contentType.startsWith("image/")) {
            return ImageContent.from(base64, contentType);
        }
        if (contentType.startsWith("audio/")) {
            return AudioContent.from(base64, contentType);
        }
        if (contentType.startsWith("video/")) {
            return VideoContent.from(base64, contentType);
        }
        if (contentType.equals("application/pdf")) {
            return PdfFileContent.from(base64, contentType);
        }
        LOG.debugf("Unsupported content type for LLM: %s", contentType);
        return null;
    }
}
