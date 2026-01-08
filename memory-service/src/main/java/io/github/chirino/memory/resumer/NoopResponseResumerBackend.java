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
    public List<String> check(List<String> conversationIds) {
        return Collections.emptyList();
    }

    private static final class NoopResponseRecorder implements ResponseRecorder {
        @Override
        public void record(String token) {
            // No-op
        }

        @Override
        public void complete() {
            // No-op
        }
    }
}
