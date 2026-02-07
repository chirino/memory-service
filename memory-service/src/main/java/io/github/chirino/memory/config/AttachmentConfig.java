package io.github.chirino.memory.config;

import jakarta.enterprise.context.ApplicationScoped;
import java.time.Duration;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class AttachmentConfig {

    @ConfigProperty(name = "memory-service.attachments.max-size", defaultValue = "10485760")
    long maxSize;

    @ConfigProperty(name = "memory-service.attachments.default-expires-in", defaultValue = "PT1H")
    Duration defaultExpiresIn;

    @ConfigProperty(name = "memory-service.attachments.max-expires-in", defaultValue = "PT24H")
    Duration maxExpiresIn;

    @ConfigProperty(name = "memory-service.attachments.upload-expires-in", defaultValue = "PT1M")
    Duration uploadExpiresIn;

    @ConfigProperty(
            name = "memory-service.attachments.upload-refresh-interval",
            defaultValue = "PT30S")
    Duration uploadRefreshInterval;

    @ConfigProperty(name = "memory-service.attachments.cleanup-interval", defaultValue = "PT5M")
    Duration cleanupInterval;

    @ConfigProperty(name = "memory-service.attachments.store", defaultValue = "db")
    String store;

    @ConfigProperty(
            name = "memory-service.attachments.s3.bucket",
            defaultValue = "memory-service-attachments")
    String s3Bucket;

    @ConfigProperty(name = "memory-service.attachments.s3.prefix")
    Optional<String> s3Prefix;

    @ConfigProperty(
            name = "memory-service.attachments.download-url-expires-in",
            defaultValue = "PT5M")
    Duration downloadUrlExpiresIn;

    @ConfigProperty(name = "memory-service.attachments.download-url-secret")
    Optional<String> downloadUrlSecret;

    public long getMaxSize() {
        return maxSize;
    }

    public Duration getDefaultExpiresIn() {
        return defaultExpiresIn;
    }

    public Duration getMaxExpiresIn() {
        return maxExpiresIn;
    }

    public Duration getUploadExpiresIn() {
        return uploadExpiresIn;
    }

    public Duration getUploadRefreshInterval() {
        return uploadRefreshInterval;
    }

    public Duration getCleanupInterval() {
        return cleanupInterval;
    }

    public String getStore() {
        return store;
    }

    public String getS3Bucket() {
        return s3Bucket;
    }

    public String getS3Prefix() {
        return s3Prefix.orElse("");
    }

    public Duration getDownloadUrlExpiresIn() {
        return downloadUrlExpiresIn;
    }

    public Optional<String> getDownloadUrlSecret() {
        return downloadUrlSecret;
    }
}
