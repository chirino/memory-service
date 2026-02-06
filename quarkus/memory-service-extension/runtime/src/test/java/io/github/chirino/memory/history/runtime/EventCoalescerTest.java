package io.github.chirino.memory.history.runtime;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Tests for {@link EventCoalescer}.
 *
 * <p>Verifies that:
 *
 * <ul>
 *   <li>Adjacent PartialResponse events are merged into single events
 *   <li>Adjacent PartialThinking events are merged into single events
 *   <li>Non-coalescable events (tool calls, etc.) pass through unchanged
 *   <li>Mixed event streams produce correct output with semantic boundaries
 *   <li>getFinalText() extracts only PartialResponse text
 *   <li>Empty streams produce empty output
 * </ul>
 */
class EventCoalescerTest {

    private ObjectMapper objectMapper;
    private EventCoalescer coalescer;

    @BeforeEach
    void setUp() {
        objectMapper = new ObjectMapper();
        coalescer = new EventCoalescer(objectMapper);
    }

    @Test
    void testCoalesceAdjacentPartialResponse() {
        // Given: Multiple adjacent PartialResponse events
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Hello\"}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\" \"}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"World\"}");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Should produce single merged event
        assertThat(events).hasSize(1);
        JsonNode event = events.get(0);
        assertThat(event.get("eventType").asText()).isEqualTo("PartialResponse");
        assertThat(event.get("chunk").asText()).isEqualTo("Hello World");
    }

    @Test
    void testCoalesceAdjacentPartialThinking() {
        // Given: Multiple adjacent PartialThinking events
        coalescer.addEvent("{\"eventType\":\"PartialThinking\",\"chunk\":\"Let me\"}");
        coalescer.addEvent("{\"eventType\":\"PartialThinking\",\"chunk\":\" think\"}");
        coalescer.addEvent("{\"eventType\":\"PartialThinking\",\"chunk\":\"...\"}");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Should produce single merged thinking event
        assertThat(events).hasSize(1);
        JsonNode event = events.get(0);
        assertThat(event.get("eventType").asText()).isEqualTo("PartialThinking");
        assertThat(event.get("chunk").asText()).isEqualTo("Let me think...");
    }

    @Test
    void testPreserveNonCoalescable() {
        // Given: Non-coalescable events (tool calls)
        String toolEvent =
                "{\"eventType\":\"BeforeToolExecution\",\"toolName\":\"get_weather\",\"input\":{\"city\":\"Seattle\"}}";
        coalescer.addEvent(toolEvent);

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Event should pass through unchanged
        assertThat(events).hasSize(1);
        JsonNode event = events.get(0);
        assertThat(event.get("eventType").asText()).isEqualTo("BeforeToolExecution");
        assertThat(event.get("toolName").asText()).isEqualTo("get_weather");
        assertThat(event.get("input").get("city").asText()).isEqualTo("Seattle");
    }

    @Test
    void testMixedEvents() {
        // Given: Mixed event stream with tool call in between text
        // PR, PR, Tool, PR, PR -> should produce 3 events
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Let me\"}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\" check\"}");
        coalescer.addEvent(
                "{\"eventType\":\"BeforeToolExecution\",\"toolName\":\"get_weather\",\"input\":{}}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"The\"}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\" weather\"}");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Should produce 3 events (merged PR, Tool, merged PR)
        assertThat(events).hasSize(3);

        assertThat(events.get(0).get("eventType").asText()).isEqualTo("PartialResponse");
        assertThat(events.get(0).get("chunk").asText()).isEqualTo("Let me check");

        assertThat(events.get(1).get("eventType").asText()).isEqualTo("BeforeToolExecution");
        assertThat(events.get(1).get("toolName").asText()).isEqualTo("get_weather");

        assertThat(events.get(2).get("eventType").asText()).isEqualTo("PartialResponse");
        assertThat(events.get(2).get("chunk").asText()).isEqualTo("The weather");
    }

    @Test
    void testMixedResponseAndThinking() {
        // Given: Mixed PartialResponse and PartialThinking events
        coalescer.addEvent("{\"eventType\":\"PartialThinking\",\"chunk\":\"Thinking\"}");
        coalescer.addEvent("{\"eventType\":\"PartialThinking\",\"chunk\":\"...\"}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Answer\"}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\" here\"}");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Should produce 2 events (merged thinking, merged response)
        assertThat(events).hasSize(2);

        assertThat(events.get(0).get("eventType").asText()).isEqualTo("PartialThinking");
        assertThat(events.get(0).get("chunk").asText()).isEqualTo("Thinking...");

        assertThat(events.get(1).get("eventType").asText()).isEqualTo("PartialResponse");
        assertThat(events.get(1).get("chunk").asText()).isEqualTo("Answer here");
    }

    @Test
    void testGetFinalText() {
        // Given: Mixed events including tool calls
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Hello\"}");
        coalescer.addEvent(
                "{\"eventType\":\"BeforeToolExecution\",\"toolName\":\"test\",\"input\":{}}");
        coalescer.addEvent("{\"eventType\":\"PartialThinking\",\"chunk\":\"thinking...\"}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\" World\"}");

        // When: Get final text
        String finalText = coalescer.getFinalText();

        // Then: Should extract only PartialResponse text
        assertThat(finalText).isEqualTo("Hello World");
    }

    @Test
    void testEmptyStream() {
        // Given: No events added

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Should return empty list
        assertThat(events).isEmpty();
    }

    @Test
    void testReset() {
        // Given: Events added to coalescer
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Hello\"}");
        assertThat(coalescer.finish()).hasSize(1);

        // When: Reset and add new events
        coalescer.reset();
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"New\"}");

        // Then: Should only contain new events
        List<JsonNode> events = coalescer.finish();
        assertThat(events).hasSize(1);
        assertThat(events.get(0).get("chunk").asText()).isEqualTo("New");
    }

    @Test
    void testToolExecutedEvent() {
        // Given: ToolExecuted event with output
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Checking...\"}");
        coalescer.addEvent(
                "{\"eventType\":\"ToolExecuted\",\"toolName\":\"get_weather\",\"output\":{\"temp\":72}}");
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Done\"}");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Tool event should pass through with output
        assertThat(events).hasSize(3);
        JsonNode toolEvent = events.get(1);
        assertThat(toolEvent.get("eventType").asText()).isEqualTo("ToolExecuted");
        assertThat(toolEvent.get("toolName").asText()).isEqualTo("get_weather");
        assertThat(toolEvent.get("output").get("temp").asInt()).isEqualTo(72);
    }

    @Test
    void testAddEventWithJsonNode() throws Exception {
        // Given: JsonNode event
        JsonNode event =
                objectMapper.readTree("{\"eventType\":\"PartialResponse\",\"chunk\":\"Test\"}");

        // When: Add as JsonNode
        coalescer.addEvent(event);

        // Then: Should work correctly
        List<JsonNode> events = coalescer.finish();
        assertThat(events).hasSize(1);
        assertThat(events.get(0).get("chunk").asText()).isEqualTo("Test");
    }

    @Test
    void testInvalidJson() {
        // Given: Invalid JSON string
        coalescer.addEvent("not valid json");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Should handle gracefully (event is lost but no exception)
        assertThat(events).isEmpty();
    }

    @Test
    void testSinglePartialResponse() {
        // Given: Single PartialResponse event
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Single\"}");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Should produce single event
        assertThat(events).hasSize(1);
        assertThat(events.get(0).get("chunk").asText()).isEqualTo("Single");
    }

    @Test
    void testEventWithNoChunk() {
        // Given: Event with eventType but no chunk field
        coalescer.addEvent("{\"eventType\":\"PartialResponse\"}");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Should pass through as non-coalescable (no chunk means it can't be merged)
        assertThat(events).hasSize(1);
        assertThat(events.get(0).get("eventType").asText()).isEqualTo("PartialResponse");
    }

    @Test
    void testChatCompletedEvent() {
        // Given: ChatCompleted event
        coalescer.addEvent("{\"eventType\":\"PartialResponse\",\"chunk\":\"Done\"}");
        coalescer.addEvent("{\"eventType\":\"ChatCompleted\",\"finishReason\":\"STOP\"}");

        // When: Finish coalescing
        List<JsonNode> events = coalescer.finish();

        // Then: Both events should be present
        assertThat(events).hasSize(2);
        assertThat(events.get(1).get("eventType").asText()).isEqualTo("ChatCompleted");
        assertThat(events.get(1).get("finishReason").asText()).isEqualTo("STOP");
    }
}
