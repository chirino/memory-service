---
layout: ../../../layouts/DocsLayout.astro
title: Response Resumption
description: How streaming responses, resumption, and cancellation work in Memory Service.
---

Response resumption lets users reconnect after a disconnect and pick up a streaming response where it left off. The Memory Service acts as a buffer between the agent producing tokens and clients consuming them, so a page reload or network blip doesn't lose the response.

## How It Works

When an agent generates a streaming response, it sends each token to the Memory Service via the `ResponseRecorderService` gRPC API. The Memory Service buffers these tokens. Frontend clients consume the stream through the agent app's REST/SSE endpoints. If a client disconnects and reconnects, the agent app asks the Memory Service to replay the buffered tokens from the beginning, and the client catches up.

```mermaid
sequenceDiagram
    participant Browser
    participant Agent as Agent App
    participant MS as Memory Service

    Browser->>Agent: POST /chat/{conversationId}
    Agent->>Agent: Call LLM (streaming)

    loop For each token from LLM
        Agent->>MS: Record (gRPC client stream)
        Agent-->>Browser: SSE token
    end
    Agent->>MS: Record (complete=true)
    MS-->>Agent: RecordResponse

    Note over Browser: User disconnects (reload, network issue)

    Browser->>Agent: GET /conversations/{id}/resume
    Agent->>MS: Replay (gRPC)
    MS-->>Agent: Stream of buffered tokens
    Agent-->>Browser: SSE tokens (catch up + live)
```

### Multi-Instance Redirect

When the Memory Service runs as multiple instances, the tokens for a given conversation are buffered on whichever instance the agent streamed them to. If a replay or cancel request arrives at a different instance, that instance returns a response with a `redirect_address` field containing the correct `host:port`. This is **not** a standard gRPC feature — raw gRPC clients will need to handle this field manually. The Quarkus and Spring client libraries provided by Memory Service handle this automatically, parsing the redirect address and retrying against the correct instance (up to 3 times).

```mermaid
sequenceDiagram
    participant Agent as Agent App
    participant MS1 as Memory Service<br/>Instance A
    participant MS2 as Memory Service<br/>Instance B

    Note over Agent,MS1: Agent streamed tokens to Instance A

    Agent->>MS2: Replay (gRPC)
    MS2-->>Agent: ReplayResponse with redirect_address="instanceA:9000"
    Agent->>MS1: Replay (gRPC, redirected)
    MS1-->>Agent: Stream of buffered tokens
```

The client library handles up to 3 consecutive redirects automatically. The same redirect mechanism applies to `Cancel` requests.

## gRPC Service

The `ResponseRecorderService` provides five operations:

### Record

Client-streaming RPC with a unary response, used by the agent app to send response tokens to the Memory Service for buffering.

```protobuf
rpc Record(stream RecordRequest) returns (RecordResponse);
```

**Request stream** — the agent sends one message per token:

| Field | Type | Description |
|-------|------|-------------|
| `conversation_id` | `bytes` | UUID (16-byte big-endian). Required in the **first** message only. |
| `content` | `string` | The token content. |
| `complete` | `bool` | Set to `true` in the **final** message to signal the response is finished. |

**Response** — returned when the stream completes or is cancelled:

| Field | Type | Description |
|-------|------|-------------|
| `status` | `RecordStatus` | `RECORD_STATUS_SUCCESS`, `RECORD_STATUS_CANCELLED`, or `RECORD_STATUS_ERROR`. |
| `error_message` | `string` | Set only when `status` is `RECORD_STATUS_ERROR`. |

The `RecordStatus` enum values:

| Value | Description |
|-------|-------------|
| `RECORD_STATUS_UNSPECIFIED` | Default/unset. |
| `RECORD_STATUS_SUCCESS` | Recording completed normally. |
| `RECORD_STATUS_CANCELLED` | A user requested cancellation — the agent should stop generating. |
| `RECORD_STATUS_ERROR` | An error occurred during recording. |

When the server returns `RECORD_STATUS_CANCELLED`, the agent should stop calling the LLM.

### Replay

Server-streaming RPC that replays all buffered tokens for a conversation.

```protobuf
rpc Replay(ReplayRequest) returns (stream ReplayResponse);
```

**Request:**

| Field | Type | Description |
|-------|------|-------------|
| `conversation_id` | `bytes` | UUID (16-byte big-endian). |

**Response stream:**

| Field | Type | Description |
|-------|------|-------------|
| `content` | `string` | Token content. |
| `redirect_address` | `string` | If set, the tokens are on another instance. Format: `host:port`. |

If the response is still in progress, the replay stream stays open and delivers new tokens as they arrive. Once the response completes, the stream closes.

If the tokens are buffered on a different instance, the first response message will contain a `redirect_address` field with the `host:port` of the correct instance.

### Cancel

Unary RPC that requests cancellation of an in-progress response.

```protobuf
rpc Cancel(CancelRecordRequest) returns (CancelRecordResponse);
```

**Request:**

| Field | Type | Description |
|-------|------|-------------|
| `conversation_id` | `bytes` | UUID (16-byte big-endian). |

**Response:**

| Field | Type | Description |
|-------|------|-------------|
| `accepted` | `bool` | `true` if the cancellation was accepted. |
| `redirect_address` | `string` | If set, the recording is on another instance. Format: `host:port`. |

After cancellation is accepted, the server completes the agent's `Record` call with `RecordStatus.RECORD_STATUS_CANCELLED`, signaling the agent to stop.

Like `Replay`, this operation returns a `redirect_address` if the tokens are buffered on a different instance.

### CheckRecordings

Unary RPC to check which conversations have responses currently in progress.

```protobuf
rpc CheckRecordings(CheckRecordingsRequest)
    returns (CheckRecordingsResponse);
```

**Request:**

| Field | Type | Description |
|-------|------|-------------|
| `conversation_ids` | `repeated bytes` | List of UUIDs to check. |

**Response:**

| Field | Type | Description |
|-------|------|-------------|
| `conversation_ids` | `repeated bytes` | Subset of the input IDs that have responses in progress. |

This is useful for frontends that need to show a "response in progress" indicator when a user opens a conversation list. The agent app can batch-check multiple conversations in a single call.

### IsEnabled

Unary RPC to check whether the response recorder is available.

```protobuf
rpc IsEnabled(google.protobuf.Empty) returns (IsEnabledResponse);
```

**Response:**

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | `bool` | `true` if response resumption is available. |

## Agent Integration Flow

A typical agent app integrates response resumption as follows:

1. **Start streaming** — When the agent receives a chat request, it calls the LLM with streaming enabled and opens a `Record` gRPC stream to the Memory Service.

2. **Forward tokens** — As each token arrives from the LLM, the agent sends it to both the Memory Service (for buffering) and the client (via SSE or similar).

3. **Monitor cancellation** — When the `Record` call completes with `RecordStatus.RECORD_STATUS_CANCELLED`, the agent stops the LLM call.

4. **Complete the stream** — After the last token, the agent sends a final message with `complete=true` and closes the gRPC stream.

5. **Handle resume requests** — When a client reconnects, the agent calls `Replay` and forwards the replayed tokens to the client. If the response is still in progress, the replay stream stays open and delivers new tokens live.

6. **Handle cancel requests** — When a user clicks "stop", the agent calls `Cancel`. The Memory Service signals the streaming agent by completing the `Record` call with `RECORD_STATUS_CANCELLED`.

## Access Control

- **Record** requires `WRITER` access to the conversation (or a valid API key).
- **Replay** requires `READER` access.
- **Cancel** requires `WRITER` access (API key auth is not accepted for cancel).
- **CheckRecordings** requires `READER` access; conversations the user cannot access are silently excluded from the response.

## Next Steps

- See the [Quarkus Response Resumption](/docs/quarkus/response-resumption/) guide for a framework-specific implementation walkthrough.
- Learn about [Conversation Forking](/docs/concepts/forking/) to understand branching conversations.
- Learn about [Entries](/docs/concepts/entries/) to understand how messages are stored.
