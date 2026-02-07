package io.github.chirino.memory.attachment;

import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class AttachmentStoreSelector {

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject PostgresAttachmentStore postgresAttachmentStore;

    @Inject MongoAttachmentStore mongoAttachmentStore;

    private AttachmentStore selected;

    @PostConstruct
    void init() {
        String type = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        if ("postgres".equals(type)) {
            selected = postgresAttachmentStore;
        } else if ("mongo".equals(type) || "mongodb".equals(type)) {
            selected = mongoAttachmentStore;
        } else {
            throw new IllegalStateException(
                    "Unsupported memory-service.datastore.type for attachments: " + datastoreType);
        }
    }

    public AttachmentStore getStore() {
        return selected;
    }
}
