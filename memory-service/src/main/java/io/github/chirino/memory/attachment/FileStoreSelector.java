package io.github.chirino.memory.attachment;

import io.github.chirino.memory.config.AttachmentConfig;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class FileStoreSelector {

    @ConfigProperty(name = "memory-service.attachments.store", defaultValue = "db")
    String storeType;

    @Inject DatabaseFileStore databaseFileStore;

    @Inject S3FileStore s3FileStore;

    @Inject AttachmentConfig attachmentConfig;

    @Inject DataEncryptionService dataEncryptionService;

    private FileStore selected;

    @PostConstruct
    void init() {
        String type = storeType == null ? "db" : storeType.trim().toLowerCase();
        FileStore base;
        if ("s3".equals(type)) {
            base = s3FileStore;
        } else if ("db".equals(type)) {
            base = databaseFileStore;
        } else {
            throw new IllegalStateException(
                    "Unsupported memory-service.attachments.store: " + storeType);
        }

        if (dataEncryptionService.isPrimaryProviderReal()) {
            if ("s3".equals(type) && attachmentConfig.isS3DirectDownload()) {
                throw new IllegalStateException(
                        "S3 direct download (memory-service.attachments.s3.direct-download=true)"
                                + " is incompatible with file encryption."
                                + " Disable S3 direct download or use the plain encryption"
                                + " provider.");
            }
            selected = new EncryptingFileStore(base, dataEncryptionService);
        } else {
            selected = base;
        }
    }

    public FileStore getFileStore() {
        return selected;
    }
}
