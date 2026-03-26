---
status: implemented
---

# Enhancement 086: SSE Event Stream for Frontend Cache Invalidation

> **Status**: Implemented — all tasks complete including Cucumber BDD feature tests. Backend infrastructure (local/Redis/PostgreSQL event buses with batching and health recovery), SSE endpoints, gRPC streaming, event publishing, metrics, OpenAPI/proto contracts, configuration docs, and frontend integration are complete. The original broadcast-plus-subscriber-filtering routing model described here was later replaced by user-scoped routing in [087](implemented/087-user-scoped-event-routing.md).

## Summary

Add a Server-Sent Events (SSE) endpoint that streams real-time notification events to frontends for cache invalidation — conversation CRUD, entry appends, response recording lifecycle, and sharing changes. A pluggable event bus enables fan-out across multi-node deployments. The first implementation filtered events per connection after broadcast; that routing model was later superseded by user-scoped routing in [087](implemented/087-user-scoped-event-routing.md).

## Motivation

1. **Stale frontend state**: Frontends have no way to learn about server-side changes without polling. Conversations created on another tab, entries appended by another agent, or sharing grants from a collaborator are invisible until the user refreshes.
2. **Response recording awareness**: When a long-running LLM response is in progress, other clients viewing the same conversation need to know when streaming starts and stops so they can show progress indicators or auto-refresh.
3. **Multi-user collaboration**: With sharing and access control, multiple users can view the same conversation. Real-time events enable collaborative UX — new messages appearing live, membership changes reflected immediately.
4. **Scalability**: Polling-based invalidation adds load proportional to connected clients regardless of actual change frequency. SSE delivers events only when something happens.

## Non-Goals

- **Full message content streaming**: This endpoint delivers lightweight notification events for cache invalidation, not the actual message/token stream (that's handled by existing response recording SSE endpoints).
- **Guaranteed delivery / offline replay**: If a client disconnects, it misses events. Clients should do a full refresh on reconnect. `Last-Event-ID` replay can be added later but is out of scope.
- **WebSocket support**: SSE is sufficient for server-to-client notifications. WebSocket adds complexity without benefit here.

## Design

This document captures the original SSE/event-bus implementation. The current implementation keeps the same public endpoints and event semantics, but publish routing now happens at user scope as described in [087](implemented/087-user-scoped-event-routing.md).

### Delivery Semantics

- Publish notifications only after the underlying mutation completes successfully.
- Treat notifications as best-effort cache invalidation hints, not as a source of record.
- Missing or duplicated notifications are acceptable; clients must recover by refetching current state.
- Do not block or fail a successful write just because the event bus publish is unavailable.

Failure handling rules:

- If post-mutation publish succeeds, connected clients should receive the event quickly.
- If publish fails, log and metric the failure, continue returning the mutation result to the caller.

#### Pub/Sub Disruption Recovery

Two failure modes can cause SSE clients to miss events:

1. **Publish failure**: A node's mutation succeeds but fails to publish to Redis/PostgreSQL. Local SSE clients on that node still receive the event (via the local bus), but clients on peer nodes miss it.
2. **Subscription loss**: A node loses its Redis/PostgreSQL subscription. SSE clients on that node miss events published by peer nodes.

In both cases the disruption is transient — the event bus reconnects in the background. The question is how to tell SSE clients that they may have missed events during the gap.

**Approach: `stream`/`invalidate` event**

Rather than closing SSE connections and forcing a reconnect storm, the server sends a `stream`/`invalidate` event to affected clients. This tells the client "you may have missed events — do a broad cache refresh" without tearing down the connection.

Flow:

1. Each cross-node event bus implementation (Redis, PostgreSQL) tracks its health state: `healthy` or `degraded`.
2. On **publish failure**: the bus transitions to `degraded` and logs a warning. It does not affect local delivery — the local bus still fans out to local SSE clients.
3. On **subscription loss**: the bus transitions to `degraded`, starts reconnecting in the background, and immediately publishes a `stream`/`invalidate` event through the **local bus only** so SSE clients on this node know they may be missing cross-node events.
4. On **recovery** (publish succeeds after failures, or subscription reconnects): the bus transitions back to `healthy` and publishes a `stream`/`invalidate` event through the **cross-node bus** so peer nodes can forward it to their SSE clients. This covers the publish-failure case where peer nodes' clients missed events.
5. The SSE handler forwards `stream` kind events to all clients without membership filtering (they are connection-level, not resource-level).
6. The client receives `{"event":"invalidate","kind":"stream","data":{"reason":"pubsub recovery"}}` and performs a broad cache invalidation/refetch.

This avoids reconnect storms, keeps existing connections alive, and achieves the same cache-healing outcome. The `stream`/`invalidate` event is idempotent — receiving it multiple times just triggers redundant refetches, which is harmless.

### SSE Endpoint

```
GET /v1/events
Accept: text/event-stream
Authorization: Bearer <token>
```

Returns a long-lived SSE stream. The connection stays open until the client disconnects or the server shuts down.

Optional query parameters:

| Parameter | Type         | Default       | Description                                                |
| --------- | ------------ | ------------- | ---------------------------------------------------------- |
| `kinds`   | string (CSV) | _(all kinds)_ | Filter to specific event kinds (e.g. `conversation,entry`) |

### Admin SSE Endpoint

```
GET /v1/admin/events
Accept: text/event-stream
Authorization: Bearer <admin-token>
```

Streams all events from all users, bypassing the membership filter. Requires admin role.

| Parameter       | Type         | Default       | Description                                                |
| --------------- | ------------ | ------------- | ---------------------------------------------------------- |
| `justification` | string       | _(required)_  | Non-empty reason for subscribing; logged for audit         |
| `kinds`         | string (CSV) | _(all kinds)_ | Filter to specific event kinds (e.g. `conversation,entry`) |

The admin identity and justification are logged on connect and periodically during the session. Returns 400 if `justification` is missing or empty.

### Event Format

Each SSE message uses a single `data:` field containing a JSON envelope with `event` (action), `kind` (resource type), and `data` (kind-specific payload):

```
data: {"event":"updated","kind":"conversation","data":{"conversation":"<uuid>","conversation_group":"<uuid>"}}

data: {"event":"appended","kind":"entry","data":{"conversation":"<uuid>","entry":"<uuid>"}}

data: {"event":"started","kind":"response","data":{"conversation":"<uuid>","recording":"<string>"}}
```

A periodic keepalive comment prevents connection timeouts:

```
: keepalive
```

### Event Kinds and Actions

The `kind` field determines the resource type and the shape of `data`. The `event` field is the action.

| Kind           | Event        | Trigger                                             | `data` Fields                        |
| -------------- | ------------ | --------------------------------------------------- | ------------------------------------ |
| `conversation` | `created`    | New conversation created                            | `conversation`, `conversation_group` |
| `conversation` | `updated`    | Title, metadata, or other conversation-level change | `conversation`, `conversation_group` |
| `conversation` | `deleted`    | Conversation archived                           | `conversation`, `conversation_group` |
| `entry`        | `appended`   | New entry added to a conversation                   | `conversation`, `entry`,             |
| `response`     | `started`    | Response recording session begins                   | `conversation`, `recording`          |
| `response`     | `completed`  | Response recording session ends successfully        | `conversation`, `recording`          |
| `response`     | `failed`     | Response recording session fails                    | `conversation`, `recording`          |
| `membership`   | `added`      | Access granted to a user                            | `conversation_group`, `user`, `role` |
| `membership`   | `updated`    | Access level changed                                | `conversation_group`, `user`, `role` |
| `membership`   | `removed`    | Access revoked from a user                          | `conversation_group`, `user`         |
| `stream`       | `evicted`    | Server closing connection (slow consumer)           | `reason`                             |
| `stream`       | `invalidate` | Events may have been missed (pub/sub disruption)    | `reason`                             |
| `session`      | `created`    | New SSE connection established (internal)           | `connection`, `user`, `node`, `createdAt` |
| `session`      | `shutdown`   | SSE connection closed (internal)                    | `connection`, `user`, `node`         |

### Access Control

Events are filtered using a **shared, node-local cache** of conversation group → member set (keyed by `conversationGroupId`, value is a set of user IDs with access level ≥ reader):

1. **Per event**: Look up the event's `conversationGroupId` in the local cache. If the entry exists and the connected user is in the member set, deliver. If the entry is missing (cache miss), fetch from the database, populate the cache, then check.
2. **TTL-based expiry**: Cache entries have a short TTL (~5 minutes). The bet is that only recently modified conversations will continue to receive modifications, so the working set stays small.
3. **Event-driven invalidation**: `membership` events (added/updated/removed) that arrive via the event bus either update the cached member set directly or invalidate the cache entry, so membership changes are reflected without waiting for TTL expiry.
4. **Local only**: This cache is per-node, not distributed. Each node maintains its own copy. This avoids cross-node cache coherency complexity — the short TTL and event-driven invalidation keep nodes consistent enough for best-effort event delivery.

This approach avoids loading all of a user's conversations on connect (which could be very large) and avoids per-event database queries for hot conversations. Cold conversations incur a single DB lookup on first event, then are cached for the TTL window.

#### Per-User Connection Limit and Session Tracking

Each SSE connection is assigned a unique connection UUID and tracked in a node-local session tracker. On connect, the node publishes an internal `session`/`created` event through the event bus (with `Internal: true` so it is never forwarded to SSE clients). On disconnect, a `session`/`shutdown` event is published. Each node processes these internal events to maintain an eventually-consistent view of all SSE sessions across the cluster.

**Session event fields:**

| Field        | Description                                        |
| ------------ | -------------------------------------------------- |
| `connection` | UUID unique to this SSE connection                 |
| `user`       | User ID that owns the connection                   |
| `node`       | Node/replica identifier (random UUID per process)  |
| `createdAt`  | RFC3339Nano timestamp when the connection started  |

**Eviction logic:**

When a new connection is established and the user's total session count (including remote sessions known via the bus) exceeds the limit, the node evicts the **oldest local** connection by closing its eviction channel. The evicted handler sends a `stream`/`evicted` event with `"reason": "replaced by newer connection"` before shutting down.

This is **best-effort** in multi-node deployments: a node can only evict its own local connections, and the cross-node session count is eventually consistent. After transient inconsistency (e.g., a node restart losing session state), the system converges as new `session`/`created` and `session`/`shutdown` events propagate. The `node` field lets each node distinguish local (evictable) sessions from remote (view-only) sessions.

The default limit is 5 connections per user.

### Multi-Node Architecture

Single-node deployments work without additional infrastructure — events are published and consumed in-process.

For multi-node deployments, a pluggable **event bus** broadcasts events across nodes:

```
┌──────────┐    mutation    ┌──────────────┐
│  Node A  │ ──────────────>│  Event Bus   │
│  (write) │   PUBLISH      │  (Redis /    │
└──────────┘                │  PostgreSQL) │
                            └──────┬───────┘
                       SUBSCRIBE   │
                    ┌──────────────┼──────────────┐
                    v              v              v
              ┌──────────┐  ┌──────────┐  ┌──────────┐
              │  Node A  │  │  Node B  │  │  Node C  │
              │  (SSE    │  │  (SSE    │  │  (SSE    │
              │  clients)│  │  clients)│  │  clients)│
              └──────────┘  └──────────┘  └──────────┘
```

#### Event Bus Plugin Interface

Following the same plugin pattern used by cache and store:

```go
// internal/registry/eventbus/plugin.go

type Event struct {
    Event               string    `json:"event"`  // action: created, updated, deleted, appended, started, completed, failed, added, removed, evicted, invalidate, shutdown
    Kind                string    `json:"kind"`   // resource type: conversation, entry, response, membership, stream, session
    Data                any       `json:"data"`   // kind-specific payload
    ConversationGroupID uuid.UUID `json:"-"`      // used for access control filtering, not serialized to SSE clients
    Internal            bool      `json:"-"`      // internal control events (e.g. session lifecycle), never forwarded to SSE clients but serialized across nodes via wire format
}

type EventBus interface {
    // Publish sends an event to all subscribers across all nodes.
    Publish(ctx context.Context, event Event) error

    // Subscribe returns a channel that receives events.
    // The channel is closed when the context is cancelled.
    Subscribe(ctx context.Context) (<-chan Event, error)

    // Close shuts down the event bus and releases resources.
    Close() error
}
```

#### Cross-Node Publish Batching

The Redis and PostgreSQL event buses use a shared publish pipeline to maximize throughput:

1. `Publish()` writes the event to an outbound channel (capacity 200) and returns immediately. If the channel is full, the publish is dropped, the bus transitions to `degraded`, and a metric is incremented. This ensures mutation handlers are never blocked by cross-node fan-out.
2. A background goroutine drains the outbound channel. It blocks waiting for the first event, then reads up to 100 additional events without blocking (non-blocking drain). Whatever it collected (1–100 events) is published as a single batch.
3. The batch is encoded as newline-delimited JSON (one JSON object per line) — the same framing SSE uses — and sent in a single Redis `PUBLISH` or PostgreSQL `pg_notify` call.
4. On the receiving side, the subscriber decodes the batch back into individual events and feeds each into the local bus.

This batching is transparent to callers of `Publish()` and `Subscribe()`. It reduces the number of Redis/PostgreSQL round-trips under burst load while adding no latency under light load (a single event is published immediately without waiting for a full batch).

#### Implementations

| Plugin     | Config Value      | Use Case                 | External Dependency           |
| ---------- | ----------------- | ------------------------ | ----------------------------- |
| `local`    | `local` (default) | Single-node / dev mode   | None                          |
| `redis`    | `redis`           | Multi-node production    | Redis (reuses cache config)   |
| `postgres` | `postgres`        | Multi-node without Redis | PostgreSQL (reuses DB config) |

**Local** (in-process):

- Bounded in-memory bus used in all modes (including as the per-node fan-out layer for Redis/PostgreSQL).
- Each subscriber gets a buffered channel. Publish fans out to all local channels.
- **Slow-consumer eviction**: If a subscriber's buffer is full, the subscription is closed immediately rather than blocking the publisher. The SSE handler detects the closed channel, terminates the HTTP response, and logs the eviction. The client reconnects and refetches.
- Zero external dependencies — suitable for `task dev:memory-service`.

**Redis Pub/Sub**:

- Publishes batches to channel `memory-service:events` using the cross-node batching pipeline.
- Reuses existing Redis connection from cache config (`MEMORY_SERVICE_REDIS_HOSTS`).
- Tracks health state (`healthy`/`degraded`). On subscription loss, publishes `stream`/`invalidate` to the local bus immediately and reconnects with exponential backoff. On recovery, publishes `stream`/`invalidate` through Redis so peer nodes forward it to their clients.
- Events during the gap are lost (acceptable — the invalidate event tells clients to refetch).

**PostgreSQL LISTEN/NOTIFY**:

- Publishes batches via `SELECT pg_notify('memory_service_events', $1)` using the cross-node batching pipeline. `LISTEN memory_service_events` to subscribe.
- 8KB payload limit per notification — batched payloads that would exceed this are split into multiple `pg_notify` calls.
- No additional infrastructure beyond existing PostgreSQL database.
- Same health tracking, `stream`/`invalidate` recovery, and batching semantics as Redis.

#### SSE Handler Flow

```
Client connects to GET /v1/events
  → Authenticate user (standard auth middleware)
  → Register session (assign connection UUID, track in session tracker)
  → Publish internal session/created event via bus
  → Evict oldest local sessions while user count > limit
  → Subscribe to event bus (bounded buffer)
  → Loop:
      ← Event from bus (channel closed = slow-consumer eviction)
      → Channel closed?
         → Write `data: {"event":"evicted","kind":"stream","data":{"reason":"slow consumer"}}\n\n`
         → Flush, close HTTP response, return
      ← Eviction signal (newer connection evicted this one)
         → Write `data: {"event":"evicted","kind":"stream","data":{"reason":"replaced by newer connection"}}\n\n`
         → Flush, close HTTP response, return
      → Is event internal session event?
         → Update cross-node session tracker, continue
      → Is event.Kind == "stream"? (evicted, invalidate)
         Yes → Deliver directly (no membership filtering — connection-level events)
      → Look up event.ConversationGroupID in local membership cache
         Cache hit  → User in member set? Deliver : Drop
         Cache miss → Fetch from DB, populate cache, then check
      → Is this a membership event for this user?
         Yes → Update or invalidate the cached member set
      ← Keepalive timer (30s)
      → Write ": keepalive\n\n", flush
      ← Context cancelled (client disconnect)
      → Publish internal session/shutdown event via bus
      → Remove session from tracker, cleanup, return
```

#### Publishing Events

Events are published from the **service/route layer** after successful store operations. Publishing is fire-and-forget — a publish failure does not fail the API request. Errors are logged and metriced.

```go
// Example: after successful conversation creation
s.eventBus.Publish(ctx, eventbus.Event{
    Event: "created",
    Kind:  "conversation",
    Data: map[string]any{
        "conversation":       conv.ID,
        "conversation_group": conv.ConversationGroupID,
    },
    ConversationGroupID: conv.ConversationGroupID,
})
```

### Configuration

| Variable                                      | Default | Description                                                  |
| --------------------------------------------- | ------- | ------------------------------------------------------------ |
| `MEMORY_SERVICE_EVENTBUS_KIND`                | `local` | Event bus implementation: `local`, `redis`, `postgres`       |
| `MEMORY_SERVICE_SSE_KEEPALIVE_INTERVAL`       | `30s`   | Interval between keepalive comments                          |
| `MEMORY_SERVICE_SSE_MEMBERSHIP_CACHE_TTL`     | `5m`    | TTL for local conversation-group → members cache entries     |
| `MEMORY_SERVICE_SSE_MAX_CONNECTIONS_PER_USER` | `2`     | Max concurrent SSE connections per user (429 if exceeded)    |
| `MEMORY_SERVICE_SSE_SUBSCRIBER_BUFFER_SIZE`   | `64`    | Per-subscriber channel buffer; full buffer triggers eviction |
| `MEMORY_SERVICE_EVENTBUS_OUTBOUND_BUFFER`     | `200`   | Outbound channel capacity for cross-node publish pipeline    |
| `MEMORY_SERVICE_EVENTBUS_BATCH_SIZE`          | `100`   | Max events per cross-node publish batch                      |

### gRPC Equivalent

For gRPC clients, a server-streaming RPC provides the same functionality:

```protobuf
rpc SubscribeEvents(SubscribeEventsRequest) returns (stream EventNotification);

message SubscribeEventsRequest {
  repeated string conversation_ids = 1;
  repeated string kinds = 2;
}

message EventNotification {
  string event = 1;              // action: created, updated, deleted, etc.
  string kind = 2;               // resource type: conversation, entry, response, membership
  google.protobuf.Struct data = 3; // kind-specific payload
}
```

Shares the same event bus and access control logic — only the transport differs.

### Frontend Integration

The chat frontend subscribes on mount using the existing `fetch`-based SSE pattern (avoids `EventSource` header limitations):

```typescript
// hooks/useEventStream.ts
const eventSource = new EventSource("/v1/events?token=" + sseTicket);

eventSource.onmessage = (e) => {
  const msg = JSON.parse(e.data);
  switch (msg.kind) {
    case "conversation":
      queryClient.invalidateQueries(["conversation", msg.data.conversation]);
      break;
    case "entry":
      queryClient.invalidateQueries(["entries", msg.data.conversation]);
      break;
    case "membership":
      queryClient.invalidateQueries(["conversations"]);
      break;
    case "stream":
      if (msg.event === "invalidate") {
        // Server reports possible missed events — broad cache refresh.
        queryClient.invalidateQueries();
      }
      // "evicted" is followed by connection close; handled by onclose.
      break;
  }
};
```

Frontend behavior:

- Open the SSE stream after login.
- On event receive, invalidate query/cache entries keyed by `kind` + entity ID.
- On `stream`/`invalidate` event, perform a broad cache invalidation — the server is signaling that events may have been lost during a pub/sub disruption.
- If the stream closes (server eviction, network drop, or explicit close), reconnect with exponential backoff and perform a broad cache invalidation/refetch after reconnect succeeds.
- On reconnect and browser refocus, refetch active data to heal any missed notifications.
- Never assume the event stream is complete enough to avoid normal server reads.

## Testing

```gherkin
Feature: SSE Event Stream

  Background:
    Given the memory service is running
    And a user "alice" exists with an API token
    And a user "bob" exists with an API token

  Scenario: Receive conversation created event
    Given "alice" is connected to the SSE event stream
    When "alice" creates a conversation "test-conv"
    Then "alice" should receive an SSE event with kind "conversation" and event "created"
    And the event data should contain "conversation"

  Scenario: Receive entry appended event
    Given "alice" has a conversation "test-conv"
    And "alice" is connected to the SSE event stream
    When "alice" appends an entry to "test-conv"
    Then "alice" should receive an SSE event with kind "entry" and event "appended"
    And the event data should contain "conversation" and "entry"

  Scenario: Events are filtered by access — no leakage
    Given "alice" has a conversation "private-conv"
    And "bob" is connected to the SSE event stream
    When "alice" appends an entry to "private-conv"
    Then "bob" should not receive any SSE event within 2 seconds

  Scenario: Receive events after being granted access
    Given "alice" has a conversation "shared-conv"
    And "bob" is connected to the SSE event stream
    When "alice" grants "reader" access to "bob" for "shared-conv"
    Then "bob" should receive an SSE event with kind "membership" and event "added"
    When "alice" appends an entry to "shared-conv"
    Then "bob" should receive an SSE event with kind "entry" and event "appended"

  Scenario: Stop receiving events after access revoked
    Given "alice" has a conversation "shared-conv"
    And "alice" grants "reader" access to "bob" for "shared-conv"
    And "bob" is connected to the SSE event stream
    When "alice" removes "bob" from "shared-conv"
    Then "bob" should receive an SSE event with kind "membership" and event "removed"
    When "alice" appends an entry to "shared-conv"
    Then "bob" should not receive any SSE event within 2 seconds

  Scenario: Response recording lifecycle events
    Given "alice" has a conversation "resp-conv"
    And "alice" is connected to the SSE event stream
    When a response recording starts for "resp-conv"
    Then "alice" should receive an SSE event with kind "response" and event "started"
    When the response recording completes for "resp-conv"
    Then "alice" should receive an SSE event with kind "response" and event "completed"

  Scenario: Filter by conversation
    Given "alice" has conversations "conv-a" and "conv-b"
    And "alice" is connected to the SSE event stream filtered to "conv-a"
    When "alice" updates "conv-a" title
    And "alice" updates "conv-b" title
    Then "alice" should receive an event for "conv-a" only

  Scenario: Keepalive on idle connection
    Given "alice" is connected to the SSE event stream
    Then "alice" should receive a keepalive comment within 35 seconds

  Scenario: Conversation deletion event
    Given "alice" has a conversation "doomed-conv"
    And "alice" is connected to the SSE event stream
    When "alice" deletes "doomed-conv"
    Then "alice" should receive an SSE event with kind "conversation" and event "deleted"

  Scenario: Slow consumer is evicted with reason
    Given "alice" has a conversation "flood-conv"
    And "alice" is connected to the SSE event stream with a buffer size of 2
    When 10 entries are rapidly appended to "flood-conv"
    Then "alice" should receive an SSE event with kind "stream" and event "evicted"
    And the event data should contain "reason" with value "slow consumer"
    And "alice" SSE connection should be closed by the server
```

## Tasks

- [x] Define `EventBus` interface and `Event` type in `internal/registry/eventbus/plugin.go`
- [x] Implement `local` event bus (in-process Go channels)
- [x] Implement `redis` event bus (Redis Pub/Sub)
- [x] Implement `postgres` event bus (LISTEN/NOTIFY)
- [x] Add `MEMORY_SERVICE_EVENTBUS_KIND` config to `internal/config/config.go`
- [x] Register event bus as a plugin, initialize in `internal/cmd/serve/server.go`
- [x] Add SSE endpoint `GET /v1/events` in `internal/plugin/route/agent/events.go`
- [x] Implement node-local conversation-group → members cache with TTL and event-driven invalidation
- [x] Implement per-user connection limit with session tracking via internal bus events (evicts oldest local connection instead of 429)
- [x] Implement slow-consumer eviction (close subscription when buffer fills, send `stream`/`evicted` event)
- [x] Add admin SSE endpoint `GET /v1/admin/events` with justification logging
- [x] Publish events from conversation create/update/delete
- [x] Publish events from entry append
- [x] Publish events from membership grant/update/revoke
- [x] Publish events from response recording start/complete/fail
- [x] Add gRPC `SubscribeEvents` server-streaming RPC
- [x] Update OpenAPI spec (`contracts/openapi/openapi.yml`)
- [x] Update proto file (`contracts/protobuf/memory/v1/memory_service.proto`)
- [x] Write Cucumber BDD feature tests
- [x] Write unit tests for event bus implementations
- [x] Add metrics: `sse_connections_active`, `eventbus_events_published_total`, `eventbus_events_delivered_total`, `eventbus_events_dropped_total`, `eventbus_subscriber_evictions_total`
- [x] Update chat frontend with `useEventStream` hook and cache invalidation
- [x] Update `site/src/pages/docs/configuration.mdx` with Event Bus and SSE configuration section
- [x] Add concept doc `site/src/pages/docs/concepts/events.mdx` with REST/curl usage and CurlTests
- [x] Create `03b-with-events` checkpoints for all 5 frameworks (proxies `/v1/events` SSE endpoint)
- [x] Add framework-specific event docs for LangGraph, LangChain, TypeScript, Quarkus, Spring
- [x] Update sidebar navigation with Real-Time Events entries

## Files to Modify

| File                                                            | Change                                                    |
| --------------------------------------------------------------- | --------------------------------------------------------- |
| `internal/registry/eventbus/plugin.go`                          | New — `EventBus` interface, `Event` type, plugin registry |
| `internal/plugin/eventbus/local/local.go`                       | New — in-process channel-based event bus                  |
| `internal/plugin/eventbus/redis/redis.go`                       | New — Redis Pub/Sub event bus                             |
| `internal/plugin/eventbus/postgres/postgres.go`                 | New — PostgreSQL LISTEN/NOTIFY event bus                  |
| `internal/config/config.go`                                     | Add event bus and SSE config fields                       |
| `internal/plugin/route/agent/events.go`                         | New — `GET /v1/events` SSE handler                        |
| `internal/plugin/route/admin/events.go`                         | New — `GET /v1/admin/events` admin SSE handler            |
| `internal/cmd/serve/server.go`                                  | Initialize event bus plugin, wire into route handlers     |
| `internal/plugin/route/conversations/conversations.go`          | Publish events on create/update/delete                    |
| `internal/plugin/route/entries/entries.go`                      | Publish events on entry append                            |
| `internal/plugin/route/memberships/memberships.go`              | Publish events on membership changes                      |
| `internal/service/responserecorder.go`                          | Publish events on recording start/complete/fail           |
| `contracts/openapi/openapi.yml`                                 | Add `GET /v1/events` and `GET /v1/admin/events` specs     |
| `contracts/protobuf/memory/v1/memory_service.proto`             | Add `SubscribeEvents` RPC and messages                    |
| `internal/bdd/testdata/features/sse-events-rest.feature`        | New — Cucumber BDD scenarios                              |
| `internal/bdd/steps_sse_events.go`                              | New — SSE event stream step definitions                   |
| `frontends/chat-frontend/src/hooks/useEventStream.ts`           | New — SSE subscription hook                               |
| `frontends/chat-frontend/src/providers/EventStreamProvider.tsx` | New — React context for global event stream               |

## Verification

```bash
# Compile Go
go build ./...

# Run BDD tests
go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1 > test.log 2>&1
# Search for failures using Grep tool on test.log

# Run event bus unit tests
go test ./internal/plugin/eventbus/... -count=1
go test ./internal/registry/eventbus/... -count=1

# Frontend
cd frontends/chat-frontend && npm run lint && npm run build
```

## Observability

- **Metrics**: `sse_connections_active` (gauge), `eventbus_events_published_total`, `eventbus_events_delivered_total`, `eventbus_events_dropped_total`, `eventbus_subscriber_evictions_total`.
- **Logs**: stream open/close, authorization failures, slow-consumer eviction, publish failures, Redis subscriber loss/reconnect, resync control events.

## Design Decisions

1. **Event bus at the route/service layer, not the store layer**: The store should remain a pure data-access layer. Publishing events is an application concern that happens after a successful store operation. This keeps stores testable without event bus mocks and avoids publishing events for operations that are later rolled back.

2. **Single pub/sub channel, not per-conversation**: Event volume is bounded by human-driven actions (conversation updates, message appends), not data throughput. A single channel avoids the complexity of managing thousands of per-conversation subscriptions in Redis/PostgreSQL. Server-side filtering per connection is cheap (O(1) set lookup).

3. **Best-effort delivery**: SSE is inherently lossy (network drops, reconnects). Frontends must treat events as cache invalidation hints — on receiving an event, they re-fetch the affected resource via the existing REST API. On reconnect, they do a full refresh. This keeps the event system simple and avoids event persistence, ordering guarantees, or replay.

4. **Shared local membership cache, not per-connection sets**: Loading all conversations per user on connect doesn't scale — users can have thousands of conversations. Instead, a node-local cache maps conversation group → members with a short TTL (~5 min). Only recently active conversations occupy cache space. Membership events update or invalidate cache entries immediately, so access changes are reflected without waiting for TTL. Cache misses trigger a single DB lookup, then the result is cached for subsequent events on that group.

5. **Reuse existing infrastructure**: The Redis event bus reuses the same Redis configuration as the cache plugin. The PostgreSQL event bus reuses the existing database connection. No new infrastructure is required beyond what's already deployed.

6. **Pluggable event bus follows existing plugin pattern**: Same `init()` self-registration, config-driven selection, and interface-based abstraction used by cache and store plugins. Consistent with the codebase's architecture.

7. **Slow consumers are evicted, not back-pressured**: A slow SSE client must never block the publisher or degrade delivery to other subscribers. When a subscriber's bounded buffer fills, the subscription is closed immediately. Before closing the HTTP response, the server writes a final `stream`/`evicted` event so the client knows why the connection was terminated and can distinguish eviction from a network drop. The client reconnects and refetches — consistent with best-effort semantics. This is simpler and safer than back-pressure or event dropping within the buffer.

8. **Pub/sub disruption recovery via `stream`/`invalidate`, not connection teardown**: When a node loses its pub/sub connection or fails to publish, it could close all SSE connections to force reconnect-based healing. Instead, we send a `stream`/`invalidate` event that tells clients to do a broad cache refresh while keeping the connection alive. This avoids reconnect storms (all clients hitting the server simultaneously), preserves the connection for future events, and achieves the same cache-healing outcome. The invalidate event is idempotent — duplicate delivery just triggers redundant refetches.

## Resolved Questions

1. **SSE authentication**: Use `fetch`-based SSE client (option c). The existing `useSseStream` hook already sends `Authorization` headers. Add a ticket endpoint later if `EventSource` support is needed.

2. **Rate limiting / max connections**: Default of 2 concurrent SSE connections per user with 429 rejection. Enforced via the cache plugin (Redis/Infinispan) so the limit holds across nodes. Each node increments a per-user counter on connect and decrements on disconnect; a short TTL on the counter key handles crash recovery (stale counters expire).

3. **Admin event access**: A separate admin endpoint `GET /v1/admin/events` streams all events from all users, bypassing the membership filter. The request must include a `justification` query parameter (non-empty string). The justification and admin identity are logged on connect and periodically during the session for audit purposes.

4. **Event coalescing**: Not implemented for now. Individual events are simpler. Coalescing can be added later if frontend re-render churn becomes a problem.

5. **Redis Pub/Sub vs Redis Streams**: Use Redis Pub/Sub. Fire-and-forget semantics are consistent with the best-effort delivery model. Streams add complexity (consumer groups, acknowledgments, trimming) without meaningful benefit given SSE is already lossy.
