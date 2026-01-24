package io.github.chirino.memoryservice.history;

import java.util.List;
import org.springframework.lang.Nullable;
import reactor.core.publisher.Flux;

public interface ResponseResumer {

    ResponseRecorder recorder(String conversationId);

    ResponseRecorder recorder(String conversationId, @Nullable String bearerToken);

    default Flux<String> replay(
            String conversationId, @Nullable String resumePosition, @Nullable String bearerToken) {
        try {
            long parsed = Long.parseLong(resumePosition);
            return replay(conversationId, parsed, bearerToken);
        } catch (NumberFormatException | NullPointerException e) {
            return Flux.empty();
        }
    }

    Flux<String> replay(String conversationId, long resumePosition, @Nullable String bearerToken);

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
