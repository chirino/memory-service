package io.github.chirino.memoryservice.memory;

import io.github.chirino.memoryservice.history.ConversationsApiFactory;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;

public class MemoryServiceChatMemoryRepositoryBuilder {

    private final ConversationsApiFactory conversationsApiFactory;
    private final OAuth2AuthorizedClientService ifAvailable;

    public MemoryServiceChatMemoryRepositoryBuilder(
            ConversationsApiFactory conversationsApiFactory,
            OAuth2AuthorizedClientService ifAvailable) {
        this.conversationsApiFactory = conversationsApiFactory;
        this.ifAvailable = ifAvailable;
    }

    public MemoryServiceChatMemoryRepository build(String bearerToken) {
        return new MemoryServiceChatMemoryRepository(
                conversationsApiFactory, ifAvailable, bearerToken);
    }
}
