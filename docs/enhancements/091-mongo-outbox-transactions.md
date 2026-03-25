---
status: proposed
---

# Enhancement 091: Mongo Transactional Event Outbox

> **Status**: Proposed.

## Summary

Upgrade the MongoDB event outbox path added by [090](090-event-outbox.md) from best-effort writes to transactional, replay-capable delivery by using `mongo.Session` transactions for business writes plus outbox inserts, and a change-stream relay for commit-ordered tail publication.

## Motivation

Enhancement [090](090-event-outbox.md) intentionally left MongoDB in a staged state:

- mutation handlers already call the shared `AppendOutboxEvents(...)` path, so the write call sites are future-safe
- Mongo outbox rows/documents are persisted, but `MongoStore.InWriteTx` is still intent-only
- replay is explicitly disabled because there is no authoritative commit-ordered cursor source yet

That is the right intermediate state, but it is not the end state. Without session transactions, Mongo business writes and outbox writes can diverge. Without change streams, Mongo cannot offer the same replay cursor contract as PostgreSQL/SQLite. This enhancement closes that gap without redesigning the handler layer again.

## Design

### Goals

1. Make Mongo business mutations and outbox writes commit atomically.
2. Make Mongo replay use a durable, commit-ordered cursor.
3. Make live tail publish from the same cursor space used by replay.
4. Preserve the existing shared handler contract introduced in [090](090-event-outbox.md).

### Current State

Today, Mongo uses:

- `MongoStore.InWriteTx(...)` as an intent marker only
- `AppendOutboxEvents(...)` to insert `outbox_events` documents directly
- `ListOutboxEvents(...)` returning `ErrOutboxReplayUnsupported`

That means Mongo is structurally aligned with the relational stores, but it does not yet provide the atomicity or replay guarantees that the outbox API advertises elsewhere.

### Target State

Mongo should move to a two-part implementation:

1. **Transactional write path**
   - `MongoStore.InWriteTx(...)` opens a `mongo.Session`
   - all business collection writes and `outbox_events` inserts run through `WithTransaction`
   - `AppendOutboxEvents(...)` uses the session-bound collection handle from the tx-scoped context

2. **Change-stream tail + replay path**
   - a Mongo change stream on `outbox_events` becomes the authoritative source for live tail
   - the public replay cursor is the change-stream resume token, encoded as an opaque string
   - replay queries must bridge from stored outbox documents to the resume token space so `after=<cursor>` and tail publication share one cursor contract

### Transaction Scope

Mongo needs a real tx-scoped context similar in spirit to the existing SQLite/PostgreSQL scope helpers:

```go
type mongoTxScope struct {
    session mongo.Session
    db      *mongo.Database
}
```

Required rules:

- store methods must use session-bound collection handles when present
- nested `InReadTx` / `InWriteTx` calls should reuse the outer session scope
- write scope inside read scope remains invalid

### Outbox Writes

`AppendOutboxEvents(...)` keeps the same handler-facing shape:

```go
func (s *MongoStore) AppendOutboxEvents(ctx context.Context, events []store.OutboxWrite) ([]store.OutboxEvent, error)
```

But its behavior changes:

- inside `InWriteTx`, inserts must use the active session transaction
- returned cursors should no longer be raw document `_id` values once replay is enabled
- the write path should return a temporary internal handle if needed, but the published/tail cursor must come from the change stream resume token

### Cursor Contract

Mongo cursors must be opaque resume tokens, not counters and not ObjectIDs.

| Source | Acceptable | Why |
|---|---|---|
| Change-stream resume token | Yes | Commit-ordered and resumable |
| ObjectID hex string | No | Insert identity, not authoritative replay position |
| Synthetic numeric sequence | No | Adds coordination complexity and still diverges from commit order |

The implementation may persist the last observed resume token alongside each outbox document or maintain a relay checkpoint table/collection, but the externally visible cursor must remain a change-stream token.

### Replay Strategy

Mongo replay should not be enabled until both of these are true:

1. business write + outbox insert are in one session transaction
2. replay can honor the same cursor space as live tail

That means this enhancement should remove the current `ErrOutboxReplayUnsupported` only once the end-to-end cursor path is real. Do not add a partial replay mode based on `_id` ordering.

### Tail Publication

When Mongo outbox replay is enabled, live EventBus publication should come from the change-stream relay, not directly from the request path. That keeps:

- replay cursors
- tail cursors
- duplicate detection during replay-then-tail

in the same space.

### Failure Handling

- if the Mongo transaction aborts, no outbox documents must remain
- if the relay loses its change stream, consumers should receive the same `invalidate` semantics used elsewhere
- if resume token retention is lost, reconnect with an old cursor should produce `invalidate`, not silent gaps

## Testing

### BDD Scenarios

```gherkin
Feature: Mongo transactional outbox
  Scenario: Mongo replay resumes after a committed mutation
    Given the memory service is started with Mongo outbox enabled
    And a conversation is created
    When I reconnect to /v1/events with the saved cursor
    Then I receive only events after that cursor

  Scenario: Mongo aborted write does not leak outbox events
    Given a Mongo write transaction fails before commit
    Then no outbox event is visible for that mutation

  Scenario: Mongo live tail publishes change-stream cursors
    Given I am connected to /v1/events
    When a conversation is updated
    Then the event includes a durable cursor

  Scenario: Stale Mongo resume token triggers invalidate
    Given a saved Mongo cursor is older than the retained resume window
    When I reconnect with after that cursor
    Then I receive an invalidate stream event
```

### Unit Tests

- `InWriteTx` uses `mongo.Session` and reuses nested scopes
- business writes and outbox inserts commit/rollback together
- change-stream resume token encoding/decoding round-trips
- replay rejects stale/expired resume tokens with the correct error
- relay publishes the same cursor later accepted by replay

## Tasks

- [ ] Add Mongo session-backed write scope to `MongoStore.InWriteTx(...)`
- [ ] Make Mongo store methods use session-bound collection handles from context
- [ ] Make `AppendOutboxEvents(...)` participate in the active Mongo transaction
- [ ] Introduce a Mongo outbox relay based on change streams
- [ ] Define the external Mongo cursor format as an opaque encoded resume token
- [ ] Implement Mongo replay reads backed by the same cursor space as the relay
- [ ] Replace `ErrOutboxReplayUnsupported` for Mongo once replay is truly available
- [ ] Add Mongo BDD coverage for replay, stale cursors, and rollback safety
- [ ] Add unit/integration tests for session rollback and relay resume behavior
- [ ] Update [090](090-event-outbox.md) once Mongo transactional replay lands

## Files to Modify

| File | Planned Change |
|---|---|
| `internal/plugin/store/mongo/mongo.go` | Replace intent-only write scope with real session transaction handling |
| `internal/plugin/store/mongo/outbox.go` | Move outbox inserts into the session scope and replace temporary cursor behavior |
| `internal/plugin/store/mongo/*.go` | Update Mongo store methods to honor tx-scoped collection/session access |
| `internal/service/eventstream/` | Add Mongo relay integration or cursor helpers if shared code is needed |
| `internal/cmd/serve/serve.go` | Start/stop any required Mongo relay components |
| `internal/plugin/route/agent/events.go` | Remove Mongo replay gate once replay is implemented |
| `internal/plugin/route/admin/events.go` | Same as agent SSE for admin replay |
| `internal/grpc/server.go` | Enable Mongo replay for gRPC once the store supports it |
| `internal/bdd/testdata/features/` | Add Mongo outbox replay scenarios |
| `internal/bdd/` | Add Mongo replay/rollback step coverage |
| `internal/FACTS.md` | Update the Mongo outbox staging rule after implementation |
| `WORKAROUNDS.md` | Remove any temporary Mongo outbox workaround notes if no longer needed |

## Verification

```bash
# Compile Go
go build ./...

# Mongo BDD replay coverage
go test ./internal/bdd -run 'TestFeaturesMongo/(sse-events-rest|sse-events-grpc)' -count=1 > test-mongo.log 2>&1
# Search for failures using Grep tool on test-mongo.log
```

## Non-Goals

- changing the REST/gRPC outbox API shape introduced in [090](090-event-outbox.md)
- adding exactly-once consumer tracking on the server side
- redesigning non-Mongo datastores

## Design Decisions

### Why require transactions before replay?

Because replay is an integrity feature, not just a convenience feature. A Mongo replay path without session transactions would let the service present a durable cursor contract while still allowing business writes and outbox writes to diverge. That would be worse than the current explicit `unsupported` behavior.

### Why use change-stream tokens instead of `_id`?

Because `_id` is document identity, not the authoritative commit resume position. The outbox cursor must be the same thing the relay uses to continue after reconnect or failover, and for Mongo that is the resume token.

## Open Questions

- Should the relay persist its own checkpoint separately from per-document cursor metadata, or is per-document cursor materialization enough for replay windows?
- Do we want Mongo replay available only on replica sets, with startup validation that rejects standalone deployments when outbox replay is enabled?
