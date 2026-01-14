package io.github.chirino.memory.resumer;

import java.time.Duration;
import java.util.Optional;

public interface ResponseResumerLocatorStore {

    boolean available();

    Optional<ResponseResumerLocator> get(String conversationId);

    void upsert(String conversationId, ResponseResumerLocator locator, Duration ttl);

    void remove(String conversationId);

    boolean exists(String conversationId);
}
