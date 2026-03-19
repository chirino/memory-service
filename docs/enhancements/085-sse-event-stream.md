---
status: proposed
---

# Enhancement 085: SSE Event Stream for Frontend Cache Invalidation

> **Status**: Proposed.

## Summary

Add a Server-Sent Events (SSE) endpoint that streams real-time notification events to frontends for cache invalidation — conversation CRUD, entry appends, response recording lifecycle, and sharing changes. Events are filtered per-connection so users only receive events for resources they have at least read access to. A pluggable event bus enables fan-out across multi-node deployments.

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

### SSE Endpoint

```
GET /v1/events
Accept: text/event-stream
Authorization: Bearer <token>
```

Returns a long-lived SSE stream. The connection stays open until the client disconnects or the server shuts down.

Optional query parameters:

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `conversations` | string (CSV of UUIDs) | _(all accessible)_ | Subscribe to events for specific conversations only |
| `types` | string (CSV) | _(all types)_ | Filter to specific event types |

### Event Format

Each SSE event uses the standard `event:` / `data:` fields:

```
event: conversation.updated
data: {"conversationId":"<uuid>","conversationGroupId":"<uuid>","timestamp":"2026-03-19T10:00:00Z"}

event: entry.appended
data: {"conversationId":"<uuid>","conversationGroupId":"<uuid>","entryId":"<uuid>","timestamp":"2026-03-19T10:00:01Z"}

event: response.started
data: {"conversationId":"<uuid>","conversationGroupId":"<uuid>","recordingId":"<string>","timestamp":"2026-03-19T10:00:02Z"}
```

A periodic keepalive comment prevents connection timeouts:

```
: keepalive
```

### Event Types

| Event Type | Trigger | Payload Fields |
|------------|---------|----------------|
| `conversation.created` | New conversation created | `conversationId`, `conversationGroupId` |
| `conversation.updated` | Title, metadata, or other conversation-level change | `conversationId`, `conversationGroupId`, `fields` |
| `conversation.deleted` | Conversation soft-deleted | `conversationId`, `conversationGroupId` |
| `entry.appended` | New entry added to a conversation | `conversationId`, `conversationGroupId`, `entryId`, `role` |
| `response.started` | Response recording session begins | `conversationId`, `conversationGroupId`, `recordingId` |
| `response.completed` | Response recording session ends successfully | `conversationId`, `conversationGroupId`, `recordingId` |
| `response.failed` | Response recording session fails | `conversationId`, `conversationGroupId`, `recordingId` |
| `membership.added` | Access granted to a user | `conversationId`, `conversationGroupId`, `userId`, `accessLevel` |
| `membership.updated` | Access level changed | `conversationId`, `conversationGroupId`, `userId`, `accessLevel` |
| `membership.removed` | Access revoked from a user | `conversationId`, `conversationGroupId`, `userId` |

All events include a `timestamp` field (ISO 8601).

### Access Control

Events are filtered **per-connection** using an in-memory membership set:

1. **On connect**: Load the set of `conversationGroupId`s the user has membership in (any access level ≥ reader).
2. **Per event**: Check if the event's `conversationGroupId` is in the set. Drop events the user cannot see.
3. **Dynamic updates**: `membership.added` events targeting the connected user expand the set. `membership.removed` events shrink it.
4. **Periodic refresh**: Every 60s, reload the full membership set from the database as a safety net for edge cases (admin operations, bulk sharing).

This approach avoids per-event database queries — filtering is O(1) per event against an in-memory set, with O(connections) periodic DB queries for refresh.

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
    Type                string    `json:"type"`
    ConversationID      uuid.UUID `json:"conversationId"`
    ConversationGroupID uuid.UUID `json:"conversationGroupId"`
    EntryID             *uuid.UUID `json:"entryId,omitempty"`
    UserID              string    `json:"userId,omitempty"`
    RecordingID         string    `json:"recordingId,omitempty"`
    Data                any       `json:"data,omitempty"`
    Timestamp           time.Time `json:"timestamp"`
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

#### Implementations

| Plugin | Config Value | Use Case | External Dependency |
|--------|-------------|----------|---------------------|
| `local` | `local` (default) | Single-node / dev mode | None |
| `redis` | `redis` | Multi-node production | Redis (reuses cache config) |
| `postgres` | `postgres` | Multi-node without Redis | PostgreSQL (reuses DB config) |

**Local** (in-process):
- `sync.Map` of subscriber channels. Publish fans out to all local channels.
- Zero external dependencies — suitable for `task dev:memory-service`.

**Redis Pub/Sub**:
- Publishes to channel `memory-service:events`. Each node subscribes and feeds events into local fan-out.
- Reuses existing Redis connection from cache config (`MEMORY_SERVICE_REDIS_HOSTS`).
- If Redis connection drops, reconnects with exponential backoff. Events during gap are lost (acceptable — SSE is best-effort).

**PostgreSQL LISTEN/NOTIFY**:
- `SELECT pg_notify('memory_service_events', $1)` to publish. `LISTEN memory_service_events` to subscribe.
- 8KB payload limit per notification — sufficient for all event types defined above.
- No additional infrastructure beyond existing PostgreSQL database.
- Same reconnection/best-effort semantics.

#### SSE Handler Flow

```
Client connects to GET /v1/events
  → Authenticate user (standard auth middleware)
  → Load user's conversation group memberships into Set
  → Subscribe to event bus
  → Loop:
      ← Event from bus
      → Is event.ConversationGroupID in membership set?
         Yes → Write SSE event to response writer, flush
         No  → Is this a membership.added for this user?
                Yes → Add to set, write event
                No  → Drop silently
      ← Keepalive timer (30s)
      → Write ": keepalive\n\n", flush
      ← Context cancelled (client disconnect)
      → Unsubscribe, cleanup, return
```

#### Publishing Events

Events are published from the **service/route layer** after successful store operations. Publishing is fire-and-forget — a publish failure does not fail the API request. Errors are logged and metriced.

```go
// Example: after successful conversation creation
s.eventBus.Publish(ctx, eventbus.Event{
    Type:                "conversation.created",
    ConversationID:      conv.ID,
    ConversationGroupID: conv.ConversationGroupID,
    Timestamp:           time.Now(),
})
```

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `MEMORY_SERVICE_EVENTBUS_KIND` | `local` | Event bus implementation: `local`, `redis`, `postgres` |
| `MEMORY_SERVICE_SSE_KEEPALIVE_INTERVAL` | `30s` | Interval between keepalive comments |
| `MEMORY_SERVICE_SSE_MEMBERSHIP_REFRESH` | `60s` | Interval to refresh membership cache per connection |
| `MEMORY_SERVICE_SSE_MAX_CONNECTIONS_PER_USER` | `5` | Max concurrent SSE connections per user (429 if exceeded) |

### gRPC Equivalent

For gRPC clients, a server-streaming RPC provides the same functionality:

```protobuf
rpc SubscribeEvents(SubscribeEventsRequest) returns (stream EventNotification);

message SubscribeEventsRequest {
  repeated string conversation_ids = 1;
  repeated string event_types = 2;
}

message EventNotification {
  string type = 1;
  string conversation_id = 2;
  string conversation_group_id = 3;
  string user_id = 4;
  string entry_id = 5;
  string recording_id = 6;
  google.protobuf.Struct data = 7;
  google.protobuf.Timestamp timestamp = 8;
}
```

Shares the same event bus and access control logic — only the transport differs.

### Frontend Integration

The chat frontend subscribes on mount using the existing `fetch`-based SSE pattern (avoids `EventSource` header limitations):

```typescript
// hooks/useEventStream.ts
const eventSource = new EventSource('/v1/events?token=' + sseTicket);

eventSource.addEventListener('conversation.updated', (e) => {
  const { conversationId } = JSON.parse(e.data);
  queryClient.invalidateQueries(['conversation', conversationId]);
});

eventSource.addEventListener('entry.appended', (e) => {
  const { conversationId } = JSON.parse(e.data);
  queryClient.invalidateQueries(['entries', conversationId]);
});

eventSource.addEventListener('membership.added', (e) => {
  queryClient.invalidateQueries(['conversations']);
});
```

## Testing

```gherkin
Feature: SSE Event Stream

  Background:
    Given the memory service is running
    And a user "alice" exists with an API token
    And a user "bob" exists with an API token

  Scenario: Receive conversation.created event
    Given "alice" is connected to the SSE event stream
    When "alice" creates a conversation "test-conv"
    Then "alice" should receive an SSE event with type "conversation.created"
    And the event should contain "conversationId"

  Scenario: Receive entry.appended event
    Given "alice" has a conversation "test-conv"
    And "alice" is connected to the SSE event stream
    When "alice" appends an entry to "test-conv"
    Then "alice" should receive an SSE event with type "entry.appended"
    And the event should contain "conversationId" and "entryId"

  Scenario: Events are filtered by access — no leakage
    Given "alice" has a conversation "private-conv"
    And "bob" is connected to the SSE event stream
    When "alice" appends an entry to "private-conv"
    Then "bob" should not receive any SSE event within 2 seconds

  Scenario: Receive events after being granted access
    Given "alice" has a conversation "shared-conv"
    And "bob" is connected to the SSE event stream
    When "alice" grants "reader" access to "bob" for "shared-conv"
    Then "bob" should receive an SSE event with type "membership.added"
    When "alice" appends an entry to "shared-conv"
    Then "bob" should receive an SSE event with type "entry.appended"

  Scenario: Stop receiving events after access revoked
    Given "alice" has a conversation "shared-conv"
    And "alice" grants "reader" access to "bob" for "shared-conv"
    And "bob" is connected to the SSE event stream
    When "alice" removes "bob" from "shared-conv"
    Then "bob" should receive an SSE event with type "membership.removed"
    When "alice" appends an entry to "shared-conv"
    Then "bob" should not receive any SSE event within 2 seconds

  Scenario: Response recording lifecycle events
    Given "alice" has a conversation "resp-conv"
    And "alice" is connected to the SSE event stream
    When a response recording starts for "resp-conv"
    Then "alice" should receive an SSE event with type "response.started"
    When the response recording completes for "resp-conv"
    Then "alice" should receive an SSE event with type "response.completed"

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
    Then "alice" should receive an SSE event with type "conversation.deleted"
```

## Tasks

- [ ] Define `EventBus` interface and `Event` type in `internal/registry/eventbus/plugin.go`
- [ ] Implement `local` event bus (in-process Go channels)
- [ ] Implement `redis` event bus (Redis Pub/Sub)
- [ ] Implement `postgres` event bus (LISTEN/NOTIFY)
- [ ] Add `MEMORY_SERVICE_EVENTBUS_KIND` config to `internal/config/config.go`
- [ ] Register event bus as a plugin, initialize in `internal/cmd/serve/server.go`
- [ ] Add SSE endpoint `GET /v1/events` in `internal/plugin/route/agent/events.go`
- [ ] Implement per-connection membership set with periodic refresh
- [ ] Implement per-user connection limit (429 on exceed)
- [ ] Publish events from conversation create/update/delete
- [ ] Publish events from entry append
- [ ] Publish events from membership grant/update/revoke
- [ ] Publish events from response recording start/complete/fail
- [ ] Add gRPC `SubscribeEvents` server-streaming RPC
- [ ] Update OpenAPI spec (`contracts/openapi/openapi.yml`)
- [ ] Update proto file (`contracts/protobuf/memory/v1/memory_service.proto`)
- [ ] Write Cucumber BDD feature tests
- [ ] Write unit tests for event bus implementations
- [ ] Add metrics: `sse_connections_active`, `eventbus_events_published_total`, `eventbus_events_delivered_total`, `eventbus_events_dropped_total`
- [ ] Update chat frontend with `useEventStream` hook and cache invalidation

## Files to Modify

| File | Change |
|------|--------|
| `internal/registry/eventbus/plugin.go` | New — `EventBus` interface, `Event` type, plugin registry |
| `internal/plugin/eventbus/local/local.go` | New — in-process channel-based event bus |
| `internal/plugin/eventbus/redis/redis.go` | New — Redis Pub/Sub event bus |
| `internal/plugin/eventbus/postgres/postgres.go` | New — PostgreSQL LISTEN/NOTIFY event bus |
| `internal/config/config.go` | Add event bus and SSE config fields |
| `internal/plugin/route/agent/events.go` | New — `GET /v1/events` SSE handler |
| `internal/cmd/serve/server.go` | Initialize event bus plugin, wire into route handlers |
| `internal/plugin/route/conversations/conversations.go` | Publish events on create/update/delete |
| `internal/plugin/route/entries/entries.go` | Publish events on entry append |
| `internal/plugin/route/memberships/memberships.go` | Publish events on membership changes |
| `internal/service/responserecorder.go` | Publish events on recording start/complete/fail |
| `contracts/openapi/openapi.yml` | Add `GET /v1/events` endpoint spec |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add `SubscribeEvents` RPC and messages |
| `internal/bdd/testdata/features/sse-events.feature` | New — Cucumber BDD scenarios |
| `frontends/chat-frontend/src/hooks/useEventStream.ts` | New — SSE subscription hook |
| `frontends/chat-frontend/src/providers/EventStreamProvider.tsx` | New — React context for global event stream |

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

## Design Decisions

1. **Event bus at the route/service layer, not the store layer**: The store should remain a pure data-access layer. Publishing events is an application concern that happens after a successful store operation. This keeps stores testable without event bus mocks and avoids publishing events for operations that are later rolled back.

2. **Single pub/sub channel, not per-conversation**: Event volume is bounded by human-driven actions (conversation updates, message appends), not data throughput. A single channel avoids the complexity of managing thousands of per-conversation subscriptions in Redis/PostgreSQL. Server-side filtering per connection is cheap (O(1) set lookup).

3. **Best-effort delivery**: SSE is inherently lossy (network drops, reconnects). Frontends must treat events as cache invalidation hints — on receiving an event, they re-fetch the affected resource via the existing REST API. On reconnect, they do a full refresh. This keeps the event system simple and avoids event persistence, ordering guarantees, or replay.

4. **Membership set per connection, not per-event DB query**: Checking access for every event would create O(events × connections) database queries. An in-memory set with periodic refresh is O(1) per event with O(connections) periodic DB queries — bounded and predictable.

5. **Reuse existing infrastructure**: The Redis event bus reuses the same Redis configuration as the cache plugin. The PostgreSQL event bus reuses the existing database connection. No new infrastructure is required beyond what's already deployed.

6. **Pluggable event bus follows existing plugin pattern**: Same `init()` self-registration, config-driven selection, and interface-based abstraction used by cache and store plugins. Consistent with the codebase's architecture.

## Open Questions

1. **SSE authentication**: The native `EventSource` API doesn't support `Authorization` headers. Options: (a) query-string token `?token=<jwt>` (appears in logs/browser history), (b) short-lived SSE ticket from `POST /v1/events/ticket` (more secure, adds an endpoint), (c) require `fetch`-based SSE client (the existing `useSseStream` hook already does this). Recommendation: option (c) initially, add ticket endpoint if needed.

2. **Rate limiting / max connections**: Should we enforce a per-user connection limit? Proposed default of 5 concurrent SSE connections per user with 429 rejection. Is this sufficient?

3. **Admin/auditor event access**: Should admin and auditor roles receive all events (bypassing the membership filter), or must they explicitly subscribe to specific conversations?

4. **Event coalescing**: Should rapid mutations (e.g., many entries appended in a burst) be coalesced into a single event per conversation per time window? Individual events are simpler; coalescing reduces frontend re-render churn but adds latency. Recommendation: start with individual events, add optional debouncing later if needed.

5. **Redis Pub/Sub vs Redis Streams**: Pub/Sub is fire-and-forget — events during a subscriber disconnect are lost. Redis Streams provide at-least-once delivery with consumer groups. Is the added complexity worth it given that SSE is already best-effort?
