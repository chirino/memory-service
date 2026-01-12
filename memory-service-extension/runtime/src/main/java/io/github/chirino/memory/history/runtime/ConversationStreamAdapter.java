package io.github.chirino.memory.history.runtime;

import io.github.chirino.memory.history.api.ConversationStore;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.subscription.MultiEmitter;

public final class ConversationStreamAdapter {

    private ConversationStreamAdapter() {}

    public static Multi<String> wrap(
            String conversationId,
            Multi<String> upstream,
            ConversationStore store,
            ResponseResumer resumer) {

        ResponseResumer.ResponseRecorder recorder =
                resumer == null
                        ? ResponseResumer.noop().recorder(conversationId)
                        : resumer.recorder(conversationId);
        StringBuilder buffer = new StringBuilder();

        return Multi.createFrom()
                .emitter(
                        emitter ->
                                upstream.subscribe()
                                        .with(
                                                token ->
                                                        handleToken(
                                                                conversationId,
                                                                store,
                                                                recorder,
                                                                buffer,
                                                                emitter,
                                                                token),
                                                failure ->
                                                        finishFailure(recorder, emitter, failure),
                                                () ->
                                                        finishSuccess(
                                                                conversationId,
                                                                store,
                                                                buffer,
                                                                recorder,
                                                                emitter)));
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
            MultiEmitter<? super String> emitter) {
        try {
            store.appendAgentMessage(conversationId, buffer.toString());
            store.markCompleted(conversationId);
        } catch (RuntimeException e) {
            // Ignore failures when recording final message to avoid breaking the primary response
            // stream.
        }
        recorder.complete();
        if (!emitter.isCancelled()) {
            emitter.complete();
        }
    }
}
