package io.github.chirino.memory.grpc;

import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.store.MemoryStore;
import io.grpc.Status;
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

    /**
     * Validates and resolves the userId on a CreateEntryRequest.
     * If userId is set, it must match the authenticated principal.
     * If userId is missing, it is defaulted to the authenticated principal.
     */
    protected void validateAndResolveUserId(
            io.github.chirino.memory.client.model.CreateEntryRequest request) {
        String requestUserId = request.getUserId();
        String authenticatedUserId = currentUserId();
        if (requestUserId != null && !requestUserId.isBlank()) {
            if (!requestUserId.equals(authenticatedUserId)) {
                throw Status.INVALID_ARGUMENT
                        .withDescription("userId does not match the authenticated user")
                        .asRuntimeException();
            }
        } else {
            request.setUserId(authenticatedUserId);
        }
    }
}
