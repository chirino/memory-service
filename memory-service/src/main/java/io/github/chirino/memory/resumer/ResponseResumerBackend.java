package io.github.chirino.memory.resumer;

import io.smallrye.mutiny.Multi;
import java.util.List;

public interface ResponseResumerBackend {

    enum CancelSignal {
        CANCEL_SIGNAL
    }

    ResponseRecorder recorder(String conversationId);

    default ResponseRecorder recorder(String conversationId, AdvertisedAddress advertisedAddress) {
        return recorder(conversationId);
    }

    Multi<String> replay(String conversationId);

    default Multi<String> replay(String conversationId, AdvertisedAddress advertisedAddress) {
        return replay(conversationId);
    }

    boolean enabled();

    boolean hasResponseInProgress(String conversationId);

    List<String> check(List<String> conversationIds);

    void requestCancel(String conversationId);

    default void requestCancel(String conversationId, AdvertisedAddress advertisedAddress) {
        requestCancel(conversationId);
    }

    Multi<CancelSignal> cancelStream(String conversationId);

    interface ResponseRecorder {
        void record(String token);

        void complete();
    }
}
