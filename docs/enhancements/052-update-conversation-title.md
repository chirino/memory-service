---
status: implemented
---

# Enhancement 052: Update Conversation Title

> **Status**: Implemented.

## Summary

Add a `PATCH /v1/conversations/{conversationId}` endpoint (and corresponding gRPC RPC) that allows updating a conversation's title. Wire it through the Quarkus/Spring proxy classes, example chat apps, and the chat frontend. The frontend adds an edit button that appears on hover next to the conversation title.

## Motivation

Once a conversation is created, its title is immutable — there is no update endpoint. Conversations created via auto-create (first entry append) often have no title at all, showing as "Untitled conversation" in the sidebar. Users should be able to rename conversations to keep their history organized.

## Design

### API Contract

**REST** — `PATCH /v1/conversations/{conversationId}`

Request body:
```json
{
  "title": "My updated title"
}
```

Response: `200 OK` with the full `Conversation` object.

Access control: requires **writer** or higher access on the conversation.

**gRPC** — `ConversationsService/UpdateConversation`

```protobuf
rpc UpdateConversation(UpdateConversationRequest) returns (Conversation);

message UpdateConversationRequest {
  bytes conversation_id = 1;  // UUID as 16-byte big-endian
  optional string title = 2;
}
```

### Frontend Behavior

When the user hovers over the conversation title in the chat header, a pencil/edit icon appears to the left of the title. Clicking it replaces the title text with an inline text input pre-filled with the current title. The user can:
- Press **Enter** or click a confirm button to save
- Press **Escape** or click away to cancel

On save, the frontend calls `PATCH /v1/conversations/{conversationId}` with the new title, then invalidates the conversation and conversation-list queries so the sidebar updates.

## Implementation Plan

### Layer 1: OpenAPI Spec

**File:** `memory-service-contracts/src/main/resources/openapi.yml`

- Add `PATCH` method to `/v1/conversations/{conversationId}` path
- Add `UpdateConversationRequest` schema with a single `title` field (string, nullable)
- Response reuses existing `Conversation` schema

### Layer 2: Proto / gRPC

**File:** `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`

- Add `UpdateConversation` RPC to `ConversationsService`
- Add `UpdateConversationRequest` message with `conversation_id` (bytes) and `title` (optional string)

**File:** `memory-service/src/main/java/io/github/chirino/memory/grpc/ConversationsGrpcService.java`

- Implement `updateConversation()` method following the existing `getConversation()` / `deleteConversation()` pattern

### Layer 3: Memory-Service Store + REST Layer

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java`

- Add `updateConversation(String conversationId, String userId, UpdateConversationRequest request)` to the interface

**Files:** `PostgresMemoryStore.java`, `MongoMemoryStore.java`

- Implement `updateConversation()`: look up conversation, verify writer+ access, update title, persist, return updated conversation

**File:** `memory-service/src/main/java/io/github/chirino/memory/api/ConversationsResource.java`

- Add `@PATCH @Path("/{conversationId}")` method that delegates to `memoryStore.updateConversation()`

### Layer 4: Cucumber Tests

**New file:** `memory-service/src/test/resources/features/update-conversation-rest.feature`

Scenarios:
- Update conversation title (happy path)
- Update title to null/empty (clear title)
- Non-existent conversation returns 404
- Reader cannot update title (403)
- Writer can update title
- Owner can update title

**New file:** `memory-service/src/test/resources/features-grpc/update-conversation-grpc.feature`

Same scenarios via gRPC.

### Layer 5: Quarkus Proxy

**File:** `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/runtime/MemoryServiceProxy.java`

- Add `updateConversation(String conversationId, String body)` method following the `updateConversationMembership()` pattern: parse body as `UpdateConversationRequest`, call `conversationsApi().updateConversation(toUuid(conversationId), request)`, return OK response

### Layer 6: Spring Proxy

**File:** `spring/memory-service-rest-spring/src/main/java/io/github/chirino/memoryservice/client/MemoryServiceProxy.java`

- Add `updateConversation(String conversationId, String body)` method following the `updateConversationMembership()` pattern

### Layer 7: Chat-Quarkus Example

**File:** `quarkus/examples/chat-quarkus/src/main/java/org/acme/ConversationsResource.java`

- Add `@PATCH @Path("/{conversationId}")` endpoint that delegates to `proxy.updateConversation(conversationId, body)`

### Layer 8: Chat-Spring Example

**File:** `spring/examples/chat-spring/src/main/java/org/acme/ConversationsController.java`

- Add `@PatchMapping("/{conversationId}")` endpoint that delegates to `proxy.updateConversation(conversationId, body)`

### Layer 9: Frontend

**File:** `frontends/chat-frontend/src/client/` (regenerated)

- Run `npm run generate-client` to pick up the new `updateConversation` method

**File:** `frontends/chat-frontend/src/components/chat-panel.tsx`

- Add inline title editing state (`isEditingTitle`, `editTitleValue`)
- Show a pencil icon to the left of the title on hover
- On click, replace the `<h2>` with an `<input>` pre-filled with the current title
- On Enter/confirm: call `PATCH /conversations/{id}` with the new title, invalidate `["conversation", conversationId]` and `["conversations"]` queries
- On Escape/blur: cancel editing

## Verification

1. `./mvnw compile` — regenerate Java client models from OpenAPI + gRPC proto stubs
2. `./mvnw install -pl quarkus/memory-service-proto-quarkus` — rebuild proto stubs
3. `./mvnw test -pl memory-service > test.log 2>&1` — run all Cucumber tests
4. `cd frontends/chat-frontend && npm run generate-client && npm run lint && npm run build`
5. Manual: edit a conversation title in the chat UI, verify sidebar updates
