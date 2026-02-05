package io.github.chirino.memory.history.runtime;

import io.github.chirino.memory.history.annotations.ConversationId;
import io.github.chirino.memory.history.annotations.RecordConversation;
import io.github.chirino.memory.history.annotations.UserMessage;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import jakarta.annotation.Priority;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.interceptor.AroundInvoke;
import jakarta.interceptor.Interceptor;
import jakarta.interceptor.InvocationContext;
import java.lang.annotation.Annotation;
import java.lang.reflect.ParameterizedType;
import java.lang.reflect.Type;
import org.jboss.logging.Logger;

@RecordConversation
@Interceptor
@Priority(Interceptor.Priority.APPLICATION)
public class ConversationInterceptor {

    private static final Logger LOG = Logger.getLogger(ConversationInterceptor.class);

    @Inject Instance<ConversationStore> storeInstance;

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
            // Check the generic return type to determine which adapter to use
            Type returnType = ctx.getMethod().getGenericReturnType();
            if (isChatEventMulti(returnType)) {
                @SuppressWarnings("unchecked")
                Multi<ChatEvent> eventMulti = (Multi<ChatEvent>) multi;
                return store.appendAgentEvents(invocation.conversationId(), eventMulti);
            } else {
                @SuppressWarnings("unchecked")
                Multi<String> stringMulti = (Multi<String>) multi;
                return store.appendAgentMessage(invocation.conversationId(), stringMulti);
            }
        }

        store.appendAgentMessage(invocation.conversationId(), String.valueOf(result));
        store.markCompleted(invocation.conversationId());
        return result;
    }

    /**
     * Check if the return type is Multi&lt;ChatEvent&gt; or a subtype.
     */
    private boolean isChatEventMulti(Type type) {
        if (type instanceof ParameterizedType pt) {
            Type[] args = pt.getActualTypeArguments();
            if (args.length == 1 && args[0] instanceof Class<?> cls) {
                return ChatEvent.class.isAssignableFrom(cls);
            }
        }
        return false;
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
