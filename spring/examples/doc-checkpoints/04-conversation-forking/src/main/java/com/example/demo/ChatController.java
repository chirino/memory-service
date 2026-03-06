package com.example.demo;

import io.github.chirino.memoryservice.history.ConversationHistoryStreamAdvisor;
import io.github.chirino.memoryservice.history.ConversationHistoryStreamAdvisorBuilder;
import io.github.chirino.memoryservice.memory.MemoryServiceChatMemoryRepositoryBuilder;
import io.github.chirino.memoryservice.security.SecurityHelper;
import org.springframework.ai.chat.client.ChatClient;
import org.springframework.ai.chat.client.advisor.MessageChatMemoryAdvisor;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.ai.chat.memory.MessageWindowChatMemory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class ChatController {
    private final ChatClient.Builder chatClientBuilder;
    private final MemoryServiceChatMemoryRepositoryBuilder repositoryBuilder;
    private final ConversationHistoryStreamAdvisorBuilder historyAdvisorBuilder;
    private final OAuth2AuthorizedClientService authorizedClientService;

    public ChatController(
            ChatClient.Builder chatClientBuilder,
            MemoryServiceChatMemoryRepositoryBuilder repositoryBuilder,
            ConversationHistoryStreamAdvisorBuilder historyAdvisorBuilder,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        this.chatClientBuilder = chatClientBuilder;
        this.repositoryBuilder = repositoryBuilder;
        this.historyAdvisorBuilder = historyAdvisorBuilder;
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
    }

    @PostMapping("/chat/{conversationId}")
    public String chat(
            @PathVariable String conversationId,
            @RequestParam(required = false) String forkedAtConversationId,
            @RequestParam(required = false) String forkedAtEntryId,
            @RequestBody String message) {

        String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
        var chatMemoryAdvisor =
                MessageChatMemoryAdvisor.builder(
                                MessageWindowChatMemory.builder()
                                        .chatMemoryRepository(repositoryBuilder.build(bearerToken))
                                        .build())
                        .build();
        var historyAdvisor = historyAdvisorBuilder.build(bearerToken);

        var chatClient =
                chatClientBuilder
                        .clone()
                        .defaultSystem("You are a helpful assistant.")
                        .defaultAdvisors(historyAdvisor, chatMemoryAdvisor)
                        .defaultAdvisors(
                                advisor -> {
                                    advisor.param(ChatMemory.CONVERSATION_ID, conversationId);
                                    if (forkedAtConversationId != null
                                            && !forkedAtConversationId.isBlank()) {
                                        advisor.param(
                                                ConversationHistoryStreamAdvisor
                                                        .FORKED_AT_CONVERSATION_ID_KEY,
                                                forkedAtConversationId);
                                    }
                                    if (forkedAtEntryId != null && !forkedAtEntryId.isBlank()) {
                                        advisor.param(
                                                ConversationHistoryStreamAdvisor
                                                        .FORKED_AT_ENTRY_ID_KEY,
                                                forkedAtEntryId);
                                    }
                                })
                        .build();

        return chatClient.prompt().user(message).call().content();
    }
}
