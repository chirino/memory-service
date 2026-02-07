package io.github.chirino.memoryservice.history;

import io.github.chirino.memoryservice.history.ResponseResumer.ResponseRecorder;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.chat.client.ChatClientRequest;
import org.springframework.ai.chat.client.ChatClientResponse;
import org.springframework.ai.chat.client.advisor.api.CallAdvisor;
import org.springframework.ai.chat.client.advisor.api.CallAdvisorChain;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.ai.chat.messages.AssistantMessage;
import org.springframework.ai.chat.model.ChatResponse;
import org.springframework.ai.chat.model.Generation;
import org.springframework.ai.chat.prompt.Prompt;
import org.springframework.ai.content.Media;
import org.springframework.core.Ordered;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;

public class ConversationHistoryCallAdvisor implements CallAdvisor {

    private static final Logger LOG = LoggerFactory.getLogger(ConversationHistoryCallAdvisor.class);

    private final ConversationStore conversationStore;
    private final ResponseResumer responseResumer;
    private final OAuth2AuthorizedClientService authorizedClientService;

    public ConversationHistoryCallAdvisor(
            ConversationStore conversationStore,
            ResponseResumer responseResumer,
            @Nullable OAuth2AuthorizedClientService authorizedClientService) {
        this.conversationStore = conversationStore;
        this.responseResumer = responseResumer;
        this.authorizedClientService = authorizedClientService;
    }

    @Override
    public ChatClientResponse adviseCall(ChatClientRequest request, CallAdvisorChain chain) {
        String conversationId = resolveConversationId(request);
        if (!StringUtils.hasText(conversationId)) {
            return chain.nextCall(request);
        }
        String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
        List<Map<String, Object>> attachments = extractAttachments(request);
        safeAppendUserMessage(
                conversationId, resolveUserMessage(request), attachments, bearerToken);
        ResponseRecorder recorder = responseResumer.recorder(conversationId, bearerToken);
        ChatClientResponse response = chain.nextCall(request);
        try {
            recordCallResponse(conversationId, bearerToken, recorder, response);
        } finally {
            recorder.complete();
        }
        return response;
    }

    @Override
    public String getName() {
        return "conversationHistory";
    }

    @Override
    public int getOrder() {
        return Ordered.LOWEST_PRECEDENCE;
    }

    private void recordCallResponse(
            String conversationId,
            @Nullable String bearerToken,
            ResponseRecorder recorder,
            ChatClientResponse response) {
        String chunk = extractChunk(response);
        if (!StringUtils.hasText(chunk)) {
            return;
        }
        try {
            conversationStore.appendAgentMessage(conversationId, chunk, bearerToken);
            conversationStore.markCompleted(conversationId);
        } catch (Exception e) {
            LOG.debug(
                    "Failed to append final agent message for conversationId={}",
                    conversationId,
                    e);
        }
        recorder.record(chunk);
    }

    private void safeAppendUserMessage(
            String conversationId,
            @Nullable String message,
            List<Map<String, Object>> attachments,
            @Nullable String bearerToken) {
        if (!StringUtils.hasText(message)) {
            return;
        }
        try {
            conversationStore.appendUserMessage(conversationId, message, attachments, bearerToken);
        } catch (Exception e) {
            LOG.debug("Failed to append user message for conversationId={}", conversationId, e);
        }
    }

    @SuppressWarnings("unchecked")
    private List<Map<String, Object>> extractAttachments(ChatClientRequest request) {
        // Prefer explicit attachment metadata from the request context
        Object explicit =
                request.context().get(ConversationHistoryStreamAdvisor.ATTACHMENT_METADATA_KEY);
        if (explicit instanceof List<?> list && !list.isEmpty()) {
            return (List<Map<String, Object>>) list;
        }

        // Fall back to extracting from Media objects
        Prompt prompt = request.prompt();
        if (prompt == null || prompt.getUserMessage() == null) {
            return List.of();
        }
        List<Media> media = prompt.getUserMessage().getMedia();
        if (media == null || media.isEmpty()) {
            return List.of();
        }
        List<Map<String, Object>> attachments = new ArrayList<>();
        for (Media m : media) {
            if (!(m.getData() instanceof String href)) {
                continue;
            }
            Map<String, Object> att = new LinkedHashMap<>();
            att.put("href", href);
            att.put("contentType", m.getMimeType().toString());
            if (m.getName() != null) {
                att.put("name", m.getName());
            }
            attachments.add(att);
        }
        return attachments;
    }

    private String resolveConversationId(ChatClientRequest request) {
        Object potential = request.context().get(ChatMemory.CONVERSATION_ID);
        if (potential instanceof String value && StringUtils.hasText(value)) {
            return value;
        }
        return ChatMemory.DEFAULT_CONVERSATION_ID;
    }

    private @Nullable String resolveUserMessage(ChatClientRequest request) {
        Prompt prompt = request.prompt();
        if (prompt == null || prompt.getUserMessage() == null) {
            return null;
        }
        return prompt.getUserMessage().getText();
    }

    private String extractChunk(ChatClientResponse response) {
        ChatResponse payload = response.chatResponse();
        if (payload == null) {
            return null;
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
        return builder.length() == 0 ? null : builder.toString();
    }
}
