package io.github.chirino.memory.history.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest.ChannelEnum;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.github.chirino.memory.subagent.runtime.SubAgentExecutionContext;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.arc.Arc;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.ws.rs.WebApplicationException;
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
    @Inject ResponseRecordingManager recordingManager;
    @Inject Instance<IndexedContentProvider> indexedContentProviderInstance;
    @Inject ObjectMapper objectMapper;
    @Inject Instance<ToolAttachmentExtractor> toolAttachmentExtractorInstance;

    private SecurityIdentity resolveIdentity() {
        if (identityAssociation != null) {
            SecurityIdentity resolved = identityAssociation.getIdentity();
            if (resolved != null && !resolved.isAnonymous()) {
                LOG.debugf(
                        "Resolved identity from association: type=%s",
                        resolved.getClass().getName());
                return resolved;
            }
        }
        if (securityIdentity != null) {
            LOG.debugf(
                    "Resolved identity from injected identity: type=%s",
                    securityIdentity.getClass().getName());
        } else {
            LOG.debug("Resolved identity from injected identity: <none>");
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
        appendUserMessage(conversationId, content, attachments, null, null, null, null, null);
    }

    public void appendUserMessage(
            String conversationId,
            String content,
            List<Map<String, Object>> attachments,
            String agentId,
            String forkedAtConversationId,
            String forkedAtEntryId,
            String startedByConversationId,
            String startedByEntryId) {
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
        if (agentId != null) {
            request.setAgentId(agentId);
        }
        if (forkedAtConversationId != null) {
            request.setForkedAtConversationId(UUID.fromString(forkedAtConversationId));
        }
        if (forkedAtEntryId != null) {
            request.setForkedAtEntryId(UUID.fromString(forkedAtEntryId));
        }
        if (startedByConversationId != null) {
            request.setStartedByConversationId(UUID.fromString(startedByConversationId));
        }
        if (startedByEntryId != null) {
            request.setStartedByEntryId(UUID.fromString(startedByEntryId));
        }
        callAppend(conversationId, request, resolveBearerToken());
    }

    public void appendAgentMessage(String conversationId, String content) {
        appendAgentMessageInternal(conversationId, content, null, resolveBearerToken());
    }

    public void appendAgentMessage(String conversationId, String content, String agentId) {
        appendAgentMessageInternal(conversationId, content, agentId, resolveBearerToken());
    }

    void appendAgentMessageWithBearerToken(
            String conversationId, String content, String bearerToken) {
        appendAgentMessageInternal(conversationId, content, null, bearerToken);
    }

    private void appendAgentMessageInternal(
            String conversationId, String content, String agentId, String bearerToken) {
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
        if (agentId != null) {
            request.setAgentId(agentId);
        }
        String effectiveToken = bearerToken != null ? bearerToken : bearerToken(securityIdentity);
        callAppend(conversationId, request, effectiveToken);
    }

    public Multi<String> appendAgentMessage(String conversationId, Multi<String> stringMulti) {
        SecurityIdentity resolvedIdentity = resolveIdentity();
        String bearerToken = resolveBearerToken();
        return ConversationStreamAdapter.wrap(
                conversationId,
                stringMulti,
                this,
                recordingManager,
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
        String bearerToken = resolveBearerToken();
        ToolAttachmentExtractor extractor =
                toolAttachmentExtractorInstance != null
                                && toolAttachmentExtractorInstance.isResolvable()
                        ? toolAttachmentExtractorInstance.get()
                        : null;
        LOG.debugf(
                "appendAgentEvents: extractor=%s (instance=%s, resolvable=%s)",
                extractor != null ? extractor.getClass().getSimpleName() : "null",
                toolAttachmentExtractorInstance != null,
                toolAttachmentExtractorInstance != null
                        && toolAttachmentExtractorInstance.isResolvable());
        return ConversationEventStreamAdapter.wrap(
                conversationId,
                eventMulti,
                this,
                recordingManager,
                objectMapper,
                resolvedIdentity,
                identityAssociation,
                bearerToken,
                extractor);
    }

    /**
     * Store an agent message with rich event data using "history/lc4j" content type.
     *
     * @param conversationId the conversation ID
     * @param finalText the accumulated response text
     * @param events the coalesced event list
     * @param bearerToken the bearer token for API calls
     */
    public void appendAgentMessageWithEvents(
            String conversationId, String finalText, List<JsonNode> events, String bearerToken) {
        appendAgentMessageWithEvents(
                conversationId, finalText, events, List.of(), null, bearerToken);
    }

    public void appendAgentMessageWithEvents(
            String conversationId,
            String finalText,
            List<JsonNode> events,
            String agentId,
            String bearerToken) {
        appendAgentMessageWithEvents(
                conversationId, finalText, events, List.of(), agentId, bearerToken);
    }

    /**
     * Store an agent message with rich event data and tool-generated attachments.
     *
     * @param conversationId the conversation ID
     * @param finalText the accumulated response text
     * @param events the coalesced event list
     * @param attachments tool-generated attachment references
     * @param bearerToken the bearer token for API calls
     */
    public void appendAgentMessageWithEvents(
            String conversationId,
            String finalText,
            List<JsonNode> events,
            List<Map<String, Object>> attachments,
            String bearerToken) {
        appendAgentMessageWithEvents(
                conversationId, finalText, events, attachments, null, bearerToken);
    }

    public void appendAgentMessageWithEvents(
            String conversationId,
            String finalText,
            List<JsonNode> events,
            List<Map<String, Object>> attachments,
            String agentId,
            String bearerToken) {
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
        if (attachments != null && !attachments.isEmpty()) {
            block.put("attachments", attachments);
        }
        request.setContent(List.of(block));

        applyIndexedContent(request, finalText, "AI");
        if (agentId != null) {
            request.setAgentId(agentId);
        }
        String effectiveToken = bearerToken != null ? bearerToken : resolveBearerToken();
        callAppend(conversationId, request, effectiveToken);
    }

    private void callAppend(String conversationId, CreateEntryRequest request, String bearerToken) {
        try {
            conversationsApi(bearerToken)
                    .appendConversationEntry(UUID.fromString(conversationId), request);
        } catch (WebApplicationException e) {
            String body = readResponseBody(e);
            LOG.warnf(
                    "Failed to append conversation entry for conversationId=%s: %d %s",
                    conversationId,
                    e.getResponse() != null ? e.getResponse().getStatus() : -1,
                    body);
        } catch (Exception e) {
            LOG.warnf(
                    e, "Failed to append conversation entry for conversationId=%s", conversationId);
        }
    }

    static String readResponseBody(WebApplicationException e) {
        try {
            if (e.getResponse() != null && e.getResponse().hasEntity()) {
                return e.getResponse().readEntity(String.class);
            }
        } catch (Exception ignored) {
        }
        return "";
    }

    private ConversationsApi conversationsApi(String bearerToken) {
        return conversationsApiBuilder.withBearerAuth(bearerToken).build(ConversationsApi.class);
    }

    private String resolveUserId() {
        if (Arc.container().requestContext().isActive()) {
            return principalName(securityIdentity);
        }
        SubAgentExecutionContext.State state = SubAgentExecutionContext.current();
        return state != null ? state.userId() : null;
    }

    private String resolveBearerToken() {
        SubAgentExecutionContext.State state = SubAgentExecutionContext.current();
        if (state != null && state.bearerToken() != null && !state.bearerToken().isBlank()) {
            return state.bearerToken();
        }
        return bearerToken(resolveIdentity());
    }
}
