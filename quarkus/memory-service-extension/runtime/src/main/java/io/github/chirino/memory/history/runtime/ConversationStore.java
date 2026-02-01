package io.github.chirino.memory.history.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest.ChannelEnum;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
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
import org.jboss.logging.Logger;

@ApplicationScoped
public class ConversationStore {
    private static final Logger LOG = Logger.getLogger(ConversationStore.class);

    @Inject MemoryServiceApiBuilder conversationsApiBuilder;

    @Inject SecurityIdentity securityIdentity;
    @Inject SecurityIdentityAssociation identityAssociation;
    @Inject ResponseResumer resumer;
    @Inject Instance<IndexedContentProvider> indexedContentProviderInstance;

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
        request.setContent(List.of(block));
        applyIndexedContent(request, content, "USER");
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
