package io.github.chirino.memory.langchain4j;

import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.util.function.Supplier;

/**
 * Produces a {@link MemoryServiceChatMemoryStore} instance. Registered as a CDI bean regardless of
 * whether the global chat-memory store is enabled, so that {@link MemoryService} (and any other
 * component that needs a store reference) can inject it without being affected by the
 * {@code memory-service.chat-memory.enabled} toggle.
 */
@ApplicationScoped
public class MemoryServiceChatMemoryStoreFactory implements Supplier<MemoryServiceChatMemoryStore> {

    private final MemoryServiceApiBuilder apiBuilder;
    private final Instance<SecurityIdentity> securityIdentityInstance;

    @Inject
    public MemoryServiceChatMemoryStoreFactory(
            MemoryServiceApiBuilder apiBuilder,
            Instance<SecurityIdentity> securityIdentityInstance) {
        this.apiBuilder = apiBuilder;
        this.securityIdentityInstance = securityIdentityInstance;
    }

    @Override
    public MemoryServiceChatMemoryStore get() {
        return new MemoryServiceChatMemoryStore(apiBuilder, securityIdentityInstance);
    }
}
