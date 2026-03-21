---
status: implemented
---

# Enhancement 087: User-Scoped Event Routing

> **Status**: Implemented. REST SSE and gRPC streams now subscribe by authenticated user, publish paths resolve recipient user IDs up front, and Redis/PostgreSQL clustered event buses route business events on per-user channels with shared broadcast/admin channels.

## Summary

Replace the original broadcast-style `/v1/events` fan-out model with user-scoped event routing. Events are resolved to their affected users at publish time and delivered through per-user subscriptions, with Redis as the recommended clustered transport and PostgreSQL LISTEN/NOTIFY retained as a lower-scale compatibility option.

## Motivation

The current event stream implementation does unnecessary work for the expected load model in [doc/typical-load-patterns.md](../../../doc/typical-load-patterns.md):

- each user typically watches 1 active conversation, sometimes 2 or 3
- per-conversation event rates are modest
- high system load comes from many concurrent users, not from one user generating extreme event volume

Today the system publishes every event cluster-wide and then filters inside each subscriber loop. That creates several problems:

1. **Broadcast amplification**: every node receives every event, and every local subscriber is offered every event.
2. **Irrelevant slow-consumer eviction**: a client can be evicted due to unrelated conversations.
3. **Membership lookup overhead**: access control is checked in the stream path instead of being reflected in routing.
4. **Poor fit for the real audience**: most events are only relevant to one user or a small set of conversation members.

The routing model should reflect the real consumer set directly.

## Design

### Core Approach

Route events by `userID` rather than by conversation group:

- On mutation, determine the target users for the event.
- Publish one event per target user.
- Each node tracks active subscribers by user ID.
- A connected client subscribes only to its own user channel plus any required control channels.

This moves authorization and audience resolution to publish time and keeps the stream path simple.

### Delivery Semantics

- Events remain **best-effort cache-invalidation hints**, not a source of record.
- Mutations still succeed even if event publication fails.
- Clients must still recover from missed events by refetching state after reconnect or `invalidate`.
- Duplicate events remain acceptable.

### Routing Model by Event Type

| Event Kind | Routing Model |
| --- | --- |
| `conversation` | Publish to each user who currently has access to the conversation group |
| `entry` | Publish to each user who currently has access to the conversation group |
| `response` | Publish to each user who currently has access to the conversation group |
| `membership` | Publish to the affected user; optionally also publish invalidation to existing members if needed |
| `stream` | Node-local or cluster-control channel, not per-user business routing |
| `session` | Internal control traffic only |

### Publish-Time User Resolution

The publisher resolves recipients immediately after a successful mutation:

- For conversation, entry, and response events, fetch the current member set for the conversation group.
- Emit one event per member user.
- For membership add/update/remove, emit directly to the affected user and any additional users who must refresh local state.

This removes the need for per-event membership checks in REST SSE and gRPC stream loops.

### Subscriber Model

Each subscriber registers under exactly one application user:

- REST `/v1/events` subscribes the authenticated user.
- gRPC `SubscribeEvents` subscribes the authenticated user.
- Optional `kinds` filtering still applies.
- Conversation-specific filters may still be added later, but they are now an optimization on top of user routing rather than the primary correctness mechanism.

### Transport Model

#### Redis

Redis is the recommended clustered transport for this design.

Illustrative channels:

```text
memory-service:events:user:<userID>
memory-service:events:stream-control
```

Reasons Redis is preferred:

- Pub/Sub naturally supports high-cardinality channel names better than PostgreSQL LISTEN/NOTIFY.
- User-scoped routing keeps channel fan-out aligned with active user sessions.
- Inactive Redis Pub/Sub channels are effectively ephemeral. They are not durable keys; when there are no subscribers, published messages simply have no recipients and are discarded.

#### PostgreSQL LISTEN/NOTIFY

PostgreSQL LISTEN/NOTIFY should still support this design so deployments can use it, but it is not the recommended high-scale clustered transport.

Proposed posture:

- Keep PostgreSQL event routing functional for development, tests, and smaller installs.
- Support user-scoped channels even if subscribe/unsubscribe churn and channel cardinality make it a weaker production choice.
- Recommend Redis when clustered event volume or active-user counts are high.

This keeps transport compatibility without pretending PostgreSQL is the best fit for this routing model.

### REST and gRPC Behavior

REST and gRPC should share the same routing implementation:

- both subscribe by authenticated user ID
- both apply `kinds` filters consistently
- neither performs per-event membership lookups in the delivery loop

The existing gRPC `conversation_ids` field can still be implemented as an optional narrowing filter, but it is no longer the primary scalability mechanism.

### Operational Behavior

#### Slow Consumers

Slow-consumer handling remains useful, but it should now reflect relevant traffic:

- per-user subscriber buffers should fill only with that user's events plus control traffic
- unrelated conversation traffic should no longer evict a subscriber

#### Recovery

If a clustered transport becomes degraded:

- continue local behavior as appropriate
- send `stream` / `invalidate` control events when the system detects that clients may have missed updates

#### Connection Limits

Per-user connection limits still apply. Session tracking can remain per user, but it no longer needs to coexist with a broadcast business-event model.

## Design Decisions

### Why user-scoped routing over conversation-scoped routing?

User-scoped routing makes the subscriber path simpler:

- one authenticated user
- one primary routing key
- no per-event membership checks
- direct alignment with authorization

It shifts some work to publish time, but that is a better trade for the expected workload, where each event usually has a small, well-defined audience.

### Why keep PostgreSQL at all?

Because compatibility still has value:

- small deployments may already rely on PostgreSQL only
- tests and local development benefit from a minimal infrastructure option
- the service can preserve transport flexibility while still recommending Redis for production scale

### Why still keep optional client-side narrowing filters?

Even with per-user routing, a user may have multiple active sessions and multiple active conversations. Additional narrowing filters can still reduce local work and bandwidth, but they are now optimization features rather than essential protection from global broadcast traffic.

## Scaling Model

The main benefit of the new design is that routing cost scales with the actual audience of an event rather than with the total number of connected subscribers.

Let:

- `E` = total events per second in the system
- `N` = number of app nodes
- `S` = average number of event-stream subscribers per node
- `Ue` = average number of users affected by a single event
- `Su` = average number of active event-stream connections for an affected user on a node

### Current Broadcast Model

In the current model:

- every node receives every event
- every local subscriber is offered every event
- filtering happens after delivery into the subscriber path

Approximate work:

- cluster transport work: `O(E x N)`
- node-local delivery attempts: `O(E x S)`
- total delivery attempts across the cluster: `O(E x N x S)`

### Proposed User-Scoped Model

In the proposed model:

- events are published only for affected users
- nodes only subscribe and fan out for users with local active sessions
- local delivery targets only that user's active connections

Approximate work:

- cluster transport work: `O(E x Ue)`
- node-local delivery attempts: `O(E x Su)`
- total delivery attempts across the cluster: `O(E x Ue x Su)`

### Relative Improvement

The approximate improvement factors are:

- cluster transport reduction: `N / Ue`
- local subscriber fan-out reduction: `S / Su`
- total end-to-end delivery reduction: `(N x S) / (Ue x Su)`

This is the key reason user-scoped routing matches the expected workload better. In normal operation:

- `Ue` is usually small, often `1` for private conversations
- `Su` is usually small, often `1` or slightly above due to multiple tabs or devices
- `S` can be very large on a busy node

### Worked Example

Assume:

- `N = 8` app nodes
- `S = 2,000` subscribers per node
- `Ue = 2` users affected per event
- `Su = 1.2` active connections per affected user on a node

Current broadcast model:

- cluster transport fan-out per event: `8`
- total subscriber delivery attempts per event: `8 x 2,000 = 16,000`

Proposed user-scoped model:

- cluster transport fan-out per event: `2`
- total subscriber delivery attempts per event: `2 x 1.2 = 2.4`

Approximate reduction:

- cluster transport: `8 / 2 = 4x`
- subscriber delivery work: `16,000 / 2.4 ~= 6,667x`

The exact numbers depend on sharing patterns and how many simultaneous sessions a user keeps open, but the asymptotic shape is the important point: the current model scales with total connected subscribers, while the proposed model scales with actual event audience size.

## Testing

### BDD Scenarios

```gherkin
Feature: User-scoped event routing

  Scenario: user only receives events for conversations they can access
    Given "alice" is connected to the SSE event stream
    And "bob" is connected to the SSE event stream
    When an entry is appended to a conversation only "alice" can access
    Then "alice" should receive an SSE event with kind "entry" and event "appended"
    And "bob" should not receive any SSE event within 2 seconds

  Scenario: shared conversation events are delivered to all members
    Given "alice" is connected to the SSE event stream
    And "bob" is connected to the SSE event stream
    And "alice" has shared the conversation with "bob"
    When an entry is appended to the shared conversation
    Then "alice" should receive an SSE event with kind "entry" and event "appended"
    And "bob" should receive an SSE event with kind "entry" and event "appended"

  Scenario: unrelated traffic does not evict a subscriber
    Given "alice" is connected to the SSE event stream
    When many unrelated events are published for other users
    Then "alice" should remain connected

  Scenario: gRPC stream uses the same user-scoped routing behavior
    Given gRPC user "alice" is subscribed to events
    When an entry is appended to a conversation only "bob" can access
    Then gRPC user "alice" should not receive any event within 2 seconds
```

### Unit and Integration Tests

- Recipient-resolution tests for each event kind.
- Transport tests for Redis user-channel publish/subscribe behavior.
- Transport tests for PostgreSQL user-channel LISTEN/NOTIFY behavior.
- REST and gRPC parity tests using a shared routing layer.
- Slow-consumer tests verifying unrelated users do not fill each other's buffers.
- Benchmarks comparing:
  - current global broadcast
  - user-scoped routing with Redis
  - user-scoped routing with PostgreSQL

## Tasks

- [x] Refactor event delivery around user-scoped routing semantics.
- [x] Introduce shared publish-time targeting used by REST SSE and gRPC publishers.
- [x] Resolve recipient users at publish time for conversation, entry, and response events.
- [x] Rework membership event publishing for direct user delivery.
- [x] Implement Redis user-channel routing.
- [x] Implement PostgreSQL user-channel LISTEN/NOTIFY routing.
- [x] Retain `stream` / `invalidate` recovery behavior for degraded clustered transport.
- [x] Add event bus and transport tests covering user-scoped routing behavior.
- [x] Benchmark Redis and PostgreSQL under representative multi-user load intentionally deferred; no benchmark work is required to consider this enhancement complete.
- [x] Update operational docs to recommend Redis for higher-scale clustered deployments.

## Files to Modify

| File | Planned Change |
| --- | --- |
| `contracts/openapi/openapi.yml` | Keep or refine `/v1/events` filtering parameters based on the final REST API shape |
| `contracts/protobuf/memory/v1/memory_service.proto` | Keep or refine stream request filters for gRPC |
| `internal/plugin/route/agent/events.go` | Replace subscriber-side membership filtering with user-scoped subscription delivery |
| `internal/grpc/server.go` | Reuse the shared user-scoped routing layer |
| `internal/registry/eventbus/plugin.go` | Extend event bus abstractions for user-scoped publish and subscribe if needed |
| `internal/plugin/eventbus/local/local.go` | Support user-keyed local fan-out |
| `internal/plugin/eventbus/redis/redis.go` | Implement Redis user-channel routing |
| `internal/plugin/eventbus/postgres/postgres.go` | Implement PostgreSQL LISTEN/NOTIFY user-channel routing |
| `internal/plugin/route/conversations/conversations.go` | Publish user-scoped conversation events |
| `internal/plugin/route/entries/entries.go` | Publish user-scoped entry events |
| `internal/plugin/route/memberships/memberships.go` | Publish user-scoped membership events |
| `internal/bdd/testdata/features/sse-events-rest.feature` | Add user-routing scenarios |
| `internal/bdd/testdata/features-grpc/sse-events-grpc.feature` | Add gRPC parity scenarios |

## Security Considerations

- Recipient resolution must reflect the same access-control rules as normal reads.
- User-scoped routing metadata must not leak hidden conversation identifiers to unauthorized users.
- Admin event streaming remains separate and explicitly privileged.
- Per-user channel naming must be internal to the transport layer; external clients must never choose arbitrary user channels.

## Open Questions

- Should response events always go to all conversation members, or only to the user whose agent initiated the response?
- Should membership changes trigger only direct user events, or also broader invalidation for existing members?
- How much optional narrowing should REST and gRPC expose once user routing is in place?
- Do we want transport-specific limits on subscribed user channels per node for PostgreSQL deployments?

## Verification

```bash
go test ./internal/plugin/eventbus/local ./internal/plugin/eventbus/redis ./internal/plugin/eventbus/postgres ./internal/service/eventing
go test ./internal/plugin/route/... ./internal/grpc
go build ./...
```
