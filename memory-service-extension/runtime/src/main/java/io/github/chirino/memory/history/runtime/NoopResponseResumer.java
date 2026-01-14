package io.github.chirino.memory.history.runtime;

import io.quarkus.arc.DefaultBean;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;

/**
 * Fallback {@link ResponseResumer} used when no other implementation is available.
 */
@ApplicationScoped
@DefaultBean
public class NoopResponseResumer implements ResponseResumer {

    static final NoopResponseResumer INSTANCE = new NoopResponseResumer();

    @Override
    public ResponseRecorder recorder(String conversationId) {
        return token -> {};
    }

    @Override
    public ResponseRecorder recorder(String conversationId, String bearerToken) {
        return token -> {};
    }

    @Override
    public Multi<String> replay(String conversationId, long resumePosition) {
        return Multi.createFrom().empty();
    }

    @Override
    public boolean enabled() {
        return false;
    }

    @Override
    public boolean hasResponseInProgress(String conversationId) {
        return false;
    }

    @Override
    public void requestCancel(String conversationId) {
        // No-op
    }
}
