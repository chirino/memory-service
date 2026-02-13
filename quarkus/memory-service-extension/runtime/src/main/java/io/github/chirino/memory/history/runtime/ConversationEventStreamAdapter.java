package io.github.chirino.memory.history.runtime;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import dev.langchain4j.agent.tool.ToolExecutionRequest;
import dev.langchain4j.data.message.AiMessage;
import dev.langchain4j.model.chat.response.ChatResponse;
import dev.langchain4j.model.chat.response.ChatResponseMetadata;
import dev.langchain4j.model.output.TokenUsage;
import dev.langchain4j.rag.content.Content;
import dev.langchain4j.service.tool.ToolExecution;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.infrastructure.Infrastructure;
import io.smallrye.mutiny.subscription.Cancellable;
import io.smallrye.mutiny.subscription.MultiEmitter;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import org.jboss.logging.Logger;

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

    private static final Logger LOG = Logger.getLogger(ConversationEventStreamAdapter.class);

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
        return wrap(
                conversationId,
                upstream,
                store,
                resumer,
                objectMapper,
                identity,
                identityAssociation,
                bearerToken,
                null);
    }

    /**
     * Wrap a ChatEvent stream with history recording, resumption support, and tool attachment
     * extraction.
     */
    public static Multi<ChatEvent> wrap(
            String conversationId,
            Multi<ChatEvent> upstream,
            ConversationStore store,
            ResponseResumer resumer,
            ObjectMapper objectMapper,
            SecurityIdentity identity,
            SecurityIdentityAssociation identityAssociation,
            String bearerToken,
            ToolAttachmentExtractor toolAttachmentExtractor) {

        ResponseResumer.ResponseRecorder recorder =
                resumer == null
                        ? ResponseResumer.noop().recorder(conversationId, bearerToken)
                        : resumer.recorder(conversationId, bearerToken);

        EventCoalescer coalescer = new EventCoalescer(objectMapper);
        List<Map<String, Object>> collectedAttachments = new CopyOnWriteArrayList<>();

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
                                        bearerToken,
                                        toolAttachmentExtractor,
                                        collectedAttachments));
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
            String bearerToken,
            ToolAttachmentExtractor toolAttachmentExtractor,
            List<Map<String, Object>> collectedAttachments) {

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
                    finishCancel(
                            conversationId,
                            store,
                            coalescer,
                            recorder,
                            bearerToken,
                            collectedAttachments);
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
                                            event,
                                            toolAttachmentExtractor,
                                            collectedAttachments);
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
                                                bearerToken,
                                                collectedAttachments);
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
                                                bearerToken,
                                                collectedAttachments);
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
            ChatEvent event,
            ToolAttachmentExtractor toolAttachmentExtractor,
            List<Map<String, Object>> collectedAttachments) {

        if (event == null) {
            return;
        }

        // Extract attachments directly from ChatEvent object (not JSON) to avoid
        // serialization issues and field-name mismatches with LangChain4j types.
        if (event instanceof ChatEvent.ToolExecutedEvent toolExecuted) {
            if (toolAttachmentExtractor == null) {
                LOG.debug("ToolExecutedEvent received but no ToolAttachmentExtractor is available");
            } else {
                try {
                    ToolExecution execution = toolExecuted.getExecution();
                    if (execution != null && execution.request() != null) {
                        String toolName = execution.request().name();
                        String output = execution.result();
                        LOG.debugf(
                                "ToolExecutedEvent: tool=%s, outputLength=%d",
                                toolName, output != null ? output.length() : -1);
                        if (toolName != null && output != null) {
                            List<Map<String, Object>> extracted =
                                    toolAttachmentExtractor.extract(toolName, output);
                            LOG.debugf(
                                    "ToolAttachmentExtractor returned %d attachments for tool=%s",
                                    extracted != null ? extracted.size() : 0, toolName);
                            if (extracted != null && !extracted.isEmpty()) {
                                collectedAttachments.addAll(extracted);
                            }
                        }
                    } else {
                        LOG.debug("ToolExecutedEvent has null execution or request");
                    }
                } catch (Exception e) {
                    LOG.debugf(e, "Error extracting attachments from ToolExecutedEvent");
                }
            }
        }

        // Serialize event to JSON. LangChain4j types use method-style accessors
        // (name() not getName()) with private fields, and some contain unserializable
        // types (Supplier, AtomicReference). Build JSON manually for all event types.
        String eventJson = buildEventJson(event, objectMapper);

        // Record to resumer as JSON line (for resumption)
        recorder.record(eventJson + "\n");

        // Add to coalescer (for history storage)
        coalescer.addEvent(eventJson);

        // Emit to downstream
        if (!emitter.isCancelled()) {
            emitter.emit(event);
        }
    }

    /**
     * Build JSON for any ChatEvent type. LangChain4j types use method-style accessors
     * with private fields, and some contain unserializable types (Supplier, AtomicReference),
     * so we always build JSON manually rather than relying on Jackson auto-detection.
     */
    private static String buildEventJson(ChatEvent event, ObjectMapper objectMapper) {
        Map<String, Object> json = new LinkedHashMap<>();
        String eventType = event.getEventType() != null ? event.getEventType().name() : "Unknown";
        json.put("eventType", eventType);

        try {
            if (event instanceof ChatEvent.PartialResponseEvent partial) {
                json.put("chunk", partial.getChunk());

            } else if (event instanceof ChatEvent.PartialThinkingEvent thinking) {
                json.put("text", thinking.getText());

            } else if (event instanceof ChatEvent.BeforeToolExecutionEvent before) {
                ToolExecutionRequest req = before.getRequest();
                if (req != null) {
                    json.put("id", req.id());
                    json.put("toolName", req.name());
                    json.put("arguments", req.arguments());
                }

            } else if (event instanceof ChatEvent.ToolExecutedEvent toolExecuted) {
                ToolExecution execution = toolExecuted.getExecution();
                if (execution != null) {
                    if (execution.request() != null) {
                        json.put("id", execution.request().id());
                        json.put("toolName", execution.request().name());
                    }
                    try {
                        json.put("output", execution.result());
                    } catch (Exception e) {
                        // result() may fail if lazy supplier fails
                    }
                }

            } else if (event instanceof ChatEvent.ChatCompletedEvent completed) {
                addChatResponseFields(json, completed.getChatResponse());

            } else if (event instanceof ChatEvent.IntermediateResponseEvent intermediate) {
                addChatResponseFields(json, intermediate.getChatResponse());

            } else if (event instanceof ChatEvent.AccumulatedResponseEvent accumulated) {
                json.put("message", accumulated.getMessage());
                addMetadataFields(json, accumulated.getMetadata());

            } else if (event instanceof ChatEvent.ContentFetchedEvent contentFetched) {
                List<Map<String, Object>> contentList = new ArrayList<>();
                if (contentFetched.getContent() != null) {
                    for (Content c : contentFetched.getContent()) {
                        Map<String, Object> item = new LinkedHashMap<>();
                        if (c.textSegment() != null) {
                            item.put("text", c.textSegment().text());
                        }
                        contentList.add(item);
                    }
                }
                json.put("content", contentList);
            }
        } catch (Exception e) {
            LOG.debugf(e, "Error building JSON for ChatEvent type=%s", eventType);
        }

        try {
            return objectMapper.writeValueAsString(json);
        } catch (Exception e) {
            return "{\"eventType\":\"" + eventType + "\"}";
        }
    }

    private static void addChatResponseFields(Map<String, Object> json, ChatResponse chatResponse) {
        if (chatResponse == null) {
            return;
        }
        AiMessage ai = chatResponse.aiMessage();
        if (ai != null) {
            Map<String, Object> aiJson = new LinkedHashMap<>();
            if (ai.text() != null) {
                aiJson.put("text", ai.text());
            }
            if (ai.thinking() != null) {
                aiJson.put("thinking", ai.thinking());
            }
            if (ai.hasToolExecutionRequests()) {
                List<Map<String, Object>> toolReqs = new ArrayList<>();
                for (ToolExecutionRequest req : ai.toolExecutionRequests()) {
                    Map<String, Object> reqJson = new LinkedHashMap<>();
                    reqJson.put("id", req.id());
                    reqJson.put("name", req.name());
                    reqJson.put("arguments", req.arguments());
                    toolReqs.add(reqJson);
                }
                aiJson.put("toolExecutionRequests", toolReqs);
            }
            json.put("aiMessage", aiJson);
        }
        addMetadataFields(json, chatResponse.metadata());
    }

    private static void addMetadataFields(Map<String, Object> json, ChatResponseMetadata metadata) {
        if (metadata == null) {
            return;
        }
        Map<String, Object> metaJson = new LinkedHashMap<>();
        if (metadata.id() != null) {
            metaJson.put("id", metadata.id());
        }
        if (metadata.modelName() != null) {
            metaJson.put("modelName", metadata.modelName());
        }
        if (metadata.finishReason() != null) {
            metaJson.put("finishReason", metadata.finishReason().name());
        }
        TokenUsage usage = metadata.tokenUsage();
        if (usage != null) {
            Map<String, Object> usageJson = new LinkedHashMap<>();
            if (usage.inputTokenCount() != null) {
                usageJson.put("inputTokenCount", usage.inputTokenCount());
            }
            if (usage.outputTokenCount() != null) {
                usageJson.put("outputTokenCount", usage.outputTokenCount());
            }
            if (usage.totalTokenCount() != null) {
                usageJson.put("totalTokenCount", usage.totalTokenCount());
            }
            metaJson.put("tokenUsage", usageJson);
        }
        if (!metaJson.isEmpty()) {
            json.put("metadata", metaJson);
        }
    }

    private static void finishSuccess(
            String conversationId,
            ConversationStore store,
            EventCoalescer coalescer,
            ResponseResumer.ResponseRecorder recorder,
            MultiEmitter<? super ChatEvent> emitter,
            String bearerToken,
            List<Map<String, Object>> collectedAttachments) {

        try {
            storeCoalescedEvents(
                    conversationId, store, coalescer, bearerToken, collectedAttachments);
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
            String bearerToken,
            List<Map<String, Object>> collectedAttachments) {

        // Store partial events on failure
        List<JsonNode> events = coalescer.finish();
        if (!events.isEmpty()) {
            try {
                storeCoalescedEvents(
                        conversationId, store, coalescer, bearerToken, collectedAttachments);
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
            String bearerToken,
            List<Map<String, Object>> collectedAttachments) {

        try {
            storeCoalescedEvents(
                    conversationId, store, coalescer, bearerToken, collectedAttachments);
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
            String bearerToken,
            List<Map<String, Object>> collectedAttachments) {

        List<JsonNode> events = coalescer.finish();
        String finalText = coalescer.getFinalText();

        LOG.debugf(
                "Storing %d events with %d attachments for conversation=%s",
                events.size(), collectedAttachments.size(), conversationId);

        // Store using the new history-events format, with any tool-generated attachments
        store.appendAgentMessageWithEvents(
                conversationId, finalText, events, collectedAttachments, bearerToken);
    }
}
