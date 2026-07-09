---
status: proposed
---

# Enhancement 108: Client-Assigned Entry Sequence and Journal Channel

> **Status**: Proposed.

## Summary

Add an optional `seq` (`uint32`) field to entries with a conversation-scoped uniqueness
constraint. The value is entirely client-assigned; the server stores it, enforces
uniqueness with `409 Conflict`, and returns entries ordered by `seq ASC` when a `fromSeq`
list parameter is supplied. Add a `journal` channel alongside `history` and `context` for
storing opaque agent-execution audit steps.

## Motivation

The existing `history` and `context` channels cover two thirds of what an agent runtime needs
from a durable store:

| Need | Covered today? |
|---|---|
| Human-readable conversation transcript | Yes — `history` channel |
| Durable LLM context window | Yes — `context` channel |
| Append-only execution audit log for replay and rollback | **No** |

The missing piece is an **execution journal**: an ordered, opaque record of every
nondeterministic effect (LLM calls, tool calls, subprocess results) plus control markers
(iteration boundaries, rollback events, approval requests). This is what makes `--resume`
safe and what ROLLBACK/RESTART branch mechanics depend on.

The existing ordering guarantee — `created_at ASC` — has three problems for this use case:

1. **No strict monotonicity** — two entries written in the same millisecond are ambiguous.
2. **Not cursor-addressable** — `readSince(N)` cannot be expressed as a `created_at`
   predicate without a full scan.
3. **No caller-visible position identifier** — a consumer wanting "steps after position N"
   must filter client-side.

A client-assigned `seq` (`uint32`) with a uniqueness constraint scoped to the conversation
solves all three with minimal server complexity: the server stores and indexes what the
client provides, rejects collisions with `409`, and returns entries ordered by `seq ASC`
when `fromSeq` is used. No server-side sequence generation, locking, or gap enforcement is
required. The scope is global per conversation (not per channel) so that a single numeric
cursor can address entries across channels. Physical indexes may include datastore-specific
columns such as `conversation_group_id`, but the observable invariant remains
"one `seq` value per conversation".

## Design

### `seq` Field

- Type: `uint32` (OpenAPI `integer` with `minimum: 0`, `maximum: 4294967295` /
  proto `uint32`).
- Optional and nullable; entries without a `seq` participate only in `created_at` ordering.
- Accepted on `CreateEntryRequest`; persisted to the store.
- Returned on `Entry` responses; `null` when not set.
- The server enforces a **conversation-scoped uniqueness constraint** across all channels.
  Attempting to append an entry with a `seq` already present in the conversation returns
  **`409 Conflict`**.
- No gap enforcement. Callers may leave holes (e.g. reserve ranges) without error.
- Negative values and values above `4294967295` are **rejected with `400 Bad Request`**.

### `fromSeq` List Parameter

When the caller supplies `fromSeq` on REST or gRPC entry-list endpoints:

- Returns only entries where `seq >= fromSeq`, ordered by `seq ASC`.
- Entries without a `seq` are **excluded** from this result set.
- `afterCursor` (UUID-based pagination) may still be combined with `fromSeq` to page within
  the `seq`-ordered result.
- If neither `fromSeq` nor `afterCursor` are supplied the existing `created_at ASC` ordering
  and behaviour are unchanged.
- Existing filters (`channel`, `epoch`, `forks`, `upToEntryId`) still apply before
  pagination. `fromSeq` changes only the seq/null filter and ordering.
- The same behavior applies to user-facing and admin/auditor list APIs so debugging and
  replay tools do not need a separate cursor model.

### `journal` Channel

Add `journal` as a new channel value alongside `history` and `context`.

| Property | Value |
|---|---|
| Default client visibility | Agent-scoped (same as `context`) |
| Search indexing | Not supported (`indexedContent` rejected with `400`) |
| Epoch semantics | None — journal entries are not epoch-scoped |
| Sync endpoint | Not applicable |
| Content validation | Opaque — no schema enforced by the server |

The `journal` channel is intended for structured but opaque execution records. Content
interpretation is entirely the caller's responsibility.

Agent-scoped visibility means:

- appending `journal` requires an authenticated client id, matching the `context` channel
  requirement
- unauthenticated/non-agent user listing still defaults to `history`
- agent listing with a client id and no explicit channel includes all visible channels,
  including `journal`
- explicit `channel=journal` lists only journal entries and requires an authenticated
  client id
- event streams keep their existing default entry-channel filter of `history`; callers
  that need journal events must request `entry_channels=journal` or include it in the
  requested channel set

### OpenAPI Changes (`contracts/openapi/openapi.yml`)

1. **`Channel` enum** — add `"journal"` to the existing `["history", "context"]` list.

2. **`Entry` schema** — add the `seq` field:

```yaml
seq:
  type: integer
  minimum: 0
  nullable: true
  description: |-
    Optional client-assigned sequence number, unique within the conversation.
    Entries without a seq are ordered by createdAt only. Gaps are permitted.
  maximum: 4294967295
```

3. **`CreateEntryRequest` schema** — add the `seq` field:

```yaml
seq:
  type: integer
  minimum: 0
  nullable: true
  description: |-
    Optional client-assigned sequence number. Must be unique within the
    conversation. Must be >= 0. Returns 409 Conflict if a duplicate seq is
    submitted. Returns 400 Bad Request if the value is negative or exceeds
    4294967295.
  maximum: 4294967295
```

4. **`listConversationEntries` operation** — add `fromSeq` query parameter:

```yaml
- name: fromSeq
  in: query
  required: false
  schema:
    type: integer
    minimum: 0
    maximum: 4294967295
    nullable: true
  description: |-
    When set, return only entries with seq >= fromSeq, ordered by seq ASC.
    Entries without a seq value are excluded. When omitted, the default
    created_at ASC ordering applies.
    Existing channel, epoch, forks, and upToEntryId filters still apply.
```

5. **`appendConversationEntry` responses** — document `409 Conflict` for duplicate `seq`.

6. **Admin OpenAPI (`contracts/openapi/openapi-admin.yml`)** — add `"journal"` to the
   admin `Channel` enum, add `seq` to the admin `Entry` schema, and add the same `fromSeq`
   parameter to `adminGetEntries`.

### Protobuf Changes (`contracts/protobuf/memory/v1/memory_service.proto`)

1. **`Channel` enum** — add `JOURNAL = 3`.

2. **`Entry` message** — add `optional uint32 seq = 9`.

3. **`CreateEntryRequest` message** — add `optional uint32 seq = 12`
   (next available field number after `started_by_entry_id = 11`).

4. **`ListEntriesRequest` message** — add `optional uint32 from_seq = 7`
   (next available field number after `up_to_entry_id = 6`).

5. **`AdminListEntriesRequest` message** — add `optional uint32 from_seq = 7`
   (same numbering shape as `ListEntriesRequest`).

### Store Changes

- **PostgreSQL**: add column `seq BIGINT NULL` with `CHECK (seq >= 0 AND seq <= 4294967295)`
  to the partitioned `entries` table; add a partial unique index on
  `(conversation_group_id, conversation_id, seq) WHERE seq IS NOT NULL`; map duplicate-key
  violations to `409`. PostgreSQL requires unique indexes on partitioned tables to include
  the partition key, so the physical index includes `conversation_group_id` while preserving
  the logical `(conversation_id, seq)` invariant.
- **SQLite**: add column `seq INTEGER NULL` with `CHECK (seq >= 0 AND seq <= 4294967295)`
  and a partial unique index on `(conversation_id, seq) WHERE seq IS NOT NULL`.
- **MongoDB**: add `seq` field to the entry document only when present; create a unique
  partial index on `{conversation_id: 1, seq: 1}` with
  `partialFilterExpression: {seq: {$exists: true}}`. Do not use a compound sparse index:
  because `conversation_id` is always present, MongoDB would still index documents missing
  `seq`.
- **`journal` channel**: ensure the channel enum value is persisted and round-tripped by
  all stores. Append and list paths must apply the same client-id scoping used by
  `context`, but must not apply context epoch or latest-context cache semantics.
- **Latest-context cache**: `fromSeq` queries must not accidentally return cached
  latest-context entries in `created_at` order. Either bypass the cache when `fromSeq` is
  present or filter/sort cached results before pagination.

## Non-Goals

- Server-side sequence generation (auto-increment). Callers own their `seq` values.
- Gap detection or enforcement. Holes are allowed.
- Per-channel `seq` namespacing. The unique constraint is conversation-scoped so a single
  cursor addresses entries across channels.
- Ordering `created_at`-ordered results by `seq`. The `fromSeq` filter activates `seq ASC`
  ordering explicitly; it does not change the default ordering mode.
- Search indexing for `journal` channel entries. The channel is for structured execution
  data, not free text.

## Design Decisions

| Decision | Rationale |
|---|---|
| `uint32` not `int64` | 4 billion steps per conversation is more than sufficient for any realistic agent run; `uint32` avoids accidental large-integer edge cases in JSON serialisation (JS `Number` safe-integer limit) and makes the non-negative constraint self-documenting in the type. |
| Client-assigned, not server-assigned | Eliminates server-side locking and sequence generation; callers that know their position at write time pay zero overhead. |
| Unique index, not monotonicity constraint | Allows batched pre-allocation (reserve a range, fill later) without blocking callers or requiring ordering enforcement. |
| Conversation-scoped, not channel-scoped | A single numeric cursor can address entries across `history`, `context`, and `journal` channels — essential for cross-channel replay. |
| `fromSeq` excludes null-seq entries | The `seq`-ordered view is a clean cursor-addressed stream; mixing un-sequenced entries into it with an arbitrary position would be confusing and would break consumer assumptions about monotonic reads. |
| `journal` channel is not epoch-scoped | Journal entries are written once and never superseded; epoch semantics (used by `context` to represent context window versions) do not apply. |
| PostgreSQL index includes `conversation_group_id` | The entries table is hash-partitioned by `conversation_group_id`; PostgreSQL unique indexes on partitioned tables must include the partition key. The observable API still enforces uniqueness per conversation. |
| MongoDB uses a partial unique index, not sparse | Sparse compound indexes with ascending/descending keys index a document when it contains at least one indexed key. Since every entry has `conversation_id`, a sparse `{conversation_id, seq}` index would still constrain missing `seq` values. |

## Testing

### Cucumber Scenarios

```gherkin
Feature: Client-assigned entry sequence

  Background:
    Given a conversation "conv-1" exists

  Scenario: Accept an entry with a seq value
    When I append an entry to "conv-1" with seq 1 and contentType "journal"
    Then the response status is 201
    And the returned entry has seq 1

  Scenario: Reject a duplicate seq in the same conversation
    Given an entry with seq 1 exists in "conv-1"
    When I append another entry to "conv-1" with seq 1
    Then the response status is 409

  Scenario: Allow the same seq in a different conversation
    Given an entry with seq 1 exists in "conv-1"
    When I append an entry to "conv-2" with seq 1
    Then the response status is 201

  Scenario: fromSeq returns seq-ordered entries and excludes null-seq entries
    Given the following entries exist in "conv-1":
      | seq  | contentType |
      | null | history     |
      | 10   | journal     |
      | 20   | journal     |
      | 30   | journal     |
    When I list entries for "conv-1" with fromSeq=10
    Then I receive exactly 3 entries with seq values [10, 20, 30] in that order
    And no entry without a seq is returned

  Scenario: fromSeq=25 returns only entries with seq >= 25
    Given entries with seq [10, 20, 30, 40] exist in "conv-1"
    When I list entries for "conv-1" with fromSeq=25
    Then I receive exactly 2 entries with seq values [30, 40]

  Scenario: Omitting fromSeq preserves existing created_at ordering
    Given entries with and without seq exist in "conv-1"
    When I list entries for "conv-1" without fromSeq
    Then entries are returned in createdAt ASC order including null-seq entries

Feature: Journal channel

  Scenario: Append a journal entry
    When I append an entry to "conv-1" with channel "journal" and arbitrary content
    Then the response status is 201
    And the returned entry channel is "journal"

  Scenario: Reject indexedContent on journal entries
    When I append a journal entry to "conv-1" with indexedContent set
    Then the response status is 400

  Scenario: List journal entries via channel filter
    Given history and journal entries exist in "conv-1"
    When I list entries for "conv-1" with channel=journal
    Then only journal entries are returned

  Scenario: User listing without a client id does not expose journal entries by default
    Given history and journal entries exist in "conv-1"
    When I list entries for "conv-1" without a channel filter as an end user
    Then only history entries are returned

  Scenario: Agent listing without a channel filter includes journal entries
    Given history, context, and journal entries exist in "conv-1"
    When I list entries for "conv-1" without a channel filter as agent client "agent-a"
    Then history, context, and journal entries visible to "agent-a" are returned

  Scenario: Journal entry events require explicit event channel subscription
    Given I subscribe to entry events without entry_channels
    When a journal entry is appended to "conv-1"
    Then no journal entry event is delivered
    When I subscribe to entry events with entry_channels "journal"
    And another journal entry is appended to "conv-1"
    Then a journal entry event is delivered
```

## Tasks

- [ ] Add `journal` to the `Channel` enum in `contracts/openapi/openapi.yml`
- [ ] Add `journal` to the `Channel` enum in `contracts/openapi/openapi-admin.yml`
- [ ] Add `seq` (nullable integer, `minimum: 0`, `maximum: 4294967295`) to `Entry` and `CreateEntryRequest` schemas in `contracts/openapi/openapi.yml`
- [ ] Add `seq` (nullable integer, `minimum: 0`, `maximum: 4294967295`) to the admin `Entry` schema in `contracts/openapi/openapi-admin.yml`
- [ ] Add `fromSeq` query parameter to `listConversationEntries` in `contracts/openapi/openapi.yml`
- [ ] Add `fromSeq` query parameter to `adminGetEntries` in `contracts/openapi/openapi-admin.yml`
- [ ] Document `409 Conflict` for duplicate `seq` on `appendConversationEntry`
- [ ] Add `JOURNAL = 3` to `Channel` enum in `contracts/protobuf/memory/v1/memory_service.proto`
- [ ] Add `optional uint32 seq = 9` to `Entry` message in the proto
- [ ] Add `optional uint32 seq = 12` to `CreateEntryRequest` message in the proto
- [ ] Add `optional uint32 from_seq = 7` to `ListEntriesRequest` in the proto
- [ ] Add `optional uint32 from_seq = 7` to `AdminListEntriesRequest` in the proto
- [ ] Regenerate Go REST clients/wrappers from updated OpenAPI specs
- [ ] Regenerate Go and Java gRPC contracts from updated proto
- [ ] PostgreSQL store: add `seq BIGINT NULL` column + range check + partial unique index on `(conversation_group_id, conversation_id, seq)`
- [ ] SQLite store: add `seq INTEGER NULL` column + range check + partial unique index (`WHERE seq IS NOT NULL`)
- [ ] MongoDB store: add `seq` field and unique partial index on `{conversation_id, seq}` where `seq` exists
- [ ] Store interface: add `fromSeq *uint32` to user/admin entry list query paths or replace the long parameter list with query structs
- [ ] Latest-context cache paths: bypass or sort/filter cache results when `fromSeq` is present
- [ ] Store append/list paths: apply client-id scoping to `journal` without applying context epoch semantics
- [ ] Go append handler: enforce unique `seq` constraint, return `409` on violation
- [ ] Go list handler: implement `fromSeq` filter with `seq ASC` ordering
- [ ] Go model / JSON marshaling: add `Seq *uint32` to `Entry`; update marshal/unmarshal
- [ ] Go REST router: validate allowed channels (`history`, `context`, `journal`) on append and list
- [ ] Go gRPC server: map `JOURNAL` in `mapChannel`, append, user list, and admin list paths
- [ ] Event-stream filters: allow `journal` in `entry_channels` and keep the default as `history`
- [ ] Reject `indexedContent` on all non-history channel entries, including `journal` (return `400`)
- [ ] BDD feature file: seq uniqueness and `fromSeq` filtering scenarios
- [ ] BDD feature file: journal channel scenarios
- [ ] BDD feature file: journal event-stream filter scenarios
- [ ] Run BDD suite against PostgreSQL, SQLite, and MongoDB stores

## Files to Modify

| File | Change |
|---|---|
| `contracts/openapi/openapi.yml` | Add `journal` to `Channel` enum; add `seq` field to `Entry` and `CreateEntryRequest`; add `fromSeq` parameter; document `409` |
| `contracts/openapi/openapi-admin.yml` | Add `journal` to `Channel`; add `seq` to `Entry`; add `fromSeq` to `adminGetEntries` |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add `JOURNAL = 3`; add `seq` fields to `Entry` and `CreateEntryRequest`; add `from_seq` to user/admin list requests |
| `internal/model/model.go` | Add `Seq *uint32`; JSON marshal/unmarshal remains symmetric through the alias path |
| `internal/plugin/store/postgres/db/schema.sql` | Add `seq` column, range check, and partition-compatible partial unique index |
| `internal/plugin/store/postgres/postgres.go` | Persist `seq`, map duplicate `seq` conflicts, and implement `fromSeq` list ordering |
| `internal/plugin/store/sqlite/db/schema.sql` | Add `seq` column, range check, and partial unique index |
| `internal/plugin/store/sqlite/sqlite.go` | Persist `seq`, map duplicate `seq` conflicts, and implement `fromSeq` list ordering |
| `internal/plugin/store/mongo/mongo.go` | Add `seq` field, unique partial index, duplicate check, and `fromSeq` list path |
| `internal/registry/store/plugin.go` | Add `seq` to `CreateEntryRequest` and entry-list query shape |
| `internal/plugin/store/metrics/metrics.go` | Forward the updated entry-list query shape |
| `internal/plugin/route/entries/entries.go` | Wire `seq` from request body; validate `journal`; return `409` on store duplicate error; pass `fromSeq` to store |
| `internal/plugin/route/admin/admin.go` | Pass `fromSeq` through admin entry listing |
| `internal/grpc/server.go` | Wire `seq`, `from_seq`, and `JOURNAL` for user/admin entry APIs |
| `internal/generated/apiclient/` | Regenerated — do not edit manually |
| `internal/bdd/testdata/features/entries-seq-rest.feature` | New BDD scenarios for REST `seq` behavior |
| `internal/bdd/testdata/features/journal-channel-rest.feature` | New BDD scenarios for REST `journal` channel behavior |
| `internal/bdd/testdata/features-grpc/entries-seq-grpc.feature` | New BDD scenarios for gRPC `seq` behavior |

## Verification

```bash
# Build
go build ./...

# BDD suite (Postgres)
go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1 > test.log 2>&1
# Search test.log for failures

# BDD suite (SQLite)
go test ./internal/bdd -run TestFeaturesSQLite -count=1 >> test.log 2>&1

# BDD suite (MongoDB)
go test ./internal/bdd -run TestFeaturesMongo -count=1 >> test.log 2>&1
```
