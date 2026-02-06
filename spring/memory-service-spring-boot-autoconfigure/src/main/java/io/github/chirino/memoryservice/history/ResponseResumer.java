package io.github.chirino.memoryservice.history;

import java.util.List;
import org.springframework.lang.Nullable;
import reactor.core.publisher.Flux;

public interface ResponseResumer {

    ResponseRecorder recorder(String conversationId);

    ResponseRecorder recorder(String conversationId, @Nullable String bearerToken);

    Flux<String> replay(String conversationId, @Nullable String bearerToken);

    /**
     * Replay rich event stream with type-safe return.
     *
     * <p>This method buffers the raw replay stream and emits complete JSON lines. Use this for
     * resuming rich event streams.
     *
     * @param conversationId the conversation ID
     * @param bearerToken the bearer token for authentication
     * @param type the return type - String.class for raw JSON lines (efficient, no
     *     deserialize/re-serialize), or a specific event class for deserialized objects
     * @param <T> the return type
     * @return stream of complete JSON lines or deserialized objects
     */
    default <T> Flux<T> replayEvents(
            String conversationId, @Nullable String bearerToken, Class<T> type) {
        throw new UnsupportedOperationException("replayEvents not implemented");
    }

    List<String> check(List<String> conversationIds, @Nullable String bearerToken);

    boolean enabled();

    void requestCancel(String conversationId, @Nullable String bearerToken);

    static ResponseResumer noop() {
        return NoopResponseResumer.INSTANCE;
    }

    interface ResponseRecorder {
        void record(String token);

        default void complete() {}

        default Flux<ResponseCancelSignal> cancelStream() {
            return Flux.empty();
        }
    }
}
