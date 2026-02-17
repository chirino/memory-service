---
status: implemented
superseded-by:
  - 049-response-resumer-api-simplification.md
---

# Drop `resumePosition` from ResponseResumer.replay

> **Status**: Implemented. API further simplified and renamed by
> [049](049-response-resumer-api-simplification.md).

## Motivation

The `resumePosition` parameter in `ResponseResumer.replay()` was originally intended to allow resuming mid-stream. However, in practice:

1. The frontend **always** passes `resumePosition: 0` for resume operations
2. The overhead of retransferring the entire response from the beginning is negligible
3. The mid-stream resume logic adds unnecessary complexity to the codebase
4. Supporting partial replay requires offset tracking that complicates both client and server implementations

This document outlines the plan to remove `resumePosition` and simplify the API.

## Scope of Changes

### 1. Proto Contract (`memory-service-contracts`)

**File:** `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`

Remove `resume_position` field from `ReplayResponseTokensRequest`:

```proto
// BEFORE
message ReplayResponseTokensRequest {
  string conversation_id = 1;
  int64 resume_position = 2;    // Byte offset to resume from
}

// AFTER
message ReplayResponseTokensRequest {
  string conversation_id = 1;
  // Field 2 (resume_position) removed - always replays from beginning
}
```

Optionally remove `offset` from `ReplayResponseTokensResponse` if clients don't need cumulative tracking:

```proto
// BEFORE
message ReplayResponseTokensResponse {
  string token = 1;
  int64 offset = 2;            // Cumulative byte offset after this token
}

// AFTER (optional simplification)
message ReplayResponseTokensResponse {
  string token = 1;
  // Field 2 (offset) removed - not needed when always replaying from start
}
```

### 2. Quarkus ResponseResumer Interface

**File:** `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ResponseResumer.java`

```java
// BEFORE
default Multi<String> replay(String conversationId, String resumePosition, String bearerToken) {
    try {
        return replay(conversationId, Long.parseLong(resumePosition), bearerToken);
    } catch (NumberFormatException e) {
        return Multi.createFrom().empty();
    }
}
Multi<String> replay(String conversationId, long resumePosition, String bearerToken);

// AFTER
Multi<String> replay(String conversationId, String bearerToken);
```

### 3. Spring ResponseResumer Interface

**File:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/ResponseResumer.java`

```java
// BEFORE
default Flux<String> replay(String conversationId, @Nullable String resumePosition, @Nullable String bearerToken) { ... }
Flux<String> replay(String conversationId, long resumePosition, @Nullable String bearerToken);

// AFTER
Flux<String> replay(String conversationId, @Nullable String bearerToken);
```

### 4. Backend Interface

**File:** `memory-service/src/main/java/io/github/chirino/memory/resumer/ResponseResumerBackend.java`

```java
// BEFORE
Multi<String> replay(String conversationId, long resumePosition);
Multi<String> replay(String conversationId, long resumePosition, AdvertisedAddress advertisedAddress);

// AFTER
Multi<String> replay(String conversationId);
Multi<String> replay(String conversationId, AdvertisedAddress advertisedAddress);
```

### 5. TempFileResumerBackend Implementation

**File:** `memory-service/src/main/java/io/github/chirino/memory/resumer/TempFileResumerBackend.java`

- Remove `resumePosition` parameter from `replay()` methods
- Simplify `replayFromFile()` to always start from offset 0
- Remove the token-skipping logic (lines 277-288 that check `tokenEndOffset <= resumePosition`)

### 6. gRPC Service Implementation

**File:** `memory-service/src/main/java/io/github/chirino/memory/grpc/ResponseResumerGrpcService.java`

- Remove `long resumePosition = request.getResumePosition();` (line 352)
- Remove `AtomicLong currentOffset = new AtomicLong(resumePosition);` (line 353)
- Update `backend.replay()` call to not pass `resumePosition`
- If keeping offset tracking in response, start from 0

### 7. gRPC Client Implementations

**Quarkus:** `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/GrpcResponseResumer.java`
- Remove `resumePosition` parameter
- Remove `.setResumePosition(resumePosition)` from request building

**Spring:** `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/GrpcResponseResumer.java`
- Remove `resumePosition` parameter
- Remove `.setResumePosition(resumePosition)` from request building

### 8. Noop Implementations

Update to match new interface signatures:
- `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/NoopResponseResumer.java`
- `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/NoopResponseResumer.java`

### 9. Example Agent Endpoints

**Quarkus SSE Resource:** `quarkus/examples/chat-quarkus/src/main/java/example/AgentSseResource.java`

Change endpoint from:
```java
@GET
@Path("/{conversationId}/resume/{resumePosition}")
public Multi<TokenFrame> resume(
        @PathParam("conversationId") String conversationId,
        @PathParam("resumePosition") String resumePosition) {
    return resumer.replay(conversationId, resumePosition, bearerToken)...
}
```

To:
```java
@GET
@Path("/{conversationId}/resume")
public Multi<TokenFrame> resume(
        @PathParam("conversationId") String conversationId) {
    return resumer.replay(conversationId, bearerToken)...
}
```

**Quarkus WebSocket Resource:** `quarkus/examples/chat-quarkus/src/main/java/example/ResumeWebSocket.java`

Change endpoint from:
```java
@WebSocket(path = "/customer-support-agent/{conversationId}/ws/{resumePosition}")
public class ResumeWebSocket {
    public void onOpen(..., @PathParam("resumePosition") String resumePosition) {
        resumer.replay(conversationId, resumePosition, bearerToken)...
    }
}
```

To:
```java
@WebSocket(path = "/customer-support-agent/{conversationId}/ws/resume")
public class ResumeWebSocket {
    public void onOpen(...) {
        resumer.replay(conversationId, bearerToken)...
    }
}
```

**Spring Agent Controller:** `spring/examples/chat-spring/src/main/java/example/agent/AgentStreamController.java`

Change endpoint from:
```java
@GetMapping(path = "/{conversationId}/resume/{resumePosition}", ...)
public SseEmitter resume(
        @PathVariable String conversationId, @PathVariable long resumePosition) {
    return responseResumer.replay(conversationId, resumePosition, bearerToken)...
}
```

To:
```java
@GetMapping(path = "/{conversationId}/resume", ...)
public SseEmitter resume(@PathVariable String conversationId) {
    return responseResumer.replay(conversationId, bearerToken)...
}
```

### 10. Frontend Changes

**Stream Types:** `frontends/chat-frontend/src/hooks/useStreamTypes.ts`

```typescript
// BEFORE
export type StreamStartParams = {
  sessionId: string;
  text: string;
  resumePosition: number;
  resetResume: boolean;
  // ...
};

// AFTER
export type StreamStartParams = {
  sessionId: string;
  text: string;
  resetResume: boolean;
  // resumePosition removed
  // ...
};
```

**SSE Hook:** `frontends/chat-frontend/src/hooks/useSseStream.ts`

- Change resume URL from `/resume/${params.resumePosition}` to `/resume`
- Remove `resumePosition` references in resume detection logic

**WebSocket Hook:** `frontends/chat-frontend/src/hooks/useWebSocketStream.ts`

- Similar changes for WebSocket resume endpoint

**Chat Panel:** `frontends/chat-frontend/src/components/chat-panel.tsx`

- Remove `resumePosition` from stream start calls

### 11. Test Updates

**Cucumber Feature:** `memory-service/src/test/resources/features/response-resumer-grpc.feature`

Update or remove scenarios that test specific `resume_position` values:

- "Replay response tokens from position zero" - Keep, but remove `resume_position: 0` from request
- "Replay response tokens from middle position" - **Remove** (no longer applicable)
- "Replay response tokens with invalid resume position" - **Remove** (no longer applicable)

Update remaining scenarios to not include `resume_position` in requests.

## Implementation Order

1. **Proto contract** - Update the proto file first (source of truth)
2. **Regenerate clients** - Run `./mvnw -pl quarkus/memory-service-rest-quarkus,quarkus/memory-service-proto-quarkus clean compile`
3. **Backend interface** - Update `ResponseResumerBackend`
4. **Backend implementation** - Simplify `TempFileResumerBackend.replayFromFile()`
5. **gRPC service** - Update `ResponseResumerGrpcService.replayResponseTokens()`
6. **Client interfaces** - Update Quarkus and Spring `ResponseResumer` interfaces
7. **Client implementations** - Update `GrpcResponseResumer` classes
8. **Noop implementations** - Update both noop classes
9. **Example endpoints** - Update `AgentSseResource` and any Spring equivalents
10. **Tests** - Update Cucumber features and any unit tests
11. **Frontend** - Update TypeScript types and hooks
12. **Compile and test** - `./mvnw compile` then `./mvnw test`

## Verification

After implementation:

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# Verify frontend builds
cd frontends/chat-frontend && npm run lint && npm run build
```

## Files to Modify (Complete List)

| File | Change Type |
|------|-------------|
| `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto` | Remove field |
| `memory-service/src/main/java/io/github/chirino/memory/resumer/ResponseResumerBackend.java` | Simplify interface |
| `memory-service/src/main/java/io/github/chirino/memory/resumer/TempFileResumerBackend.java` | Remove offset logic |
| `memory-service/src/main/java/io/github/chirino/memory/grpc/ResponseResumerGrpcService.java` | Remove param |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ResponseResumer.java` | Simplify interface |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/GrpcResponseResumer.java` | Remove param |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/NoopResponseResumer.java` | Update signature |
| `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/ResponseResumer.java` | Simplify interface |
| `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/GrpcResponseResumer.java` | Remove param |
| `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/NoopResponseResumer.java` | Update signature |
| `quarkus/examples/chat-quarkus/src/main/java/example/AgentSseResource.java` | Simplify endpoint |
| `quarkus/examples/chat-quarkus/src/main/java/example/ResumeWebSocket.java` | Simplify endpoint |
| `spring/examples/chat-spring/src/main/java/example/agent/AgentStreamController.java` | Simplify endpoint |
| `frontends/chat-frontend/src/hooks/useStreamTypes.ts` | Remove field |
| `frontends/chat-frontend/src/hooks/useSseStream.ts` | Update URL |
| `frontends/chat-frontend/src/hooks/useWebSocketStream.ts` | Update URL |
| `frontends/chat-frontend/src/components/chat-panel.tsx` | Remove param |
| `memory-service/src/test/resources/features/response-resumer-grpc.feature` | Update scenarios |

## Notes

- No backward compatibility concerns per AGENTS.md - this is pre-release development
- The `offset` field in `ReplayResponseTokensResponse` can optionally be kept for debugging/monitoring purposes
- This item is tracked in `TODO.md` line 6
- After implementation, remove the TODO entry
