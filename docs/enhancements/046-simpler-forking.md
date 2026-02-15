# Enhancement 046: Simpler Forking — Auto-Create Fork on First Entry

## Summary

Replace the explicit fork endpoint (`POST /v1/conversations/{id}/entries/{entryId}/fork`) with implicit fork creation during the first entry append. When `forkedAtConversationId` and `forkedAtEntryId` are provided on a `CreateEntryRequest` and the target conversation doesn't exist yet, the server auto-creates it as a fork — exactly how new root conversations are already auto-created today.

This eliminates the two-step fork-then-send flow, fixes a race condition where attachments are deleted before the first entry is created, and removes the need for the frontend's `pendingForkRef` synchronization mechanism.

## Motivation

### Bug: Attachments Deleted During Fork Send

When a user forks a message with a freshly-uploaded attachment:

1. User uploads attachment in fork edit box
2. Clicks Send → `handleForkSend()` calls `POST /fork`, stores pending message in `pendingForkRef`, sets `editingMessage = null`
3. A `useEffect` cleanup fires `editClearAll()` which sends `DELETE /v1/attachments/{id}` for non-existing-reference attachments
4. The `pendingForkRef` useEffect fires later and submits the entry with the now-deleted attachment ID
5. Backend's `rewriteAttachmentIds()` silently skips the missing attachment
6. Entry is created without the attachment

### Fragile Multi-Step Flow

The current fork flow requires three separate operations: create fork conversation → switch UI to new conversation → wait for React state sync → submit first message. This creates a window for races and inconsistent state (empty fork conversations, lost attachments).

### Inconsistency with New Conversation Flow

New conversations are auto-created when the first entry is appended. Forks should work the same way — the conversation materializes when content is first written to it, not as a separate setup step.

## Current Architecture

### New Conversation Auto-Creation

In `PostgresMemoryStore.appendUserEntry()`:

```java
if (conversation == null) {
    conversation = new ConversationEntity();
    conversation.setId(cid);
    conversation.setOwnerUserId(userId);
    conversation.setTitle(encryptTitle(inferTitleFromUserEntry(request)));
    // Creates new ConversationGroup with same ID
    conversation.setConversationGroup(conversationGroup);
    conversationRepository.persist(conversation);
    membershipRepository.createMembership(conversationGroup, userId, AccessLevel.OWNER);
}
```

The same pattern exists in `appendAgentEntries()` and in `MongoMemoryStore`.

### Fork Conversation Creation

In `PostgresMemoryStore.forkConversationAtEntry()`:

```java
forkEntity.setOwnerUserId(originalEntity.getOwnerUserId());
forkEntity.setTitle(encryptTitle(title));
forkEntity.setConversationGroup(originalEntity.getConversationGroup()); // shares parent's group
forkEntity.setForkedAtConversationId(originalEntity.getId());
forkEntity.setForkedAtEntryId(previousId);  // entry BEFORE the fork point
conversationRepository.persist(forkEntity);
```

Key difference from root creation: forks join the parent's `ConversationGroup` and set `forkedAtConversationId`/`forkedAtEntryId`. The fork point entry ID is resolved to the **previous** entry (so "fork at entry X" means "include entries before X, exclude X and after").

### Frontend Fork Flow (Current)

```
handleForkSend() → POST /fork → pendingForkRef → onSelectConversationId
  → useEffect detects pendingForkRef → submit(message, attachments)
    → sendMessage → startEventStream → POST /chat SSE
```

### SSE Chain

```
Frontend POST body: { message, attachments }
  → Proxy AgentSseResource.stream()
    → AttachmentResolver.resolve()
    → agent.chat(conversationId, message, attachments)
      → @RecordConversation interceptor
        → ConversationStore.appendUserMessage(conversationId, content, attachments)
          → POST /v1/conversations/{id}/entries on memory-service
```

## Design: Merge Fork Creation Into Entry Append

### API Change

Add two optional fields to `CreateEntryRequest`:

```yaml
CreateEntryRequest:
  properties:
    # ... existing fields ...
    forkedAtConversationId:
      type: string
      format: uuid
      description: >-
        If the conversation doesn't exist yet, auto-create it as a fork of this
        conversation. Ignored when the conversation already exists.
    forkedAtEntryId:
      type: string
      format: uuid
      description: >-
        Entry ID marking the fork point. Entries before this point are inherited;
        entries at and after this point are excluded. Required when
        forkedAtConversationId is set.
```

Remove the fork endpoint and `ForkFromEntryRequest` schema entirely.

### Example Usage

```bash
curl -X POST http://localhost:8080/v1/conversations/{newConversationId}/entries \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "forkedAtConversationId": "parent-conversation-uuid",
    "forkedAtEntryId": "entry-to-fork-at-uuid",
    "channel": "HISTORY",
    "contentType": "history",
    "content": [{"role": "USER", "text": "Hello forked world!"}]
  }'
```

The server:
1. Sees `{newConversationId}` doesn't exist
2. Sees fork metadata is present
3. Creates the conversation as a fork (joins parent's group, sets fork pointers)
4. Appends the entry
5. Returns the entry (201)

If the conversation already exists, the fork fields are ignored and the entry is appended normally.

### Frontend Fork Flow (New)

```
handleForkSend()
  → newConversationId = crypto.randomUUID()
  → onSelectConversationId(newConversationId)
  → startEventStream(newConversationId, message, attachments,
      forkedAtConversationId, forkedAtEntryId)
```

One step. No `pendingForkRef`. No separate fork API call.

## Implementation Status

| Layer | Description | Status |
|-------|-------------|--------|
| 1 | OpenAPI Spec | Done |
| 2 | Proto / gRPC | Done |
| 3 | Memory-Service Store Layer | Done |
| 4 | Quarkus Proxy Extension | Done |
| 5 | Spring Boot Autoconfigure | Done |
| 6 | Example Chat Proxies | Done |
| 7 | Frontend | Done |
| 8 | Update Cucumber Tests | Done |
| 9 | Reproducer Test | Done |

## Implementation Plan

### Layer 1: OpenAPI Spec

**File:** `memory-service-contracts/src/main/resources/openapi.yml`

- Add `forkedAtConversationId` and `forkedAtEntryId` to `CreateEntryRequest` schema
- Remove `POST /v1/conversations/{conversationId}/entries/{entryId}/fork` endpoint
- Remove `ForkFromEntryRequest` schema

### Layer 2: Proto / gRPC

**File:** `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`

Add fork fields to `CreateEntryRequest` message (line 151):
```protobuf
message CreateEntryRequest {
  string user_id = 1;
  Channel channel = 2;
  reserved 3;
  string content_type = 4;
  repeated google.protobuf.Value content = 5;
  optional string indexed_content = 6;
  // Fork metadata: if the target conversation doesn't exist yet, auto-create
  // it as a fork of forked_at_conversation_id at forked_at_entry_id.
  optional bytes forked_at_conversation_id = 7;  // UUID as 16-byte big-endian
  optional bytes forked_at_entry_id = 8;          // UUID as 16-byte big-endian
}
```

Remove from `ConversationsService` (line 389):
```protobuf
// REMOVE:
rpc ForkConversation(ForkConversationRequest) returns (Conversation);
```

Keep `ListForks` RPC — the frontend and admin UI still use it to display fork trees.

Remove `ForkConversationRequest` message (lines 134-140).

**File:** `memory-service/src/main/java/io/github/chirino/memory/grpc/EntriesGrpcService.java`

In `appendEntry()` (line 98): after converting the gRPC `CreateEntryRequest` to the internal `CreateEntryRequest`, also map `forked_at_conversation_id` and `forked_at_entry_id` bytes to string UUIDs and set them on the internal request. These flow through to the store's auto-creation logic.

**File:** `memory-service/src/main/java/io/github/chirino/memory/grpc/ConversationsGrpcService.java`

Remove `forkConversation()` method (lines 112-132). Keep `listForks()`.

**gRPC tests:** `memory-service/src/test/resources/features-grpc/forking-grpc.feature`

Rewrite fork scenarios to use `EntriesService/AppendEntry` with fork metadata instead of `ConversationsService/ForkConversation`. Keep `ListForks` test scenarios.

Also update `conversations-grpc.feature` scenario "Deleting a conversation deletes all forks" (line 183) which currently uses the REST fork step — change to entry-based forking.

### Layer 3: Memory-Service Store Layer

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`

Modify the auto-creation block in `appendUserEntry()` and `appendAgentEntries()`: when fork metadata is present on the request, create the conversation as a fork instead of a root:

1. Look up the parent conversation, validate it exists and user has WRITER access
2. Find the "previous entry" before `forkedAtEntryId` (reuse existing logic from `forkConversationAtEntry`)
3. Use the parent's `ConversationGroup` instead of creating a new one
4. Set `forkedAtConversationId` and `forkedAtEntryId` on the new entity

Extract the common fork-setup logic from `forkConversationAtEntry()` into a reusable private helper, then delete `forkConversationAtEntry()`.

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java`

Same changes for Mongo.

**File:** `memory-service/src/main/java/io/github/chirino/memory/api/dto/CreateUserEntryRequest.java`

Add `forkedAtConversationId` and `forkedAtEntryId` String fields + getters/setters.

**File:** `memory-service/src/main/java/io/github/chirino/memory/api/ConversationsResource.java`

- In `appendEntry()`: copy fork fields from `CreateEntryRequest` to `CreateUserEntryRequest` for the user path
- Fix `rewriteAttachmentIds()`: throw `ResourceNotFoundException("attachment", attachmentId)` instead of silently continuing when an attachment is not found
- Remove `forkConversation()` method

**Remove:**
- `MemoryStore.forkConversationAtEntry()` from interface
- `MeteredMemoryStore` delegation
- `ForkFromEntryRequest.java` DTO

### Layer 4: Quarkus Proxy Extension — Thread Fork Metadata

**File:** `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ConversationInvocation.java`

Add fork fields to the record:
```java
public record ConversationInvocation(
    String conversationId, String userMessage, List<Map<String, Object>> attachments,
    String forkedAtConversationId, String forkedAtEntryId) {}
```

**New files:** `@ForkedAtConversationId` and `@ForkedAtEntryId` parameter annotations in `io.github.chirino.memory.history.annotations`

**File:** `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ConversationInterceptor.java`

In `resolveInvocation()`: detect the new annotations and extract fork fields from method parameters.

In `around()`: pass fork fields to `store.appendUserMessage()`.

**File:** `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ConversationStore.java`

Add overload of `appendUserMessage` that accepts fork metadata and sets the fields on `CreateEntryRequest`.

### Layer 5: Spring Boot Autoconfigure — Thread Fork Metadata

The Spring architecture uses Spring AI Advisors instead of `@RecordConversation` annotations. Fork metadata is passed via request context rather than method parameters.

**File:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/ConversationStore.java`

Add overload of `appendUserMessage` that accepts fork metadata:
```java
public void appendUserMessage(String conversationId, String content,
    List<Map<String, Object>> attachments, @Nullable String bearerToken,
    @Nullable String forkedAtConversationId, @Nullable String forkedAtEntryId)
```
Set the fork fields on `CreateEntryRequest` before calling the memory-service API.

**File:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/ConversationHistoryStreamAdvisor.java`

Extract `forkedAtConversationId` and `forkedAtEntryId` from the `ChatClientRequest` context (passed in by the controller) and pass them to `conversationStore.appendUserMessage()`.

**File:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/ConversationHistoryCallAdvisor.java`

Same change as the stream advisor — extract fork metadata from request context.

### Layer 6: Example Chat Proxies (Quarkus + Spring)

**Quarkus — File:** `quarkus/examples/chat-quarkus/src/main/java/example/AgentSseResource.java`

Add `forkedAtConversationId` and `forkedAtEntryId` to `MessageRequest` DTO. Pass to agent.

**File:** `quarkus/examples/chat-quarkus/src/main/java/example/HistoryRecordingAgent.java`

Add annotated fork parameters:
```java
@RecordConversation
public Multi<ChatEvent> chat(
    @ConversationId String conversationId,
    @UserMessage String userMessage,
    Attachments attachments,
    @ForkedAtConversationId String forkedAtConversationId,
    @ForkedAtEntryId String forkedAtEntryId) {
    return agent.chat(conversationId, userMessage, attachments.contents());
}
```

**Spring — File:** `spring/examples/chat-spring/src/main/java/example/agent/AgentStreamController.java`

Add `forkedAtConversationId` and `forkedAtEntryId` to `MessageRequest` DTO. Pass fork metadata into the `ChatClientRequest` context so the advisors can extract it:
```java
ChatClient.stream()
    .user(message)
    .advisors(spec -> spec.param("forkedAtConversationId", forkConvId)
                          .param("forkedAtEntryId", forkEntryId))
    ...
```

### Layer 7: Frontend

**File:** `frontends/chat-frontend/src/hooks/useStreamTypes.ts`

Add `forkedAtConversationId?` and `forkedAtEntryId?` to `StreamStartParams`.

**File:** `frontends/chat-frontend/src/hooks/useSseStream.ts`

Include fork fields in POST body when present.

**File:** `frontends/chat-frontend/src/components/chat-panel.tsx`

1. **Fix attachment deletion**: Destructure `resetAfterSend` from `useAttachments()`. Call `editResetAfterSend()` before `onForkSend()` in the Send button handler.

2. **Replace `handleForkSend`**: Generate new UUID, start SSE stream directly with fork metadata:
   ```typescript
   const handleForkSend = useCallback(async (attachments?: AttachmentRef[]) => {
     const trimmed = editingText.trim();
     if (!editingMessage || !trimmed) return;
     const newConversationId = crypto.randomUUID();
     const forkConvId = editingMessage.conversationId;
     const forkEntryId = editingMessage.id;
     setEditingMessage(null);
     setEditingText("");
     onSelectConversationId?.(newConversationId);
     markConversationAsStreaming(newConversationId);
     startEventStream(newConversationId, trimmed, true, callbacks,
       streamAttachments, forkConvId, forkEntryId);
   }, [...]);
   ```

3. **Remove `pendingForkRef`**: Delete the ref, the useEffect that processes it, and all references.

4. **Update `startEventStream`**: Add optional fork parameters, pass to `StreamStartParams`.

### Layer 8: Update Cucumber Tests

**File:** `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java`

Replace fork step definitions with a new step that creates an entry with fork metadata:
```gherkin
When I create a forked entry at "${entryId}" from "${conversationId}" with body:
"""
{ "content": [{"role": "USER", "text": "New message"}] }
"""
```

Update all feature files:
- `forking-rest.feature` — many scenarios
- `forked-attachments-rest.feature` — 6 scenarios
- `conversations-rest.feature` — several scenarios
- `sharing-rest.feature` — 2 scenarios
- `forking-grpc.feature` — 8 scenarios (rewrite fork creation tests, keep ListForks tests)
- `conversations-grpc.feature` — 1 scenario (deleting forks)

### Layer 9: Reproducer Test

**New file:** `memory-service/src/test/resources/features/fork-attachment-deletion-rest.feature`

**Scenario 1:** Creating entry referencing a deleted attachment returns 404 (not silent success)

**Scenario 2:** Fork with fresh attachment succeeds when attachment exists

## Verification

1. `./mvnw compile` — regenerate Java client models from OpenAPI + gRPC proto stubs
2. `./mvnw install -pl quarkus/memory-service-proto-quarkus` — rebuild proto stubs module
3. `./mvnw test -pl memory-service > test.log 2>&1` — run all Cucumber tests (REST + gRPC)
4. `cd frontends/chat-frontend && npm run generate-client && npm run lint && npm run build`
5. Manual: fork a message with attachment in the chat UI, verify it works end-to-end
