package io.github.chirino.memory.resumer;

import jakarta.enterprise.context.ApplicationScoped;
import java.time.Duration;
import java.util.Optional;

@ApplicationScoped
public class NoopResponseResumerLocatorStore implements ResponseResumerLocatorStore {

    @Override
    public boolean available() {
        return false;
    }

    @Override
    public Optional<ResponseResumerLocator> get(String conversationId) {
        return Optional.empty();
    }

    @Override
    public void upsert(String conversationId, ResponseResumerLocator locator, Duration ttl) {}

    @Override
    public void remove(String conversationId) {}

    @Override
    public boolean exists(String conversationId) {
        return false;
    }
}
