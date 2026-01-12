package io.github.chirino.memory.langchain4j;

import dev.langchain4j.memory.ChatMemory;
import dev.langchain4j.memory.chat.ChatMemoryProvider;
import dev.langchain4j.memory.chat.MessageWindowChatMemory;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

@Singleton
public class MemoryServiceChatMemoryProvider implements ChatMemoryProvider {

    private static final Logger LOG = Logger.getLogger(MemoryServiceChatMemoryProvider.class);

    @Inject @RestClient ConversationsApi conversationsApi;

    @Inject RequestContextExecutor requestContextExecutor;

    @Inject Instance<SecurityIdentity> securityIdentityInstance;

    @Inject Instance<SecurityIdentityAssociation> securityIdentityAssociationInstance;

    @Override
    public ChatMemory get(Object memoryId) {

        // this is here so that we have a way of not storing the messages for a chat interaction,
        // for example if you usigng the LLM as summarize service, and not really part of a
        // history.
        if ("".equals(memoryId)) {
            return MessageWindowChatMemory.builder().maxMessages(10).build();
        }

        SecurityIdentity securityIdentity =
                securityIdentityInstance.isResolvable() ? securityIdentityInstance.get() : null;
        SecurityIdentityAssociation securityIdentityAssociation =
                securityIdentityAssociationInstance.isResolvable()
                        ? securityIdentityAssociationInstance.get()
                        : null;
        return new MemoryServiceChatMemory(
                conversationsApi,
                memoryId.toString(),
                requestContextExecutor,
                securityIdentity,
                securityIdentityAssociation);
    }
}
