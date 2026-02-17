---
status: implemented
supersedes:
  - 023-chat-app-implementation.md
---

# Enhancement 048: Align Chat Apps with Doc-Checkpoint Conventions

> **Status**: Implemented. Realigns class names and REST paths from
> [023](023-chat-app-implementation.md).

## Status
Implemented

## Goal

Refactor `chat-quarkus` and `chat-spring` to adopt the naming conventions, package structure, and REST path patterns established in the `doc-checkpoints` examples. This makes it easy to tell users: "if you combine all the features from the user guides, you get the chat app."

## Current State

### Doc-Checkpoint Conventions (`quarkus/examples/doc-checkpoints/*`)

| Aspect | Convention |
|--------|-----------|
| Package | `org.acme` |
| Chat endpoint | `POST /chat/{conversationId}` — `ChatResource` |
| Conversations proxy | `GET/DELETE /v1/conversations/**` — `ConversationsResource` |
| Resume | `GET /v1/conversations/{id}/resume`, `POST /v1/conversations/resume-check`, `POST /v1/conversations/{id}/cancel` — `ResumeResource` |
| Sharing | Membership endpoints in `ConversationsResource` |
| Ownership transfers | `/v1/ownership-transfers/**` — `OwnershipTransfersResource` |
| Attachments | `/v1/attachments/**` — `AttachmentsProxyResource` |
| Attachment downloads | `/v1/attachments/download/**` — `AttachmentDownloadProxyResource` |
| Search index provider | `PassThroughIndexedContentProvider` |
| Agent | `Agent` interface + `HistoryRecordingAgent` wrapper |
| Config port | 9090 |

### chat-quarkus (Current)

| Aspect | Current |
|--------|---------|
| Package | `example` |
| Chat endpoint | `POST /v1/conversations/{conversationId}/chat` — `AgentSseResource` |
| Conversations proxy | `/v1/conversations/**` — `MemoryServiceProxyResource` |
| Resume (SSE) | `GET /v1/conversations/{id}/resume` — in `AgentSseResource` |
| Resume (check) | `POST /v1/conversations/resume-check` — `ResumeResource` |
| Cancel response | `DELETE /v1/conversations/{id}/response` — in `MemoryServiceProxyResource` |
| Sharing | Membership endpoints in `MemoryServiceProxyResource` |
| Ownership transfers | `/v1/ownership-transfers/**` — `OwnershipTransfersProxyResource` |
| Attachments | `/v1/attachments/**` — `AttachmentsProxyResource` |
| Attachment downloads | `/v1/attachments/download/**` — `AttachmentDownloadProxyResource` |
| Current user | `/v1/me` — `CurrentUserResource` |
| Image generation | `ImageGenerationTool` |
| Attachment client | `AttachmentClient` |
| Transcript indexing | `/v1/conversations/{id}/index` — `TranscriptIndexingResource` |
| Redaction | `RedactionAssistant` |
| Search index provider | `PassThroughIndexedContentProvider` |

### chat-spring (Current)

| Aspect | Current |
|--------|---------|
| Package | `example.agent` |
| Chat endpoint | `POST /v1/conversations/{conversationId}/chat` — `AgentStreamController` |
| Conversations proxy | `/v1/conversations/**` — `MemoryServiceProxyController` |
| Resume (SSE) | `GET /v1/conversations/{id}/resume` — in `AgentStreamController` |
| Resume (check) | `POST /v1/conversations/resume-check` — `ResumeController` |
| Cancel response | `DELETE /v1/conversations/{id}/response` — in `MemoryServiceProxyController` |
| Sharing | Membership endpoints in `MemoryServiceProxyController` |
| Ownership transfers | `/v1/ownership-transfers/**` — `OwnershipTransfersProxyController` |
| Attachments | `/v1/attachments/**` — `AttachmentsProxyController` |
| Attachment downloads | `/v1/attachments/download/**` — `AttachmentDownloadProxyController` |
| Current user | `/v1/me` — `CurrentUserController` |
| Image generation | `ImageGenerationTool` |
| Attachment client | `AttachmentClient` |

---

## Changes

### 1. Package Rename

| App | Current | Target |
|-----|---------|--------|
| chat-quarkus | `example` | `org.acme` |
| chat-spring | `example.agent` | `org.acme` |

All Java source files in both apps get their package declaration updated and imports adjusted.

### 2. Class Renames (chat-quarkus)

| Current Name | New Name | Rationale |
|-------------|----------|-----------|
| `AgentSseResource` | `ChatResource` | Matches checkpoints. Still handles SSE — the name describes the feature, not the transport. |
| `MemoryServiceProxyResource` | `ConversationsResource` | Matches checkpoints. |
| `OwnershipTransfersProxyResource` | `OwnershipTransfersResource` | Matches checkpoints (drop "Proxy" suffix). |
| `ResumeResource` | *(merge into ChatResource)* | In checkpoints, resume endpoints live alongside chat. In chat-quarkus, resume-check is a separate class. Consolidate into `ChatResource` or a new `ResumeResource` matching the checkpoint pattern. |

Classes that have no checkpoint equivalent keep their names:
- `CurrentUserResource` (no checkpoint equivalent, keep as-is)
- `TranscriptIndexingResource` (no checkpoint equivalent, keep as-is)
- `RedactionAssistant` (no checkpoint equivalent, keep as-is)
- `ImageGenerationTool` (no checkpoint equivalent, keep as-is)
- `AttachmentClient` (no checkpoint equivalent, keep as-is)
- `AttachmentsProxyResource` (already matches checkpoint naming)
- `AttachmentDownloadProxyResource` (already matches checkpoint naming)
- `PassThroughIndexedContentProvider` (already matches)
- `Agent` (already matches)
- `HistoryRecordingAgent` (already matches)

### 3. Class Renames (chat-spring)

| Current Name | New Name | Rationale |
|-------------|----------|-----------|
| `AgentStreamController` | `ChatController` | Mirrors `ChatResource` from checkpoints. |
| `MemoryServiceProxyController` | `ConversationsController` | Mirrors `ConversationsResource`. |
| `OwnershipTransfersProxyController` | `OwnershipTransfersController` | Drop "Proxy" to match. |
| `ResumeController` | *(merge into ChatController)* | Same rationale as Quarkus. |
| `AttachmentsProxyController` | `AttachmentsController` | Drop "Proxy" to match. |
| `AttachmentDownloadProxyController` | `AttachmentDownloadController` | Drop "Proxy" to match. |
| `CurrentUserController` | (keep as-is) | |

### 4. REST Path Changes

The only REST path that differs between checkpoints and the chat apps:

| Endpoint | Current (chat apps) | Checkpoint | Change? |
|----------|-------------------|------------|---------|
| Chat (POST) | `/v1/conversations/{id}/chat` | `/chat/{id}` | **Yes** — move to `/chat/{id}` |
| Resume SSE (GET) | `/v1/conversations/{id}/resume` | `/v1/conversations/{id}/resume` | No change needed |
| Resume check (POST) | `/v1/conversations/resume-check` | `/v1/conversations/resume-check` | No change needed |
| Cancel response | `DELETE /v1/conversations/{id}/response` | `POST /v1/conversations/{id}/cancel` | **Yes** — change method to POST and path to `/cancel` |
| All other endpoints | `/v1/conversations/**`, `/v1/attachments/**`, etc. | Same | No change needed |

The chat endpoint path change (`/v1/conversations/{id}/chat` → `/chat/{id}`) cascades to:
- **chat-frontend**: `useSseStream.ts` constructs the SSE URL
- **chat-spring**: `AgentStreamController` path annotation

The cancel response change (`DELETE .../response` → `POST .../cancel`) cascades to:
- **chat-frontend**: `ConversationsService.deleteConversationResponse()` — both the generated client method and any direct calls
- **chat-spring**: `MemoryServiceProxyController` path/method

### 5. Resource Class Consolidation

In the checkpoints, the `ResumeResource` contains resume-check, resume SSE, AND cancel-response. In chat-quarkus these are currently spread across three classes:
- Resume SSE → `AgentSseResource`
- Resume check → `ResumeResource`
- Cancel response → `MemoryServiceProxyResource`

**Target**: Match the checkpoint structure. Create a `ResumeResource` (Quarkus) / `ResumeController` (Spring) that owns all three endpoints, matching checkpoint 05.

### 6. Auth Path Updates

chat-quarkus `application.properties` currently secures `/v1/*`. After the chat path moves to `/chat/*`, the OIDC permission must also cover `/chat/*`:

```properties
# Current
quarkus.http.auth.permission.api.paths=/v1/*

# Updated
quarkus.http.auth.permission.api.paths=/v1/*,/chat/*
```

Or use the same pattern as the checkpoints:
```properties
quarkus.http.auth.permission.authenticated.paths=/chat/*
quarkus.http.auth.permission.authenticated.policy=authenticated
```

The `/v1/*` permission remains since the proxy endpoints still live there.

For chat-spring, the `SecurityConfig` must also permit/protect `/chat/**` accordingly.

---

## Frontend Changes (chat-frontend)

### SSE URL

**File**: `frontends/chat-frontend/src/hooks/useSseStream.ts`

The chat stream URL changes:

```typescript
// Current
url = `/v1/conversations/${conversationId}/chat`

// New
url = `/chat/${conversationId}`
```

The resume URL (`/v1/conversations/${conversationId}/resume`) does NOT change.

### Cancel Response

**File**: `frontends/chat-frontend/src/client/services.gen.ts` (generated)

The cancel endpoint changes from `DELETE /v1/conversations/{id}/response` to `POST /v1/conversations/{id}/cancel`.

This is an auto-generated file from the OpenAPI spec used by the frontend. Either:
1. Update the OpenAPI spec that generates this client, or
2. If the frontend calls the backend directly (not memory-service), update the hand-written call sites.

Check all references to `deleteConversationResponse` / `/response` in the frontend and update.

### No Other Path Changes

All other frontend API paths (`/v1/conversations`, `/v1/attachments`, `/v1/me`, `/v1/ownership-transfers`) remain unchanged.

---

## Task Breakdown

### Phase 1: Package Rename (all three apps)

1. **chat-quarkus**: Rename package `example` → `org.acme` in all Java files under `src/main/java` and `src/test/java`
2. **chat-spring**: Rename package `example.agent` → `org.acme` in all Java files under `src/main/java` and `src/test/java`
3. Update `application.properties` if any properties reference the old package name (e.g., log categories)
4. Update `pom.xml` if the main class or any plugin references the package
5. Compile and verify: `./mvnw compile -pl quarkus/examples/chat-quarkus,spring/examples/chat-spring`

### Phase 2: Class Renames (chat-quarkus)

6. Rename `AgentSseResource` → `ChatResource`
   - Move chat POST and resume GET into this class
   - Keep it at `@Path("/chat")` for the chat endpoint
7. Rename `MemoryServiceProxyResource` → `ConversationsResource`
   - Remove cancel-response endpoint (moves to ResumeResource)
8. Rename `OwnershipTransfersProxyResource` → `OwnershipTransfersResource`
9. Consolidate resume/cancel endpoints into `ResumeResource` matching checkpoint 05 pattern:
   - `POST /v1/conversations/resume-check`
   - `GET /v1/conversations/{id}/resume`
   - `POST /v1/conversations/{id}/cancel`
10. Compile and verify

### Phase 3: Class Renames (chat-spring)

11. Rename `AgentStreamController` → `ChatController`
    - Move chat POST to `/chat/{conversationId}`
12. Rename `MemoryServiceProxyController` → `ConversationsController`
    - Remove cancel-response endpoint
13. Rename `OwnershipTransfersProxyController` → `OwnershipTransfersController`
14. Rename `AttachmentsProxyController` → `AttachmentsController`
15. Rename `AttachmentDownloadProxyController` → `AttachmentDownloadController`
16. Consolidate resume/cancel endpoints into `ResumeController`:
    - `POST /v1/conversations/resume-check`
    - `GET /v1/conversations/{id}/resume`
    - `POST /v1/conversations/{id}/cancel`
17. Compile and verify

### Phase 4: REST Path Changes

18. **chat-quarkus**: Change `ChatResource` path from `/v1/conversations` to `/chat`
    - Chat POST: `/chat/{conversationId}` (was `/v1/conversations/{conversationId}/chat`)
19. **chat-quarkus**: Change cancel from `DELETE .../response` to `POST .../cancel` in `ResumeResource`
20. **chat-spring**: Same chat path change in `ChatController`
21. **chat-spring**: Same cancel change in `ResumeController`
22. **chat-quarkus**: Update auth permission paths in `application.properties` to cover `/chat/*`
23. **chat-spring**: Update `SecurityConfig` to cover `/chat/**`
24. Compile and verify

### Phase 5: Frontend Updates

25. Update SSE URL in `useSseStream.ts`: `/v1/conversations/${id}/chat` → `/chat/${id}`
26. Update cancel-response calls: `DELETE .../response` → `POST .../cancel` (in services, hooks, or generated client)
27. Run frontend lint and build: `cd frontends/chat-frontend && npm run lint && npm run build`

### Phase 6: Verification

28. Run chat-quarkus tests: `./mvnw test -pl quarkus/examples/chat-quarkus`
29. Run chat-spring tests: `./mvnw test -pl spring/examples/chat-spring`
30. Manual smoke test: start chat-quarkus in dev mode, verify frontend chat works end-to-end
31. Manual smoke test: verify resume, sharing, attachments, image generation all still work

---

## Risk Assessment

- **Package rename** is mechanical but touches every file. Low risk.
- **Class renames** within the same module are also mechanical. Low risk.
- **Chat path change** (`/v1/conversations/{id}/chat` → `/chat/{id}`) is the highest-impact change — it affects both backends and the frontend. Must be done atomically across all three.
- **Cancel endpoint change** (DELETE → POST, `/response` → `/cancel`) is a small but breaking API change. Must update frontend in the same commit.
- No data migration needed — this is purely a code/API refactoring.

## Out of Scope

- Changing the doc-checkpoint examples themselves (they are the reference)
- Refactoring the memory-service core
- Changing OpenAPI contracts in `memory-service-contracts` (the proxy endpoints are not contract-defined)
- Making the chat app configuration match checkpoints (chat-quarkus uses dev services; checkpoints use external services — this is intentional)
