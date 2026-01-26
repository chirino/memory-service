package io.github.chirino.memoryservice.history;

import java.util.List;
import org.springframework.lang.Nullable;
import reactor.core.publisher.Flux;

final class NoopResponseResumer implements ResponseResumer {

    static final NoopResponseResumer INSTANCE = new NoopResponseResumer();
    private static final ResponseRecorder RECORDER = new NoopResponseRecorder();

    @Override
    public ResponseRecorder recorder(String conversationId) {
        return RECORDER;
    }

    @Override
    public ResponseRecorder recorder(String conversationId, @Nullable String bearerToken) {
        return RECORDER;
    }

    @Override
    public Flux<String> replay(String conversationId, @Nullable String bearerToken) {
        return Flux.empty();
    }

    @Override
    public List<String> check(List<String> conversationIds, @Nullable String bearerToken) {
        return List.of();
    }

    @Override
    public boolean enabled() {
        return false;
    }

    @Override
    public void requestCancel(String conversationId, @Nullable String bearerToken) {}

    private static final class NoopResponseRecorder implements ResponseRecorder {
        @Override
        public void record(String token) {}
    }
}
