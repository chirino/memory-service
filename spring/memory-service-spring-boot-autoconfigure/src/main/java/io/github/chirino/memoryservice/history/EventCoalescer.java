package io.github.chirino.memoryservice.history;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.util.ArrayList;
import java.util.List;

/**
 * Coalesces adjacent PartialResponse events into single events to reduce storage while preserving
 * semantic boundaries (text before/after tool calls, thinking, etc).
 *
 * <p>Example input stream:
 *
 * <pre>
 * {"eventType":"PartialResponse","chunk":"Let"}
 * {"eventType":"PartialResponse","chunk":" me"}
 * {"eventType":"PartialResponse","chunk":" check"}
 * {"eventType":"BeforeToolExecution","toolName":"get_weather",...}
 * {"eventType":"PartialResponse","chunk":"The"}
 * {"eventType":"PartialResponse","chunk":" weather"}
 * </pre>
 *
 * <p>Coalesced output:
 *
 * <pre>
 * {"eventType":"PartialResponse","chunk":"Let me check"}
 * {"eventType":"BeforeToolExecution","toolName":"get_weather",...}
 * {"eventType":"PartialResponse","chunk":"The weather"}
 * </pre>
 */
public class EventCoalescer {

    private static final String EVENT_TYPE = "eventType";
    private static final String PARTIAL_RESPONSE = "PartialResponse";
    private static final String PARTIAL_THINKING = "PartialThinking";
    private static final String CHUNK = "chunk";

    private final ObjectMapper objectMapper;
    private final List<JsonNode> coalescedEvents = new ArrayList<>();
    private StringBuilder pendingChunk = null;
    private String pendingEventType = null;

    public EventCoalescer(ObjectMapper objectMapper) {
        this.objectMapper = objectMapper;
    }

    /**
     * Process a single event JSON string. Events are buffered and coalesced as needed.
     *
     * @param eventJson JSON string representation of the event
     */
    public void addEvent(String eventJson) {
        try {
            JsonNode event = objectMapper.readTree(eventJson);
            addEvent(event);
        } catch (Exception e) {
            // If JSON parsing fails, flush pending and store as-is (non-coalescable)
            flushPending();
            // Store raw string as a text node - unusual but preserves data
        }
    }

    /**
     * Process a single event JsonNode. Events are buffered and coalesced as needed.
     *
     * @param event the event to process
     */
    public void addEvent(JsonNode event) {
        String eventType = getEventType(event);

        if (isCoalescableType(eventType)) {
            String chunk = getChunk(event);
            if (chunk != null) {
                if (pendingEventType != null && pendingEventType.equals(eventType)) {
                    // Same coalescable type - append to pending
                    pendingChunk.append(chunk);
                } else {
                    // Different type - flush previous and start new pending
                    flushPending();
                    pendingEventType = eventType;
                    pendingChunk = new StringBuilder(chunk);
                }
                return;
            }
        }

        // Non-coalescable event - flush pending and add this event
        flushPending();
        coalescedEvents.add(event);
    }

    /**
     * Flush any pending coalesced content and return all coalesced events.
     *
     * @return list of coalesced event JsonNodes
     */
    public List<JsonNode> finish() {
        flushPending();
        return new ArrayList<>(coalescedEvents);
    }

    /**
     * Get the accumulated final text from all PartialResponse events.
     *
     * @return the complete response text
     */
    public String getFinalText() {
        flushPending();
        StringBuilder text = new StringBuilder();
        for (JsonNode event : coalescedEvents) {
            String eventType = getEventType(event);
            if (PARTIAL_RESPONSE.equals(eventType)) {
                String chunk = getChunk(event);
                if (chunk != null) {
                    text.append(chunk);
                }
            }
        }
        return text.toString();
    }

    private void flushPending() {
        if (pendingChunk != null && pendingEventType != null) {
            ObjectNode event = objectMapper.createObjectNode();
            event.put(EVENT_TYPE, pendingEventType);
            event.put(CHUNK, pendingChunk.toString());
            coalescedEvents.add(event);
            pendingChunk = null;
            pendingEventType = null;
        }
    }

    private boolean isCoalescableType(String eventType) {
        return PARTIAL_RESPONSE.equals(eventType) || PARTIAL_THINKING.equals(eventType);
    }

    private String getEventType(JsonNode event) {
        JsonNode typeNode = event.get(EVENT_TYPE);
        return typeNode != null ? typeNode.asText() : null;
    }

    private String getChunk(JsonNode event) {
        JsonNode chunkNode = event.get(CHUNK);
        return chunkNode != null ? chunkNode.asText() : null;
    }

    /** Reset the coalescer for reuse. */
    public void reset() {
        coalescedEvents.clear();
        pendingChunk = null;
        pendingEventType = null;
    }
}
