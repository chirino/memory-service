# user-guide.md — Headless Conversation Components

This guide explains how to wire **backend APIs** (streaming, persistence) and **styling** into the new Radix-style headless conversation primitives implemented in `conversation.tsx`. These primitives encapsulate state + reconciliation, but **do not** render any visual design by default.

---

## Mental model

### What the primitives do
- Hold **active conversation state** in context (`conversationId`, `conversationGroupId`, streaming phase, input draft).
- Reconcile **backend messages** with **in-flight streaming messages** (dedupe by ID; show pending/streaming status).
- Provide **generic extension points** for message-level UI customization via `MessageContext` and render props.

### What you (the app) must do
- Provide messages from your backend to `<Conversation.Root>` via props.
- Implement a `ConversationController` that knows how to:
  - start / resume / cancel streaming
  - switch conversations
- Apply styles using `className` and the exposed `data-*` attributes (and/or `asChild` composition).
- Implement app-specific features (forking, editing, etc.) using the generic extension points.

---

## Quick start (composition)

A minimal, styled composition looks like:

```tsx
import { Conversation } from "./conversation";

export function ChatUI({ controller, conversationId, conversationGroupId, messages }) {
  return (
    <Conversation.Root
      controller={controller}
      conversationId={conversationId}
      conversationGroupId={conversationGroupId}
      messages={messages}
    >
      <Conversation.Viewport className="h-full overflow-y-auto p-4">
        <Conversation.Messages className="flex flex-col gap-3">
          {(items) =>
            items.map((m) => (
              <Conversation.Message
                key={m.id}
                message={m}
                className={[
                  "rounded-xl px-3 py-2 text-sm",
                  m.author === "user" ? "ml-auto bg-blue-600 text-white" : "mr-auto bg-zinc-100 text-zinc-900",
                  m.displayState === "streaming" ? "opacity-90" : "",
                  m.displayState === "pending" ? "opacity-70" : "",
                  m.displayState === "error" ? "ring-2 ring-red-500" : "",
                ].join(" ")}
              />
            ))
          }
        </Conversation.Messages>
      </Conversation.Viewport>

      <div className="border-t p-3">
        <Conversation.Input className="w-full resize-none rounded-lg border p-2" placeholder="Message…" />
      </div>
    </Conversation.Root>
  );
}
```

Notes:

* `Conversation.Messages` supports a **render prop** to pass the reconciled list. 
* `Conversation.Message` takes a `message` object (already includes `displayState`). 

---

## Data you provide

### `ConversationMessage`

Your backend "stable" messages should be shaped like:

* `id` (stable, unique)
* `conversationId`
* `author`: `"user" | "assistant" | "system"`
* `content`
* optional lineage hints:

  * `previousMessageId?: string | null`
  * `forkedFrom?: { conversationId; messageId }` (for app-specific fork tracking)

**Ordering matters:** If you omit `previousMessageId`, the primitives infer it based on chronological order. If messages can arrive out of order, provide `previousMessageId` explicitly. 

---

## Backend integration via `ConversationController`

### Controller interface (what you implement)

```ts
export type ConversationController = {
  startStream: (conversationId: string, text: string, callbacks: StreamCallbacks) => void | Promise<void>;
  resumeStream: (
    conversationId: string,
    callbacks: StreamCallbacks & { replaceMessageId?: string | null }
  ) => void | Promise<void>;
  cancelStream: (conversationId: string) => void | Promise<void>;
  selectConversation: (conversationId: string) => void | Promise<void>;
  idFactory?: () => string;
};
```

### Streaming callbacks (what you must call)
```ts
export type StreamCallbacks = {
  onChunk?: (chunk: string) => void;
  onComplete?: () => void;
  onError?: (error: unknown) => void;
  onCancel?: () => void;
};
```

#### Typical streaming flow
1. User submits text → primitives call `controller.startStream(conversationId, text, callbacks)`.
2. Your transport (SSE/WS/fetch streaming) calls:
   - `callbacks.onChunk(tokenOrDelta)`
   - `callbacks.onComplete()` when done
   - `callbacks.onError(err)` on failure
   - `callbacks.onCancel()` if you support remote cancel

### Selecting a conversation (navigation)
The primitives never mutate your router/app state directly. They call:
- `controller.selectConversation(conversationId)`

Your implementation should update whatever drives:
- `conversationId` prop passed into `<Conversation.Root ... />`
- `messages` prop for that conversation

---

## IMPORTANT: Message IDs and "echo" reconciliation

The primitives dedupe "optimistic" (pending/streaming) messages by **ID**, not content.

That means your backend should eventually return messages whose `id` matches the IDs the UI used for the in-flight messages, otherwise you can end up with duplicates (optimistic pending + backend message).

### Recommended ID handoff pattern: `idFactory` queue
The primitives generate IDs by calling `controller.idFactory()` (if provided) before calling `startStream` / `resumeStream`.

You can exploit that to pass the exact IDs to your backend without changing the primitives:

```ts
function createController() {
  const pendingIds: string[] = [];

  return {
    idFactory() {
      const id = crypto.randomUUID();
      pendingIds.push(id);
      return id;
    },

    async startStream(conversationId, text, cb) {
      // Primitives call idFactory() twice on send:
      //   1) pendingAssistantId
      //   2) userMessage.id
      const pendingAssistantId = pendingIds.shift();
      const userMessageId = pendingIds.shift();

      // 1) persist user message using userMessageId
      // 2) start assistant stream and persist assistant message using pendingAssistantId
      // 3) call cb.onChunk / cb.onComplete as tokens arrive
    },

    async resumeStream(conversationId, cb) {
      // If replaceMessageId is not provided, primitives may have generated one via idFactory():
      const newAssistantId = pendingIds.shift();
      // Use cb.replaceMessageId ?? newAssistantId depending on your backend semantics.
    },

    cancelStream(conversationId) { /* ... */ },
    selectConversation(conversationId) { /* ... */ },
  };
}
```

This keeps the "echo" model working without modifying the component API.

---

## Two integration modes: choose what you own

### Mode A — "Primitives drive streaming" (default)

* Use `<Conversation.Input />` with no `onSubmit`
* Let primitives call `controller.startStream/resumeStream/cancelStream`
* Implement the controller to:

  * persist messages (optional)
  * stream tokens and call callbacks
* Make sure your backend can return the same message IDs (use the `idFactory` queue pattern above)

This is the simplest mode and preserves built-in reconciliation.

### Mode B — "App owns submit" (custom pipelines)

`<Conversation.Input>` supports `onSubmit`. If you pass it, the input **delegates entirely** to you. 

You can use this if you want:

* custom validation / moderation
* richer payloads (attachments, tools)
* server-generated IDs only

Example pattern:

```tsx
function Composer() {
  const { sendMessage } = useConversationStreaming();
  return (
    <Conversation.Input
      onSubmit={(value) => {
        // 1) do app-specific work (persist, route, etc)
        // 2) optionally still call sendMessage(value) to engage primitives streaming state
      }}
    />
  );
}
```

If you go fully "server IDs only", be aware you may need extra glue so optimistic messages don't duplicate.

---

## Generic extension points

The primitives provide generic extension points for building app-specific features like forking, editing, or custom message actions. These are **not** tied to any specific feature.

### Message context hook

`useMessageContext()` provides access to the current message when used within `<Conversation.Message>`:

```tsx
import { useMessageContext } from "./conversation";

function MessageActions() {
  const message = useMessageContext();
  if (!message) {
    return null; // Not inside a message context
  }
  
  return (
    <div className="message-actions">
      <button onClick={() => editMessage(message.id)}>Edit</button>
      <button onClick={() => forkAtMessage(message.id)}>Fork</button>
    </div>
  );
}

// Usage:
<Conversation.Message message={m}>
  <MessageBubble message={m} />
  <MessageActions />
</Conversation.Message>
```

### Render prop composition

`Conversation.Messages` supports a render prop that gives you the reconciled message list:

```tsx
<Conversation.Messages>
  {(messages) => (
    <div className="messages-list">
      {messages.map((message) => (
        <Conversation.Message key={message.id} message={message}>
          <YourMessageComponent 
            message={message}
            onEdit={handleEdit}
            onFork={handleFork}
          />
        </Conversation.Message>
      ))}
    </div>
  )}
</Conversation.Messages>
```

### Custom message children

`Conversation.Message` accepts children, allowing you to compose custom UI:

```tsx
<Conversation.Message message={m} asChild>
  <div className="custom-message-wrapper">
    <MessageContent message={m} />
    {m.author === "user" && (
      <MessageMenu 
        message={m}
        onEdit={() => editMessage(m.id)}
        onFork={() => forkAtMessage(m.id)}
      />
    )}
  </div>
</Conversation.Message>
```

---

## Styling guide

### The "headless" contract

Everything is style-agnostic. You style by:

* Passing `className` / style props directly (all parts forward props)
* Using `data-*` attributes for state-based styling
* Using `asChild` to render your own components via Radix `Slot` 

### State selectors you can style against

#### Viewport

* `data-state={streaming.phase}` (`idle | sending | streaming | completed | canceled | error`) 

#### Messages container

* `data-state="empty" | "rendered"` 

#### Message item

* `data-author="user" | "assistant" | "system"`
* `data-state="pending" | "streaming" | "completed" | "canceled" | "error"`
* `data-conversation-id="..."` 

#### Input

* `data-state={streaming.phase}`
* `aria-disabled={isBusy}` 

### `asChild` composition

All main parts support `asChild`, so you can wrap your own components without losing attributes:

```tsx
<Conversation.Input asChild>
  <MyTextarea className="..." />
</Conversation.Input>

<Conversation.Message message={m} asChild>
  <MyMessageComponent className="..." />
</Conversation.Message>
```

This is the same ergonomics as Radix primitives.

---

## Actions (send / resume / cancel)

Use `<Conversation.Actions>` to get the streaming intents without manually importing hooks:

```tsx
<Conversation.Actions>
  {({ send, resume, cancel, state }) => (
    <div className="flex gap-2">
      <button onClick={() => send("Hello")}>Send</button>
      <button onClick={() => resume()}>Resume</button>
      <button onClick={cancel} disabled={state.phase === "idle"}>
        Cancel
      </button>
    </div>
  )}
</Conversation.Actions>
```

The `state` includes:

* `phase`, `error`
* `replaceMessageId`
* `userMessage` and `assistantMessage` snapshots (when present) 

---

## Accessibility defaults (you should keep them)

The parts apply sensible ARIA roles by default:

* Viewport: `role="log"`, `aria-live="polite"` 
* Messages: `role="list"`
* Message: `role="listitem"`, `aria-busy` during streaming/pending 

You can still override attributes if needed, but it's recommended to preserve the semantics.

---

## Common pitfalls & best practices

1. **Always pass messages for the active conversation**
   The primitives sync `messages` props directly into internal state. 
   Make sure you're not accidentally passing messages from a previous conversation during a conversation switch.

2. **Prefer stable IDs and stable ordering**
   If you can't guarantee chronological ordering, provide `previousMessageId` explicitly. 

3. **Implement cancel defensively**
   `cancelStream(conversationId)` should be safe to call even if the stream already ended.

4. **Use extension points for app-specific features**
   Features like forking, editing, or custom actions should be implemented in your app code using `useMessageContext()` and render props, not built into the primitives.

---

## Reference: exports you'll use

Components:

* `Conversation.Root`
* `Conversation.Viewport`
* `Conversation.Messages`
* `Conversation.Message`
* `Conversation.Input`
* `Conversation.Actions` 

Hooks:

* `useConversationContext()` (throws if outside Root)
* `useOptionalConversationContext()` (returns null if outside Root)
* `useConversationMessages()`
* `useConversationStreaming()`
* `useConversationInput()`
* `useMessageContext()` (returns message when inside Conversation.Message)

---

## Appendix: minimal controller skeleton

```ts
import type { ConversationController } from "./conversation";

export function buildController(): ConversationController {
  return {
    async startStream(conversationId, text, cb) {
      // Connect to your backend (SSE/WS/fetch streaming)
      // Call cb.onChunk(delta), cb.onComplete(), cb.onError(err)
    },
    async resumeStream(conversationId, cb) {
      // Resume server-side stream
    },
    async cancelStream(conversationId) {
      // Cancel in-flight request if supported
    },
    async selectConversation(conversationId) {
      // Update router / state; cause Conversation.Root props to update
    },
    idFactory() {
      return crypto.randomUUID();
    },
  };
}
```

---

## Design doc cross-reference

If you want the broader design intent and rationale (reducer vs selector reconciliation, etc.), see `008-headless-ui-component-design.md`.
