---
status: implemented
---

# Enhancement 095: PostgreSQL Commit-Ordered Outbox Cursor

> **Status**: Implemented.
>
> **Current Implementation Note**: PostgreSQL relay cursors were later simplified by [096](096-postgres-relay-event-seq-cursor.md). This document remains the historical design record for the intermediate `commit_lsn` + `tx_seq` implementation.

## Summary

Bring the PostgreSQL outbox implementation into alignment with [090](../090-event-outbox.md) by replacing the current insert-order `seq` cursor with a commit-ordered cursor derived from logical decoding. Rename the current row identity column to `tx_seq`, keep it as `BIGSERIAL`, add `commit_lsn`, and use `(commit_lsn, tx_seq)` as the public replay cursor.

## Motivation

The current PostgreSQL implementation diverges from the intended design in [090](../090-event-outbox.md):

- [internal/plugin/store/postgres/outbox.go](../../internal/plugin/store/postgres/outbox.go) currently exposes `outbox_events.seq` as the public cursor.
- [internal/service/eventstream/outbox.go](../../internal/service/eventstream/outbox.go) currently publishes those cursor-bearing events directly after commit from the request path.
- Auto-increment sequence allocation is not commit-ordered, so concurrent PostgreSQL transactions can be observed out of order.

That breaks the replay guarantee that `after=<cursor>` should resume from the same committed event stream used by live tail delivery.

This enhancement narrows the PostgreSQL-specific corrective work:

1. stop treating the row identity as the public cursor
2. use a stable per-row tie-breaker alongside the commit LSN
3. move cursor assignment and live publish authority to a PostgreSQL logical-decoding relay
4. keep the existing row-order tie-breaker but stop exposing it as the cursor by itself
5. support multi-replica memory-service deployments with a single active relay selected by PostgreSQL advisory lock

## Design

### Goals

1. Make PostgreSQL replay cursors reflect commit order.
2. Keep live tail and replay in the same cursor space.
3. Rename `seq BIGSERIAL` to `tx_seq BIGSERIAL` so the row identity and the intra-commit tie-breaker are the same field.
4. Preserve deterministic ordering for multiple outbox rows emitted by one transaction.

### Non-Goals

- redesigning SQLite outbox cursors
- changing the external REST or gRPC `cursor` field shape beyond making it opaque and commit-ordered
- introducing exactly-once server-side consumer tracking

### Current State

Today PostgreSQL uses:

- `outbox_events.seq BIGSERIAL PRIMARY KEY`
- `Cursor = strconv.FormatInt(seq, 10)`
- replay ordered by `seq ASC`
- stale-cursor checks based on `WHERE seq = ?`
- live publish from the request path with the same `seq`-based cursor

This is easy to implement, but it is not logically correct under concurrent commits.

### Landed State

PostgreSQL should use two distinct cursor components:

| Field | Type | Role |
|---|---|---|
| `tx_seq` | `BIGSERIAL` | Stable row identity and deterministic tie-breaker within a commit |
| `commit_lsn` | `pg_lsn` | Commit-order position assigned by the logical-decoding relay after commit |

The public cursor becomes an opaque string derived from:

```text
<commit_lsn>:<tx_seq>
```

Example:

```text
0/16B3740:2
```

Replay order becomes:

```sql
ORDER BY commit_lsn ASC, tx_seq ASC
```

Replay resume predicate becomes:

```sql
WHERE (commit_lsn, tx_seq) > ($1, $2)
```

### Why Keep `BIGSERIAL` for `tx_seq`?

`tx_seq` is no longer the public cursor by itself, but it is still useful as:

- a stable per-row identity before `commit_lsn` is known
- a deterministic tie-breaker when multiple rows share the same `commit_lsn`
- a simple suffix for the external cursor `<commit_lsn>:<tx_seq>`

The problem with the current schema is not that `BIGSERIAL` exists. The problem is that it is exposed as the cursor by itself. Keeping the numeric row identity but moving it behind the commit-ordered `commit_lsn` fixes the correctness issue without introducing a second identity column.

### PostgreSQL Schema

Replace the current PostgreSQL outbox schema:

```sql
CREATE TABLE outbox_events (
    seq         BIGSERIAL PRIMARY KEY,
    event       TEXT NOT NULL,
    kind        TEXT NOT NULL,
    data        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);
```

with:

```sql
CREATE TABLE outbox_events (
    tx_seq      BIGSERIAL PRIMARY KEY,
    commit_lsn  pg_lsn,
    event       TEXT NOT NULL,
    kind        TEXT NOT NULL,
    data        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_outbox_events_commit_lsn_tx_seq
    ON outbox_events (commit_lsn, tx_seq);

CREATE INDEX idx_outbox_events_kind_commit_lsn_tx_seq
    ON outbox_events (kind, commit_lsn, tx_seq);

CREATE INDEX idx_outbox_events_created_at
    ON outbox_events (created_at);
```

Additional constraint:
No additional uniqueness constraint is required on `(commit_lsn, tx_seq)` because `tx_seq` is already globally unique as the primary key. The replay cursor remains unique once combined with `commit_lsn`.

### Write Path

`AppendOutboxEvents(...)` should:

1. insert rows and let PostgreSQL assign `tx_seq`
2. persist them with `commit_lsn = NULL`
3. return rows without claiming that the final public cursor is known

That means PostgreSQL should stop assigning the external cursor from the write path.

### Relay Path

When PostgreSQL outbox replay is enabled:

1. a logical replication slot/publication tails committed inserts for `outbox_events`
2. the relay decodes each committed transaction and obtains its commit LSN
3. for each inserted outbox row in that transaction, the relay:
   - reads the inserted `tx_seq`
   - writes back `commit_lsn`
   - publishes the corresponding EventBus event with `OutboxCursor = "<commit_lsn>:<tx_seq>"`

Live EventBus publication for PostgreSQL outbox mode must come from this relay, not from the request path.

### Logical-Decoding Correlation Requirement

This design assumes the logical-decoding stream exposes the inserted `outbox_events` row values, including `tx_seq`, `event`, `kind`, and `data`, so the relay can correlate committed WAL inserts back to stored rows without a second correlation key.

Implementation requirement:

- the chosen logical-decoding path must expose inserted `outbox_events` tuples with generated `tx_seq` values present in the insert row image

If the decoding path cannot reliably expose inserted `tx_seq`, this enhancement must be revised before implementation begins.

### Relay Idempotency and Ordering

The durable source of truth for replay visibility is the `outbox_events.commit_lsn` backfill in the primary table.

Required order of operations for each decoded outbox row:

1. decode committed insert and derive cursor `<commit_lsn>:<tx_seq>`
2. update the matching `outbox_events` row to set `commit_lsn` if it is still `NULL`
3. publish the EventBus event after the successful `commit_lsn` update

Required idempotency rules:

- the `commit_lsn` update must be idempotent, for example `UPDATE ... SET commit_lsn = $1 WHERE tx_seq = $2 AND commit_lsn IS NULL`
- if the relay restarts after updating `commit_lsn` but before publishing, replay remains correct because the durable row state is already materialized
- on restart, the relay may republish rows from the replication slot position, so consumers must continue to tolerate at-least-once delivery
- the relay must never overwrite an already-populated `commit_lsn` with a different value

### Pub/Sub Ownership

When PostgreSQL outbox replay is enabled, the relay is the sole publisher of durable business events to the shared EventBus / pub-sub transport.

Required rule:

- request handlers still write business mutations and outbox rows inside the transaction
- request handlers do not publish those durable business events directly after commit
- the relay publishes them only after decoding the committed WAL record and constructing the final cursor `<commit_lsn>:<tx_seq>`

This is required because the relay is the only component that knows the authoritative commit-ordered cursor. If the request path also publishes, the system would either:

- publish duplicates, once from the request path and once from the relay
- or publish inconsistent cursor-less / pre-cursor events before the relay emits the authoritative event

Non-durable control events such as local stream `phase`, `evicted`, or `invalidate` notifications remain outside the outbox and may still be emitted by the request/stream handling path as they are today.

For implementation purposes, "durable business events" are the outbox-backed entity events normalized by [090](../090-event-outbox.md): `conversation`, `entry`, `membership`, and `response`. Stream/session control events remain outside this rule unless a future enhancement explicitly moves them into the outbox.

### Relay Ownership in Multi-Replica Deployments

Multiple memory-service replicas may share the same PostgreSQL datastore and EventBus. In that deployment shape, only one replica must run the PostgreSQL outbox relay at a time.

This enhancement uses a PostgreSQL advisory lock for relay leadership:

1. each replica may start a relay candidate loop
2. the candidate attempts to acquire a well-known advisory lock
3. only the lock holder opens or advances the logical replication stream and publishes outbox events
4. if the lock holder exits or loses the lock, another replica may acquire it and resume from the replication slot position

This keeps relay ownership singleton without requiring an external coordinator.

Required rules:

- advisory-lock ownership must gate relay startup before any publish loop begins
- the relay must stop publishing if the lock is lost
- replicas that do not hold the lock remain normal API/EventBus nodes but do not consume the replication slot
- use one shared logical replication slot for the cluster, not one slot per replica

This avoids duplicate EventBus publication and avoids multiplying WAL retention by the number of replicas.

#### Slot Lifecycle

The advisory-lock leader is also responsible for replication-slot ownership.

Required rules:

- the leader validates that the shared logical replication slot exists before entering the publish loop
- if the slot does not exist, the leader creates it
- replicas that do not hold the advisory lock do not open a logical replication stream on that slot
- when leadership changes, the new leader resumes from the shared slot position rather than creating a fresh slot
- relay startup must fail clearly when PostgreSQL logical replication prerequisites are missing

### Replay Path

Replay should only read rows with `commit_lsn IS NOT NULL`.

Cursor parsing becomes:

```text
<commit_lsn>:<tx_seq>
```

Stale-cursor checks become a composite existence test:

```sql
SELECT 1
FROM outbox_events
WHERE commit_lsn = $1
  AND tx_seq = $2
LIMIT 1;
```

Replay listing becomes:

```sql
SELECT ...
FROM outbox_events
WHERE commit_lsn IS NOT NULL
  AND (commit_lsn, tx_seq) > ($1, $2)
ORDER BY commit_lsn ASC, tx_seq ASC
LIMIT $3;
```

Rows with `commit_lsn IS NULL` are not replay-visible. They represent outbox inserts that have not yet been materialized by the relay.

Operational rule:

- `commit_lsn IS NULL` rows should be short-lived during normal operation
- if they accumulate, that is a relay-health failure and should surface in logs, metrics, or admin diagnostics
- replay must not guess or synthesize ordering for those rows

### Eviction

Eviction should remain batched. The current code selects rows first and then deletes them so each pass respects `limit` and avoids an unbounded delete.

That behavior should stay, but PostgreSQL should stop depending on `seq` for the bounded delete. The implementation can continue to batch primarily by `created_at`, for example:

```sql
DELETE FROM outbox_events
WHERE ctid IN (
  SELECT ctid
  FROM outbox_events
  WHERE created_at < $1
  ORDER BY created_at, tx_seq
  LIMIT $2
);
```

Rows with `commit_lsn IS NULL` should normally be transient and short-lived. If the relay is healthy, eviction ordering should effectively operate on committed rows.

### EventStream Integration

The current shared helper in [internal/service/eventstream/outbox.go](../../internal/service/eventstream/outbox.go) assumes the write path returns the final cursor immediately. PostgreSQL needs a store-specific exception to that assumption.

Target behavior:

- SQLite may keep write-path cursor assignment because `seq` is commit-ordered there
- PostgreSQL must persist rows on the write path but assign the final cursor only in the relay
- Mongo remains governed by [091](../091-mongo-outbox-transactions.md)

This likely requires splitting:

- durable write persistence
- live publish source
- replay cursor materialization

so PostgreSQL no longer pretends those all happen in one write-path method call.

## Testing

### BDD Scenarios

```gherkin
Feature: PostgreSQL commit-ordered outbox replay
  Scenario: Concurrent PostgreSQL transactions replay in commit order
    Given the memory service is started with PostgreSQL outbox replay enabled
    And two concurrent writes append outbox events in different transactions
    When the later-inserted transaction commits first
    Then replay returns events in commit order

  Scenario: PostgreSQL tail cursor matches replay cursor space
    Given I am connected to /v1/events
    When a transaction emits two outbox events
    Then both events include cursors using the <lsn>:<tx_seq> format
    And reconnecting with the last cursor resumes after the second event

  Scenario: PostgreSQL stale cursor triggers invalidate after eviction
    Given outbox retention has removed events before a saved cursor
    When I reconnect with after that cursor
    Then I receive an invalidate stream event
```

### Unit and Integration Tests

- cursor encode/decode round-trips for `<lsn>:<tx_seq>`
- `AppendOutboxEvents(...)` inserts rows and receives generated `tx_seq` values
- replay ignores rows with `commit_lsn IS NULL`
- relay backfills `commit_lsn` onto rows identified by `tx_seq`
- replay orders events by `(commit_lsn, tx_seq)` even when inserts happened in a different order
- live EventBus publish for PostgreSQL comes from relay output, not request-path immediate publish
- only the replica holding the PostgreSQL advisory lock runs the relay publish loop
- request-path publication is disabled for PostgreSQL durable business events when outbox replay is enabled
- relay restart preserves correctness via idempotent `commit_lsn` backfill and at-least-once EventBus publish

## Tasks

- [x] Add PostgreSQL enhancement doc for commit-ordered cursor migration
- [x] Rename PostgreSQL `outbox_events.seq` to `tx_seq` and add `commit_lsn pg_lsn`
- [x] Update PostgreSQL outbox row model and schema migration
- [x] Make PostgreSQL `AppendOutboxEvents(...)` stop treating generated `tx_seq` as the final public cursor
- [x] Introduce PostgreSQL logical-decoding relay for outbox rows
- [x] Validate that the chosen logical-decoding path exposes inserted `tx_seq` values for `outbox_events`
- [x] Guard PostgreSQL relay ownership with a well-known advisory lock so only one replica publishes
- [x] Define replication-slot create/validate/resume behavior for the advisory-lock leader
- [x] Backfill `commit_lsn` from the relay and publish EventBus events from that relay
- [x] Disable request-path EventBus publish for PostgreSQL outbox-backed business events
- [x] Replace PostgreSQL replay cursor parsing from integer `seq` to `<lsn>:<tx_seq>`
- [x] Update PostgreSQL replay queries, stale-cursor checks, and eviction batching to stop using `seq`
- [ ] Add logs/metrics/admin diagnostics for stranded `commit_lsn IS NULL` rows
- [ ] Add PostgreSQL BDD coverage for concurrent commit ordering and replay resume
- [x] Update [090](../090-event-outbox.md) if implementation details diverge while landing this work
- [x] Remove the PostgreSQL row-cursor workaround entry from [WORKAROUNDS.md](../../WORKAROUNDS.md) after implementation

## Files to Modify

| File | Planned Change |
|---|---|
| `docs/enhancements/090-event-outbox.md` | Update PostgreSQL implementation notes if the landed design differs |
| `internal/plugin/store/postgres/db/schema.sql` | Rename `seq` to `tx_seq` and add `commit_lsn` plus commit-ordered indexes |
| `internal/plugin/store/postgres/outbox.go` | Replace `seq`-based cursor handling with commit-ordered replay logic using `tx_seq` |
| `internal/plugin/store/postgres/` | Add advisory-lock guarded PostgreSQL relay implementation and cursor helpers |
| `internal/service/eventstream/outbox.go` | Stop assuming PostgreSQL has the final cursor during the write path |
| `internal/service/eventstream/` | Split durable outbox persistence from PostgreSQL relay-owned EventBus publication |
| `internal/plugin/route/admin/stats.go` | Expose relay-health or stranded-row diagnostics if admin stats is the chosen surface |
| `internal/plugin/route/agent/events.go` | Consume the new PostgreSQL cursor format during replay |
| `internal/plugin/route/admin/events.go` | Same as agent replay path for admin SSE |
| `internal/grpc/server.go` | Consume the new PostgreSQL cursor format and relay-backed replay semantics |
| `internal/bdd/` | Add PostgreSQL concurrent replay coverage |
| `internal/FACTS.md` | Update the PostgreSQL outbox implementation gap note after implementation |
| `WORKAROUNDS.md` | Remove the PostgreSQL row-cursor workaround after implementation |

## Verification

```bash
# Compile Go
go build ./...

# PostgreSQL outbox coverage
go test ./internal/bdd -run 'TestFeaturesPgOutbox' -count=1 > test-pg-outbox.log 2>&1
# Search for failures using Grep tool on test-pg-outbox.log
```

## Design Decisions

### Why keep `tx_seq` if it is no longer the public cursor?

Because `tx_seq` is still the stable row identity and the deterministic suffix for per-event cursors inside the same commit. Multiple rows can share one `commit_lsn`, so replay still needs a second ordered component.

### Why not make the relay assign a separate transaction-local ordinal?

Because the relay already needs a stable identifier to correlate decoded rows with persisted rows before backfilling `commit_lsn`. Reusing the existing generated row identity keeps the design simpler than inventing a second relay-assigned ordinal.

## Open Questions

- Should the relay update `commit_lsn` row-by-row or in transaction-sized batches keyed by inserted `tx_seq` values?
- Do we want a separate relay checkpoint table in addition to per-row `commit_lsn`, or is row materialization enough for replay and operational recovery?
