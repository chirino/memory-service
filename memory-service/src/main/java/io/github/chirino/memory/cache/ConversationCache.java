package io.github.chirino.memory.cache;

import io.github.chirino.memory.api.dto.ConversationDto;

public interface ConversationCache {

    void put(ConversationDto conversation);

    ConversationDto get(String conversationId);

    void evict(String conversationId);
}
