package io.github.chirino.memoryservice.history;

import java.util.List;
import org.springframework.lang.Nullable;
import reactor.core.publisher.Flux;

final class NoopResponseRecordingManager implements ResponseRecordingManager {

    static final NoopResponseRecordingManager INSTANCE = new NoopResponseRecordingManager();
    private static final RecordingSession RECORDER = new NoopResponseRecorder();

    @Override
    public RecordingSession recorder(String conversationId) {
        return RECORDER;
    }

    @Override
    public RecordingSession recorder(String conversationId, @Nullable String bearerToken) {
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

    private static final class NoopResponseRecorder implements RecordingSession {
        @Override
        public void record(String token) {}
    }
}
