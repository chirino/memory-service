package io.github.chirino.memoryservice.history;

import io.github.chirino.memoryservice.history.ResponseResumer.ResponseRecorder;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.chat.client.ChatClientRequest;
import org.springframework.ai.chat.client.ChatClientResponse;
import org.springframework.ai.chat.client.advisor.api.CallAdvisor;
import org.springframework.ai.chat.client.advisor.api.CallAdvisorChain;
import org.springframework.ai.chat.client.advisor.api.StreamAdvisor;
import org.springframework.ai.chat.client.advisor.api.StreamAdvisorChain;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.ai.chat.messages.AssistantMessage;
import org.springframework.ai.chat.model.ChatResponse;
import org.springframework.ai.chat.model.Generation;
import org.springframework.ai.chat.prompt.Prompt;
import org.springframework.core.Ordered;
import org.springframework.lang.Nullable;
import org.springframework.util.StringUtils;
import reactor.core.Disposable;
import reactor.core.publisher.Flux;
import reactor.core.publisher.FluxSink;
import reactor.core.publisher.Mono;
import reactor.core.scheduler.Scheduler;
import reactor.core.scheduler.Schedulers;

/**
 * Stream advisor that records conversation history to the memory-service.
 *
 * <p>This advisor should be created per-request using {@link
 * ConversationHistoryStreamAdvisorBuilder} with a bearer token captured on the HTTP request thread.
 * This ensures the token is available throughout the advisor's lifecycle, even when processing
 * moves to worker threads where the SecurityContext is not available.
 */
public class ConversationHistoryStreamAdvisor implements CallAdvisor, StreamAdvisor {

    private static final Logger LOG =
            LoggerFactory.getLogger(ConversationHistoryStreamAdvisor.class);

    private final ConversationStore conversationStore;
    private final ResponseResumer responseResumer;
    private final String bearerToken;

    public ConversationHistoryStreamAdvisor(
            ConversationStore conversationStore,
            ResponseResumer responseResumer,
            @Nullable String bearerToken) {
        this.conversationStore = conversationStore;
        this.responseResumer = responseResumer;
        this.bearerToken = bearerToken;
    }

    @Override
    public ChatClientResponse adviseCall(ChatClientRequest request, CallAdvisorChain chain) {
        return chain.nextCall(request);
    }

    @Override
    public Flux<ChatClientResponse> adviseStream(
            ChatClientRequest request, StreamAdvisorChain chain) {
        String conversationId = resolveConversationId(request);
        if (!StringUtils.hasText(conversationId)) {
            return chain.nextStream(request);
        }

        LOG.info(
                "adviseStream: conversationId={} bearerTokenPresent={} thread={}",
                conversationId,
                StringUtils.hasText(bearerToken),
                Thread.currentThread().getName());

        Scheduler scheduler = Schedulers.boundedElastic();

        return Mono.just(request)
                .publishOn(scheduler)
                .map(
                        req -> {
                            safeAppendUserMessage(
                                    conversationId, resolveUserMessage(req), bearerToken);
                            return req;
                        })
                .flatMapMany(
                        req -> {
                            ResponseRecorder recorder =
                                    responseResumer.recorder(conversationId, bearerToken);
                            Flux<ChatClientResponse> upstream = chain.nextStream(req);
                            return wrapStream(upstream, conversationId, bearerToken, recorder);
                        });
    }

    @Override
    public String getName() {
        return "conversationHistory";
    }

    @Override
    public int getOrder() {
        // Use highest precedence to be first in the chain, receiving the
        // original user message before any other advisors can modify it
        return Ordered.HIGHEST_PRECEDENCE;
    }

    private Flux<ChatClientResponse> wrapStream(
            Flux<ChatClientResponse> upstream,
            String conversationId,
            @Nullable String bearerToken,
            ResponseRecorder recorder) {
        return Flux.create(
                (FluxSink<ChatClientResponse> sink) -> {
                    AtomicBoolean canceled = new AtomicBoolean(false);
                    AtomicBoolean finalized = new AtomicBoolean(false);
                    AtomicReference<Disposable> upstreamRef = new AtomicReference<>();
                    AtomicReference<Disposable> cancelRef = new AtomicReference<>();
                    StringBuilder buffer = new StringBuilder();

                    Disposable cancelSubscription =
                            recorder.cancelStream()
                                    .subscribe(
                                            signal -> {
                                                if (signal != ResponseCancelSignal.CANCEL) {
                                                    return;
                                                }
                                                if (!canceled.compareAndSet(false, true)) {
                                                    return;
                                                }
                                                cancelUpstream(upstreamRef.get());
                                                finalizeConversation(
                                                        conversationId,
                                                        buffer,
                                                        recorder,
                                                        bearerToken,
                                                        sink,
                                                        finalized);
                                            },
                                            failure ->
                                                    LOG.warn(
                                                            "Cancel stream errored, ignoring",
                                                            failure));
                    cancelRef.set(cancelSubscription);

                    // Use scheduler for blocking operations as recommended in Spring AI docs
                    Scheduler scheduler = Schedulers.boundedElastic();

                    Disposable upstreamSubscription =
                            upstream.publishOn(scheduler)
                                    .subscribe(
                                            response -> {
                                                if (canceled.get()) {
                                                    return;
                                                }
                                                recordChunk(
                                                        conversationId, buffer, recorder, response);
                                                sink.next(response);
                                            },
                                            failure -> {
                                                cancelRef.get().dispose();
                                                if (finalized.compareAndSet(false, true)) {
                                                    recorder.complete();
                                                    sink.error(failure);
                                                }
                                            },
                                            () -> {
                                                cancelRef.get().dispose();
                                                finalizeConversation(
                                                        conversationId,
                                                        buffer,
                                                        recorder,
                                                        bearerToken,
                                                        sink,
                                                        finalized);
                                            });
                    upstreamRef.set(upstreamSubscription);

                    sink.onCancel(
                            () -> {
                                cancelUpstream(upstreamRef.get());
                                Disposable cancelHandle = cancelRef.get();
                                if (cancelHandle != null && !cancelHandle.isDisposed()) {
                                    cancelHandle.dispose();
                                }
                                finalizeConversation(
                                        conversationId,
                                        buffer,
                                        recorder,
                                        bearerToken,
                                        sink,
                                        finalized);
                            });
                });
    }

    private void recordChunk(
            String conversationId,
            StringBuilder buffer,
            ResponseRecorder recorder,
            ChatClientResponse response) {
        String chunk = extractChunk(response);
        if (!StringUtils.hasText(chunk)) {
            return;
        }
        buffer.append(chunk);
        try {
            conversationStore.appendPartialAgentMessage(conversationId, chunk);
        } catch (Exception e) {
            LOG.debug("Failed to append partial token", e);
        }
        recorder.record(chunk);
    }

    private void finalizeConversation(
            String conversationId,
            StringBuilder buffer,
            ResponseRecorder recorder,
            @Nullable String bearerToken,
            FluxSink<ChatClientResponse> sink,
            AtomicBoolean finalized) {
        if (!finalized.compareAndSet(false, true)) {
            return;
        }
        try {
            conversationStore.appendAgentMessage(conversationId, buffer.toString(), bearerToken);
            conversationStore.markCompleted(conversationId);
        } catch (Exception e) {
            LOG.debug("Failed to append final agent message", e);
        }
        recorder.complete();
        sink.complete();
    }

    private void safeAppendUserMessage(
            String conversationId, @Nullable String message, @Nullable String bearerToken) {
        if (!StringUtils.hasText(message)) {
            return;
        }
        try {
            conversationStore.appendUserMessage(conversationId, message, bearerToken);
        } catch (Exception e) {
            LOG.debug("Failed to append user message for conversationId={}", conversationId, e);
        }
    }

    private String resolveConversationId(ChatClientRequest request) {
        Object potential = request.context().get(ChatMemory.CONVERSATION_ID);
        if (potential instanceof String value && StringUtils.hasText(value)) {
            return value;
        }
        return ChatMemory.DEFAULT_CONVERSATION_ID;
    }

    private @Nullable String resolveUserMessage(ChatClientRequest request) {
        Prompt prompt = request.prompt();
        if (prompt == null || prompt.getUserMessage() == null) {
            return null;
        }
        return prompt.getUserMessage().getText();
    }

    private String extractChunk(ChatClientResponse response) {
        ChatResponse payload = response.chatResponse();
        if (payload == null) {
            return null;
        }
        StringBuilder builder = new StringBuilder();
        for (Generation generation : payload.getResults()) {
            Object output = generation.getOutput();
            if (output instanceof AssistantMessage assistant) {
                String text = assistant.getText();
                if (StringUtils.hasText(text)) {
                    builder.append(text);
                }
                continue;
            }
            if (output != null) {
                builder.append(output.toString());
            }
        }
        return builder.length() == 0 ? null : builder.toString();
    }

    private void cancelUpstream(@Nullable Disposable upstream) {
        if (upstream != null && !upstream.isDisposed()) {
            upstream.dispose();
        }
    }
}
