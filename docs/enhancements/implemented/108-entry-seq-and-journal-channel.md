---
status: implemented
---

# Enhancement 108: Client-Assigned Entry Sequence and Journal Channel

> **Status**: Implemented.

## Summary

Add an optional `seq` (`uint32`) field to entries with a conversation-scoped uniqueness
constraint. The value is entirely client-assigned; the server stores it, enforces
uniqueness with `409 Conflict`, returns entries ordered by `seq ASC` when a `fromSeq`
list parameter is supplied, and uses `seq ASC NULLS FIRST` only to break ties between
entries with the same `created_at` in default timestamp ordering. Add a `journal` channel
alongside `history` and `context` for storing opaque agent-execution audit steps. This
document also tracks a follow-up plan to allow journal entries to serve as fork anchor
points for replay/debug branching.

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
- Optional and nullable; entries without a `seq` sort before sequenced entries only when
  default ordering compares entries with the same `created_at`.
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
- If neither `fromSeq` nor `afterCursor` are supplied, default ordering is
  `created_at ASC, seq ASC NULLS FIRST, id ASC`.
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

### Follow-up: Journal-Anchored Fork Points

Fork metadata already stores a generic `forkedAtEntryId`, and fork ancestry filtering is
entry-order based rather than channel-specific. To support replay and rollback workflows,
`journal` entries should also be valid fork anchors.

The intended behavior is:

- Fork anchors are allowed on `history` and `journal` entries.
- Fork anchors remain disallowed on `context` entries. Context is epoch-scoped state, not
  an append-only event boundary, so using it as a fork point would make replay semantics
  ambiguous.
- When `forkedAtEntryId` references a `journal` entry, the caller must have an
  authenticated client id and must be allowed to read that journal entry. Since journal
  entries are client-scoped, a different client must not be able to create a fork at
  another client's journal entry.
- The existing exclusive-stop rule remains unchanged: `forkedAtEntryId` is the first
  parent entry excluded from the fork, regardless of whether that entry is `history` or
  `journal`.
- User-facing chat examples should continue to demonstrate history-message forks. Journal
  anchors are an advanced runtime/debug feature and should be documented in concept and
  API docs rather than replacing the basic tutorial path.

Backend validation must be normalized across stores. MongoDB currently rejects
non-`history` fork points explicitly, while PostgreSQL and SQLite validate only that the
entry exists in the fork tree. The stores should converge on a shared rule:

```go
if entry.Channel != model.ChannelHistory && entry.Channel != model.ChannelJournal {
    return validationError("can only fork at history or journal entries")
}
if entry.Channel == model.ChannelJournal {
    requireAuthenticatedClient()
    requireJournalEntryVisibleToClient(entry, clientID)
}
```

REST and gRPC do not need new wire fields because `forkedAtConversationId` and
`forkedAtEntryId` already exist on entry append requests. They do need consistent error
mapping for invalid or unauthorized journal fork anchors.

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
    Default listing uses seq only as a createdAt tie-breaker, with entries
    without seq sorted before sequenced entries at the same timestamp. Gaps
    are permitted.
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
    ordering is created_at ASC, seq ASC NULLS FIRST, then id ASC.
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
  the logical `(conversation_id, seq)` invariant. Add a read index for default ordering
  `(conversation_group_id, created_at ASC, seq ASC NULLS FIRST, id ASC)`; drop the legacy
  `(conversation_group_id, created_at)` index because the default-order index covers it.
- **SQLite**: add column `seq INTEGER NULL` with `CHECK (seq >= 0 AND seq <= 4294967295)`
  and a partial unique index on `(conversation_id, seq) WHERE seq IS NOT NULL`. Add a
  default-order `(conversation_group_id, created_at, seq, id)` read index; drop the legacy
  `(conversation_group_id, created_at)` index.
- **MongoDB**: add `seq` field to the entry document only when present; create a unique
  partial index on `{conversation_id: 1, seq: 1}` with
  `partialFilterExpression: {seq: {$exists: true}}`. Add a read index for default ordering
  `{conversation_group_id: 1, created_at: 1, seq: 1, _id: 1}`. Do not use a compound sparse
  index: because `conversation_id` is always present, MongoDB would still index documents
  missing `seq`.
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
- Letting `seq` override timestamp order. Default listing uses `seq` only as a
  same-timestamp tie-breaker; the `fromSeq` filter remains the explicit `seq ASC`
  replay view.
- Search indexing for `journal` channel entries. The channel is for structured execution
  data, not free text.
- Fork anchors on `context` entries. This follow-up intentionally permits only `history`
  and `journal` anchors.

## Design Decisions

| Decision | Rationale |
|---|---|
| `uint32` not `int64` | 4 billion steps per conversation is more than sufficient for any realistic agent run; `uint32` avoids accidental large-integer edge cases in JSON serialisation (JS `Number` safe-integer limit) and makes the non-negative constraint self-documenting in the type. |
| Client-assigned, not server-assigned | Eliminates server-side locking and sequence generation; callers that know their position at write time pay zero overhead. |
| Unique index, not monotonicity constraint | Allows batched pre-allocation (reserve a range, fill later) without blocking callers or requiring ordering enforcement. |
| Conversation-scoped, not channel-scoped | A single numeric cursor can address entries across `history`, `context`, and `journal` channels — essential for cross-channel replay. |
| `fromSeq` excludes null-seq entries | The `seq`-ordered view is a clean cursor-addressed stream; mixing un-sequenced entries into it with an arbitrary position would be confusing and would break consumer assumptions about monotonic reads. |
| `journal` channel is not epoch-scoped | Journal entries are written once and never superseded; epoch semantics (used by `context` to represent context window versions) do not apply. |
| Journal entries can be fork anchors | Rollback/replay needs branch points at opaque execution steps, not only user-visible chat messages. The same exclusive-stop ancestry model works for any ordered entry, provided journal client scoping is enforced. |
| Context entries cannot be fork anchors | Context entries represent derived state snapshots/epochs. Forking from them would blur event replay and state replacement semantics. |
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
    And entries with the same createdAt are ordered by seq ASC NULLS FIRST, then id ASC

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

Feature: Journal-anchored conversation forks

  Scenario: Same client can fork at a journal entry
    Given a conversation "conv-1" has a journal entry "J1" written by client "agent-a"
    When client "agent-a" appends the first history entry to new conversation "fork-1" with forkedAtConversationId "conv-1" and forkedAtEntryId "J1"
    Then the response status is 201
    And conversation "fork-1" has forkedAtConversationId "conv-1"
    And conversation "fork-1" has forkedAtEntryId "J1"

  Scenario: User without client identity cannot fork at a journal entry
    Given a conversation "conv-1" has a journal entry "J1" written by client "agent-a"
    When user "alice" appends the first history entry to new conversation "fork-1" with forkedAtConversationId "conv-1" and forkedAtEntryId "J1"
    Then the response status is 403

  Scenario: Different client cannot fork at another client's journal entry
    Given a conversation "conv-1" has a journal entry "J1" written by client "agent-a"
    When client "agent-b" appends the first history entry to new conversation "fork-1" with forkedAtConversationId "conv-1" and forkedAtEntryId "J1"
    Then the response status is 403

  Scenario: Context entries cannot be fork anchors
    Given a conversation "conv-1" has a context entry "C1"
    When client "agent-a" appends the first history entry to new conversation "fork-1" with forkedAtConversationId "conv-1" and forkedAtEntryId "C1"
    Then the response status is 400

  Scenario: History listing honors a fork anchored at a journal entry
    Given a conversation "conv-1" has entries in order:
      | id | channel |
      | H1 | history |
      | J1 | journal |
      | H2 | history |
    When client "agent-a" creates fork "fork-1" at journal entry "J1"
    And user "alice" lists entries for "fork-1" with channel=history
    Then only history entry "H1" is returned from the parent path
```

## Tasks

- [x] Add `journal` to the `Channel` enum in `contracts/openapi/openapi.yml`
- [x] Add `journal` to the `Channel` enum in `contracts/openapi/openapi-admin.yml`
- [x] Add `seq` (nullable integer, `minimum: 0`, `maximum: 4294967295`) to `Entry` and `CreateEntryRequest` schemas in `contracts/openapi/openapi.yml`
- [x] Add `seq` (nullable integer, `minimum: 0`, `maximum: 4294967295`) to the admin `Entry` schema in `contracts/openapi/openapi-admin.yml`
- [x] Add `fromSeq` query parameter to `listConversationEntries` in `contracts/openapi/openapi.yml`
- [x] Add `fromSeq` query parameter to `adminGetEntries` in `contracts/openapi/openapi-admin.yml`
- [x] Document `409 Conflict` for duplicate `seq` on `appendConversationEntry`
- [x] Add `JOURNAL = 3` to `Channel` enum in `contracts/protobuf/memory/v1/memory_service.proto`
- [x] Add `optional uint32 seq = 9` to `Entry` message in the proto
- [x] Add `optional uint32 seq = 12` to `CreateEntryRequest` message in the proto
- [x] Add `optional uint32 from_seq = 7` to `ListEntriesRequest` in the proto
- [x] Add `optional uint32 from_seq = 7` to `AdminListEntriesRequest` in the proto
- [x] Regenerate Go REST clients/wrappers from updated OpenAPI specs
- [x] Regenerate Go gRPC contracts from updated proto (Java proto regeneration is a separate task)
- [x] PostgreSQL store: add `seq BIGINT NULL` column + range check + partial unique index on `(conversation_group_id, conversation_id, seq)`
- [x] SQLite store: add `seq INTEGER NULL` column + range check + partial unique index (`WHERE seq IS NOT NULL`)
- [x] MongoDB store: add `seq` field and unique partial index on `{conversation_id, seq}` where `seq` exists
- [x] Add default ordering read indexes and replace legacy `(conversation_group_id, created_at)` indexes
- [x] Store interface: add `fromSeq *uint32` to user/admin entry list query paths
- [x] Latest-context cache paths: bypass when `fromSeq` is present (cache is skipped and direct store query used)
- [x] Store append/list paths: apply client-id scoping to `journal` without applying context epoch semantics
- [x] Go append handler: enforce unique `seq` constraint, return `409` on violation
- [x] Go list handler: implement `fromSeq` filter with `seq ASC` ordering
- [x] Go model / JSON marshaling: add `Seq *uint32` to `Entry`; marshal/unmarshal symmetric via alias path
- [x] Go REST router: validate allowed channels (`history`, `context`, `journal`) on append and list
- [x] Go gRPC server: map `JOURNAL` in `mapChannel`, append, user list, and admin list paths
- [x] Event-stream filters: allow `journal` in `entry_channels` and keep the default as `history`
- [x] Reject `indexedContent` on all non-history channel entries, including `journal` (return `400`)
- [x] BDD feature file: seq uniqueness and `fromSeq` filtering scenarios
- [x] BDD feature file: journal channel scenarios
- [ ] BDD feature file: journal event-stream filter scenarios (deferred — event-stream BDD requires Docker/outbox setup)
- [x] Run BDD suite (SQLite): all seq and journal scenarios pass
- [x] PostgreSQL store: validate fork anchors allow `history` and `journal`, reject `context`, and enforce journal client visibility
- [x] SQLite store: validate fork anchors allow `history` and `journal`, reject `context`, and enforce journal client visibility
- [x] MongoDB store: relax fork-anchor validation from history-only to history-or-journal and enforce journal client visibility
- [x] REST append/create-conversation paths: return consistent validation/authorization errors for invalid journal fork anchors
- [x] gRPC append/create-conversation paths: return consistent validation/authorization errors for invalid journal fork anchors
- [x] BDD feature file: REST journal-anchored fork scenarios across PostgreSQL, SQLite, and MongoDB
- [x] BDD feature file: gRPC journal-anchored fork scenarios
- [x] Frontend chat library: ensure fork view helpers handle fork anchors that are not present in the visible history list
- [x] Developer frontend: ensure fork badges, labels, and scroll targets handle journal-anchored forks without assuming a user history message
- [x] Docs: update `docs/entry-data-model.md`, `docs/design.md`, and `docs/db-design.md` for journal fork-anchor semantics and current channel names
- [x] Site docs: update concept forking docs, framework forking guides, and FAQ with the journal-anchor caveat

## Files to Modify

| File | Change |
|---|---|
| `contracts/openapi/openapi.yml` | Add `journal` to `Channel` enum; add `seq` field to `Entry` and `CreateEntryRequest`; add `fromSeq` parameter; document `409`; describe history-or-journal fork anchors |
| `contracts/openapi/openapi-admin.yml` | Add `journal` to `Channel`; add `seq` to `Entry`; add `fromSeq` to `adminGetEntries` |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add `JOURNAL = 3`; add `seq` fields to `Entry` and `CreateEntryRequest`; add `from_seq` to user/admin list requests; document history-or-journal fork anchors |
| `internal/model/model.go` | Add `Seq *uint32`; JSON marshal/unmarshal remains symmetric through the alias path |
| `internal/plugin/store/postgres/db/schema.sql` | Add `seq` column, range check, and partition-compatible partial unique index |
| `internal/plugin/store/postgres/postgres.go` | Persist `seq`, map duplicate `seq` conflicts, implement `fromSeq` list ordering, and allow same-client journal fork anchors |
| `internal/plugin/store/sqlite/db/schema.sql` | Add `seq` column, range check, and partial unique index |
| `internal/plugin/store/sqlite/sqlite.go` | Persist `seq`, map duplicate `seq` conflicts, implement `fromSeq` list ordering, and allow same-client journal fork anchors |
| `internal/plugin/store/mongo/mongo.go` | Add `seq` field, unique partial index, duplicate check, `fromSeq` list path, and same-client journal fork anchors |
| `internal/registry/store/plugin.go` | Add `seq` to `CreateEntryRequest` and entry-list query shape |
| `internal/plugin/store/metrics/metrics.go` | Forward the updated entry-list query shape |
| `internal/plugin/route/entries/entries.go` | Wire `seq` from request body; validate `journal`; return `409` on store duplicate error; pass `fromSeq` to store |
| `internal/plugin/route/admin/admin.go` | Pass `fromSeq` through admin entry listing |
| `internal/grpc/server.go` | Wire `seq`, `from_seq`, and `JOURNAL` for user/admin entry APIs; propagate fork auto-create validation errors |
| `internal/generated/apiclient/` | Regenerated — do not edit manually |
| `internal/bdd/testdata/features/entries-seq-rest.feature` | New BDD scenarios for REST `seq` behavior |
| `internal/bdd/testdata/features/journal-channel-rest.feature` | New BDD scenarios for REST `journal` channel behavior |
| `internal/bdd/testdata/features-grpc/entries-seq-grpc.feature` | New BDD scenarios for gRPC `seq` behavior |
| `internal/bdd/testdata/features/forking-rest.feature` | Add journal-anchored fork scenarios and context-anchor rejection |
| `internal/bdd/testdata/features-grpc/forking-grpc.feature` | Add gRPC journal-anchored fork coverage and context-anchor rejection |
| `frontends/chat-frontend/src/lib/conversation.ts` | Handle fork anchors that are not visible in history-only entry views |
| `frontends/developer/src/lib/conversation.ts` | Mirror fork-view handling for non-history fork anchors |
| `frontends/developer/src/components/ui/fork-point-badge.tsx` | Avoid assuming journal-anchored forks can be labeled from the next user message |
| `docs/entry-data-model.md` | Document history-or-journal fork anchors, journal client scoping, and exclusive-stop semantics |
| `docs/design.md` | Update high-level fork and channel descriptions; remove stale explicit fork-endpoint wording where appropriate |
| `docs/db-design.md` | Update stale channel names and note journal entries may be fork anchors |
| `site/src/pages/docs/concepts/forking.md` | Document journal-anchored fork points as an advanced runtime/debug feature |
| `site/src/pages/docs/*/conversation-forking.mdx` | Add framework guide notes that tutorials show history forks while journal anchors are supported for trusted runtimes |
| `site/src/pages/docs/faq.mdx` | Clarify fork points across visible history and trusted journal entries |

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
