package io.github.chirino.memoryservice.memory;

import io.github.chirino.memoryservice.history.ConversationsApiFactory;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.ai.chat.memory.ChatMemoryRepository;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.context.annotation.Bean;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;

/**
 * Auto-configuration for Memory Service backed ChatMemory.
 */
@AutoConfiguration
@ConditionalOnClass({ChatMemory.class, ChatMemoryRepository.class})
@ConditionalOnBean(ConversationsApiFactory.class)
public class ChatMemoryAutoConfiguration {

    /**
     * Creates a MemoryServiceChatMemoryRepositoryFactory backed by the Memory Service API.
     */
    @Bean
    public MemoryServiceChatMemoryRepositoryBuilder memoryServiceChatMemoryRepository(
            ConversationsApiFactory conversationsApiFactory,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        return new MemoryServiceChatMemoryRepositoryBuilder(
                conversationsApiFactory, authorizedClientServiceProvider.getIfAvailable());
    }
}
