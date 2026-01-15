package io.github.chirino.memory.history.runtime;

import io.smallrye.mutiny.Multi;
import java.util.List;

public interface ResponseResumer {

    ResponseRecorder recorder(String conversationId);

    default ResponseRecorder recorder(String conversationId, String bearerToken) {
        return recorder(conversationId);
    }

    Multi<String> replay(String conversationId, long resumePosition);

    default Multi<String> replay(String conversationId, String resumePosition) {
        try {
            return replay(conversationId, Long.parseLong(resumePosition));
        } catch (NumberFormatException e) {
            return Multi.createFrom().empty();
        }
    }

    default Multi<String> replay(String conversationId, long resumePosition, String bearerToken) {
        return replay(conversationId, resumePosition);
    }

    /**
     * Check which conversations from the provided list have responses in progress,
     * optionally propagating a bearer token to downstream resumer implementations.
     *
     * @param conversationIds list of history IDs to check
     * @param bearerToken token to use for authentication when calling out (may be null)
     * @return a list of history IDs that have responses in progress
     */
    List<String> check(List<String> conversationIds, String bearerToken);

    boolean enabled();

    /**
     * Request cancel of a response, optionally propagating a bearer token.
     *
     * @param conversationId the history ID to cancel
     */
    default void requestCancel(String conversationId) {
        requestCancel(conversationId, null);
    }

    /**
     * Request cancel of a response, optionally propagating a bearer token.
     *
     * @param conversationId the history ID to cancel
     * @param bearerToken token to use for authentication when calling out (may be null)
     */
    void requestCancel(String conversationId, String bearerToken);

    static ResponseResumer noop() {
        return NoopResponseResumer.INSTANCE;
    }

    interface ResponseRecorder {
        void record(String token);

        /**
         * Optional hook invoked when the response stream completes. Default is no-op.
         */
        default void complete() {}

        /**
         * Optional stream of cancel signals for this response recording.
         */
        default Multi<ResponseCancelSignal> cancelStream() {
            return Multi.createFrom().empty();
        }
    }
}
