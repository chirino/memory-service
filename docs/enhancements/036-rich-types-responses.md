# 036: Rich Event Types in History Entries and Resumable Streams

## Status
Implemented (Quarkus complete, Spring partial - see Known Limitations)

## Current State

The memory-service handles history entries and resumable response streams using simple string tokens:

1. **History Entries**: Store messages with `content_type: "history"` containing `{text, role}` objects
2. **Resumable Streams**: The `ResponseResumerBackend` records and replays raw string tokens via temp files

## Reason for Change

AI systems produce richer event types during streaming. In Quarkus LangChain4j, the streaming API returns `Multi<ChatEvent>` with events like:

| Event Type | Description |
|------------|-------------|
| `PartialResponseEvent` | A partial text token from the main response |
| `PartialThinkingEvent` | Reasoning/thinking chunks (experimental) |
| `BeforeToolExecutionEvent` | Emitted before tool execution |
| `ToolExecutedEvent` | Tool execution metadata |
| `ContentFetchedEvent` | RAG content retrieval |
| `IntermediateResponseEvent` | Intermediate response during multi-turn tool interactions |
| `ChatCompletedEvent` | Final response with complete metadata |

**Example SSE output from `/chat-detailed` endpoint:**
```
data:{"chunk":" if","eventType":"PartialResponse"}
data:{"chunk":" you","eventType":"PartialResponse"}
data:{"eventType":"BeforeToolExecution","toolName":"get_weather","input":{"city":"Seattle"}}
data:{"eventType":"ToolExecuted","toolName":"get_weather","output":{"temp":72}}
data:{"chunk":"The weather","eventType":"PartialResponse"}
```

Applications want to:
1. **Store** rich event metadata in history (tool calls, thinking, RAG sources)
2. **Resume** streams with full event fidelity
3. **Display** richer UIs with tool calls, thinking sections, etc.

---

## New Design

### History Content Type

The `content_type` field uses a subtype convention to identify the event format:

| Content Type | Description |
|--------------|-------------|
| `history` | Simple text-only history (backward compatible) |
| `history/lc4j` | LangChain4j `ChatEvent` types (Quarkus) |

```typescript
interface HistoryBlock {
  role: "USER" | "AI";
  text?: string;      // Simple token stream recording (Multi<String>)
  events?: Event[];   // Rich event stream recording (Multi<ChatEvent>)
}
```

- Use `history` with `text` for simple token streams
- Use `history/lc4j` with `events` for LangChain4j rich event streams
- Both `text` and `events` can be present: `text` for search indexing, `events` for rich UI
- Event structure is not validated per subtype - stored as provided

**Example (simple text):**
```json
{
  "content_type": "history",
  "content": [
    { "role": "USER", "text": "What's the weather in Seattle?" },
    { "role": "AI", "text": "The weather in Seattle is 72°F" }
  ]
}
```

**Example (LangChain4j events):**
```json
{
  "content_type": "history/lc4j",
  "content": [
    { "role": "USER", "text": "What's the weather in Seattle?" },
    {
      "role": "AI",
      "text": "Let me check. The weather in Seattle is 72°F",
      "events": [
        {"eventType": "PartialResponse", "chunk": "Let me check"},
        {"eventType": "BeforeToolExecution", "toolName": "get_weather", "input": {"city": "Seattle"}},
        {"eventType": "ToolExecuted", "toolName": "get_weather", "output": {"temp": 72}},
        {"eventType": "PartialResponse", "chunk": "The weather in Seattle is 72°F"}
      ]
    }
  ]
}
```

### Event Coalescing

Adjacent `PartialResponse` events are merged for storage efficiency while preserving semantic boundaries:

```
Streaming:  "Let" → " me" → " check" → [tool call] → "The" → " weather"
Stored:     "Let me check" → [tool call] → "The weather"
```

### Resumption with JSON Lines

Reuse the existing `ResponseResumerBackend` with JSON-encoded events:

1. **Recorder**: Appends each event as JSON followed by `\n`
2. **Resumer**: Buffers incoming bytes and splits on `\n` before emitting

```
Recording:  {"e":"A"}\n  then  {"e":"B"}\n
File:       {"e":"A"}\n{"e":"B"}\n
Resumer:    "{"e":"A"}"  then  "{"e":"B"}"  (complete events)
```

Clients receive complete JSON events, one per emission.

### Resume API Example

The resumer returns `Multi<ChatEvent>` to mirror what was recorded:

**Quarkus endpoint:**
```java
@GET
@Path("/{conversationId}/resume")
@Produces(MediaType.SERVER_SENT_EVENTS)
public Multi<String> resume(@PathParam("conversationId") String conversationId) {
    String bearerToken = bearerToken(securityIdentity);

    // Returns Multi<ChatEvent> - mirrors what was recorded
    return resumer.replayEvents(conversationId, bearerToken)
            .map(this::toJson);  // Convert to JSON for SSE
}

private String toJson(ChatEvent event) {
    return objectMapper.writeValueAsString(event);
}
```

**Resumer interface:**
```java
public interface ResponseResumer {
    // For simple token streams
    Multi<String> replay(String conversationId, String bearerToken);

    // For rich event streams - choose return type:
    // - String.class: raw JSON lines (no decode/re-encode overhead)
    // - ChatEvent.class: deserialized objects
    <T> Multi<T> replayEvents(String conversationId, String bearerToken, Class<T> type);
}
```

**Usage - raw JSON (efficient for SSE):**
```java
return resumer.replayEvents(conversationId, bearerToken, String.class);
// Returns: Multi<String> with complete JSON lines
```

**Usage - deserialized (for processing):**
```java
return resumer.replayEvents(conversationId, bearerToken, ChatEvent.class)
        .filter(e -> e instanceof PartialResponseEvent);
// Returns: Multi<ChatEvent> for manipulation
```

**Client receives SSE stream:**
```
data:{"eventType":"PartialResponse","chunk":"Let me check"}

data:{"eventType":"BeforeToolExecution","toolName":"get_weather","input":{"city":"Seattle"}}

data:{"eventType":"ToolExecuted","toolName":"get_weather","output":{"temp":72}}

data:{"eventType":"PartialResponse","chunk":"The weather in Seattle is 72°F"}
```

### Response Type Support

| Response Type | Quarkus | Spring | Resumption? | History Entry? |
|---------------|---------|--------|-------------|----------------|
| Non-streaming (sync) | `String` return | `ChatClientResponse` | ❌ No (not needed) | ✅ Yes |
| Simple streaming | `Multi<String>` | `Flux<String>` | ✅ Yes | ✅ Yes |
| Rich streaming | `Multi<ChatEvent>` | `Flux<ChatClientResponse>` | ✅ Yes (new) | ✅ Yes (with events) |

**Resumption is only for streaming**: Non-streaming responses complete immediately, so there's nothing to resume. The recorder is only used for streaming responses.

**Simple streams must still work**: The existing `Multi<String>` / `Flux<String>` recording must continue to work unchanged. Rich event recording is additive.

### Event Filtering

All events emitted by the AI are stored. **The application is responsible for filtering** out any events that should not be recorded before they reach the history recording system.

---

## Spring AI Comparison

### Spring AI Event Model

Spring AI uses `Flux<ChatClientResponse>` for streaming, where each response contains:

```java
ChatClientResponse
└── ChatResponse chatResponse()
    └── List<Generation> getResults()
        └── Generation
            ├── AssistantMessage getOutput()
            │   ├── String getText()           // partial text chunk
            │   └── List<ToolCall> getToolCalls()  // tool call requests
            └── ChatGenerationMetadata getMetadata()
                └── String getFinishReason()   // STOP, TOOL_CALLS, etc.
```

**Key difference from Quarkus**: Spring AI doesn't have a `ChatEvent` hierarchy. Instead, rich metadata is embedded in the `ChatClientResponse` structure.

**Known limitation**: In streaming mode, `toolCalls` may not be retained in aggregated messages ([GitHub issue #3366](https://github.com/spring-projects/spring-ai/issues/3366)).

### Spring AI Event Mapping

Map Spring AI responses to the same event format used by Quarkus:

| Spring AI Source | Event Type | Data |
|------------------|------------|------|
| `assistantMessage.getText()` | `PartialResponse` | `{chunk: "..."}` |
| `assistantMessage.getToolCalls()` | `ToolExecuted` | `{toolName, input, output}` |
| `metadata.getFinishReason()` | `ChatCompleted` | `{finishReason}` |

### Spring AI Recorder Example

```java
@Component
public class SpringEventStreamAdapter {

    private final ObjectMapper objectMapper;

    public Flux<ChatClientResponse> wrapWithRecording(
            Flux<ChatClientResponse> upstream,
            ResponseRecorder recorder,
            EventCoalescer coalescer) {

        return upstream.doOnNext(response -> {
            // Extract and record events from ChatClientResponse
            ChatResponse chat = response.chatResponse();
            if (chat == null) return;

            for (Generation gen : chat.getResults()) {
                AssistantMessage msg = (AssistantMessage) gen.getOutput();

                // Record text chunk
                String text = msg.getText();
                if (text != null && !text.isEmpty()) {
                    String event = toJson(Map.of(
                        "eventType", "PartialResponse",
                        "chunk", text
                    ));
                    recorder.record(event + "\n");
                    coalescer.addEvent(event);
                }

                // Record tool calls (if available)
                for (ToolCall tool : msg.getToolCalls()) {
                    String event = toJson(Map.of(
                        "eventType", "ToolExecuted",
                        "toolName", tool.name(),
                        "input", tool.arguments()
                    ));
                    recorder.record(event + "\n");
                    coalescer.addEvent(event);
                }
            }
        });
    }
}
```

### Spring AI Resume Example

```java
@GetMapping(value = "/{conversationId}/resume", produces = MediaType.TEXT_EVENT_STREAM_VALUE)
public Flux<String> resume(@PathVariable String conversationId) {
    // Returns raw JSON lines (efficient - no decode/re-encode)
    return resumer.replayEvents(conversationId, bearerToken, String.class);
}
```

### References

- [Spring AI Chat Client API](https://docs.spring.io/spring-ai/reference/api/chatclient.html)
- [Spring AI Tool Calling](https://docs.spring.io/spring-ai/reference/api/tools.html)
- [Streaming tool calls issue](https://github.com/spring-projects/spring-ai/issues/3366)

---

## Implementation

### Prototype Components (Quarkus)

- [EventCoalescer.java](../../quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/EventCoalescer.java) - Coalesces adjacent PartialResponse/PartialThinking events
- [ConversationEventStreamAdapter.java](../../quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ConversationEventStreamAdapter.java) - Wraps `Multi<ChatEvent>` with recording
- [JsonLineBufferingTransformer.java](../../quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/JsonLineBufferingTransformer.java) - Buffers replay stream and emits complete JSON lines

### Changes Required

**memory-service (core)**
- Update "history" validation to accept `{role, text?, events?}` structure

**history-recording library (Quarkus/Spring)**
- Detect `Multi<ChatEvent>` return types automatically
- Record events as JSON lines with newline delimiter
- Coalesce events and create history entry on stream completion

**chat-frontend**
- Check for `events` array and render rich UI if present
- Tool call display component
- Thinking/reasoning collapsible section

### Example: Agent Integration

```java
@RecordConversation  // Automatically handles Multi<ChatEvent>
public Multi<ChatEvent> chat(
    @ConversationId String conversationId,
    @UserMessage String userMessage) {
    return agent.chatDetailed(conversationId, userMessage);
}
```

---

## Implementation Plan

### Phase 1: Core Infrastructure

#### 1.1 Memory Service - History Validation

**File:** `memory-service/src/main/java/.../validation/HistoryContentValidator.java`

Update the "history" content type validation to accept the new structure:

```java
// Accept: {role: "USER"|"AI", text?: string, events?: object[]}
// - role is required
// - text is optional (present for simple streams or as search index for rich streams)
// - events is optional (present only for rich event streams)
```

**Changes:**
- Modify validation to make `text` optional when `events` is present
- Add validation for `events` array structure (must have `eventType` field)
- Update OpenAPI spec with new schema

#### 1.2 ResponseResumer Interface Extension

**Quarkus file:** `quarkus/memory-service-extension/runtime/src/main/java/.../ResponseResumer.java`
**Spring file:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/.../ResponseResumer.java`

Add the new `replayEvents` method:

```java
/**
 * Replay rich event stream with type-safe return.
 * @param type String.class for raw JSON lines, or event class for deserialized objects
 */
<T> Multi<T> replayEvents(String conversationId, String bearerToken, Class<T> type);

// Spring equivalent uses Flux<T>
```

**Implementation:**
1. Call existing `replay()` to get `Multi<String>` / `Flux<String>` raw stream
2. Apply `JsonLineBufferingTransformer.bufferLines()` to get complete JSON lines
3. If `type == String.class`, return buffered stream directly
4. Otherwise, deserialize each line to the requested type

### Phase 2: Quarkus Implementation

#### 2.1 ConversationInterceptor Enhancement

**File:** `quarkus/memory-service-extension/runtime/src/main/java/.../ConversationInterceptor.java`

Current code (line 46-48):
```java
if (result instanceof Multi<?> multi) {
    Multi<String> stringMulti = (Multi<String>) multi;
    return store.appendAgentMessage(invocation.conversationId(), stringMulti);
}
```

Updated code:
```java
if (result instanceof Multi<?> multi) {
    // Check the generic type to determine which adapter to use
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
```

Helper method:
```java
private boolean isChatEventMulti(Type type) {
    if (type instanceof ParameterizedType pt) {
        Type[] args = pt.getActualTypeArguments();
        if (args.length == 1 && args[0] instanceof Class<?> cls) {
            return ChatEvent.class.isAssignableFrom(cls);
        }
    }
    return false;
}
```

#### 2.2 GrpcResponseResumer Enhancement

**File:** `quarkus/memory-service-extension/runtime/src/main/java/.../GrpcResponseResumer.java`

Add the `replayEvents` implementation:
```java
@Override
public <T> Multi<T> replayEvents(String conversationId, String bearerToken, Class<T> type) {
    Multi<String> raw = replay(conversationId, bearerToken);
    Multi<String> buffered = JsonLineBufferingTransformer.bufferLines(raw);

    if (type == String.class) {
        @SuppressWarnings("unchecked")
        Multi<T> result = (Multi<T>) buffered;
        return result;
    }

    return buffered.map(json -> objectMapper.readValue(json, type));
}
```

#### 2.3 Chat Example Endpoint

**File:** `quarkus/examples/chat-quarkus/src/main/java/.../ChatResource.java`

Add `/chat-detailed` endpoint for rich event streaming:
```java
@POST
@Path("/{conversationId}/chat-detailed")
@Produces(MediaType.SERVER_SENT_EVENTS)
public Multi<String> chatDetailed(
        @PathParam("conversationId") UUID conversationId,
        String userMessage) {
    return agent.chatDetailed(conversationId.toString(), userMessage)
            .map(this::toJson);
}
```

Add resume endpoint for rich events:
```java
@GET
@Path("/{conversationId}/resume-events")
@Produces(MediaType.SERVER_SENT_EVENTS)
public Multi<String> resumeEvents(@PathParam("conversationId") UUID conversationId) {
    return resumer.replayEvents(
            conversationId.toString(),
            bearerToken(securityIdentity),
            String.class);
}
```

### Phase 3: Spring Implementation

#### 3.1 SpringEventCoalescer

**New file:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/.../history/EventCoalescer.java`

Port the Quarkus `EventCoalescer` to Spring:
- Same coalescing logic for PartialResponse events
- Use `ObjectMapper` for JSON handling
- Return `List<Map<String, Object>>` instead of `List<JsonNode>`

#### 3.2 ConversationHistoryStreamAdvisor Enhancement

**File:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/.../ConversationHistoryStreamAdvisor.java`

The existing `adviseStream` method records `Flux<ChatClientResponse>` as simple text. Add option for rich event recording:

```java
private void recordChunkWithEvents(
        String conversationId,
        StringBuilder buffer,
        EventCoalescer coalescer,
        ResponseRecorder recorder,
        ChatClientResponse response) {

    ChatResponse chat = response.chatResponse();
    if (chat == null) return;

    for (Generation gen : chat.getResults()) {
        AssistantMessage msg = (AssistantMessage) gen.getOutput();

        // Record text as PartialResponse event
        String text = msg.getText();
        if (StringUtils.hasText(text)) {
            buffer.append(text);
            Map<String, Object> event = Map.of(
                "eventType", "PartialResponse",
                "chunk", text
            );
            String eventJson = toJson(event);
            recorder.record(eventJson + "\n");
            coalescer.addEvent(eventJson);
        }

        // Record tool calls (if available - see Spring AI #3366 limitation)
        List<ToolCall> toolCalls = msg.getToolCalls();
        if (toolCalls != null) {
            for (ToolCall tool : toolCalls) {
                Map<String, Object> event = Map.of(
                    "eventType", "ToolExecuted",
                    "toolName", tool.name(),
                    "input", tool.arguments()
                );
                String eventJson = toJson(event);
                recorder.record(eventJson + "\n");
                coalescer.addEvent(eventJson);
            }
        }
    }
}
```

#### 3.3 ConversationStore Enhancement

**File:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/.../history/ConversationStore.java`

Add method for storing rich events:
```java
public void appendAgentMessageWithEvents(
        String conversationId,
        String text,
        List<Map<String, Object>> events,
        @Nullable String bearerToken) {

    Map<String, Object> block = new HashMap<>();
    block.put("role", "AI");
    block.put("text", text);
    block.put("events", events);

    CreateEntryRequest request = new CreateEntryRequest();
    request.channel(Channel.HISTORY);
    request.contentType("history");
    request.content(List.of(block));
    // ... rest of implementation
}
```

#### 3.4 GrpcResponseResumer Enhancement

**File:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/.../history/GrpcResponseResumer.java`

Add `replayEvents` implementation (same pattern as Quarkus):
```java
public <T> Flux<T> replayEvents(String conversationId, String bearerToken, Class<T> type) {
    Flux<String> raw = replay(conversationId, bearerToken);
    Flux<String> buffered = JsonLineBufferingTransformer.bufferLines(raw);

    if (type == String.class) {
        @SuppressWarnings("unchecked")
        Flux<T> result = (Flux<T>) buffered;
        return result;
    }

    return buffered.map(json -> objectMapper.readValue(json, type));
}
```

#### 3.5 JsonLineBufferingTransformer for Spring

**New file:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/.../history/JsonLineBufferingTransformer.java`

Port from Quarkus using Reactor operators:
```java
public static Flux<String> bufferLines(Flux<String> upstream) {
    return upstream
        .scan(new BufferState(), BufferState::append)
        .flatMapIterable(BufferState::getCompleteLines);
}
```

### Phase 4: Frontend Updates

#### 4.1 Rich Event Rendering

**File:** `frontends/chat-frontend/src/components/MessageBubble.tsx`

Add event rendering when `events` array is present:
```tsx
{message.events ? (
    <RichEventRenderer events={message.events} />
) : (
    <MarkdownRenderer content={message.text} />
)}
```

#### 4.2 RichEventRenderer Component

**New file:** `frontends/chat-frontend/src/components/RichEventRenderer.tsx`

```tsx
interface RichEventRendererProps {
    events: ChatEvent[];
}

function RichEventRenderer({ events }: RichEventRendererProps) {
    return (
        <div className="rich-events">
            {events.map((event, i) => (
                <EventBlock key={i} event={event} />
            ))}
        </div>
    );
}

function EventBlock({ event }: { event: ChatEvent }) {
    switch (event.eventType) {
        case 'PartialResponse':
            return <MarkdownRenderer content={event.chunk} />;
        case 'BeforeToolExecution':
            return <ToolCallPending name={event.toolName} input={event.input} />;
        case 'ToolExecuted':
            return <ToolCallResult name={event.toolName} output={event.output} />;
        case 'PartialThinking':
            return <ThinkingSection content={event.chunk} />;
        default:
            return null;
    }
}
```

---

## Test Plan

### Unit Tests

#### Quarkus Unit Tests

**File:** `quarkus/memory-service-extension/runtime/src/test/java/.../EventCoalescerTest.java`

| Test Case | Description |
|-----------|-------------|
| `testCoalesceAdjacentPartialResponse` | Multiple PartialResponse → single merged event |
| `testCoalesceAdjacentPartialThinking` | Multiple PartialThinking → single merged event |
| `testPreserveNonCoalescable` | Tool events pass through unchanged |
| `testMixedEvents` | PR, PR, Tool, PR, PR → 3 events (merged PR, Tool, merged PR) |
| `testGetFinalText` | Extracts only PartialResponse text |
| `testEmptyStream` | Empty input → empty output |

**File:** `quarkus/memory-service-extension/runtime/src/test/java/.../JsonLineBufferingTransformerTest.java`

| Test Case | Description |
|-----------|-------------|
| `testCompleteLines` | `"a\nb\n"` → `["a", "b"]` |
| `testPartialChunks` | `"a\nb"` + `"c\n"` → `["a", "bc"]` |
| `testSplitAcrossBoundary` | `'{"e":"A'` + `'"}\n'` → `['{"e":"A"}']` |
| `testNoNewline` | `"abc"` (no newline) → `["abc"]` on completion |
| `testEmptyLines` | `"a\n\nb\n"` → `["a", "b"]` (skip empty) |

**File:** `quarkus/memory-service-extension/runtime/src/test/java/.../ConversationEventStreamAdapterTest.java`

| Test Case | Description |
|-----------|-------------|
| `testRecordsEventsAsJsonLines` | Each event recorded with trailing `\n` |
| `testCoalescesOnCompletion` | Coalesced events stored in history |
| `testEmitsChatEventsDownstream` | Events passed through to consumer |
| `testHandlesCancel` | Partial events stored on cancellation |
| `testHandlesError` | Partial events stored on error |

#### Spring Unit Tests

**File:** `spring/memory-service-spring-boot-autoconfigure/src/test/java/.../EventCoalescerTest.java`

Same test cases as Quarkus version.

**File:** `spring/memory-service-spring-boot-autoconfigure/src/test/java/.../JsonLineBufferingTransformerTest.java`

Same test cases using Reactor `StepVerifier`.

### Integration Tests

#### Memory Service Integration Tests

**File:** `memory-service/src/test/resources/features/history-events.feature`

```gherkin
Feature: Rich Event History Storage

  Scenario: Store history entry with events
    Given a conversation exists
    When I append a history entry with events:
      | eventType         | chunk               |
      | PartialResponse   | Hello               |
      | BeforeToolExecution | get_weather       |
      | PartialResponse   | The weather is nice |
    Then the entry should have text "HelloThe weather is nice"
    And the entry should have 3 events

  Scenario: Store history entry with text only (backward compat)
    Given a conversation exists
    When I append a history entry with text "Simple message"
    Then the entry should have text "Simple message"
    And the entry should not have events

  Scenario: Store history entry with both text and events
    Given a conversation exists
    When I append a history entry with:
      | text   | Search-optimized text      |
      | events | [{"eventType":"PartialResponse","chunk":"Rich"}] |
    Then the entry should have text "Search-optimized text"
    And the entry should have 1 event
```

#### Quarkus E2E Tests

**File:** `quarkus/examples/chat-quarkus/src/test/java/.../ChatResourceIT.java`

| Test Case | Description |
|-----------|-------------|
| `testChatDetailedEndpoint` | `/chat-detailed` returns SSE with eventType |
| `testResumeEventsEndpoint` | `/resume-events` replays buffered JSON lines |
| `testSimpleStreamStillWorks` | `/chat` still returns simple string tokens |
| `testNonStreamingHistoryEntry` | Non-streaming creates history entry |

#### Spring E2E Tests

**File:** `spring/examples/chat-spring/src/test/java/.../AgentStreamControllerIT.java`

| Test Case | Description |
|-----------|-------------|
| `testStreamWithRichEvents` | Streaming records rich events |
| `testResumeWithRichEvents` | Resume replays JSON lines |
| `testSimpleStreamBackwardCompat` | Simple streams work unchanged |

### Manual Test Scenarios

#### Scenario 1: Rich Event Streaming (Quarkus)

1. Start chat-quarkus in dev mode
2. Create conversation
3. Send message that triggers tool call: "What's the weather?"
4. Verify SSE stream contains:
   - `{"eventType":"PartialResponse","chunk":"..."}`
   - `{"eventType":"BeforeToolExecution","toolName":"..."}`
   - `{"eventType":"ToolExecuted","toolName":"...","output":"..."}`
5. Verify history entry contains `events` array

#### Scenario 2: Stream Resumption

1. Start streaming response
2. Disconnect client mid-stream
3. Reconnect and call `/resume-events/{conversationId}`
4. Verify:
   - Stream picks up from beginning
   - Events are complete JSON objects (not partial)
   - All events eventually delivered

#### Scenario 3: Frontend Rendering

1. Open chat-frontend
2. Send message that triggers tool call
3. Verify UI shows:
   - Tool call indicator with name/input
   - Tool result display
   - Response text with proper formatting

---

## Implementation Checklist

### Phase 1: Core Infrastructure
- [x] Update memory-service history validation schema
- [x] Update OpenAPI spec for history content type
- [x] Add `replayEvents` to ResponseResumer interface (Quarkus)
- [x] Add `replayEvents` to ResponseResumer interface (Spring)

### Phase 2: Quarkus Implementation
- [x] Update ConversationInterceptor for Multi<ChatEvent>
- [x] Implement `replayEvents` in GrpcResponseResumer
- [x] Add `/chat-detailed` endpoint in chat-quarkus example
- [x] Add `/resume-events` endpoint in chat-quarkus example
- [x] Write unit tests for EventCoalescer
- [x] Write unit tests for JsonLineBufferingTransformer
- [x] Implement ConversationEventStreamAdapter

### Phase 3: Spring Implementation
- [x] Port EventCoalescer to Spring
- [x] Port JsonLineBufferingTransformer to Spring (Reactor)
- [x] Add `appendAgentMessageWithEvents` to ConversationStore
- [x] Implement `replayEvents` in GrpcResponseResumer
- [x] Write unit tests for EventCoalescer
- [x] Write unit tests for JsonLineBufferingTransformer
- N/A: Rich event streaming not supported by Spring AI (see Known Limitations)

### Phase 4: Frontend
- [x] Add RichEventRenderer component
- [x] Add ToolCallPending/ToolCallResult components
- [x] Add ThinkingSection component
- [x] Update conversations-ui to use rich rendering
- [ ] Test with real streaming responses

### Phase 5: Documentation
- [x] Update API documentation (OpenAPI spec)
- [x] Add example code snippets
- [x] Document event type format
- [ ] Add troubleshooting guide

---

## Known Limitations

### Spring AI

1. **No rich event streaming support**: Spring AI does not provide a rich event stream like Quarkus LangChain4j's `Multi<ChatEvent>`. Spring AI's `Flux<ChatClientResponse>` only contains text chunks - it lacks discrete events for tool execution, thinking, RAG content, etc. As a result, Spring applications can only record simple text streams, not rich event streams.

2. **Tool calls in streaming** ([#3366](https://github.com/spring-projects/spring-ai/issues/3366)): Tool call information is not available in streaming mode. Even if rich event recording were implemented, tool execution events would not be captured.

3. **Infrastructure ready for future use**: The Spring implementation includes `EventCoalescer`, `JsonLineBufferingTransformer`, `appendAgentMessageWithEvents`, and `replayEvents` - all infrastructure needed for rich events. If Spring AI adds rich event streaming in the future, these components are ready to use.

### General

1. **Event format is framework-agnostic**: The stored event format uses generic `eventType` discriminator, not framework-specific types. This enables cross-framework compatibility but loses some type information.

2. **Coalescing is storage-only**: Events are coalesced only for history storage. The resumption stream replays the original (uncoalesced) events.

---

## References

- [Quarkus LangChain4j Streaming Guide](https://docs.quarkiverse.io/quarkus-langchain4j/dev/guide-streamed-responses.html)
- [LangChain4j Response Streaming](https://docs.langchain4j.dev/tutorials/response-streaming/)
- [Spring AI Chat Client API](https://docs.spring.io/spring-ai/reference/api/chatclient.html)
- [Spring AI Tool Calling](https://docs.spring.io/spring-ai/reference/api/tools.html)
- [Spring AI Streaming Tool Calls Issue #3366](https://github.com/spring-projects/spring-ai/issues/3366)
