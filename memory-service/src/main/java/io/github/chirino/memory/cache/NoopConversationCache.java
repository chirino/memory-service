package io.github.chirino.memory.cache;

import io.github.chirino.memory.api.dto.ConversationDto;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class NoopConversationCache implements ConversationCache {

    @Override
    public void put(ConversationDto conversation) {
        // no-op
    }

    @Override
    public ConversationDto get(String conversationId) {
        return null;
    }

    @Override
    public void evict(String conversationId) {
        // no-op
    }
}
