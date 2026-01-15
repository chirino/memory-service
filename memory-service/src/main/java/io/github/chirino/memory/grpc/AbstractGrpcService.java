package io.github.chirino.memory.grpc;

import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.store.MemoryStore;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;

public abstract class AbstractGrpcService {

    @Inject MemoryStoreSelector storeSelector;

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

    protected MemoryStore store() {
        return storeSelector.getStore();
    }

    protected String currentUserId() {
        return identity.getPrincipal().getName();
    }

    protected boolean hasValidApiKey() {
        return apiKeyContext != null && apiKeyContext.hasValidApiKey();
    }

    protected String currentClientId() {
        return hasValidApiKey() ? apiKeyContext.getClientId() : null;
    }
}
