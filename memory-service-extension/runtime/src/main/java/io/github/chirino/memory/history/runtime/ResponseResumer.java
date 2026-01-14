package io.github.chirino.memory.history.runtime;

import io.smallrye.mutiny.Multi;
import java.util.List;
import java.util.stream.Collectors;
import org.jboss.logging.Logger;

public interface ResponseResumer {

    static final Logger LOG = Logger.getLogger(ResponseResumer.class);

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
     * Check which conversations from the provided list have responses in progress.
     *
     * @param conversationIds list of history IDs to check
     * @return a list of history IDs that have responses in progress
     */
    default List<String> check(List<String> conversationIds) {
        return check(conversationIds, null);
    }

    /**
     * Check which conversations from the provided list have responses in progress,
     * optionally propagating a bearer token to downstream resumer implementations.
     *
     * @param conversationIds list of history IDs to check
     * @param bearerToken token to use for authentication when calling out (may be null)
     * @return a list of history IDs that have responses in progress
     */
    default List<String> check(List<String> conversationIds, String bearerToken) {
        if (conversationIds == null || conversationIds.isEmpty()) {
            return List.of();
        }

        return conversationIds.stream()
                .filter(
                        conversationId -> {
                            try {
                                return hasResponseInProgress(conversationId, bearerToken);
                            } catch (Exception e) {
                                LOG.warnf(
                                        e,
                                        "Failed to check if history %s has response in"
                                                + " progress",
                                        conversationId);
                                return false;
                            }
                        })
                .collect(Collectors.toList());
    }

    boolean enabled();

    /**
     * Check if a history has a response currently in progress.
     * @param conversationId the history ID to check
     * @return true if a response is in progress, false otherwise
     */
    boolean hasResponseInProgress(String conversationId);

    /**
     * Check if a history has a response currently in progress, with an optional bearer token.
     *
     * @param conversationId the history ID to check
     * @param bearerToken token to use for authentication when calling out (may be null)
     * @return true if a response is in progress, false otherwise
     */
    default boolean hasResponseInProgress(String conversationId, String bearerToken) {
        return hasResponseInProgress(conversationId);
    }

    void requestCancel(String conversationId);

    /**
     * Request cancel of a response, optionally propagating a bearer token.
     *
     * @param conversationId the history ID to cancel
     * @param bearerToken token to use for authentication when calling out (may be null)
     */
    default void requestCancel(String conversationId, String bearerToken) {
        requestCancel(conversationId);
    }

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
