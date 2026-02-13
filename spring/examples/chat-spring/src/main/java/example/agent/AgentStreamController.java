package example.agent;

import io.github.chirino.memoryservice.history.AttachmentRef;
import io.github.chirino.memoryservice.history.AttachmentResolver;
import io.github.chirino.memoryservice.history.Attachments;
import io.github.chirino.memoryservice.history.ConversationHistoryStreamAdvisor;
import io.github.chirino.memoryservice.history.ConversationHistoryStreamAdvisorBuilder;
import io.github.chirino.memoryservice.history.ResponseResumer;
import io.github.chirino.memoryservice.memory.MemoryServiceChatMemoryRepositoryBuilder;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.io.IOException;
import java.util.List;
import org.springframework.ai.chat.client.ChatClient;
import org.springframework.ai.chat.client.ChatClientResponse;
import org.springframework.ai.chat.client.advisor.MessageChatMemoryAdvisor;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.ai.chat.memory.MessageWindowChatMemory;
import org.springframework.ai.chat.messages.AssistantMessage;
import org.springframework.ai.chat.model.ChatResponse;
import org.springframework.ai.chat.model.Generation;
import org.springframework.ai.content.Media;
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
@RequestMapping("/v1/conversations")
class AgentStreamController {

    private final ChatClient.Builder chatClientBuilder;
    private final MemoryServiceChatMemoryRepositoryBuilder repositoryBuilder;
    private final ResponseResumer responseResumer;
    private final ConversationHistoryStreamAdvisorBuilder historyAdvisorBuilder;
    private final OAuth2AuthorizedClientService authorizedClientService;
    private final AttachmentResolver attachmentResolver;
    private final ImageGenerationTool imageGenerationTool;

    AgentStreamController(
            ChatClient.Builder chatClientBuilder,
            MemoryServiceChatMemoryRepositoryBuilder repositoryBuilder,
            ResponseResumer responseResumer,
            ConversationHistoryStreamAdvisorBuilder historyAdvisorBuilder,
            AttachmentResolver attachmentResolver,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider,
            ObjectProvider<ImageGenerationTool> imageGenerationToolProvider) {
        this.chatClientBuilder = chatClientBuilder;
        this.repositoryBuilder = repositoryBuilder;
        this.responseResumer = responseResumer;
        this.historyAdvisorBuilder = historyAdvisorBuilder;
        this.attachmentResolver = attachmentResolver;
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
        this.imageGenerationTool = imageGenerationToolProvider.getIfAvailable();
    }

    @PostMapping(
            path = "/{conversationId}/chat",
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

        // Capture bearer token on the HTTP request thread before any reactive processing.
        // This token is passed to both the history advisor and the memory repository
        // since SecurityContext is not available on worker threads.
        String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
        var historyAdvisor = historyAdvisorBuilder.build(bearerToken);
        var repository = repositoryBuilder.build(bearerToken);
        var chatMemory = MessageWindowChatMemory.builder().chatMemoryRepository(repository).build();

        var builder =
                chatClientBuilder
                        .clone()
                        .defaultSystem("You are a helpful assistant.")
                        .defaultAdvisors(
                                historyAdvisor,
                                MessageChatMemoryAdvisor.builder(chatMemory).build());
        if (imageGenerationTool != null) {
            builder = builder.defaultTools(imageGenerationTool);
        }
        var chatClient = builder.build();

        Attachments attachments = attachmentResolver.resolve(toRefs(request.getAttachments()));

        ChatClient.ChatClientRequestSpec requestSpec =
                chatClient
                        .prompt()
                        .advisors(
                                advisor -> {
                                    advisor.param(ChatMemory.CONVERSATION_ID, conversationId);
                                    if (!attachments.isEmpty()) {
                                        advisor.param(
                                                ConversationHistoryStreamAdvisor.ATTACHMENTS_KEY,
                                                attachments);
                                    }
                                    if (StringUtils.hasText(request.getForkedAtConversationId())) {
                                        advisor.param(
                                                ConversationHistoryStreamAdvisor
                                                        .FORKED_AT_CONVERSATION_ID_KEY,
                                                request.getForkedAtConversationId());
                                    }
                                    if (StringUtils.hasText(request.getForkedAtEntryId())) {
                                        advisor.param(
                                                ConversationHistoryStreamAdvisor
                                                        .FORKED_AT_ENTRY_ID_KEY,
                                                request.getForkedAtEntryId());
                                    }
                                })
                        .user(
                                spec -> {
                                    spec.text(request.getMessage());
                                    if (!attachments.media().isEmpty()) {
                                        spec.media(attachments.media().toArray(new Media[0]));
                                    }
                                });

        Flux<String> responseFlux =
                requestSpec.stream().chatClientResponse().map(this::extractContent);

        SseEmitter emitter = new SseEmitter(0L);
        Disposable subscription =
                responseFlux.subscribe(
                        chunk -> safeSendChunk(conversationId, emitter, new TokenFrame(chunk)),
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

    @GetMapping(path = "/{conversationId}/resume", produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    public SseEmitter resume(@PathVariable String conversationId) {
        if (!StringUtils.hasText(conversationId)) {
            throw new ResponseStatusException(
                    HttpStatus.BAD_REQUEST, "Conversation ID is required");
        }

        SseEmitter emitter = new SseEmitter(0L);
        String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
        Disposable subscription =
                responseResumer
                        .replay(conversationId, bearerToken)
                        .subscribe(
                                chunk ->
                                        safeSendChunk(
                                                conversationId, emitter, new TokenFrame(chunk)),
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

    private static List<AttachmentRef> toRefs(List<RequestAttachmentRef> attachments) {
        if (attachments == null || attachments.isEmpty()) {
            return List.of();
        }
        return attachments.stream()
                .map(
                        a ->
                                new AttachmentRef(
                                        a.getAttachmentId(),
                                        a.getContentType(),
                                        a.getName(),
                                        a.getHref()))
                .toList();
    }

    private void safeSendChunk(String conversationId, SseEmitter emitter, TokenFrame frame) {
        try {
            emitter.send(SseEmitter.event().name("token").data(frame));
        } catch (IOException | IllegalStateException ignored) {
            // Client disconnected or emitter already completed.
            // Silently ignore - the upstream will continue recording the full response.
        }
    }

    private void safeComplete(SseEmitter emitter) {
        try {
            emitter.complete();
        } catch (IllegalStateException ignored) {
            // Emitter already completed - ignore
        }
    }

    private void safeCompleteWithError(SseEmitter emitter, Throwable failure) {
        try {
            emitter.completeWithError(failure);
        } catch (IllegalStateException ignored) {
            // Emitter already completed - ignore
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
        private List<RequestAttachmentRef> attachments;
        private String forkedAtConversationId;
        private String forkedAtEntryId;

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

        public List<RequestAttachmentRef> getAttachments() {
            return attachments;
        }

        public void setAttachments(List<RequestAttachmentRef> attachments) {
            this.attachments = attachments;
        }

        public String getForkedAtConversationId() {
            return forkedAtConversationId;
        }

        public void setForkedAtConversationId(String forkedAtConversationId) {
            this.forkedAtConversationId = forkedAtConversationId;
        }

        public String getForkedAtEntryId() {
            return forkedAtEntryId;
        }

        public void setForkedAtEntryId(String forkedAtEntryId) {
            this.forkedAtEntryId = forkedAtEntryId;
        }
    }

    public static final class RequestAttachmentRef {

        private String href;
        private String attachmentId;
        private String contentType;
        private String name;

        public RequestAttachmentRef() {}

        public String getHref() {
            return href;
        }

        public void setHref(String href) {
            this.href = href;
        }

        public String getAttachmentId() {
            return attachmentId;
        }

        public void setAttachmentId(String attachmentId) {
            this.attachmentId = attachmentId;
        }

        public String getContentType() {
            return contentType;
        }

        public void setContentType(String contentType) {
            this.contentType = contentType;
        }

        public String getName() {
            return name;
        }

        public void setName(String name) {
            this.name = name;
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
