package io.github.chirino.memory.history.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest.ChannelEnum;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.arc.Arc;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.stream.Collectors;
import org.jboss.logging.Logger;

@ApplicationScoped
public class ConversationStore {
    private static final Logger LOG = Logger.getLogger(ConversationStore.class);

    @Inject MemoryServiceApiBuilder conversationsApiBuilder;

    @Inject SecurityIdentity securityIdentity;
    @Inject SecurityIdentityAssociation identityAssociation;
    @Inject ResponseResumer resumer;
    @Inject Instance<IndexedContentProvider> indexedContentProviderInstance;
    @Inject ObjectMapper objectMapper;

    private SecurityIdentity resolveIdentity() {
        if (identityAssociation != null) {
            SecurityIdentity resolved = identityAssociation.getIdentity();
            if (resolved != null && !resolved.isAnonymous()) {
                LOG.infof(
                        "Resolved identity from association: type=%s",
                        resolved.getClass().getName());
                return resolved;
            }
        }
        if (securityIdentity != null) {
            LOG.infof(
                    "Resolved identity from injected identity: type=%s",
                    securityIdentity.getClass().getName());
        } else {
            LOG.info("Resolved identity from injected identity: <none>");
        }
        return securityIdentity;
    }

    private void applyIndexedContent(CreateEntryRequest request, String text, String role) {
        if (indexedContentProviderInstance != null
                && indexedContentProviderInstance.isResolvable()) {
            String indexedContent =
                    indexedContentProviderInstance.get().getIndexedContent(text, role);
            if (indexedContent != null) {
                request.setIndexedContent(indexedContent);
            }
        }
    }

    public void appendUserMessage(String conversationId, String content) {
        appendUserMessage(conversationId, content, List.of());
    }

    public void appendUserMessage(
            String conversationId, String content, List<Map<String, Object>> attachments) {
        appendUserMessage(conversationId, content, attachments, null, null);
    }

    public void appendUserMessage(
            String conversationId,
            String content,
            List<Map<String, Object>> attachments,
            String forkedAtConversationId,
            String forkedAtEntryId) {
        CreateEntryRequest request = new CreateEntryRequest();
        request.setChannel(ChannelEnum.HISTORY);
        request.setContentType("history");
        String userId = resolveUserId();
        if (userId != null) {
            request.setUserId(userId);
        }
        Map<String, Object> block = new HashMap<>();
        block.put("text", content);
        block.put("role", "USER");
        if (attachments != null && !attachments.isEmpty()) {
            block.put("attachments", attachments);
        }
        request.setContent(List.of(block));
        applyIndexedContent(request, content, "USER");
        if (forkedAtConversationId != null) {
            request.setForkedAtConversationId(UUID.fromString(forkedAtConversationId));
        }
        if (forkedAtEntryId != null) {
            request.setForkedAtEntryId(UUID.fromString(forkedAtEntryId));
        }
        conversationsApi(bearerToken(securityIdentity))
                .appendConversationEntry(UUID.fromString(conversationId), request);
    }

    public void appendAgentMessage(String conversationId, String content) {
        String bearerToken = bearerToken(resolveIdentity());
        appendAgentMessage(conversationId, content, bearerToken);
    }

    public void appendAgentMessage(String conversationId, String content, String bearerToken) {
        CreateEntryRequest request = new CreateEntryRequest();
        request.setChannel(ChannelEnum.HISTORY);
        request.setContentType("history");
        String userId = resolveUserId();
        if (userId != null) {
            request.setUserId(userId);
        }
        Map<String, Object> block = new HashMap<>();
        block.put("text", content);
        block.put("role", "AI");
        request.setContent(List.of(block));
        applyIndexedContent(request, content, "AI");
        String effectiveToken;
        effectiveToken = bearerToken != null ? bearerToken : bearerToken(securityIdentity);
        conversationsApi(effectiveToken)
                .appendConversationEntry(UUID.fromString(conversationId), request);
    }

    public Multi<String> appendAgentMessage(String conversationId, Multi<String> stringMulti) {
        SecurityIdentity resolvedIdentity = resolveIdentity();
        String bearerToken = bearerToken(resolvedIdentity);
        return ConversationStreamAdapter.wrap(
                conversationId,
                stringMulti,
                this,
                resumer,
                resolvedIdentity,
                identityAssociation,
                bearerToken);
    }

    public void appendPartialAgentMessage(String conversationId, String delta) {}

    public void markCompleted(String conversationId) {}

    /**
     * Wrap a ChatEvent stream with history recording and event coalescing.
     *
     * @param conversationId the conversation ID
     * @param eventMulti the upstream ChatEvent stream
     * @return wrapped Multi that records events as they stream
     */
    public Multi<ChatEvent> appendAgentEvents(String conversationId, Multi<ChatEvent> eventMulti) {
        SecurityIdentity resolvedIdentity = resolveIdentity();
        String bearerToken = bearerToken(resolvedIdentity);
        return ConversationEventStreamAdapter.wrap(
                conversationId,
                eventMulti,
                this,
                resumer,
                objectMapper,
                resolvedIdentity,
                identityAssociation,
                bearerToken);
    }

    /**
     * Store an agent message with rich event data using "history/lc4j" content type.
     *
     * <p>The history/lc4j content type supports LangChain4j event format: {role, text?, events?}
     *
     * @param conversationId the conversation ID
     * @param finalText the accumulated response text
     * @param events the coalesced event list
     * @param bearerToken the bearer token for API calls
     */
    public void appendAgentMessageWithEvents(
            String conversationId, String finalText, List<JsonNode> events, String bearerToken) {
        CreateEntryRequest request = new CreateEntryRequest();
        request.setChannel(ChannelEnum.HISTORY);
        request.setContentType("history/lc4j");
        String userId = resolveUserId();
        if (userId != null) {
            request.setUserId(userId);
        }

        // Convert JsonNode list to Object list for the API
        List<Object> eventObjects =
                events.stream()
                        .map(node -> objectMapper.convertValue(node, Object.class))
                        .collect(Collectors.toList());

        Map<String, Object> block = new HashMap<>();
        block.put("role", "AI");
        block.put("events", eventObjects);
        request.setContent(List.of(block));

        applyIndexedContent(request, finalText, "AI");
        String effectiveToken = bearerToken != null ? bearerToken : bearerToken(securityIdentity);
        conversationsApi(effectiveToken)
                .appendConversationEntry(UUID.fromString(conversationId), request);
    }

    private ConversationsApi conversationsApi(String bearerToken) {
        return conversationsApiBuilder.withBearerAuth(bearerToken).build(ConversationsApi.class);
    }

    private String resolveUserId() {
        if (!Arc.container().requestContext().isActive()) {
            return null;
        }
        return principalName(securityIdentity);
    }
}
