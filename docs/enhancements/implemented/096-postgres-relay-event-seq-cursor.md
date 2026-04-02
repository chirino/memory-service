---
status: implemented
---

# Enhancement 096: PostgreSQL Relay-Assigned Event Sequence Cursor

> **Status**: Implemented.

## Summary

Simplify PostgreSQL outbox cursors by replacing the current relay-materialized `<commit_lsn>:<tx_seq>` cursor with a relay-assigned monotonic `event_seq` stored on each outbox row.

The PostgreSQL relay remains the sole cursor assigner and the sole publisher of durable business events. It assigns `event_seq` only after `CommitMessage`, stores it before publish, and reuses any previously stored value during replay after failover.

## Motivation

Enhancement [095](095-postgres-commit-ordered-outbox-cursor.md) fixed the correctness problem in PostgreSQL by moving cursor assignment to the logical-decoding relay and using `(commit_lsn, tx_seq)` as the replay order. That design is correct, but it leaves PostgreSQL-specific complexity in places that do not benefit from exposing WAL details:

- PostgreSQL replay cursors are harder to parse and reason about than SQLite's integer cursor.
- Replay queries and stale-cursor checks are more complex because they compare two ordered fields instead of one.
- Tests and documentation must describe a PostgreSQL-specific `<commit_lsn>:<tx_seq>` cursor shape even though the API contract says cursors are opaque.
- The relay is already the authoritative ordering point, so it can assign a simpler derived cursor without losing commit ordering.

This enhancement keeps the hard part from 095, the relay-owned commit ordering, and simplifies the public cursor and replay path around it.

## Design

### Goals

1. Preserve commit-ordered replay and tail semantics for PostgreSQL.
2. Keep live tail and replay in the same cursor space.
3. Replace the PostgreSQL public cursor with an opaque decimal `event_seq`.
4. Keep the relay efficient by batching cursor materialization per committed transaction.

### Non-Goals

- removing the PostgreSQL logical-decoding relay
- changing SQLite or MongoDB outbox cursor behavior
- introducing exactly-once delivery
- batching across multiple committed transactions before publish

### Schema

Replace the relay-materialized ordering column `commit_lsn` with a relay-materialized `event_seq`:

```sql
CREATE TABLE outbox_events (
    tx_seq      BIGSERIAL PRIMARY KEY,
    event_seq   BIGINT,
    event       TEXT NOT NULL,
    kind        TEXT NOT NULL,
    data        JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX idx_outbox_events_event_seq
    ON outbox_events (event_seq)
    WHERE event_seq IS NOT NULL;

CREATE INDEX idx_outbox_events_kind_event_seq
    ON outbox_events (kind, event_seq)
    WHERE event_seq IS NOT NULL;

CREATE INDEX idx_outbox_events_created_at
    ON outbox_events (created_at);
```

`tx_seq` remains the stable row identity and the correlation key decoded from WAL inserts. `event_seq` becomes the only replay-visible ordering field.

### Cursor Format

The PostgreSQL public cursor becomes the decimal string form of `event_seq`.

Example:

```text
1042
```

The cursor remains opaque to clients. Consumers must continue to store and replay it as an uninterpreted string.

### Replay Path

Replay should only read rows with `event_seq IS NOT NULL`.

Cursor parsing becomes:

```text
<event_seq>
```

Stale-cursor checks become:

```sql
SELECT 1
FROM outbox_events
WHERE event_seq = $1
LIMIT 1;
```

Replay listing becomes:

```sql
SELECT ...
FROM outbox_events
WHERE event_seq IS NOT NULL
  AND event_seq > $1
ORDER BY event_seq ASC
LIMIT $2;
```

Rows with `event_seq IS NULL` are not replay-visible. Under normal operation they should be short-lived and indicate relay lag only until the relay materializes them.

### Relay Assignment Model

The relay continues to derive authoritative ordering from WAL commit order, but it no longer exposes WAL positions.

At startup after the relay becomes leader:

1. ensure publication and replication slot as in [095](095-postgres-commit-ordered-outbox-cursor.md)
2. initialize a local allocator from `SELECT COALESCE(MAX(event_seq), 0) FROM outbox_events`
3. start streaming WAL from the shared replication slot

During logical decoding:

- `BeginMessage` starts a new per-transaction batch
- `InsertMessage` appends decoded outbox rows to `pending`
- `CommitMessage` ends the batch and triggers batched cursor materialization for that transaction only

The relay must not wait for additional WAL commits to enlarge the batch. The batch boundary is exactly one committed transaction.

### Store Before Publish

The relay must continue to store the durable replay cursor before publishing the event to the EventBus.

Required order:

1. derive candidate `event_seq` values for the committed `pending` rows
2. write `event_seq` values to the matching `outbox_events` rows
3. load the persisted `event_seq` values actually present on those rows
4. publish events using the persisted `event_seq`

This preserves the invariant that any cursor observed by live SSE or gRPC consumers is already replayable from the outbox.

### Idempotent Failover Behavior

On failover, the new leader may replay WAL for rows that an earlier leader already materialized and possibly already published.

Required behavior:

- the relay proposes new candidate `event_seq` values from its local allocator
- rows whose `event_seq` is still `NULL` receive the candidate value
- rows whose `event_seq` is already populated keep the stored value
- publication always uses the stored row value, not the candidate value

This allows at-least-once delivery with stable cursors across relay restarts.

Dense numbering is not required. Gaps are acceptable because cursors are opaque.

### Batched Materialization

To keep the relay efficient, `event_seq` assignment is done in one CTE statement per committed transaction. The statement batch-updates rows whose `event_seq` is still `NULL`, returns the newly assigned values, and falls back to already-persisted row values for replayed rows.

Target shape:

1. reserve a candidate contiguous range in memory for `len(pending)` rows
2. execute one batched CTE keyed by `tx_seq`
3. read the effective persisted `event_seq` values for that commit in the original `pending` order
4. publish in the original `pending` order

That keeps the relay on a fixed per-commit round-trip budget while preserving strict event order within the committed transaction.

### EventStream Integration

No API contract changes are required beyond the PostgreSQL cursor contents.

- REST SSE `cursor` remains an opaque string
- gRPC `cursor` remains an opaque string
- request-path durable publish suppression remains unchanged for PostgreSQL outbox mode
- replay handlers continue to treat a missing persisted cursor as stale, not pending

Because this design keeps store-before-publish, SSE and gRPC replay handlers do not need a new "cursor pending materialization" state.

## Testing

### BDD Scenarios

```gherkin
Feature: PostgreSQL relay-assigned outbox cursor
  Scenario: Concurrent PostgreSQL transactions replay in relay commit order
    Given the memory service is started with PostgreSQL outbox replay enabled
    And two concurrent writes append outbox events in different transactions
    When the later-inserted transaction commits first
    Then replay returns events in commit order
    And each event has a numeric opaque cursor

  Scenario: PostgreSQL live SSE tail uses replayable numeric cursors
    Given I am connected to /v1/events
    When a transaction emits two outbox events
    Then both events include numeric opaque cursors
    And reconnecting with the last cursor resumes after the second event

  Scenario: Relay restart reuses previously stored event_seq values
    Given the relay stores event_seq values for committed outbox rows
    When the relay restarts before the replication slot fully advances
    Then replayed events reuse the same cursors
    And consumers may observe duplicate delivery with identical cursors
```

### Unit and Integration Tests

- cursor encode/decode round-trips for decimal `event_seq`
- replay ignores rows with `event_seq IS NULL`
- replay orders rows by `event_seq ASC`
- stale cursor detection checks `event_seq`
- concurrent commit integration test still returns replay in relay commit order
- a single transaction with multiple outbox rows receives strictly increasing `event_seq` values
- relay restart after storing but before slot advancement republishes with the stored `event_seq`
- batched materialization preserves publish order matching `pending`
- PostgreSQL SSE/gRPC BDD assertions accept numeric opaque cursors instead of `<commit_lsn>:<tx_seq>`

## Tasks

- [x] Replace PostgreSQL `commit_lsn` schema/index usage with nullable `event_seq`
- [x] Update PostgreSQL cursor parsing/formatting helpers to use decimal `event_seq`
- [x] Change PostgreSQL replay queries and stale-cursor checks to use `event_seq`
- [x] Update the relay startup allocator to initialize from `MAX(event_seq)`
- [x] Replace per-row relay materialization with per-commit batched `event_seq` materialization
- [x] Keep store-before-publish and idempotent replay behavior in the relay
- [x] Update unit tests and BDD coverage to assert numeric opaque PostgreSQL cursors
- [x] Update docs that currently describe PostgreSQL cursor format as `<commit_lsn>:<tx_seq>`

## Files to Modify

| File | Planned Change |
|---|---|
| `docs/enhancements/implemented/096-postgres-relay-event-seq-cursor.md` | Record the landed `event_seq` design and verification |
| `docs/enhancements/090-event-outbox.md` | Update the PostgreSQL cursor note to point at 096 |
| `docs/enhancements/implemented/095-postgres-commit-ordered-outbox-cursor.md` | Mark 095 as the historical intermediate design |
| `internal/plugin/store/postgres/db/schema.sql` | Replace `commit_lsn` columns/indexes with `event_seq` columns/indexes |
| `internal/plugin/store/postgres/outbox.go` | Replace `<commit_lsn>:<tx_seq>` replay logic with `event_seq` replay logic |
| `internal/plugin/store/postgres/outbox_relay.go` | Initialize allocator, batch-assign `event_seq`, and publish with stored values |
| `internal/plugin/store/postgres/outbox_test.go` | Update cursor tests and replay tests for `event_seq` |
| `internal/bdd/testdata/features-pg-outbox/` | Update cursor-format and replay assertions for numeric opaque cursors |
| `internal/bdd/steps_sse_events.go` | Replace PostgreSQL cursor-shape assertions if they currently require `<lsn>:<tx_seq>` |
| `internal/bdd/steps_sse_events_grpc.go` | Same as SSE for gRPC cursor assertions |
| `internal/FACTS.md` | Update the PostgreSQL outbox relay note after implementation |

## Verification

```bash
# Compile Go
go build ./...

# Targeted PostgreSQL store tests
go test ./internal/plugin/store/postgres -count=1

# PostgreSQL outbox BDD coverage
go test ./internal/bdd -run 'TestFeaturesPgOutbox' -count=1 > test-pg-outbox.log 2>&1
# Search for failures using Grep tool on test-pg-outbox.log
```

## Design Decisions

### Why keep `tx_seq`?

`tx_seq` is still needed as the persisted row identity decoded from WAL insert tuples. The relay needs a durable correlation key before the final cursor is assigned.

### Why keep store-before-publish?

Store-before-publish guarantees that any cursor observed by live consumers is already replayable from the outbox. That keeps stale-cursor behavior simple and avoids introducing a new "pending cursor materialization" replay state.

### Why batch only within one committed transaction?

The relay already has a natural batch boundary at `CommitMessage`. Batching across multiple commits would introduce avoidable delivery latency and more complex failure handling without improving correctness.

### Why use a CTE for batched materialization?

The landed implementation uses a single CTE statement to batch candidate `event_seq` assignments and read back the persisted values in the original commit order. That keeps the relay on a fixed per-commit round-trip budget while still reusing previously stored values during failover replay.
