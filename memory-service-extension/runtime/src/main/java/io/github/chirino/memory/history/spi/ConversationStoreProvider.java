package io.github.chirino.memory.history.spi;

import io.github.chirino.memory.history.api.ConversationStore;

public interface ConversationStoreProvider {

    ConversationStore get();
}
