package io.github.chirino.memoryservice.history;

import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.model.Channel;
import io.github.chirino.memoryservice.client.model.CreateEntryRequest;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;

public class ConversationStore {

    private static final Logger LOG = LoggerFactory.getLogger(ConversationStore.class);

    private final ConversationsApiFactory apiFactory;
    private final OAuth2AuthorizedClientService authorizedClientService;

    public ConversationStore(
            ConversationsApiFactory apiFactory,
            @Nullable OAuth2AuthorizedClientService authorizedClientService) {
        this.apiFactory = apiFactory;
        this.authorizedClientService = authorizedClientService;
    }

    public void appendUserMessage(
            String conversationId, String content, @Nullable String bearerToken) {
        if (!StringUtils.hasText(content)) {
            return;
        }
        CreateEntryRequest request = createRequest(content, "USER");
        callAppend(conversationId, request, resolveBearerToken(bearerToken));
    }

    public void appendAgentMessage(
            String conversationId, String content, @Nullable String bearerToken) {
        if (!StringUtils.hasText(content)) {
            return;
        }
        CreateEntryRequest request = createRequest(content, "AI");
        callAppend(conversationId, request, resolveBearerToken(bearerToken));
    }

    public void appendPartialAgentMessage(String conversationId, String delta) {}

    public void markCompleted(String conversationId) {}

    private void callAppend(
            String conversationId, CreateEntryRequest request, @Nullable String bearerToken) {
        try {
            ConversationsApi api = apiFactory.create(bearerToken);
            api.appendConversationEntry(conversationId, request).block();
        } catch (Exception e) {
            LOG.warn(
                    "Failed to append conversation entry for conversationId={}, continuing"
                            + " without recording.",
                    conversationId,
                    e);
        }
    }

    private String resolveUserId() {
        return SecurityHelper.principalName();
    }

    private CreateEntryRequest createRequest(String content, String role) {
        CreateEntryRequest request = new CreateEntryRequest();
        request.channel(Channel.HISTORY);
        request.contentType("message");
        String userId = resolveUserId();
        if (userId != null) {
            request.userId(userId);
        }
        Map<String, Object> block = new HashMap<>();
        block.put("text", content);
        block.put("role", role);
        request.content(List.of(block));
        return request;
    }

    private String resolveBearerToken(@Nullable String explicitToken) {
        if (StringUtils.hasText(explicitToken)) {
            return explicitToken;
        }
        return SecurityHelper.bearerToken(authorizedClientService);
    }
}
