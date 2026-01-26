package io.github.chirino.memory.resumer;

import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.Collections;
import java.util.List;

/**
 * No-op implementation of ResponseResumerBackend used when resumer is disabled.
 */
@ApplicationScoped
public class NoopResponseResumerBackend implements ResponseResumerBackend {

    @Override
    public ResponseRecorder recorder(String conversationId) {
        return new NoopResponseRecorder();
    }

    @Override
    public Multi<String> replay(String conversationId) {
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
    public List<String> check(List<String> conversationIds) {
        return Collections.emptyList();
    }

    @Override
    public void requestCancel(String conversationId) {}

    @Override
    public Multi<CancelSignal> cancelStream(String conversationId) {
        return Multi.createFrom().empty();
    }
}
