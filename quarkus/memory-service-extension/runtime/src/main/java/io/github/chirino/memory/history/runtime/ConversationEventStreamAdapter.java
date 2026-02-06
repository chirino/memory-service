package io.github.chirino.memory.history.runtime;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.infrastructure.Infrastructure;
import io.smallrye.mutiny.subscription.Cancellable;
import io.smallrye.mutiny.subscription.MultiEmitter;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Wraps a Multi&lt;ChatEvent&gt; stream to:
 *
 * <ol>
 *   <li>Record each event as JSON to the ResponseResumer for resumption
 *   <li>Coalesce adjacent PartialResponse events for efficient history storage
 *   <li>Store the coalesced events in the conversation history on completion
 * </ol>
 */
public final class ConversationEventStreamAdapter {

    private ConversationEventStreamAdapter() {}

    /**
     * Wrap a ChatEvent stream with history recording and resumption support.
     *
     * @param conversationId the conversation ID
     * @param upstream the upstream ChatEvent stream from the AI service
     * @param store the conversation store for persisting history
     * @param resumer the response resumer for streaming replay
     * @param objectMapper Jackson ObjectMapper for JSON serialization
     * @param identity the security identity
     * @param identityAssociation the security identity association
     * @param bearerToken the bearer token for API calls
     * @return wrapped Multi that records events as they stream
     */
    public static Multi<ChatEvent> wrap(
            String conversationId,
            Multi<ChatEvent> upstream,
            ConversationStore store,
            ResponseResumer resumer,
            ObjectMapper objectMapper,
            SecurityIdentity identity,
            SecurityIdentityAssociation identityAssociation,
            String bearerToken) {

        ResponseResumer.ResponseRecorder recorder =
                resumer == null
                        ? ResponseResumer.noop().recorder(conversationId, bearerToken)
                        : resumer.recorder(conversationId, bearerToken);

        EventCoalescer coalescer = new EventCoalescer(objectMapper);

        Multi<ResponseCancelSignal> cancelStream =
                recorder.cancelStream().emitOn(Infrastructure.getDefaultExecutor());
        Multi<ChatEvent> safeUpstream = upstream.emitOn(Infrastructure.getDefaultExecutor());

        return Multi.createFrom()
                .emitter(
                        emitter ->
                                attachStreams(
                                        conversationId,
                                        safeUpstream,
                                        cancelStream,
                                        store,
                                        recorder,
                                        coalescer,
                                        objectMapper,
                                        emitter,
                                        identity,
                                        identityAssociation,
                                        bearerToken));
    }

    private static void attachStreams(
            String conversationId,
            Multi<ChatEvent> upstream,
            Multi<ResponseCancelSignal> cancelStream,
            ConversationStore store,
            ResponseResumer.ResponseRecorder recorder,
            EventCoalescer coalescer,
            ObjectMapper objectMapper,
            MultiEmitter<? super ChatEvent> emitter,
            SecurityIdentity identity,
            SecurityIdentityAssociation identityAssociation,
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
                    finishCancel(conversationId, store, coalescer, recorder, bearerToken);
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
                                    Cancellable upstreamHandle = upstreamSubscription.get();
                                    if (upstreamHandle != null) {
                                        upstreamHandle.cancel();
                                    }
                                    completeCancel.run();
                                },
                                failure -> {
                                    // Ignore cancel stream failures
                                });
        cancelSubscription.set(cancelWatcher);

        Cancellable upstreamHandle =
                upstream.subscribe()
                        .with(
                                event -> {
                                    if (canceled.get()) {
                                        return;
                                    }
                                    handleEvent(
                                            conversationId,
                                            store,
                                            recorder,
                                            coalescer,
                                            objectMapper,
                                            emitter,
                                            event);
                                },
                                failure -> {
                                    if (canceled.get()) {
                                        completeCancel.run();
                                    } else {
                                        finishFailure(
                                                conversationId,
                                                store,
                                                coalescer,
                                                recorder,
                                                emitter,
                                                failure,
                                                bearerToken);
                                        cancelWatcherStop.run();
                                    }
                                },
                                () -> {
                                    if (canceled.get()) {
                                        completeCancel.run();
                                    } else {
                                        finishSuccess(
                                                conversationId,
                                                store,
                                                coalescer,
                                                recorder,
                                                emitter,
                                                bearerToken);
                                        cancelWatcherStop.run();
                                    }
                                });
        upstreamSubscription.set(upstreamHandle);

        if (canceled.get()) {
            upstreamHandle.cancel();
            completeCancel.run();
        }
    }

    private static void handleEvent(
            String conversationId,
            ConversationStore store,
            ResponseResumer.ResponseRecorder recorder,
            EventCoalescer coalescer,
            ObjectMapper objectMapper,
            MultiEmitter<? super ChatEvent> emitter,
            ChatEvent event) {

        if (event == null) {
            return;
        }

        // Serialize event to JSON for recording and coalescing
        String eventJson;
        try {
            eventJson = objectMapper.writeValueAsString(event);
        } catch (JsonProcessingException e) {
            // Skip events that can't be serialized
            if (!emitter.isCancelled()) {
                emitter.emit(event);
            }
            return;
        }

        // Record to resumer as JSON line (for resumption)
        recorder.record(eventJson + "\n");

        // Add to coalescer (for history storage)
        coalescer.addEvent(eventJson);

        // Emit to downstream
        if (!emitter.isCancelled()) {
            emitter.emit(event);
        }
    }

    private static void finishSuccess(
            String conversationId,
            ConversationStore store,
            EventCoalescer coalescer,
            ResponseResumer.ResponseRecorder recorder,
            MultiEmitter<? super ChatEvent> emitter,
            String bearerToken) {

        try {
            storeCoalescedEvents(conversationId, store, coalescer, bearerToken);
            store.markCompleted(conversationId);
        } catch (RuntimeException e) {
            // Ignore failures to avoid breaking the response stream
        }
        recorder.complete();
        if (!emitter.isCancelled()) {
            emitter.complete();
        }
    }

    private static void finishFailure(
            String conversationId,
            ConversationStore store,
            EventCoalescer coalescer,
            ResponseResumer.ResponseRecorder recorder,
            MultiEmitter<? super ChatEvent> emitter,
            Throwable failure,
            String bearerToken) {

        // Store partial events on failure
        List<JsonNode> events = coalescer.finish();
        if (!events.isEmpty()) {
            try {
                storeCoalescedEvents(conversationId, store, coalescer, bearerToken);
                store.markCompleted(conversationId);
            } catch (RuntimeException e) {
                // Ignore to avoid masking original error
            }
        }
        recorder.complete();
        if (!emitter.isCancelled()) {
            emitter.fail(failure);
        }
    }

    private static void finishCancel(
            String conversationId,
            ConversationStore store,
            EventCoalescer coalescer,
            ResponseResumer.ResponseRecorder recorder,
            String bearerToken) {

        try {
            storeCoalescedEvents(conversationId, store, coalescer, bearerToken);
            store.markCompleted(conversationId);
        } catch (RuntimeException e) {
            // Ignore
        }
        recorder.complete();
    }

    private static void storeCoalescedEvents(
            String conversationId,
            ConversationStore store,
            EventCoalescer coalescer,
            String bearerToken) {

        List<JsonNode> events = coalescer.finish();
        String finalText = coalescer.getFinalText();

        // Store using the new history-events format
        store.appendAgentMessageWithEvents(conversationId, finalText, events, bearerToken);
    }
}
