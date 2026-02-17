---
status: superseded
superseded-by:
  - 022-chat-app-design.md
  - 023-chat-app-implementation.md
---

# Headless Conversation & Fork Primitives — Design & Implementation Plan

> **Status**: Superseded. Standalone headless library approach replaced by a full chat app
> built directly with Radix/shadcn primitives in
> [022](022-chat-app-design.md) and [023](023-chat-app-implementation.md).

This document defines the design of a family of **headless, composable conversation primitives** for building AI chat UIs in this application.  
These primitives encapsulate conversation state, fork logic, and streaming message lifecycles in a Radix-style headless API without UI or styling.

---

## Overview

We are extracting core conversation behavior from existing UI code (`ChatPanel`) into reusable, composable primitives that:

- Are headless and styling-agnostic
- Provide a Radix-inspired parts + hooks API
- Encapsulate conversation state and streaming logic
- Integrate with backend APIs via an injected controller
- Support forks and switching between them
- Simplify UI components by consolidating logic

This document outlines the API design, state models, streaming behavior, fork semantics, integration plan, and implementation roadmap.

---

## Terminology

| Term | Meaning |
|------|---------|
| **Conversation** | A specific chat session identified by a `conversationId`. |
| **Fork** | A conversation derived from another conversation at a specific message boundary. |
| **ConversationGroup** | A logical grouping of related conversations (the primitives receive but do not manage this). |
| **Controller** | An external, injected set of callbacks that handles backend APIs, networking, and persistence. |

---

## Architectural Layers

```

Application UI (styled)
├── Sidebar / Navigation
├── ConversationController (React Query, WebSockets)
│      └── backend APIs
├── Headless Primitives (Context + Hooks + Parts)
│      └── No UI / no styling
└── Composed UI Components (unstyled composition)
└── Consumer styles and layout

````

- **Controller:** Owns backend integration (fetching, streaming, fork listing).
- **Primitives:** Own state, reconciliation, part structure, and internal transitions.
- **Consumer UI:** Renders and styles based on primitives state and callbacks.

---

## Controller Integration

The primitives do **not** call backend APIs directly.  
Instead, they accept a **controller object** with the following interface (example):

```ts
interface ConversationController {
  startStream(conversationId: string, text: string): void
  resumeStream(conversationId: string): void
  cancelStream(conversationId: string, messageId: string): void
  selectConversation(conversationId: string): void
  listForksAtPoint(
    conversationId: string,
    previousMessageId: string | null
  ): ForkSummary[]
}
````

* The controller is implemented by your app (React Query / WebSocket logic).
* Primitives call controller functions in response to user actions.

---

## State Models

### Conversation

```ts
interface Conversation {
  conversationId: string
  conversationGroupId: string
  messages: ChatMessage[]
  forkPoints: ForkSummary[]
}
```

### ChatMessage

```ts
interface ChatMessage {
  id: string
  previousMessageId: string | null
  actor: "user" | "assistant"
  text: string
  partialText?: string
  status: "idle" | "sending" | "streaming" | "completed" | "canceled" | "error"
}
```

### ForkSummary

```ts
interface ForkSummary {
  forkedConversationId: string
  previousMessageId: string | null
  shortName: string
}
```

### Streaming Identity

To ensure streaming events apply only to the intended context, each streaming event is tagged by a **streamId** generated when a message begins streaming.

---

## State Machine

A message’s lifecycle transitions:

```
idle
  └─ startStream()
     → sending
        └─ first token arrives
           → streaming
              └─ done
                 → completed
              └─ cancel/resume
                 → canceled / streaming
              └─ error
                 → error
```

* Only one active assistant message may stream at a time per conversation.
* Streaming identity (`streamId`) guards state updates across rapid cancel/resume.

---

## Primitives API

These are the composable parts available to UI:

### Components

| Component                    | Purpose                                              |
| ---------------------------- | ---------------------------------------------------- |
| `<Conversation.Root>`        | Provides context and holds active conversation state |
| `<Conversation.Viewport>`    | Scrollable region for messages                       |
| `<Conversation.Messages>`    | List container for message rendering                 |
| `<Conversation.Message>`     | Individual message container, with status tags       |
| `<Conversation.Input>`       | Text input for user messages                         |
| `<Conversation.ForkTrigger>` | Renders a fork action trigger point                  |
| `<Conversation.ForkList>`    | Renders a list of forks at a message boundary        |
| `<Conversation.Actions>`     | Actions like cancel / resume for message streams     |

### Hooks

| Hook                               | Return                                                        |
| ---------------------------------- | ------------------------------------------------------------- |
| `useConversationContext()`         | Core context with conversationId, groupId, selectConversation |
| `useOptionalConversationContext()` | Context or null (safe)                                        |
| `useConversationMessages()`        | Messages + derived list (reconciled)                          |
| `useConversationStreaming()`       | Streaming intents: start, resume, cancel                      |
| `useConversationInput()`           | Input value + submit handling                                 |
| `useConversationForks(messageId)`  | Fork options at a given message                               |

---

## Message Reconciliation

Instead of echo detection in the reducer, messages are reconciled in selectors (e.g., `useConversationMessages`):

* Base messages come from backend
* Partial assistant message may be appended during streaming
* Completed messages replace partial ones
* Echo collisions (same content from backend) are filtered out
* UI receives a stable, deduplicated list

---

## Fork Handling

Fork data and actions are keyed per message boundary using a unique `forkKey(conversationId, previousMessageId)`.
Fork loading state is **per fork key**, not global.

```ts
forkCache: Record<string, ForkSummary[]>
forkLoading: Record<string, boolean>
```

* `ForkTrigger` opens a fork menu for a message boundary
* `ForkList` renders the options
* Selecting a fork calls `controller.selectConversation(forkedConversationId)`

---

## Integration Patterns

### Conversation Root

```tsx
<Conversation.Root
  controller={controller}
  activeConversationId={currentId}
>
  {children}
</Conversation.Root>
```

* Provides context
* Does not render UI
* Accepts controller and active conversationId

### Message List Example

```tsx
<Conversation.Viewport>
  <Conversation.Messages>
    {messages.map(msg => (
      <Conversation.Message
        key={msg.id}
        id={msg.id}
        data-state={msg.status}
        data-author={msg.actor}
      >
        {msg.partialText ?? msg.text}
        <Conversation.ForkTrigger messageId={msg.id} />
        <Conversation.Actions messageId={msg.id} />
      </Conversation.Message>
    ))}
  </Conversation.Messages>
</Conversation.Viewport>
```

### Input & Actions

```tsx
<Conversation.Input />
```

* Input does not execute streaming directly
* Calls `onSubmit` provided via context or hooks
* Avoids double-submission; action handlers own streaming calls

---

## Implementation Roadmap

### Phase 1 — Scaffolding

1. Create `ConversationContext` and provider.
2. Implement raw reducer (`conversationReducer`) with minimal state.
3. Build placeholder parts: `Root`, `Viewport`, `Messages`, `Message`.

### Phase 2 — Streaming Logic

1. Add streaming state with `streamId`.
2. Build hooks: `useConversationStreaming`.
3. Move reconciliation into selector hooks.

### Phase 3 — Input & Actions

1. Build `Conversation.Input` with controlled value.
2. Build `Conversation.Actions` with cancel/resume.
3. Connect to controller via hooks.

### Phase 4 — Forks

1. Change fork cache to key by message boundary.
2. Implement `ForkTrigger` and `ForkList`.
3. Build safe loading state & UI integration.

### Phase 5 — Accessibility & Polishing

1. Add `aria-live` for streaming text.
2. Keyboard navigation in viewport.
3. Normalize ID generation (controller or injected factory).

---

## Accessibility & UX Notes

* Use `role="list"` / `role="listitem"` on messages if needed.
* Use `aria-live="polite"` on partial assistant output.
* Provide `data-state` attributes for consumer styling.

---

## Future Enhancements

* Support message editing
* Add message context menus
* Collaboration features
* Conversation export / history

