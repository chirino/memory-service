package io.github.chirino.memory.history.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.arc.Arc;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

@ApplicationScoped
public class ConversationStore {

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
        conversationsApi(bearerToken(securityIdentity))
                .appendConversationMessage(conversationId, request);
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
        String effectiveToken;
        effectiveToken = bearerToken != null ? bearerToken : bearerToken(securityIdentity);
        conversationsApi(effectiveToken).appendConversationMessage(conversationId, request);
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
