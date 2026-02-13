# Simplify ResponseResumerService gRPC API

## Motivation

The current ResponseResumerService gRPC API has accumulated naming inconsistencies and unnecessary complexity:

1. **Verbose RPC and service names**: `ResponseResumerService` → `ResponseRecorderService`; `StreamResponseTokens`, `ReplayResponseTokens`, `CancelResponse` should be simpler `Record`, `Replay`, `Cancel`
2. **"token" field name is misleading**: The `token` field carries content (text chunks), not authentication tokens. Rename to `content`.
3. **Unnecessary offset fields**: `current_offset` and `offset` are tracked but never consumed by any client.
4. **Bidirectional streaming is overkill for Record**: `StreamResponseTokens` uses bidirectional streaming (`stream -> stream`) primarily to push `cancel_requested` signals, but this can be handled with a unary response + status enum.
5. **Redirect via gRPC trailing metadata is fragile**: Custom `x-resumer-redirect-host` / `x-resumer-redirect-port` headers require trailer parsing. Better to use explicit response fields.
6. **Boolean success/cancel_requested flags are ambiguous**: Replace with a clear `RecordStatus` enum.

## New Proto Contract

### Service Definition

```protobuf
service ResponseRecorderService {
  // Record response content for a conversation (client streaming, unary response)
  rpc Record(stream RecordRequest) returns (RecordResponse);

  // Replay a recording of a conversation
  rpc Replay(ReplayRequest) returns (stream ReplayResponse);

  // Cancel an in-progress recording
  rpc Cancel(CancelRecordRequest) returns (CancelRecordResponse);

  // Check if response resumer is enabled
  rpc IsEnabled(google.protobuf.Empty) returns (IsEnabledResponse);

  // Check which conversations have active recordings
  rpc CheckRecordings(CheckRecordingsRequest) returns (CheckRecordingsResponse);
}
```

### Message Definitions

```protobuf
enum RecordStatus {
  RECORD_STATUS_UNSPECIFIED = 0;
  RECORD_STATUS_SUCCESS = 1;
  RECORD_STATUS_CANCELLED = 2;
  RECORD_STATUS_ERROR = 3;
}

message RecordRequest {
  bytes conversation_id = 1;  // UUID (16-byte big-endian), required in first message
  string content = 2;         // Content chunk (was "token")
  bool complete = 3;          // Set to true in final message
}

message RecordResponse {
  RecordStatus status = 1;
  string error_message = 2;   // Only set when status = ERROR
}

message ReplayRequest {
  bytes conversation_id = 1;  // UUID (16-byte big-endian)
}

message ReplayResponse {
  string content = 1;            // Content chunk (was "token"; removed "offset" field)
  string redirect_address = 2;   // Set when redirect is needed (format: "host:port")
}

message CancelRecordRequest {
  bytes conversation_id = 1;  // UUID (16-byte big-endian)
}

message CancelRecordResponse {
  bool accepted = 1;
  string redirect_address = 2;   // Set when redirect is needed (format: "host:port")
}

message CheckRecordingsRequest {
  // Conversation identifiers (UUID as 16-byte big-endian binary each)
  repeated bytes conversation_ids = 1;
}

message CheckRecordingsResponse {
  // Conversation IDs with active recordings (UUID as 16-byte big-endian binary each)
  repeated bytes conversation_ids = 1;
}

// IsEnabledResponse unchanged
```

### Name Mapping

| Old | New |
|-----|-----|
| `StreamResponseTokens` (RPC) | `Record` |
| `ReplayResponseTokens` (RPC) | `Replay` |
| `CancelResponse` (RPC) | `Cancel` |
| `StreamResponseTokenRequest` | `RecordRequest` |
| `StreamResponseTokenResponse` | `RecordResponse` |
| `ReplayResponseTokensRequest` | `ReplayRequest` |
| `ReplayResponseTokensResponse` | `ReplayResponse` |
| `CancelResponseRequest` | `CancelRecordRequest` |
| `CancelResponseResponse` | `CancelRecordResponse` |
| `CheckConversations` (RPC) | `CheckRecordings` |
| `CheckConversationsRequest` | `CheckRecordingsRequest` |
| `CheckConversationsResponse` | `CheckRecordingsResponse` |
| `ResponseResumerService` (service) | `ResponseRecorderService` |

### Field Changes

| Message | Old Field | New Field | Notes |
|---------|-----------|-----------|-------|
| `RecordRequest` | `token` | `content` | Rename only |
| `RecordResponse` | `success`, `cancel_requested` | `status` (enum) | Replaces boolean flags |
| `RecordResponse` | `current_offset` | *(removed)* | Unused by all clients |
| `ReplayResponse` | `token` | `content` | Rename only |
| `ReplayResponse` | `offset` | *(removed)* | Unused by all clients |
| `ReplayResponse` | *(new)* | `redirect_address` | Replaces gRPC trailing metadata |
| `CancelRecordResponse` | *(new)* | `redirect_address` | Replaces gRPC trailing metadata |

### Redirect Mechanism Change

**Old approach**: Server throws `ResponseResumerRedirectException` which is converted to gRPC `Status.UNAVAILABLE` with custom trailing metadata headers (`x-resumer-redirect-host`, `x-resumer-redirect-port`). Clients must parse error trailers to extract redirect target.

**New approach**: Server returns a normal response with `redirect_address` field set (format: `"host:port"`). For `Replay`, the server emits a single `ReplayResponse` with only `redirect_address` set and completes the stream. For `Cancel`, the server returns `CancelRecordResponse` with `accepted=false` and `redirect_address` set. Clients check the field and retry against the redirect target.

Benefits:
- No custom gRPC metadata parsing
- Redirect is part of the explicit API contract
- Easier to understand, test, and document
- Works naturally with any gRPC client (no need for trailer interceptors)

### Record Streaming Change

**Old**: Bidirectional streaming (`stream RecordRequest -> stream RecordResponse`). Server pushes `cancel_requested=true` mid-stream.

**New**: Client streaming with unary response (`stream RecordRequest -> RecordResponse`). When the server detects a cancel request, it terminates the call early by returning `RecordResponse` with `status=CANCELLED`. The gRPC call completion signals the client to stop sending.

In gRPC client-streaming RPCs, the server CAN send the response before the client finishes streaming. This effectively terminates the call. The client detects call completion and stops.

### Service Rename Impact

Renaming the proto service from `ResponseResumerService` to `ResponseRecorderService` affects:

- **Generated gRPC stubs**: `ResponseResumerServiceGrpc` → `ResponseRecorderServiceGrpc`, `MutinyResponseResumerServiceGrpc` → `MutinyResponseRecorderServiceGrpc`
- **Server interface**: `implements ResponseResumerService` → `implements ResponseRecorderService`
- **Quarkus gRPC client config**: `@GrpcClient("responseresumer")` and `quarkus.grpc.clients.responseresumer.*` properties → `@GrpcClient("responserecorder")` and `quarkus.grpc.clients.responserecorder.*`
- **Spring gRPC client**: `MemoryServiceGrpcClients` stubs factory method name
- **Feature test RPC paths**: `ResponseResumerService/Record` → `ResponseRecorderService/Record`

Internal Java classes (`ResponseResumerBackend`, `ResponseResumerSelector`, `GrpcResponseResumer`, `ResponseResumer` interface, etc.) are NOT renamed in this change. They could be renamed in a follow-up for consistency, but are not part of the public gRPC API contract.

## Scope of Changes

### 1. Proto Contract

**File**: `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`

- Replace service definition (lines 422-437) with new RPCs
- Replace all message definitions (lines 439-485) with new messages
- Add `RecordStatus` enum
- Add `redirect_address` fields to `ReplayResponse` and `CancelRecordResponse`
- Remove `offset`, `current_offset`, `success`, `cancel_requested` fields

### 2. Server gRPC Implementation

**File**: `memory-service/src/main/java/io/github/chirino/memory/grpc/ResponseResumerGrpcService.java`

**Record method** (biggest change):
- Method signature: `Multi<StreamResponseTokenResponse> streamResponseTokens(Multi<StreamResponseTokenRequest>)` becomes `Uni<RecordResponse> record(Multi<RecordRequest>)`
- Use `Uni.createFrom().emitter()` instead of `Multi.createFrom().emitter()`
- Cancel signal handling: When `backend.cancelStream()` fires, complete the `Uni` with `RecordResponse(status=CANCELLED)` instead of emitting a response with `cancel_requested=true`
- Success handling: When client sends `complete=true` or stream ends, complete with `RecordResponse(status=SUCCESS)`
- Error handling: For access/validation errors, either fail the Uni with gRPC status (for `PERMISSION_DENIED`, `NOT_FOUND`) or complete with `RecordResponse(status=ERROR, error_message=...)` for application errors
- Remove all `currentOffset` tracking

**Replay method**:
- Rename `replayResponseTokens` to `replay`, update types
- Remove `currentOffset` / `offset` tracking entirely
- Redirect handling: Instead of `toRedirectStatus(redirect)`, catch `ResponseResumerRedirectException` and emit a single `ReplayResponse` with `redirect_address` set, then complete the stream
- Field: `.setToken(token)` becomes `.setContent(token)`

**Cancel method**:
- Rename `cancelResponse` to `cancel`, update types
- Redirect handling: Instead of `toRedirectStatus(redirect)`, return `CancelRecordResponse` with `accepted=false` and `redirect_address` set

**Remove**:
- `REDIRECT_HOST_HEADER` and `REDIRECT_PORT_HEADER` constants (no longer needed)
- `toRedirectStatus()` helper method (no longer needed)

### 3. Quarkus Client

**File**: `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/GrpcResponseResumer.java`

**Replay with redirect**:
- `replayWithRedirect()`: Instead of catching `StatusRuntimeException` and parsing trailers, check each `ReplayResponse` for `redirect_address`. If set, collect it and retry with a new channel to that address.
- Implementation: Use `transformToMultiAndMerge` or `onItem().transformToMulti()` to intercept a redirect response, create a new stub, and retry.
- Remove `REDIRECT_HOST_HEADER`, `REDIRECT_PORT_HEADER` constants
- Remove `resolveRedirect(Throwable)` method

**Cancel with redirect**:
- `cancelWithRedirect()`: Check `CancelRecordResponse.redirect_address`. If set, retry against redirect target.
- Simpler than error-based approach since it's a normal response field.

**GrpcResponseRecorder** (inner class):
- `resumerService.streamResponseTokens(requestStream)` returning `Multi` becomes `resumerService.record(requestStream)` returning `Uni<RecordResponse>`
- Subscribe to the `Uni` completion: if `status=CANCELLED` → emit cancel signal; if `status=SUCCESS` → complete; if `status=ERROR` → log error
- `.setToken(token)` becomes `.setContent(content)` in `record()` and `complete()` methods
- Update request/response types throughout

### 4. Spring Client

**File**: `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/GrpcResponseResumer.java`

**Replay**:
- Update types, rename `.replayResponseTokens()` to `.replay()`
- `response.getToken()` becomes `response.getContent()`
- No redirect handling exists in Spring client currently (confirmed by search)

**Cancel**:
- Update types, rename `.cancelResponse()` to `.cancel()`

**GrpcResponseRecorder** (inner class):
- `service.streamResponseTokens(responseObserver)` becomes `service.record(responseObserver)` where `responseObserver` is now `StreamObserver<RecordResponse>` (unary)
- `onNext(RecordResponse response)` is called exactly once; check `status` for cancel signal
- `.setToken(token)` becomes `.setContent(content)`

### 5. Backend Interface

**File**: `memory-service/src/main/java/io/github/chirino/memory/resumer/ResponseResumerBackend.java`

No changes required. The `ResponseRecorder.record(String token)` parameter name is internal Java, not proto-visible. The backend continues to throw `ResponseResumerRedirectException` internally; only the gRPC layer changes how it surfaces to clients.

### 6. Tests

**Feature file**: `memory-service/src/test/resources/features/response-resumer-grpc.feature`

- Rename all RPC references:
  - `ResponseResumerService/StreamResponseTokens` → `ResponseRecorderService/Record`
  - `ResponseResumerService/ReplayResponseTokens` → `ResponseRecorderService/Replay`
  - `ResponseResumerService/CancelResponse` → `ResponseRecorderService/Cancel`
  - `ResponseResumerService/CheckConversations` → `ResponseRecorderService/CheckRecordings`
  - `ResponseResumerService/IsEnabled` → `ResponseRecorderService/IsEnabled`
- Rename field in request bodies: `token:` → `content:`
- Remove assertion: `the gRPC response field "currentOffset" should be 5`
- Add assertion: `the gRPC response field "status" should be "RECORD_STATUS_SUCCESS"`

**Step definitions**: `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java`

- Update all type references (`StreamResponseTokenRequest` → `RecordRequest`, `CheckConversationsRequest` → `CheckRecordingsRequest`, etc.)
- `.setToken()` → `.setContent()`, `.getToken()` → `.getContent()`
- `streamResponseTokens()` → `record()`, `replayResponseTokens()` → `replay()`, `cancelResponse()` → `cancel()`, `checkConversations()` → `checkRecordings()`
- For streaming steps: `resumerService.streamResponseTokens(requestStream)` returns `Multi` → `resumerService.record(requestStream)` returns `Uni`. Adjust subscriptions accordingly.
- For replay steps: `response.getToken()` → `response.getContent()`
- Update the generic `callResponseResumerService()` dispatch method with new case names and types

### 7. Documentation

**File**: `site/src/pages/docs/concepts/response-resumption.md`

- Update all RPC names, message names, field names
- Replace redirect header documentation with redirect response field documentation
- Update sequence diagrams
- Update bidirectional streaming description to client streaming + unary response
- Replace cancel signaling explanation (no more `cancel_requested` in stream, now `RecordStatus.CANCELLED` in unary response)

### 8. Cleanup

**File**: `TODO.md`
- Remove completed items related to this refactoring

**Server code to delete**:
- `REDIRECT_HOST_HEADER`, `REDIRECT_PORT_HEADER` constants in `ResponseResumerGrpcService`
- `toRedirectStatus()` method in `ResponseResumerGrpcService`

**Client code to delete**:
- `REDIRECT_HOST_HEADER`, `REDIRECT_PORT_HEADER` constants in Quarkus `GrpcResponseResumer`
- `resolveRedirect(Throwable)` method in Quarkus `GrpcResponseResumer`

## Implementation Order

1. Update proto file and compile (`./mvnw clean compile`)
2. Update server `ResponseResumerGrpcService` (most complex: Record method rewrite + redirect changes)
3. Update Quarkus client `GrpcResponseResumer` (redirect + Record changes)
4. Update Spring client `GrpcResponseResumer` (type renames + Record changes)
5. Update tests (feature file + step definitions)
6. Update documentation
7. Full build and test verification

## Verification

```bash
# Compile everything
./mvnw clean compile

# Run all tests (redirect to file for analysis)
./mvnw test > test.log 2>&1
# Search test.log for failures

# Specifically verify the response resumer tests
grep -A5 "response-resumer-grpc" test.log

# Verify client modules compile
./mvnw compile -pl quarkus/memory-service-extension/runtime
./mvnw compile -pl spring/memory-service-spring-boot-autoconfigure
```
