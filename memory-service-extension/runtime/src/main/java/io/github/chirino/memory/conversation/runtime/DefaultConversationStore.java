package io.github.chirino.memory.conversation.runtime;

import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.conversation.api.ConversationStore;
import io.github.chirino.memory.langchain4j.RequestContextExecutor;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

@ApplicationScoped
public class DefaultConversationStore implements ConversationStore {

    private static final Logger LOG = Logger.getLogger(DefaultConversationStore.class);

    @Inject @RestClient ConversationsApi conversationsApi;

    @Inject RequestContextExecutor requestContextExecutor;

    @Inject SecurityIdentity securityIdentity;

    @Inject SecurityIdentityAssociation securityIdentityAssociation;

    @Override
    public void appendUserMessage(String conversationId, String content) {
        runWithRequestContext(
                () -> {
                    CreateMessageRequest request = new CreateMessageRequest();
                    request.setChannel(CreateMessageRequest.ChannelEnum.HISTORY);
                    if (securityIdentity != null && securityIdentity.getPrincipal() != null) {
                        request.setUserId(securityIdentity.getPrincipal().getName());
                    }
                    Map<String, Object> block = new HashMap<>();
                    block.put("text", content);
                    block.put("role", "USER");
                    request.setContent(List.of(block));
                    conversationsApi.appendConversationMessage(conversationId, request);
                    LOG.infof("Added user message to conversation %s", conversationId);
                });
    }

    @Override
    public void appendAgentMessage(String conversationId, String content) {
        // For now, agent messages use the same append endpoint; the backend
        // determines the role (user vs agent) from authentication context.
        runWithRequestContext(
                () -> {
                    CreateMessageRequest request = new CreateMessageRequest();
                    request.setChannel(CreateMessageRequest.ChannelEnum.HISTORY);
                    if (securityIdentity != null && securityIdentity.getPrincipal() != null) {
                        request.setUserId(securityIdentity.getPrincipal().getName());
                    }
                    Map<String, Object> block = new HashMap<>();
                    block.put("text", content);
                    block.put("role", "AI");
                    request.setContent(List.of(block));
                    conversationsApi.appendConversationMessage(conversationId, request);
                    LOG.infof("Added agent message to conversation %s", conversationId);
                });
    }

    private void runWithRequestContext(Runnable action) {
        if (requestContextExecutor == null) {
            action.run();
            return;
        }
        requestContextExecutor.run(
                () -> {
                    propagateIdentity();
                    try {
                        action.run();
                    } finally {
                        clearIdentity();
                    }
                });
    }

    private void propagateIdentity() {
        if (securityIdentity != null && securityIdentityAssociation != null) {
            securityIdentityAssociation.setIdentity(securityIdentity);
        }
    }

    private void clearIdentity() {
        // Do not clear identity here; the association is managed by Quarkus.
    }
}
