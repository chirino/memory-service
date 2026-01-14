package io.github.chirino.memory.history.runtime;

import io.github.chirino.memory.history.annotations.ConversationId;
import io.github.chirino.memory.history.annotations.RecordConversation;
import io.github.chirino.memory.history.annotations.UserMessage;
import io.github.chirino.memory.history.api.ConversationStore;
import io.github.chirino.memory.langchain4j.RequestContextExecutor;
import io.quarkus.oidc.AccessTokenCredential;
import io.quarkus.security.credential.TokenCredential;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import io.smallrye.mutiny.Multi;
import jakarta.annotation.Priority;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.interceptor.AroundInvoke;
import jakarta.interceptor.Interceptor;
import jakarta.interceptor.InvocationContext;
import java.lang.annotation.Annotation;
import org.jboss.logging.Logger;

@RecordConversation
@Interceptor
@Priority(Interceptor.Priority.APPLICATION)
public class ConversationInterceptor {

    private static final Logger LOG = Logger.getLogger(ConversationInterceptor.class);

    @Inject Instance<ConversationStore> storeInstance;
    @Inject ResponseResumer resumer;
    @Inject SecurityIdentity identity;
    @Inject SecurityIdentityAssociation identityAssociation;
    @Inject RequestContextExecutor requestContextExecutor;

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
            SecurityIdentity resolvedIdentity = resolveIdentity();
            String bearerToken = resolveBearerToken(resolvedIdentity);
            @SuppressWarnings("unchecked")
            Multi<String> stringMulti = (Multi<String>) multi;
            return ConversationStreamAdapter.wrap(
                    invocation.conversationId(),
                    stringMulti,
                    store,
                    resumer,
                    resolvedIdentity,
                    identityAssociation,
                    requestContextExecutor,
                    bearerToken);
        }

        String bearerToken = resolveBearerToken(resolveIdentity());
        store.appendAgentMessage(invocation.conversationId(), String.valueOf(result), bearerToken);
        store.markCompleted(invocation.conversationId());

        return result;
    }

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
        if (identity != null) {
            LOG.infof(
                    "Resolved identity from injected identity: type=%s",
                    identity.getClass().getName());
        } else {
            LOG.info("Resolved identity from injected identity: <none>");
        }
        return identity;
    }

    private String resolveBearerToken(SecurityIdentity resolvedIdentity) {
        if (resolvedIdentity == null) {
            LOG.info("Resolved bearer token: <none> (no identity)");
            return null;
        }
        AccessTokenCredential atc = resolvedIdentity.getCredential(AccessTokenCredential.class);
        if (atc != null) {
            LOG.info("Resolved bearer token from AccessTokenCredential");
            return atc.getToken();
        }
        TokenCredential tc = resolvedIdentity.getCredential(TokenCredential.class);
        if (tc != null) {
            LOG.info("Resolved bearer token from TokenCredential");
            return tc.getToken();
        }
        LOG.info("Resolved bearer token: <none> (no credential)");
        return null;
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
