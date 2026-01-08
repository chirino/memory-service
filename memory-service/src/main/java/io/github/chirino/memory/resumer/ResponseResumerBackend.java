package io.github.chirino.memory.resumer;

import io.smallrye.mutiny.Multi;
import java.util.List;

public interface ResponseResumerBackend {
    ResponseRecorder recorder(String conversationId);

    Multi<String> replay(String conversationId, long resumePosition);

    boolean enabled();

    boolean hasResponseInProgress(String conversationId);

    List<String> check(List<String> conversationIds);

    interface ResponseRecorder {
        void record(String token);

        void complete();
    }
}
