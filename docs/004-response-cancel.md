# Response Cancel Design (Draft)

## Goals
- Allow a user or agent to cancel an in-progress response generation.
- Expose clear API endpoints to request cancellation.
- Make cancellation observable to agents that are currently streaming tokens.
- Preserve partial response tokens already emitted and recorded.
- Keep the design compatible with the existing Response Resumer flow.

## Non-Goals
- Building a full job scheduler or queue system.
- Guaranteeing sub-second cancellation; best-effort is acceptable.
- Supporting multiple concurrent responses per conversation (single in-progress response remains the default assumption).
- Supporting cancellation when the response resumer is disabled.

## Background / Current State
- The response resumer streams tokens to Redis under a conversation-specific key
  (`conversation:response:{conversationId}`) and exposes gRPC operations to replay
  or check if a response is in progress.
- There is no concept of an explicit response session ID and no cancellation API.
- The agent streams tokens and the Conversation interceptor records them via the
  response resumer but has no way to stop based on user input.

## Proposed Design Overview
Introduce a **cancel signal stream** per conversation that the service observes
and reflect cancellation to agents via the bidirectional token stream.

Key idea:
- When a response is streaming, a separate Redis stream key exists for cancel
  signals: `conversation:response-x:{conversationId}`.
- Clients call a cancel endpoint which XADDs a cancel signal into that stream.
- The recorder (or response streaming adapter) subscribes to the cancel stream
  and stops the upstream token stream when a cancel signal appears.

The cancellation API is available in REST and gRPC so the SPA or backend proxy
can request it, and the agent observes cancellation through the same gRPC stream
used for token streaming.

## Data Model
### Cancel Signal (logical)
- `conversationId` (string)
- `signal` (enum: CANCEL)
- `requestedAt` (timestamp)
- `requestedBy` (userId or "agent")

### Storage Strategy
- **Redis**
  - Cancel stream key: `conversation:response-x:{conversationId}`.
  - Created by the recorder at the start of streaming.
  - Each cancel request is XADDed with fields:
    - `signal=CANCEL`

## API Design
### REST (user/agent)
1) **Cancel an in-progress response**
   - `POST /v1/conversations/{conversationId}/cancel-response`
   - Request: `{ }`
   - Implementation:
     - XADD cancel signal into `conversation:response-x:{conversationId}`.
     - Wait until `conversation:response:{conversationId}` is deleted or 30s elapse.


### gRPC (ResponseResumerService)
Add RPCs to `ResponseResumerService`:
- `CancelResponse(CancelResponseRequest) returns (CancelResponseResponse)`
 - `StreamResponseTokens` becomes bidirectional and can send a cancel signal
   (`cancel_requested=true`) to stop the upstream stream.

### Access Control
- **CancelResponse**:
  - User must have WRITER access to the conversation.
  - Agent API keys are not allowed for cancellation (user session only).
- **HasResponseInProgress**:
  - READER access or valid agent API key.

## Agent Integration
1) The agent begins a response:
   - Begins token streaming via `StreamResponseTokens`.
2) During token streaming:
   - The recorder/adapter:
      - starts the request stream as usual.
      - listens for `cancel_requested` responses from the server.
   - If a cancel signal is received, the adapter stops the upstream LLM stream and
     completes the response recorder.
3) On cancel:
   - The recorder/adapter should stop streaming tokens.

## Response Resumer Integration
- Cancel requests only signal the recorder to stop; resumers will stop naturally
    when no more tokens are recorded.

## DTO Details
### CancelResponseRequest
- `conversationId` (string, path parameter)
- No request body fields.

## Error Semantics
- `POST /v1/conversations/{conversationId}/cancel-response` returns 200 even if no response is active (idempotent).
- 404 only if the conversation is not found or caller lacks access.
- 409 if the response resumer is disabled.

## Detailed Implementation Plan
1) **Contract updates**
   - Add REST endpoints to `memory-service-client/src/main/openapi/openapi.yml`.
   - Add proto messages and RPCs to `memory-service-proto/src/main/proto/.../memory_service.proto`.
   - Regenerate clients (Java + TS) as required.

2) **Server DTOs and resource endpoints**
   - Add DTOs for `CancelResponseRequest` and `ResponseStatus`.
   - Implement REST resource methods in `memory-service` to cancel and fetch status.
   - Implement gRPC methods in `ResponseResumerGrpcService`.

3) **Redis cancel stream support**
   - Extend `ResponseResumerBackend` with:
     - `requestCancel(conversationId, requestedBy, reason)` (XADD cancel signal).
     - `cancelStream(conversationId)` returning `Multi<CancelSignal>`.
   - Implement in `RedisResponseResumerBackend`:
     - `conversation:response-x:{conversationId}` stream with TTL.
     - `cancelStream` using XREAD with blocking and offset tracking.

4) **Streaming integration**
   - Update `ConversationStreamAdapter` to subscribe to `cancelStream` while the
     response is streaming.
   - On cancel signal, stop emitting tokens and terminate the upstream subscription.
   - Ensure recorder `complete()` is called when cancellation is observed.

5) **Agent runtime hooks**
   - Extend `ResponseResumer.ResponseRecorder` with a `cancelStream`.
   - Update `GrpcResponseResumer` to emit cancel signals from the bidirectional
     `StreamResponseTokens` response stream.

6) **Conversation store updates (optional)**
   - Add `markCanceled(conversationId)` to `ConversationStore` so the
     partial response can be persisted with a canceled marker (optional for v1).

7) **UI / Proxy updates**
   - In the agent app, add a proxy endpoint in `MemoryServiceProxyResource` for
     cancel requests so the SPA can call it.
   - Update SPA to expose a "Cancel" action while streaming and call the cancel
     endpoint.

8) **Tests**
   - Add Cucumber feature(s) for:
     - Cancel during streaming tokens.
     - Access control (writer vs reader vs agent API key).
     - Cancel is idempotent with no active response.
     - for both rest and grpc
  
