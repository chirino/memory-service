package example.agent;

import java.io.IOException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.chat.client.ChatClient;
import org.springframework.ai.chat.client.advisor.MessageChatMemoryAdvisor;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
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

@RestController
@RequestMapping("/customer-support-agent")
class AgentStreamController {

    private static final Logger LOG = LoggerFactory.getLogger(AgentStreamController.class);

    private final ChatClient chatClient;
    private final ChatMemory chatMemory;

    AgentStreamController(ChatClient chatClient, ChatMemory chatMemory) {
        this.chatClient = chatClient;
        this.chatMemory = chatMemory;
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

        LOG.info("Received SSE request for conversationId={}", conversationId);

        SseEmitter emitter = new SseEmitter(0L);
        ChatClient requestClient =
                chatClient
                        .mutate()
                        .defaultAdvisors(
                                MessageChatMemoryAdvisor.builder(chatMemory)
                                        .conversationId(conversationId)
                                        .build())
                        .build();

        Flux<String> responseFlux =
                requestClient.prompt().user(request.getMessage()).stream().content();

        Disposable subscription =
                responseFlux.subscribe(
                        chunk -> safeSendChunk(conversationId, emitter, new TokenFrame(chunk)),
                        failure -> {
                            LOG.warn("Chat failed for conversationId={}", conversationId, failure);
                            emitter.completeWithError(failure);
                        },
                        () -> {
                            LOG.info(
                                    "Chat stream completed for conversationId={}, closing"
                                            + " connection",
                                    conversationId);
                            emitter.complete();
                        });

        emitter.onCompletion(subscription::dispose);
        emitter.onTimeout(
                () -> {
                    LOG.info("Chat stream timed out for conversationId={}", conversationId);
                    subscription.dispose();
                    emitter.complete();
                });

        return emitter;
    }

    private void safeSendChunk(String conversationId, SseEmitter emitter, TokenFrame frame) {
        try {
            emitter.send(SseEmitter.event().name("token").data(frame));
        } catch (IOException failure) {
            LOG.warn(
                    "Failed to send SSE chunk for conversationId={}, closing emitter",
                    conversationId,
                    failure);
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
}
