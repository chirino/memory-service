package io.github.chirino.memory.attachment;

import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class FileStoreSelector {

    @ConfigProperty(name = "memory-service.attachments.store", defaultValue = "db")
    String storeType;

    @Inject DatabaseFileStore databaseFileStore;

    @Inject S3FileStore s3FileStore;

    private FileStore selected;

    @PostConstruct
    void init() {
        String type = storeType == null ? "db" : storeType.trim().toLowerCase();
        if ("s3".equals(type)) {
            selected = s3FileStore;
        } else if ("db".equals(type)) {
            selected = databaseFileStore;
        } else {
            throw new IllegalStateException(
                    "Unsupported memory-service.attachments.store: " + storeType);
        }
    }

    public FileStore getFileStore() {
        return selected;
    }
}
