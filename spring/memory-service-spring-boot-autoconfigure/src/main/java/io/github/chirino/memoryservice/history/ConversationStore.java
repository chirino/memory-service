package io.github.chirino.memoryservice.history;

import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.model.Channel;
import io.github.chirino.memoryservice.client.model.CreateEntryRequest;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;

public class ConversationStore {

    private static final Logger LOG = LoggerFactory.getLogger(ConversationStore.class);

    private final ConversationsApiFactory apiFactory;
    private final OAuth2AuthorizedClientService authorizedClientService;
    private final IndexedContentProvider indexedContentProvider;

    public ConversationStore(
            ConversationsApiFactory apiFactory,
            @Nullable OAuth2AuthorizedClientService authorizedClientService,
            @Nullable IndexedContentProvider indexedContentProvider) {
        this.apiFactory = apiFactory;
        this.authorizedClientService = authorizedClientService;
        this.indexedContentProvider = indexedContentProvider;
    }

    public void appendUserMessage(
            String conversationId, String content, @Nullable String bearerToken) {
        appendUserMessage(conversationId, content, List.of(), bearerToken);
    }

    public void appendUserMessage(
            String conversationId,
            String content,
            List<Map<String, Object>> attachments,
            @Nullable String bearerToken) {
        appendUserMessage(conversationId, content, attachments, bearerToken, null, null);
    }

    public void appendUserMessage(
            String conversationId,
            String content,
            List<Map<String, Object>> attachments,
            @Nullable String bearerToken,
            @Nullable String forkedAtConversationId,
            @Nullable String forkedAtEntryId) {
        if (!StringUtils.hasText(content)) {
            return;
        }
        CreateEntryRequest request = createRequest(content, "USER");
        if (attachments != null && !attachments.isEmpty()) {
            // Add attachments to the content block
            @SuppressWarnings("unchecked")
            Map<String, Object> block = (Map<String, Object>) request.getContent().get(0);
            block.put("attachments", attachments);
        }
        if (forkedAtConversationId != null) {
            request.forkedAtConversationId(UUID.fromString(forkedAtConversationId));
        }
        if (forkedAtEntryId != null) {
            request.forkedAtEntryId(UUID.fromString(forkedAtEntryId));
        }
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

    /**
     * Store an agent message with rich event data using "history/lc4j" content type.
     *
     * <p>The history/lc4j content type supports LangChain4j event format: {role, text?, events?}
     *
     * @param conversationId the conversation ID
     * @param finalText the accumulated response text (for search indexing)
     * @param events the coalesced event list as Maps
     * @param bearerToken the bearer token for API calls
     */
    public void appendAgentMessageWithEvents(
            String conversationId,
            String finalText,
            List<Map<String, Object>> events,
            @Nullable String bearerToken) {
        CreateEntryRequest request = new CreateEntryRequest();
        request.channel(Channel.HISTORY);
        request.contentType("history/lc4j");
        String userId = resolveUserId();
        if (userId != null) {
            request.userId(userId);
        }

        Map<String, Object> block = new HashMap<>();
        block.put("role", "AI");
        block.put("events", events);
        request.content(List.of(block));

        if (indexedContentProvider != null) {
            String indexedContent = indexedContentProvider.getIndexedContent(finalText, "AI");
            if (indexedContent != null) {
                request.indexedContent(indexedContent);
            }
        }

        callAppend(conversationId, request, resolveBearerToken(bearerToken));
    }

    public void appendPartialAgentMessage(String conversationId, String delta) {}

    public void markCompleted(String conversationId) {}

    private void callAppend(
            String conversationId, CreateEntryRequest request, @Nullable String bearerToken) {
        try {
            ConversationsApi api = apiFactory.create(bearerToken);
            api.appendConversationEntry(UUID.fromString(conversationId), request).block();
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
        request.contentType("history");
        String userId = resolveUserId();
        if (userId != null) {
            request.userId(userId);
        }
        Map<String, Object> block = new HashMap<>();
        block.put("text", content);
        block.put("role", role);
        request.content(List.of(block));
        if (indexedContentProvider != null) {
            String indexedContent = indexedContentProvider.getIndexedContent(content, role);
            if (indexedContent != null) {
                request.indexedContent(indexedContent);
            }
        }
        return request;
    }

    private String resolveBearerToken(@Nullable String explicitToken) {
        if (StringUtils.hasText(explicitToken)) {
            return explicitToken;
        }
        return SecurityHelper.bearerToken(authorizedClientService);
    }
}
