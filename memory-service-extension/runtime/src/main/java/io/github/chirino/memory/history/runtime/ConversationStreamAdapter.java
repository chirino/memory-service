package io.github.chirino.memory.history.runtime;

import io.github.chirino.memory.langchain4j.RequestContextExecutor;
import io.quarkus.arc.Arc;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.infrastructure.Infrastructure;
import io.smallrye.mutiny.subscription.Cancellable;
import io.smallrye.mutiny.subscription.MultiEmitter;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import org.jboss.logging.Logger;

public final class ConversationStreamAdapter {

    private static final Logger LOG = Logger.getLogger(ConversationStreamAdapter.class);

    private ConversationStreamAdapter() {}

    public static Multi<String> wrap(
            String conversationId,
            Multi<String> upstream,
            ConversationStore store,
            ResponseResumer resumer) {
        return wrap(conversationId, upstream, store, resumer, null, null, null, null);
    }

    public static Multi<String> wrap(
            String conversationId,
            Multi<String> upstream,
            ConversationStore store,
            ResponseResumer resumer,
            SecurityIdentity identity,
            SecurityIdentityAssociation identityAssociation,
            RequestContextExecutor requestContextExecutor,
            String bearerToken) {

        ResponseResumer.ResponseRecorder recorder =
                resumer == null
                        ? ResponseResumer.noop().recorder(conversationId, bearerToken)
                        : resumer.recorder(conversationId, bearerToken);
        StringBuilder buffer = new StringBuilder();
        Multi<ResponseCancelSignal> cancelStream =
                recorder.cancelStream().emitOn(Infrastructure.getDefaultExecutor());
        Multi<String> safeUpstream = upstream.emitOn(Infrastructure.getDefaultExecutor());

        return Multi.createFrom()
                .emitter(
                        emitter ->
                                attachStreams(
                                        conversationId,
                                        safeUpstream,
                                        cancelStream,
                                        store,
                                        recorder,
                                        buffer,
                                        emitter,
                                        identity,
                                        identityAssociation,
                                        requestContextExecutor,
                                        bearerToken));
    }

    private static void attachStreams(
            String conversationId,
            Multi<String> upstream,
            Multi<ResponseCancelSignal> cancelStream,
            ConversationStore store,
            ResponseResumer.ResponseRecorder recorder,
            StringBuilder buffer,
            MultiEmitter<? super String> emitter,
            SecurityIdentity identity,
            SecurityIdentityAssociation identityAssociation,
            RequestContextExecutor requestContextExecutor,
            String bearerToken) {
        AtomicBoolean canceled = new AtomicBoolean(false);
        AtomicBoolean completed = new AtomicBoolean(false);
        AtomicReference<Cancellable> upstreamSubscription = new AtomicReference<>();
        AtomicReference<Cancellable> cancelSubscription = new AtomicReference<>();

        Runnable cancelWatcherStop =
                () -> {
                    Cancellable cancelHandle = cancelSubscription.get();
                    if (cancelHandle != null) {
                        cancelHandle.cancel();
                    }
                };

        Runnable completeCancel =
                () -> {
                    if (!completed.compareAndSet(false, true)) {
                        return;
                    }
                    runWithIdentity(
                            identity,
                            identityAssociation,
                            requestContextExecutor,
                            () ->
                                    finishCancel(
                                            conversationId, store, buffer, recorder, bearerToken));
                    cancelWatcherStop.run();
                    if (!emitter.isCancelled()) {
                        emitter.complete();
                    }
                };

        Cancellable cancelWatcher =
                cancelStream
                        .subscribe()
                        .with(
                                signal -> {
                                    if (!canceled.compareAndSet(false, true)) {
                                        return;
                                    }
                                    LOG.infof(
                                            "Cancel signal received for conversation %s",
                                            conversationId);
                                    Cancellable upstreamHandle = upstreamSubscription.get();
                                    if (upstreamHandle != null) {
                                        upstreamHandle.cancel();
                                        LOG.infof(
                                                "Upstream canceled for conversation %s",
                                                conversationId);
                                    }
                                    completeCancel.run();
                                },
                                failure -> {
                                    // Ignore cancel stream failures and keep the upstream running.
                                });
        cancelSubscription.set(cancelWatcher);

        Cancellable upstreamHandle =
                upstream.subscribe()
                        .with(
                                token -> {
                                    if (canceled.get()) {
                                        return;
                                    }
                                    handleToken(
                                            conversationId,
                                            store,
                                            recorder,
                                            buffer,
                                            emitter,
                                            token);
                                },
                                failure -> {
                                    if (canceled.get()) {
                                        completeCancel.run();
                                    } else {
                                        finishFailure(recorder, emitter, failure);
                                        cancelWatcherStop.run();
                                    }
                                },
                                () -> {
                                    if (canceled.get()) {
                                        completeCancel.run();
                                    } else {
                                        runWithIdentity(
                                                identity,
                                                identityAssociation,
                                                requestContextExecutor,
                                                () ->
                                                        finishSuccess(
                                                                conversationId,
                                                                store,
                                                                buffer,
                                                                recorder,
                                                                emitter,
                                                                bearerToken));
                                        cancelWatcherStop.run();
                                    }
                                });
        upstreamSubscription.set(upstreamHandle);
        if (canceled.get()) {
            upstreamHandle.cancel();
            LOG.infof("Upstream canceled for conversation %s", conversationId);
            completeCancel.run();
        }
    }

    private static void handleToken(
            String conversationId,
            ConversationStore store,
            ResponseResumer.ResponseRecorder recorder,
            StringBuilder buffer,
            MultiEmitter<? super String> emitter,
            String token) {
        if (token == null) {
            return;
        }
        buffer.append(token);
        try {
            store.appendPartialAgentMessage(conversationId, token);
        } catch (RuntimeException e) {
            // Ignore failures when recording partial tokens to avoid breaking the primary response
            // stream.
        }
        recorder.record(token);
        if (!emitter.isCancelled()) {
            emitter.emit(token);
        }
    }

    private static void finishFailure(
            ResponseResumer.ResponseRecorder recorder,
            MultiEmitter<? super String> emitter,
            Throwable failure) {
        recorder.complete();
        if (!emitter.isCancelled()) {
            emitter.fail(failure);
        }
    }

    private static void finishSuccess(
            String conversationId,
            ConversationStore store,
            StringBuilder buffer,
            ResponseResumer.ResponseRecorder recorder,
            MultiEmitter<? super String> emitter,
            String bearerToken) {
        try {
            store.appendAgentMessage(conversationId, buffer.toString(), bearerToken);
            store.markCompleted(conversationId);
        } catch (RuntimeException e) {
            // Ignore failures when recording final message to avoid breaking the primary response
            // stream.
        }
        LOG.infof(
                "Upstream completed for conversation %s (stored %d chars)",
                conversationId, buffer.length());
        recorder.complete();
        if (!emitter.isCancelled()) {
            emitter.complete();
        }
    }

    private static void finishCancel(
            String conversationId,
            ConversationStore store,
            StringBuilder buffer,
            ResponseResumer.ResponseRecorder recorder,
            String bearerToken) {
        try {
            store.appendAgentMessage(conversationId, buffer.toString(), bearerToken);
            store.markCompleted(conversationId);
        } catch (RuntimeException e) {
            // Ignore failures when recording final message to avoid breaking the primary response
            // stream.
        }
        LOG.infof(
                "Canceled response stored for conversation %s (stored %d chars)",
                conversationId, buffer.length());
        recorder.complete();
    }

    private static void runWithIdentity(
            SecurityIdentity identity,
            SecurityIdentityAssociation identityAssociation,
            RequestContextExecutor requestContextExecutor,
            Runnable action) {
        boolean requestContextActive = Arc.container().requestContext().isActive();
        boolean hasIdentity = identity != null;
        LOG.infof(
                "Cancel flow identity before context activation: present=%b contextActive=%b"
                        + " type=%s",
                hasIdentity,
                requestContextActive,
                hasIdentity ? identity.getClass().getName() : "<none>");
        if (identity == null || identityAssociation == null) {
            action.run();
            return;
        }
        Runnable withIdentity =
                () -> {
                    SecurityIdentity currentIdentity = identityAssociation.getIdentity();
                    boolean hasCurrent = currentIdentity != null;
                    boolean currentContextActive = Arc.container().requestContext().isActive();
                    LOG.infof(
                            "Cancel flow identity before set: present=%b contextActive=%b type=%s",
                            hasCurrent,
                            currentContextActive,
                            hasCurrent ? currentIdentity.getClass().getName() : "<none>");
                    SecurityIdentity previous = identityAssociation.getIdentity();
                    try {
                        identityAssociation.setIdentity(identity);
                        SecurityIdentity applied = identityAssociation.getIdentity();
                        boolean hasApplied = applied != null;
                        boolean appliedContextActive = Arc.container().requestContext().isActive();
                        LOG.infof(
                                "Cancel flow identity after set: present=%b contextActive=%b"
                                        + " type=%s",
                                hasApplied,
                                appliedContextActive,
                                hasApplied ? applied.getClass().getName() : "<none>");
                        action.run();
                    } finally {
                        identityAssociation.setIdentity(previous);
                        SecurityIdentity restored = identityAssociation.getIdentity();
                        boolean hasRestored = restored != null;
                        boolean restoredContextActive = Arc.container().requestContext().isActive();
                        LOG.infof(
                                "Cancel flow identity after restore: present=%b contextActive=%b"
                                        + " type=%s",
                                hasRestored,
                                restoredContextActive,
                                hasRestored ? restored.getClass().getName() : "<none>");
                    }
                };
        if (requestContextExecutor != null) {
            requestContextExecutor.run(withIdentity);
        } else {
            withIdentity.run();
        }
    }
}
