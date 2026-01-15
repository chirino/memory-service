package io.github.chirino.memory.history.runtime;

import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.arc.Arc;
import io.quarkus.oidc.AccessTokenCredential;
import io.quarkus.security.credential.TokenCredential;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.jboss.logging.Logger;

@ApplicationScoped
public class ConversationStore {

    private static final Logger LOG = Logger.getLogger(ConversationStore.class);

    @Inject MemoryServiceApiBuilder conversationsApiBuilder;

    @Inject SecurityIdentity securityIdentity;

    public void appendUserMessage(String conversationId, String content) {
        CreateMessageRequest request = new CreateMessageRequest();
        request.setChannel(CreateMessageRequest.ChannelEnum.HISTORY);
        String userId = resolveUserId();
        if (userId != null) {
            request.setUserId(userId);
        }
        Map<String, Object> block = new HashMap<>();
        block.put("text", content);
        block.put("role", "USER");
        request.setContent(List.of(block));
        conversationsApi(resolveBearerTokenFromIdentity())
                .appendConversationMessage(conversationId, request);
        LOG.infof("Added user message to history %s", conversationId);
    }

    public void appendAgentMessage(String conversationId, String content, String bearerToken) {
        // For now, agent messages use the same append endpoint; the backend
        // determines the role (user vs agent) from authentication context.
        CreateMessageRequest request = new CreateMessageRequest();
        request.setChannel(CreateMessageRequest.ChannelEnum.HISTORY);
        String userId = resolveUserId();
        if (userId != null) {
            request.setUserId(userId);
        }
        Map<String, Object> block = new HashMap<>();
        block.put("text", content);
        block.put("role", "AI");
        request.setContent(List.of(block));
        String effectiveToken =
                bearerToken != null ? bearerToken : resolveBearerTokenFromIdentity();
        conversationsApi(effectiveToken).appendConversationMessage(conversationId, request);
        LOG.infof("Added agent message to history %s", conversationId);
    }

    public void appendAgentMessage(String conversationId, String content) {
        appendAgentMessage(conversationId, content, null);
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
        if (securityIdentity == null) {
            return null;
        }
        if (securityIdentity.getPrincipal() == null) {
            return null;
        }
        return securityIdentity.getPrincipal().getName();
    }

    private String resolveBearerTokenFromIdentity() {
        if (!Arc.container().requestContext().isActive()) {
            return null;
        }
        if (securityIdentity == null) {
            return null;
        }
        AccessTokenCredential atc = securityIdentity.getCredential(AccessTokenCredential.class);
        if (atc != null) {
            return atc.getToken();
        }
        TokenCredential tc = securityIdentity.getCredential(TokenCredential.class);
        if (tc != null) {
            return tc.getToken();
        }
        return null;
    }
}
