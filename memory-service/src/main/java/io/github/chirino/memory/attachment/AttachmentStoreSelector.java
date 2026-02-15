package io.github.chirino.memory.attachment;

import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class AttachmentStoreSelector {

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject Instance<PostgresAttachmentStore> postgresAttachmentStore;

    @Inject Instance<MongoAttachmentStore> mongoAttachmentStore;

    private AttachmentStore selected;

    @PostConstruct
    void init() {
        String type = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        if ("postgres".equals(type)) {
            selected = postgresAttachmentStore.get();
        } else if ("mongo".equals(type) || "mongodb".equals(type)) {
            selected = mongoAttachmentStore.get();
        } else {
            throw new IllegalStateException(
                    "Unsupported memory-service.datastore.type for attachments: " + datastoreType);
        }
    }

    public AttachmentStore getStore() {
        return selected;
    }
}
