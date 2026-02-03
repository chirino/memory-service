# Chat Frontend Design

This document describes the architecture of the chat frontend in `common/chat-frontend/`.

## Overview

The chat frontend is built using **headless conversation primitives** following the Radix UI pattern. This separates logic from presentation, allowing the conversation state machine to be reused with different UI implementations.

```mermaid
graph TB
    subgraph "Application Layer"
        ChatPanel[ChatPanel]
        ConversationsUI[ConversationsUI]
    end

    subgraph "Headless Primitives"
        Root[Conversation.Root]
        Viewport[Conversation.Viewport]
        Messages[Conversation.Messages]
        Message[Conversation.Message]
        Input[Conversation.Input]
        Actions[Conversation.Actions]
    end

    subgraph "State Management"
        Reducer[conversationReducer]
        StreamingState[StreamingSnapshot]
        Hooks[Selector Hooks]
    end

    subgraph "Backend Integration"
        Controller[ConversationController]
        SSE[useSseStream]
    end

    ChatPanel --> Root
    ChatPanel --> Controller
    Controller --> SSE
    Root --> Reducer
    Root --> Hooks
    ConversationsUI --> Viewport
    ConversationsUI --> Messages
    ConversationsUI --> Input
```

## Component Architecture

### Headless Components

The `Conversation` namespace exports headless components that manage state without prescribing UI:

| Component | Purpose |
|-----------|---------|
| `Conversation.Root` | Provider that initializes state and exposes context |
| `Conversation.Viewport` | Container with ARIA `role="log"` for accessibility |
| `Conversation.Messages` | Renders messages via render prop or children |
| `Conversation.Message` | Individual message wrapper with display state |
| `Conversation.Input` | Textarea with submit handling |
| `Conversation.Actions` | Render prop for send/resume/cancel actions |

### Hook Exports

| Hook | Purpose |
|------|---------|
| `useConversationMessages()` | Returns reconciled messages with display states |
| `useConversationStreaming()` | Returns streaming state and control functions |
| `useConversationInput()` | Returns input value/setValue/submit/reset |
| `useConversationContext()` | Full context (throws if outside Root) |
| `useOptionalConversationContext()` | Safe context access (returns null) |
| `useMessageContext()` | Access current message within Message component |

## State Machine

### Streaming Phases

```mermaid
stateDiagram-v2
    [*] --> idle
    idle --> sending: SEND_START / RESUME_START
    sending --> streaming: STREAM_CHUNK
    streaming --> streaming: STREAM_CHUNK
    streaming --> completed: STREAM_COMPLETE
    streaming --> canceled: STREAM_CANCEL
    streaming --> error: STREAM_ERROR
    sending --> error: STREAM_ERROR
    completed --> idle: auto RESET_STREAM
    canceled --> idle: auto RESET_STREAM
    error --> idle: auto RESET_STREAM
```

### State Shape

```typescript
type ConversationState = {
  conversationId: string | null;
  conversationGroupId: string | null;
  baseMessages: ConversationMessage[];      // Messages from backend /entries
  inputValue: string;                        // Current input text
  streaming: StreamingSnapshot;              // Active stream state
};

type StreamingSnapshot = {
  phase: "idle" | "sending" | "streaming" | "completed" | "canceled" | "error";
  error: string | null;
  userMessage: ConversationMessage | null;   // Optimistic user message
  assistantMessage: ConversationMessage | null; // Streaming assistant content (temporary)
  streamId: string | null;                   // Prevents stale chunk application
  pendingAssistantId: string | null;         // Temporary ID for streaming message
};
```

### Reducer Actions

| Action | Effect |
|--------|--------|
| `SET_CONVERSATION` | Switch conversation, optionally reset stream |
| `SET_MESSAGES` | Update base messages from backend |
| `SET_INPUT` | Update input field value |
| `SEND_START` | Begin new message stream |
| `RESUME_START` | Begin resume stream |
| `STREAM_CHUNK` | Append chunk to assistant message |
| `STREAM_COMPLETE` | Mark stream as finished (auto-triggers RESET_STREAM) |
| `STREAM_CANCEL` | Mark stream as canceled (auto-triggers RESET_STREAM) |
| `STREAM_ERROR` | Store error (auto-triggers RESET_STREAM) |
| `RESET_STREAM` | Return to idle state, clear streaming message |

## Message Reconciliation

The `useConversationMessages()` hook reconciles base messages (from backend `/entries`) with streaming state.

**Key Principle**: Streaming messages are **temporary**. They're shown during active streaming and automatically removed when streaming completes. The backend's `/entries` endpoint provides the final, authoritative messages.

```mermaid
flowchart TD
    A[Base Messages from /entries] --> B[Deduplicate by ID]
    B --> C{Has streaming.userMessage?}

    C -->|Yes| D{Is back-to-back duplicate?}
    D -->|No| E[Add optimistic user message]
    D -->|Yes| F[Skip duplicate]

    E --> G{Is actively streaming?}
    F --> G
    C -->|No| G

    G -->|sending/streaming| H[Add temporary streaming assistant message]
    G -->|idle/completed/etc| I[Return items]

    H --> I
```

### Reconciliation Rules

1. **User Message Echo Detection**: Content-based matching (normalized text) because backends may assign new IDs
2. **Streaming Assistant Message**: Temporary - shown only during `sending` or `streaming` phases
3. **Auto-cleanup**: When streaming completes, `RESET_STREAM` is dispatched automatically, removing the temporary message
4. **Final Message**: The `/entries` query refreshes after stream completion, providing the authoritative final message

## Streaming Lifecycle

```mermaid
sequenceDiagram
    participant User
    participant ChatPanel
    participant Reducer
    participant SSE
    participant EntriesQuery

    User->>ChatPanel: Send message
    ChatPanel->>Reducer: SEND_START (creates temp streaming message)
    ChatPanel->>SSE: Start stream

    loop Streaming
        SSE-->>Reducer: STREAM_CHUNK
        Note over Reducer: Append to temp streaming.assistantMessage
    end

    SSE-->>Reducer: STREAM_COMPLETE
    Note over Reducer: Auto-dispatch RESET_STREAM
    Note over Reducer: Temp streaming message removed

    ChatPanel->>EntriesQuery: Invalidate query
    EntriesQuery-->>ChatPanel: Refetch /entries
    Note over ChatPanel: Final message from backend<br/>replaces temp message
```

## Resume Flow

Resume handles the case where a page reloads while a response is in progress.

```mermaid
sequenceDiagram
    participant Browser
    participant ChatPanel
    participant Conversation.Root
    participant SSE Backend

    Note over Browser: Page loads with ?conversationId=xxx
    Browser->>ChatPanel: Mount with conversationId

    ChatPanel->>ChatPanel: Check resumableConversationIds

    alt conversationId is resumable
        ChatPanel->>Conversation.Root: resumeStream()
        Conversation.Root->>Conversation.Root: Generate streamId, pendingAssistantId
        Conversation.Root->>Conversation.Root: Dispatch RESUME_START

        Conversation.Root->>SSE Backend: GET /v1/conversations/{id}/resume

        loop Streaming
            SSE Backend-->>Conversation.Root: data: chunk
            Conversation.Root->>Conversation.Root: Dispatch STREAM_CHUNK
        end

        SSE Backend-->>Conversation.Root: [DONE]
        Conversation.Root->>Conversation.Root: Dispatch STREAM_COMPLETE
        Note over Conversation.Root: Auto RESET_STREAM clears temp message
        Note over Conversation.Root: /entries refresh provides final message
    end
```

## StreamId Guards

StreamIds prevent stale chunks from previous (canceled/failed) streams from affecting current state.

```typescript
// Generated when starting a stream
const streamId = `stream-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;

// In reducer, all stream actions check:
case "STREAM_CHUNK": {
  if (state.streaming.streamId !== action.streamId) {
    return state; // Ignore stale chunk
  }
  // Apply chunk...
}
```

## Data Flow Summary

```mermaid
flowchart TB
    subgraph User Actions
        Send[Send Message]
        Cancel[Cancel Stream]
        SwitchConvo[Switch Conversation]
    end

    subgraph State Updates
        Reducer[conversationReducer]
        BaseMessages[baseMessages from /entries]
    end

    subgraph Selectors
        Reconcile[useConversationMessages]
        Streaming[useConversationStreaming]
    end

    subgraph Rendering
        Messages[Message List]
        Input[Input Field]
        StreamIndicator[Streaming Indicator]
    end

    Send --> Reducer
    Cancel --> Reducer
    SwitchConvo --> Reducer

    Reducer --> Reconcile
    BaseMessages --> Reconcile

    Reconcile --> Messages
    Streaming --> StreamIndicator
    Streaming --> Input
```

## Files Reference

| File | Purpose |
|------|---------|
| [conversation.tsx](../common/chat-frontend/src/components/conversation.tsx) | Headless primitives, reducer, hooks |
| [chat-panel.tsx](../common/chat-frontend/src/components/chat-panel.tsx) | Main chat UI, auto-resume |
| [useSseStream.ts](../common/chat-frontend/src/hooks/useSseStream.ts) | SSE stream handling |
| [conversations-ui.tsx](../common/chat-frontend/src/components/conversations-ui.tsx) | Styled UI components |

## Design Decisions

### Why Headless Primitives?
- Separates state machine from presentation
- Allows different UI implementations (ChatPanel, future mobile, etc.)
- Enables testing of logic without rendering

### Why Temporary Streaming Messages?
- Simple mental model: streaming message = preview, backend message = final
- No complex ID mapping between frontend-generated and backend-assigned IDs
- Auto-cleanup ensures no stale streaming messages remain
- `/entries` is the single source of truth for message history

### Why Content-Based User Echo Detection?
- Backends may not echo client-generated IDs for user messages
- Content matching (normalized) catches duplicates regardless of ID

### Why StreamId Guards?
- Prevents race conditions when streams overlap
- User might cancel and immediately send new message
- Stale chunks from old stream would corrupt new stream without guards
