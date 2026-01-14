package io.github.chirino.memory.langchain4j;

import dev.langchain4j.memory.ChatMemory;
import dev.langchain4j.memory.chat.ChatMemoryProvider;
import dev.langchain4j.memory.chat.MessageWindowChatMemory;
import io.github.chirino.memory.history.runtime.ConversationsApiBuilder;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import org.jboss.logging.Logger;

@Singleton
public class MemoryServiceChatMemoryProvider implements ChatMemoryProvider {

    private static final Logger LOG = Logger.getLogger(MemoryServiceChatMemoryProvider.class);

    @Inject ConversationsApiBuilder conversationsApiBuilder;

    @Inject RequestContextExecutor requestContextExecutor;

    @Inject Instance<SecurityIdentity> securityIdentityInstance;

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
        return new MemoryServiceChatMemory(
                conversationsApiBuilder,
                memoryId.toString(),
                requestContextExecutor,
                securityIdentity);
    }
}
