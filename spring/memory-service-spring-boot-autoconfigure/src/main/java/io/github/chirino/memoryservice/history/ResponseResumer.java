package io.github.chirino.memoryservice.history;

import java.util.List;
import org.springframework.lang.Nullable;
import reactor.core.publisher.Flux;

public interface ResponseResumer {

    ResponseRecorder recorder(String conversationId);

    ResponseRecorder recorder(String conversationId, @Nullable String bearerToken);

    Flux<String> replay(String conversationId, @Nullable String bearerToken);

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
