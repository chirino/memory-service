package example.agent;

import io.github.chirino.memoryservice.history.ResponseResumer;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.io.IOException;
import org.springframework.ai.chat.client.ChatClient;
import org.springframework.ai.chat.client.ChatClientResponse;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.ai.chat.messages.AssistantMessage;
import org.springframework.ai.chat.model.ChatResponse;
import org.springframework.ai.chat.model.Generation;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;
import reactor.core.Disposable;
import reactor.core.publisher.Flux;

@RestController
@RequestMapping("/customer-support-agent")
class AgentStreamController {

    private final ChatClient chatClient;
    private final ChatMemory chatMemory;
    private final ResponseResumer responseResumer;
    private final OAuth2AuthorizedClientService authorizedClientService;

    AgentStreamController(
            ChatClient chatClient,
            ChatMemory chatMemory,
            ResponseResumer responseResumer,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        this.chatClient = chatClient;
        this.chatMemory = chatMemory;
        this.responseResumer = responseResumer;
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
    }

    @PostMapping(
            path = "/{conversationId}/sse",
            consumes = MediaType.APPLICATION_JSON_VALUE,
            produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    public SseEmitter stream(
            @PathVariable String conversationId, @RequestBody MessageRequest request) {
        if (!StringUtils.hasText(conversationId)) {
            throw new ResponseStatusException(
                    HttpStatus.BAD_REQUEST, "Conversation ID is required");
        }
        if (request == null || !StringUtils.hasText(request.getMessage())) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "Message is required");
        }

        SseEmitter emitter = new SseEmitter(0L);

        // conversationHistoryStreamAdvisor is already registered as a default advisor
        // in AgentChatConfiguration, so we only need to set the conversation ID parameter
        ChatClient.ChatClientRequestSpec requestSpec =
                chatClient
                        .prompt()
                        .advisors(
                                advisor ->
                                        advisor.param(ChatMemory.CONVERSATION_ID, conversationId))
                        .user(request.getMessage());

        Flux<String> responseFlux =
                requestSpec.stream().chatClientResponse().map(this::extractContent);

        Disposable subscription =
                responseFlux.subscribe(
                        chunk -> safeSendChunk(conversationId, emitter, new TokenFrame(chunk)),
                        emitter::completeWithError,
                        emitter::complete);

        emitter.onCompletion(subscription::dispose);
        emitter.onTimeout(
                () -> {
                    subscription.dispose();
                    emitter.complete();
                });

        return emitter;
    }

    @GetMapping(
            path = "/{conversationId}/resume/{resumePosition}",
            produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    public SseEmitter resume(
            @PathVariable String conversationId, @PathVariable long resumePosition) {
        if (!StringUtils.hasText(conversationId)) {
            throw new ResponseStatusException(
                    HttpStatus.BAD_REQUEST, "Conversation ID is required");
        }

        SseEmitter emitter = new SseEmitter(0L);
        String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
        Disposable subscription =
                responseResumer
                        .replay(conversationId, resumePosition, bearerToken)
                        .subscribe(
                                chunk ->
                                        safeSendChunk(
                                                conversationId, emitter, new TokenFrame(chunk)),
                                emitter::completeWithError,
                                emitter::complete);

        emitter.onCompletion(subscription::dispose);
        emitter.onTimeout(
                () -> {
                    subscription.dispose();
                    emitter.complete();
                });

        return emitter;
    }

    private void safeSendChunk(String conversationId, SseEmitter emitter, TokenFrame frame) {
        try {
            emitter.send(SseEmitter.event().name("token").data(frame));
        } catch (IOException failure) {
            emitter.completeWithError(failure);
        }
    }

    public static final class TokenFrame {

        private final String token;

        public TokenFrame(String token) {
            this.token = token;
        }

        public String getToken() {
            return token;
        }
    }

    public static final class MessageRequest {

        private String message;

        public MessageRequest() {}

        public MessageRequest(String message) {
            this.message = message;
        }

        public String getMessage() {
            return message;
        }

        public void setMessage(String message) {
            this.message = message;
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
}
