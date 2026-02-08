package com.example.demo;

import io.github.chirino.memoryservice.history.ConversationHistoryStreamAdvisorBuilder;
import io.github.chirino.memoryservice.memory.MemoryServiceChatMemoryRepositoryBuilder;
import io.github.chirino.memoryservice.security.SecurityHelper;
import org.springframework.ai.chat.client.ChatClient;
import org.springframework.ai.chat.client.advisor.MessageChatMemoryAdvisor;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.ai.chat.memory.MessageWindowChatMemory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;
import reactor.core.Disposable;
import reactor.core.publisher.Flux;

import java.io.IOException;

@RestController
@RequestMapping("/chat")
class ChatController {
    private final ChatClient.Builder chatClientBuilder;
    private final MemoryServiceChatMemoryRepositoryBuilder repositoryBuilder;
    private final ConversationHistoryStreamAdvisorBuilder historyAdvisorBuilder;
    private final OAuth2AuthorizedClientService authorizedClientService;

    ChatController(
            ChatClient.Builder chatClientBuilder,
            MemoryServiceChatMemoryRepositoryBuilder repositoryBuilder,
            ConversationHistoryStreamAdvisorBuilder historyAdvisorBuilder,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        this.chatClientBuilder = chatClientBuilder;
        this.repositoryBuilder = repositoryBuilder;
        this.historyAdvisorBuilder = historyAdvisorBuilder;
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
    }

    @PostMapping(
            path = "/{conversationId}",
            consumes = MediaType.TEXT_PLAIN_VALUE,
            produces = MediaType.TEXT_PLAIN_VALUE)
    public SseEmitter chat(
            @PathVariable String conversationId, @RequestBody String userMessage) {

        String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
        var chatMemoryAdvisor = MessageChatMemoryAdvisor.builder(MessageWindowChatMemory.builder()
                .chatMemoryRepository(repositoryBuilder.build(bearerToken))
                .build()).build();
        var historyAdvisor = historyAdvisorBuilder.build(bearerToken);

        var chatClient = chatClientBuilder.clone()
                .defaultSystem("You are a helpful assistant.")
                .defaultAdvisors(historyAdvisor, chatMemoryAdvisor)
                .defaultAdvisors(advisor -> advisor.param(ChatMemory.CONVERSATION_ID, conversationId))
                .build();

        Flux<String> responseFlux = chatClient.prompt().user(userMessage).stream().content();

        SseEmitter emitter = new SseEmitter(0L);
        Disposable subscription = responseFlux.subscribe(
                chunk -> safeSend(emitter, chunk),
                emitter::completeWithError,
                emitter::complete);

        emitter.onCompletion(subscription::dispose);
        emitter.onTimeout(() -> {
            subscription.dispose();
            emitter.complete();
        });
        return emitter;
    }

    private void safeSend(SseEmitter emitter, String chunk) {
        try {
            emitter.send(chunk);
        } catch (IOException | IllegalStateException ignored) {
        }
    }
}
