package io.github.chirino.memory.conversation.spi;

import io.github.chirino.memory.conversation.api.ConversationStore;

public interface ConversationStoreProvider {

    ConversationStore get();
}
