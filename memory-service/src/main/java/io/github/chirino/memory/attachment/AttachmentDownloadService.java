package io.github.chirino.memory.attachment;

import io.github.chirino.memory.config.AttachmentConfig;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.io.InputStream;
import java.net.InetAddress;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.time.Duration;
import java.time.Instant;
import java.util.HexFormat;
import org.jboss.logging.Logger;

/**
 * Downloads attachment content from a source URL asynchronously, stores it in the FileStore, and
 * updates the attachment record status.
 */
@ApplicationScoped
public class AttachmentDownloadService {

    private static final Logger LOG = Logger.getLogger(AttachmentDownloadService.class);

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject FileStoreSelector fileStoreSelector;

    @Inject AttachmentConfig config;

    /**
     * Start an asynchronous download of the source URL content. The download runs on a virtual
     * thread.
     */
    public void downloadAsync(String attachmentId, String sourceUrl, String contentType) {
        Thread.startVirtualThread(
                () -> {
                    try {
                        download(attachmentId, sourceUrl, contentType);
                    } catch (Exception e) {
                        LOG.errorf(
                                e,
                                "Failed to download attachment %s from %s",
                                attachmentId,
                                sourceUrl);
                        try {
                            attachmentStoreSelector.getStore().updateStatus(attachmentId, "failed");
                        } catch (Exception ex) {
                            LOG.errorf(
                                    ex, "Failed to update status to failed for %s", attachmentId);
                        }
                    }
                });
    }

    private void download(String attachmentId, String sourceUrl, String contentType)
            throws Exception {
        validateUrl(sourceUrl);

        HttpClient client =
                HttpClient.newBuilder()
                        .followRedirects(HttpClient.Redirect.NORMAL)
                        .connectTimeout(Duration.ofSeconds(30))
                        .build();

        HttpRequest request =
                HttpRequest.newBuilder()
                        .uri(URI.create(sourceUrl))
                        .timeout(Duration.ofMinutes(5))
                        .GET()
                        .build();

        HttpResponse<InputStream> response =
                client.send(request, HttpResponse.BodyHandlers.ofInputStream());

        if (response.statusCode() < 200 || response.statusCode() >= 300) {
            throw new RuntimeException(
                    "HTTP " + response.statusCode() + " downloading " + sourceUrl);
        }

        Path tempFile = Files.createTempFile("attachment-download-", ".tmp");
        try {
            // Stream to temp file with size counting and SHA-256
            MessageDigest sha256Digest = MessageDigest.getInstance("SHA-256");
            try (InputStream bodyStream = response.body();
                    CountingInputStream counting =
                            new CountingInputStream(bodyStream, config.getMaxSize());
                    DigestInputStream digestStream =
                            new DigestInputStream(counting, sha256Digest)) {

                Files.copy(
                        digestStream, tempFile, java.nio.file.StandardCopyOption.REPLACE_EXISTING);
            }

            // Store in FileStore
            long size = Files.size(tempFile);
            String sha256Hex = HexFormat.of().formatHex(sha256Digest.digest());
            FileStoreResult storeResult;
            try (InputStream tempStream = Files.newInputStream(tempFile)) {
                storeResult =
                        fileStoreSelector
                                .getFileStore()
                                .store(tempStream, config.getMaxSize(), contentType);
            }

            // Update attachment record
            AttachmentStore store = attachmentStoreSelector.getStore();
            Instant expiresAt = Instant.now().plus(config.getDefaultExpiresIn());
            store.updateAfterUpload(
                    attachmentId,
                    storeResult.storageKey(),
                    storeResult.size(),
                    sha256Hex,
                    expiresAt);

            LOG.infof(
                    "Downloaded attachment %s from %s (%d bytes)",
                    attachmentId, sourceUrl, storeResult.size());
        } finally {
            Files.deleteIfExists(tempFile);
        }
    }

    /** Validate that the URL is not targeting localhost or private IP ranges (SSRF protection). */
    private void validateUrl(String sourceUrl) {
        URI uri = URI.create(sourceUrl);
        String scheme = uri.getScheme();
        if (scheme == null || (!scheme.equals("http") && !scheme.equals("https"))) {
            throw new IllegalArgumentException("Only http and https URLs are supported");
        }

        String host = uri.getHost();
        if (host == null) {
            throw new IllegalArgumentException("URL must have a host");
        }

        try {
            InetAddress address = InetAddress.getByName(host);
            if (address.isLoopbackAddress()
                    || address.isSiteLocalAddress()
                    || address.isLinkLocalAddress()
                    || address.isAnyLocalAddress()) {
                throw new IllegalArgumentException(
                        "URLs targeting localhost or private networks are not allowed");
            }
        } catch (java.net.UnknownHostException e) {
            throw new IllegalArgumentException("Cannot resolve host: " + host);
        }
    }
}
