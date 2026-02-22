package io.github.chirino.memory.config;

import io.quarkus.runtime.configuration.MemorySize;
import jakarta.enterprise.context.ApplicationScoped;
import java.net.URI;
import java.time.Duration;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class AttachmentConfig {

    @ConfigProperty(name = "memory-service.attachments.max-size", defaultValue = "10M")
    MemorySize maxSize;

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

    @ConfigProperty(name = "memory-service.attachments.s3.direct-download", defaultValue = "true")
    boolean s3DirectDownload;

    @ConfigProperty(name = "memory-service.attachments.s3.external-endpoint")
    Optional<String> s3ExternalEndpoint;

    @ConfigProperty(
            name = "memory-service.attachments.download-url-expires-in",
            defaultValue = "PT5M")
    Duration downloadUrlExpiresIn;

    @ConfigProperty(name = "memory-service.attachments.download-url-secret")
    Optional<String> downloadUrlSecret;

    @ConfigProperty(name = "memory-service.attachments.encryption.enabled", defaultValue = "false")
    boolean encryptionEnabled;

    public long getMaxSize() {
        return maxSize.asLongValue();
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

    public boolean isS3DirectDownload() {
        return s3DirectDownload;
    }

    public Optional<URI> getS3ExternalEndpoint() {
        return s3ExternalEndpoint.filter(s -> !s.isBlank()).map(URI::create);
    }

    public Duration getDownloadUrlExpiresIn() {
        return downloadUrlExpiresIn;
    }

    public Optional<String> getDownloadUrlSecret() {
        return downloadUrlSecret;
    }

    public boolean isEncryptionEnabled() {
        return encryptionEnabled;
    }
}
