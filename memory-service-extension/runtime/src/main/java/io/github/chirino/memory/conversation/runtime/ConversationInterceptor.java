package io.github.chirino.memory.conversation.runtime;

import io.github.chirino.memory.conversation.annotations.ConversationAware;
import io.github.chirino.memory.conversation.annotations.ConversationId;
import io.github.chirino.memory.conversation.annotations.UserMessage;
import io.github.chirino.memory.conversation.api.ConversationStore;
import io.smallrye.mutiny.Multi;
import jakarta.annotation.Priority;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.interceptor.AroundInvoke;
import jakarta.interceptor.Interceptor;
import jakarta.interceptor.InvocationContext;
import java.lang.annotation.Annotation;
import org.jboss.logging.Logger;

@ConversationAware
@Interceptor
@Priority(Interceptor.Priority.APPLICATION)
public class ConversationInterceptor {

    private static final Logger LOG = Logger.getLogger(ConversationInterceptor.class);

    @Inject Instance<ConversationStore> storeInstance;
    @Inject ResponseResumer resumer;

    @AroundInvoke
    public Object around(InvocationContext ctx) throws Exception {
        if (storeInstance == null || storeInstance.isUnsatisfied()) {
            return ctx.proceed();
        }

        ConversationStore store = storeInstance.get();
        ConversationInvocation invocation = resolveInvocation(ctx);

        try {
            store.appendUserMessage(invocation.conversationId(), invocation.userMessage());
        } catch (RuntimeException e) {
            LOG.warnf(
                    e,
                    "Failed to append user message for conversationId=%s, continuing without"
                            + " recording.",
                    invocation.conversationId());
        }

        Object result = ctx.proceed();

        if (result instanceof Multi<?> multi) {
            @SuppressWarnings("unchecked")
            Multi<String> stringMulti = (Multi<String>) multi;
            return ConversationStreamAdapter.wrap(
                    invocation.conversationId(), stringMulti, store, resumer);
        }

        store.appendAgentMessage(invocation.conversationId(), String.valueOf(result));
        store.markCompleted(invocation.conversationId());

        return result;
    }

    private ConversationInvocation resolveInvocation(InvocationContext ctx) {
        Object[] args = ctx.getParameters();
        Annotation[][] annotations = ctx.getMethod().getParameterAnnotations();

        String conversationId = null;
        String userMessage = null;

        for (int i = 0; i < args.length; i++) {
            for (Annotation a : annotations[i]) {
                if (a instanceof ConversationId) {
                    conversationId = (String) args[i];
                }
                if (a instanceof UserMessage) {
                    userMessage = (String) args[i];
                }
            }
        }

        if (conversationId == null || userMessage == null) {
            throw new IllegalStateException(
                    "Missing @ConversationId or @UserMessage on intercepted method");
        }

        return new ConversationInvocation(conversationId, userMessage);
    }
}
