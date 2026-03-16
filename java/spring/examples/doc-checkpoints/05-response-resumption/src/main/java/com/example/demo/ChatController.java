package com.example.demo;

import io.github.chirino.memoryservice.history.ConversationHistoryStreamAdvisorBuilder;
import io.github.chirino.memoryservice.memory.MemoryServiceChatMemoryRepositoryBuilder;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.io.IOException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.chat.client.ChatClient;
import org.springframework.ai.chat.client.ChatClientResponse;
import org.springframework.ai.chat.client.advisor.MessageChatMemoryAdvisor;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.ai.chat.memory.MessageWindowChatMemory;
import org.springframework.ai.chat.messages.AssistantMessage;
import org.springframework.ai.chat.model.ChatResponse;
import org.springframework.ai.chat.model.Generation;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.http.MediaType;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;
import reactor.core.Disposable;
import reactor.core.publisher.Flux;
import reactor.core.scheduler.Schedulers;

@RestController
@RequestMapping("/chat")
class ChatController {
    private static final Logger LOG = LoggerFactory.getLogger(ChatController.class);

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
            produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    public SseEmitter chat(@PathVariable String conversationId, @RequestBody String userMessage) {

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
                                advisor ->
                                        advisor.param(ChatMemory.CONVERSATION_ID, conversationId))
                        .build();

        Flux<String> responseFlux =
                chatClient.prompt().user(userMessage).stream()
                        .chatClientResponse()
                        .map(this::extractContent)
                        // Schedule subscription work off the request thread so the SSE response
                        // can be committed before an upstream failure is translated to HTTP 500.
                        .subscribeOn(Schedulers.boundedElastic());

        SseEmitter emitter = new SseEmitter(0L);
        Disposable subscription =
                responseFlux.subscribe(
                        chunk -> safeSendChunk(emitter, new TokenFrame(chunk)),
                        failure -> safeCompleteWithError(emitter, failure),
                        () -> safeComplete(emitter));

        emitter.onCompletion(subscription::dispose);
        emitter.onTimeout(
                () -> {
                    subscription.dispose();
                    safeComplete(emitter);
                });
        return emitter;
    }

    private void safeSendChunk(SseEmitter emitter, TokenFrame frame) {
        try {
            emitter.send(SseEmitter.event().data(frame));
        } catch (IOException | IllegalStateException ignored) {
            // Client disconnected or emitter already completed.
        }
    }

    private void safeComplete(SseEmitter emitter) {
        try {
            emitter.complete();
        } catch (IllegalStateException ignored) {
            // Emitter already completed.
        }
    }

    private void safeCompleteWithError(SseEmitter emitter, Throwable failure) {
        LOG.warn("Streaming chat failed", failure);
        try {
            emitter.completeWithError(failure);
        } catch (IllegalStateException ignored) {
            // Emitter already completed.
        }
    }

    private String extractContent(ChatClientResponse response) {
        ChatResponse payload = response.chatResponse();
        if (payload == null) {
            return "";
        }
        StringBuilder builder = new StringBuilder();
        for (Generation generation : payload.getResults()) {
            Object output = generation.getOutput();
            if (output instanceof AssistantMessage assistant) {
                String text = assistant.getText();
                if (StringUtils.hasText(text)) {
                    builder.append(text);
                }
                continue;
            }
            if (output != null) {
                builder.append(output.toString());
            }
        }
        return builder.toString();
    }

    public static final class TokenFrame {
        private final String text;

        public TokenFrame(String text) {
            this.text = text;
        }

        public String getText() {
            return text;
        }
    }
}
