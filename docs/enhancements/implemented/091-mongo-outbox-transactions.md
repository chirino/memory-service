---
status: implemented
---

# Enhancement 091: Mongo Transactional Event Outbox

> **Status**: Implemented — outbox-enabled writes use MongoDB session transactions and a change-stream relay supplies ordered live and replay cursors.

## Summary

Upgrade the MongoDB event outbox path added by [090](../090-event-outbox.md) from best-effort writes to transactional, replay-capable delivery by using `mongo.Session` transactions for business writes plus outbox inserts, and a change-stream relay for commit-ordered tail publication.

## Motivation

Enhancement [090](../090-event-outbox.md) intentionally left MongoDB in a staged state:

- mutation handlers already call the shared `AppendOutboxEvents(...)` path, so the write call sites are future-safe
- Mongo outbox rows/documents are persisted, but `MongoStore.InWriteTx` is still intent-only
- replay is explicitly disabled because there is no authoritative commit-ordered cursor source yet

That is the right intermediate state, but it is not the end state. Without session transactions, Mongo business writes and outbox writes can diverge. Without change streams, Mongo cannot offer the same replay cursor contract as PostgreSQL/SQLite. This enhancement closes that gap without redesigning the handler layer again.

## Design

### Goals

1. Make Mongo business mutations and outbox writes commit atomically.
2. Make Mongo replay use a durable, commit-ordered cursor.
3. Make live tail publish from the same cursor space used by replay.
4. Preserve the existing shared handler contract introduced in [090](../090-event-outbox.md).

### Implemented State

Mongo now uses:

- `MongoStore.InWriteTx(...)` with `mongo.Session.WithTransaction` whenever the outbox is enabled
- the transaction session carried by `context.Context` for business collection and `outbox_events` writes
- startup topology validation that requires a replica set or mongos when the outbox is enabled
- a change-stream relay that stores its durable resume token and assigns a stable event sequence inside one transaction
- opaque `mongo:<base64url BSON>` cursors: change-stream tokens for live rows and stable relay-generated anchors for committed rows recovered outside retained stream history
- `ListOutboxEvents(...)` paging materialized events in relay order and reporting evicted cursor anchors as stale
- request-path publication suppression so live subscribers receive only relay cursors
- a durable published sequence that makes every relay retry the oldest unpublished event before a later event can be published

The mutation, outbox insert, relay checkpoint, replay materialization, and live publication now share one resumable cursor space.

### Target State

Mongo should move to a two-part implementation:

1. **Transactional write path**
   - `MongoStore.InWriteTx(...)` opens a `mongo.Session`
   - all business collection writes and `outbox_events` inserts run through `WithTransaction`
   - `AppendOutboxEvents(...)` uses the session-bound collection handle from the tx-scoped context

2. **Change-stream tail + replay path**
   - a Mongo change stream on `outbox_events` becomes the authoritative source for live tail
   - public replay cursors are opaque BSON anchors encoded as strings
   - live inserts use their change-stream resume token, while startup/history-loss recovery assigns a stable BSON anchor to committed unmaterialized rows
   - replay queries resolve either form through the stored outbox document, while a separate state token resumes the change stream

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
- the write path returns no public cursor before commit; the relay assigns the public cursor during materialization

### Cursor Contract

Mongo cursors must be opaque BSON anchors, not counters and not raw ObjectIDs.

| Source | Acceptable | Why |
|---|---|---|
| Change-stream resume token | Yes | Commit-ordered and resumable |
| Stable recovery anchor | Yes | Preserves ordered replay when the original stream token is unavailable |
| ObjectID hex string | No | Insert identity, not authoritative replay position |
| Synthetic numeric sequence | No | Adds coordination complexity and still diverges from commit order |

The relay persists the last observed resume token in its state collection. Each outbox
document stores its distinct public BSON cursor. Recovered cursors include the document
identity inside an opaque BSON value, while ordering remains the relay-assigned event
sequence.

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
- if resume token retention is lost, publish `invalidate`, open a fresh stream before scanning, and materialize every committed row still lacking a sequence
- if materialization succeeds but publication fails, do not advance the durable published sequence; any relay may retry, and duplicate delivery is allowed
- retention may delete only rows whose event sequence is at or below the durable published sequence; it retains unmaterialized and unpublished rows, including when relay state is absent

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

- [x] Add Mongo session-backed write scope to `MongoStore.InWriteTx(...)`
- [x] Make Mongo store methods use the active session context
- [x] Make `AppendOutboxEvents(...)` participate in the active Mongo transaction
- [x] Introduce a Mongo outbox relay based on change streams
- [x] Define the external Mongo cursor format as an opaque encoded resume token
- [x] Implement Mongo replay reads backed by the same cursor space as the relay
- [x] Replace `ErrOutboxReplayUnsupported` for Mongo once replay is truly available
- [x] Add Mongo BDD replay coverage and replica-set integration coverage for stale cursors and rollback safety
- [x] Add unit/integration tests for session rollback and relay resume behavior
- [x] Update [090](../090-event-outbox.md) once Mongo transactional replay lands

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
go test -tags='sqlite_fts5 auth_testfixtures' ./internal/bdd -run TestFeaturesMongoOutbox -count=1 > test-mongo.log 2>&1
go test ./internal/plugin/store/mongo -run TestMongoOutboxTransactionRelayReplayAndStaleCursor -count=1
# Search for failures using Grep tool on test-mongo.log
```

## Non-Goals

- changing the REST/gRPC outbox API shape introduced in [090](../090-event-outbox.md)
- adding exactly-once consumer tracking on the server side
- redesigning non-Mongo datastores

## Design Decisions

### Why require transactions before replay?

Because replay is an integrity feature, not just a convenience feature. A Mongo replay path without session transactions would let the service present a durable cursor contract while still allowing business writes and outbox writes to diverge. That would be worse than the current explicit `unsupported` behavior.

### Why use change-stream tokens instead of `_id`?

Because `_id` is document identity, not the authoritative commit resume position. The outbox cursor must be the same thing the relay uses to continue after reconnect or failover, and for Mongo that is the resume token.

## Resolved Questions

- The relay persists one checkpoint resume token and also materializes the token on each replayable outbox document.
- The relay retries a failed event-sequence materialization before it reads the next change, so it cannot checkpoint past an unmaterialized event.
- Outbox-enabled MongoDB startup requires a replica set or mongos; standalone deployments are rejected.
