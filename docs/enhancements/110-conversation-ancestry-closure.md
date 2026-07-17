---
status: proposed
---

# Enhancement 110: Conversation Ancestry Closure

> **Status**: Proposed.

> **Schema note**: The schema-numbering and migration-baseline portions of this proposal are superseded by [Enhancement 114](implemented/114-clean-break-schema-and-compatibility-reset.md). The conversation-ancestry design remains current.

## Summary

Replace fork-parent columns on `conversations` with a materialized conversation ancestry closure. The closure records every ancestor visible from a descendant, its depth, and the exclusive entry boundary contributed by that ancestor. Entry-listing queries can then apply ancestry, channel, sequence, ordering, and pagination in the datastore without loading an entire conversation group.

This is an intentionally reset-only schema change. PostgreSQL and SQLite schemas will be squashed into new baselines, and MongoDB migration logic will be reduced to creation of the current collections and indexes. Existing datastore contents are not migrated. SQL stores use one closure row per ancestor; MongoDB stores the equivalent closure as one atomic document per descendant.

## Motivation

Entry listing currently builds ancestry by loading all conversations in a group and commonly loads all group entries before filtering and paginating in memory. This makes a request proportional to the size of a fork tree rather than its requested page. [GitHub issue 371](https://github.com/chirino/memory-service/issues/371) requires common entry-listing paths to scale with ancestry depth and page size.

The current `conversations` representation stores only the direct edge:

```text
forked_at_conversation_id
forked_at_entry_id
```

That representation is compact, but every read must reconstruct the transitive path. It also encourages group-wide loading because the datastore cannot directly join a requested conversation to all of its visible ancestry segments.

A materialized closure makes the complete path directly queryable and moves the exclusive visibility boundary next to the relationship where it applies. It also makes the direct fork columns on `conversations` redundant: SQL stores the direct edge at depth 1, while MongoDB stores it in the separate ancestry document.

## Design

### Terminology

- **Descendant**: the conversation whose visible path is being described.
- **Ancestor**: a conversation that occurs on the descendant's fork path. Every conversation is its own depth-0 ancestor.
- **Exclusive boundary**: the first entry in one ancestry segment that is not inherited by the descendant.
- **Fork anchor**: the entry requested when creating the direct fork edge. It may be owned by the direct parent or inherited by the parent from an older ancestor.
- **Blank-segment fork**: a fork whose direct parent contributes no entries from its own segment because its boundary is null. Earlier ancestors may still contribute entries according to their already-materialized boundaries.

### Relational Schema

PostgreSQL uses UUID for entry identifiers and SQLite uses text, matching their respective `entries.id` representations.

```sql
CREATE UNIQUE INDEX conversations_group_id_id_unique
    ON conversations (conversation_group_id, id);

CREATE TABLE conversation_ancestry (
    conversation_group_id UUID NOT NULL
        REFERENCES conversation_groups(id) ON DELETE CASCADE,
    descendant_conversation_id TEXT NOT NULL,
    ancestor_conversation_id TEXT NOT NULL,
    depth INTEGER NOT NULL CHECK (depth >= 0),
    before_entry_id UUID,
    forked_at_entry_id UUID,
    PRIMARY KEY (conversation_group_id, descendant_conversation_id, ancestor_conversation_id),
    UNIQUE (conversation_group_id, descendant_conversation_id, depth),
    FOREIGN KEY (conversation_group_id, descendant_conversation_id)
        REFERENCES conversations(conversation_group_id, id) ON DELETE CASCADE,
    FOREIGN KEY (conversation_group_id, ancestor_conversation_id)
        REFERENCES conversations(conversation_group_id, id) ON DELETE CASCADE,
    CHECK (
        (depth = 0
            AND descendant_conversation_id = ancestor_conversation_id
            AND before_entry_id IS NULL
            AND forked_at_entry_id IS NULL)
        OR
        (depth = 1 AND descendant_conversation_id <> ancestor_conversation_id)
        OR
        (depth > 1
            AND descendant_conversation_id <> ancestor_conversation_id
            AND forked_at_entry_id IS NULL)
    )
);

CREATE INDEX idx_conversation_ancestry_ancestor
    ON conversation_ancestry (
        conversation_group_id,
        ancestor_conversation_id,
        depth,
        descendant_conversation_id
    );
```

SQLite uses the same logical schema with `conversation_group_id TEXT`, `before_entry_id TEXT`, and `forked_at_entry_id TEXT`.

`before_entry_id` and `forked_at_entry_id` are not declared as foreign keys. Although an entry ID and group could reference the partitioned entries key, that would not prove that the entry belongs to the ancestry segment represented by the row. Fork creation instead validates ownership, group membership, visibility, and permitted history/journal channel before writing ancestry rows.

The unique `(conversation_group_id, descendant_conversation_id, depth)` constraint enforces one path rather than a general DAG. A conversation cannot have two ancestors at the same distance. `conversation_group_id` is deliberately stored on every ancestry row because the group is the lifecycle and authorization boundary: it supports direct group-scoped cleanup and queries without deriving the group through either conversation endpoint.

Both referenced conversations must belong to `conversation_group_id`. The composite foreign keys enforce this invariant in PostgreSQL and SQLite, while fork creation validates it before inserting rows. MongoDB enforces the same rule in application logic and schema tests.

### Row Invariants

Every conversation has exactly one self row:

| group | descendant | ancestor | depth | before entry | fork anchor |
| --- | --- | --- | ---: | --- | --- |
| `G` | `C` | `C` | 0 | null | null |

For a direct child `C` forked from `P` before entry `E`, `C` receives:

1. A self row `(C, C, 0, null, null)`.
2. A direct-parent row `(C, P, 1, E, E)` when `E` is owned by `P`.
3. One row for each ancestor of `P`, with depth incremented and its existing boundary preserved.

For example:

| group | descendant | ancestor | depth | before entry | fork anchor |
| --- | --- | --- | ---: | --- | --- |
| `G` | `root` | `root` | 0 | null | null |
| `G` | `child` | `child` | 0 | null | null |
| `G` | `child` | `root` | 1 | `root-entry-5` | `root-entry-5` |
| `G` | `grandchild` | `grandchild` | 0 | null | null |
| `G` | `grandchild` | `child` | 1 | `child-entry-3` | `child-entry-3` |
| `G` | `grandchild` | `root` | 2 | `root-entry-5` | null |

The boundary is exclusive. Entries whose composite order is equal to or after the boundary are not visible through that ancestor row.

A null boundary has two meanings distinguished by depth:

- At depth 0, it means the descendant's own segment is unbounded.
- At depth greater than 0, it means that ancestor segment contributes no entries. This preserves current blank-segment fork behavior while retaining lineage for fork-tree APIs.

`forked_at_entry_id` is separate from `before_entry_id` because the direct fork anchor can be inherited. If `grandchild` forks from `child` before `root-entry-2`, its rows are:

| group | descendant | ancestor | depth | before entry | fork anchor |
| --- | --- | --- | ---: | --- | --- |
| `G` | `grandchild` | `grandchild` | 0 | null | null |
| `G` | `grandchild` | `child` | 1 | null | `root-entry-2` |
| `G` | `grandchild` | `root` | 2 | `root-entry-2` | null |

The depth-1 row retains the requested direct-edge metadata, the child segment contributes no entries, and the row for the entry-owning ancestor receives the actual visibility boundary.

### MongoDB Representation

MongoDB uses a `conversation_ancestry` collection with one document per descendant. The complete closure is embedded as an immutable array so it is inserted atomically without requiring replica-set transactions:

```javascript
{
  _id: "grandchild", // descendant conversation id
  conversation_group_id: "group-id",
  parent_conversation_id: "child",
  forked_at_entry_id: "child-entry-3",
  ancestors: [
    {
      conversation_id: "grandchild",
      depth: 0,
      before_entry_id: null
    },
    {
      conversation_id: "child",
      depth: 1,
      before_entry_id: "child-entry-3"
    },
    {
      conversation_id: "root",
      depth: 2,
      before_entry_id: "root-entry-5"
    }
  ]
}
```

`conversation_group_id` is stored once at the document level because every embedded ancestor belongs to that group. `_id` provides the unique descendant constraint. `parent_conversation_id` and `forked_at_entry_id` are the MongoDB equivalent of the SQL depth-1 edge fields and are authoritative for public direct-fork metadata. Keeping them outside the array permits an efficient non-multikey direct-child index without duplicating them in the conversation document. Application validation enforces unique `conversation_id` and `depth` values within `ancestors`, along with the same self-row and boundary invariants as SQL.

Required indexes are:

```javascript
{ conversation_group_id: 1, _id: 1 }
{ conversation_group_id: 1, parent_conversation_id: 1, _id: 1 }
{ conversation_group_id: 1, "ancestors.conversation_id": 1 }
```

Queries for transitive descendants use the multikey `ancestors.conversation_id` index. Direct-child queries use `parent_conversation_id`.

Before inserting a MongoDB ancestry document, the store BSON-encodes it and verifies it is below the connected server's maximum BSON document size. A fork that would exceed that datastore limit is rejected with a validation error rather than surfacing a raw driver failure. This limit only affects extraordinarily deep MongoDB fork chains; SQL closure rows do not share a single-document size limit.

### Simplification of `conversations`

Remove these persisted fields from SQL conversation rows and MongoDB conversation documents:

```text
forked_at_conversation_id
forked_at_entry_id
```

Retain these fields in public REST, gRPC, and internal store response types for API compatibility. SQL reads hydrate them from the depth-1 ancestry row:

```text
forkedAtConversationId = ancestor_conversation_id where depth = 1
forkedAtEntryId        = forked_at_entry_id where depth = 1
```

MongoDB reads hydrate them from `conversation_ancestry.parent_conversation_id` and `conversation_ancestry.forked_at_entry_id`.

The request fields used to create a fork also remain unchanged.

`started_by_conversation_id` and `started_by_entry_id` remain on `conversations`. Started-by lineage represents cross-group agent orchestration provenance, not same-group fork ancestry, and does not participate in entry visibility.

### Ancestry Construction

Root creation writes the conversation and its self ancestry row as one logical operation, using the datastore-specific atomicity protocol below.

Fork creation validates the source conversation, access, group, and optional anchor. A non-null anchor must be visible in the source conversation's closure and must be a history entry or an authorized journal entry. The store resolves which ancestry row owns the anchor and its depth before writing the child.

For each copied parent row:

- rows older than the anchor-owning segment preserve their existing boundaries;
- the anchor-owning segment receives the new anchor as `before_entry_id`;
- newer segments between the anchor owner and the direct parent receive a null `before_entry_id` and contribute no entries;
- the copied parent self row becomes depth 1 and receives the requested anchor as `forked_at_entry_id`, regardless of which segment owns it.

When the requested anchor is null, existing ancestor boundaries are preserved and the direct parent segment receives a null boundary. Conceptually:

```sql
INSERT INTO conversation_ancestry (
    conversation_group_id,
    descendant_conversation_id,
    ancestor_conversation_id,
    depth,
    before_entry_id,
    forked_at_entry_id
)
SELECT
    :conversation_group_id,
    :child_id,
    ancestor_conversation_id,
    depth + 1,
    CASE
        WHEN :fork_anchor_id IS NULL THEN before_entry_id
        WHEN depth < :anchor_owner_depth THEN NULL
        WHEN depth = :anchor_owner_depth THEN :fork_anchor_id
        ELSE before_entry_id
    END,
    CASE WHEN depth = 0 THEN :fork_anchor_id ELSE NULL END
FROM conversation_ancestry
WHERE conversation_group_id = :conversation_group_id
  AND descendant_conversation_id = :parent_id;

INSERT INTO conversation_ancestry (
    conversation_group_id,
    descendant_conversation_id,
    ancestor_conversation_id,
    depth,
    before_entry_id,
    forked_at_entry_id
) VALUES (:conversation_group_id, :child_id, :child_id, 0, NULL, NULL);
```

Copying the parent's closure rows makes fork creation O(ancestry depth). It does not depend on the number of conversations or entries in the group.

An idempotent retry that finds an existing conversation must load its depth-1 row when reconstructing the response. It must not silently accept closure rows that disagree with the requested parent or boundary.

Requiring a non-null anchor to be visible in the source ancestry tightens the current SQL and Mongo implementations, which only verify that the entry belongs to the same group. The tighter rule matches the contract's fork-ancestry semantics and prevents an anchor on a sibling branch from creating an incoherent closure.

### SQL Transactionality

PostgreSQL and SQLite write the conversation and all ancestry rows in the existing route-scoped write transaction. A failure rolls back the complete conversation creation.

### MongoDB Write Protocol

The current MongoDB store does not start database transactions: `InWriteTx` records write intent in the context, but `createConversation` issues independent operations. The ancestry design must not assume transaction support because the supported development and Kubernetes MongoDB deployments are standalone servers rather than replica sets.

MongoDB therefore uses an idempotent publish-last protocol:

1. Validate and resolve the parent closure and optional anchor.
2. Construct the complete ancestry array in memory from the parent's single closure document.
3. Insert one ancestry document with `_id` equal to the new conversation ID. On duplicate key, verify the existing document exactly matches the requested group and lineage.
4. Create or verify the group membership records needed for a new root/started conversation.
5. Insert the conversation document last, making the conversation visible to normal reads only after its closure is complete.

The ancestry insert is both the complete closure and the concurrency claim, so two requests cannot combine partial lineages. Orphan ancestry documents without a conversation are ignored by reads, deleted best-effort after a failed creation, and removed by a startup repair pass in bounded batches. The implementation must never expose a conversation document before its complete closure exists.

### Entry Visibility Query

The datastore first loads the requested conversation's closure rows ordered by descending depth (oldest visible ancestor first). It then builds one globally ordered entry query whose predicates describe the visible segments:

```sql
SELECT e.*
FROM entries e
JOIN conversation_ancestry ca
  ON ca.conversation_group_id = :conversation_group_id
 AND ca.descendant_conversation_id = :conversation_id
 AND ca.ancestor_conversation_id = e.conversation_id
 AND e.conversation_group_id = ca.conversation_group_id
LEFT JOIN entries boundary
  ON boundary.conversation_group_id = ca.conversation_group_id
 AND boundary.id = ca.before_entry_id
WHERE
    ca.depth = 0
    OR (
        ca.depth > 0
        AND ca.before_entry_id IS NOT NULL
        AND (
            e.created_at < boundary.created_at
            OR (
                e.created_at = boundary.created_at
                AND (
                    (boundary.seq IS NULL AND e.seq IS NULL AND e.id < boundary.id)
                    OR
                    (boundary.seq IS NOT NULL AND (
                        e.seq IS NULL
                        OR e.seq < boundary.seq
                        OR (e.seq = boundary.seq AND e.id < boundary.id)
                    ))
                )
            )
        )
    )
ORDER BY e.created_at ASC, e.seq ASC NULLS FIRST, e.id ASC
LIMIT :limit_plus_one;
```

The comparison above implements the required `created_at ASC, seq ASC NULLS FIRST, id ASC` order explicitly. SQLite uses the equivalent expression. MongoDB may resolve the small set of boundary ordering keys before constructing its globally sorted entry query instead of using a `$lookup` if explain plans show that to be more efficient.

All ancestry segments participate in one global sort. Implementations must not concatenate independently sorted segment batches because equal timestamps can violate the required `id` tie-break ordering across segment boundaries.

### Bidirectional Pagination

The closure determines entry visibility independently of pagination direction. Forward, backward, and tail reads reuse the same ancestry join and visibility predicates:

- An initial/forward page orders by `created_at ASC, seq ASC NULLS FIRST, id ASC` and applies a strictly-after composite cursor when present.
- A backward or tail page orders by `created_at DESC, seq DESC NULLS LAST, id DESC`, applies a strictly-before composite cursor when present, fetches `limit + 1`, and reverses the retained page before returning it.
- Cursor validation joins the cursor entry to the requested descendant's closure and applies that segment's boundary. A cursor from a sibling branch, an excluded portion of an ancestor, or a filtered channel is rejected.
- `afterCursor` and `beforeCursor` continue to identify the last and first returned entry respectively when another page exists in that direction.

Because the datastore sees all visible segments as one ordered relation, a cursor can cross an ancestor/descendant boundary without loading or probing every entry in either segment.

Rejecting a non-visible `afterCursor` intentionally tightens the legacy in-memory helper, which silently restarted at the first page when it could not find that cursor. `beforeCursor` already rejects non-visible anchors. Both directions use the same validation rule after this enhancement.

The common query builder applies additional filters before pagination:

- requested channel and client/agent visibility;
- `fromSeq`, including `seq IS NOT NULL` and deterministic `seq ASC, id ASC` ordering;
- `upToEntryId` when its anchor is visible;
- forward, backward, and tail composite cursor predicates;
- epoch selection for context entries.

`forks=all` does not use a single descendant's ancestry. It queries `entries` by `conversation_group_id` with datastore-side filters and pagination.

`upToEntryId` is resolved against the caller-visible ancestry before channel or epoch filtering. The cutoff is inclusive in the full visible composite order; filtered results then retain only rows at or before that cutoff. This preserves the current behavior where the cutoff entry itself need not belong to the requested channel.

Context `epoch=latest` is also resolved before pagination. For ancestry reads, the datastore computes the maximum epoch among visible context rows in the requested client/agent scope and returns visible rows at that epoch. For `forks=all`, the maximum is computed across the authorized group scope. `epoch=all` and explicit numeric epochs become ordinary predicates. These calculations use a CTE/subquery or aggregation pipeline and must not materialize the complete result set in application memory.

When no channel is requested, history is unscoped while context and journal rows remain visible only when their `client_id` matches the caller's authenticated client. User-facing context queries retain client/agent scope; admin entry queries use the same ancestry and pagination relation without user-facing client suppression.

### Conversation Queries

Queries that currently inspect direct fork columns are expressed through ancestry:

| Behavior | Closure predicate |
| --- | --- |
| Hydrate direct parent | group and descendant match, with `depth = 1` |
| List roots | no row in the group for descendant with `depth = 1` |
| Test whether `A` is an ancestor of `D` | group-scoped row `(D, A)` exists |
| Find direct fork children of `P` | group matches and `ancestor_conversation_id = P AND depth = 1` |

Conversation list and fork-summary queries should join only the depth-1 row needed for public fork metadata. They must not load the complete closure unless ancestry is required.

The public `ancestry=roots|children|all` conversation-list parameter describes `started_by_conversation_id`, not fork ancestry, and remains unchanged. `mode=latest-fork` continues to select the most recently updated conversation in each group; it is not equivalent to selecting a leaf in the fork closure.

### Delete Semantics

The service only hard-deletes conversation groups; it does not delete individual fork nodes. `conversation_ancestry.conversation_group_id` therefore references `conversation_groups(id) ON DELETE CASCADE`, making ancestry cleanup follow the supported lifecycle directly. Deleting a group cascades to conversations, entries, memberships, and ancestry rows. Consequently, removing the direct-parent foreign key does not change supported deletion behavior.

Individual conversation hard deletion remains unsupported. If it is introduced later, it must define whether descendants are deleted, reparented, or rejected; closure-row foreign keys alone are not a substitute for that policy.

### Schema Reset and Migration Squash

This enhancement deliberately does not preserve existing datastore contents. Deployments must reset PostgreSQL, SQLite, and MongoDB data before starting the new version.

Reset detection uses an explicit datastore schema version. Fresh SQL baselines create a single-row `schema_metadata` table, and MongoDB creates an equivalent metadata document. Before applying the baseline, the migrator follows these rules:

1. An empty datastore is initialized at schema version 110.
2. A datastore already marked version 110 is accepted and converged only for idempotent indexes/collections belonging to that baseline.
3. A datastore containing Memory Service tables/collections without version 110, including every pre-enhancement layout, fails with an error instructing the operator to reset it.

When `MEMORY_SERVICE_DB_MIGRATE_AT_START=false`, the service does not initialize or upgrade the datastore; deployment tooling is responsible for installing the version-110 baseline before the service starts.

The schema work will:

- remove `forked_at_conversation_id` and `forked_at_entry_id` from baseline conversation schemas;
- add the complete `conversation_ancestry` baseline schema and indexes;
- fold SQLite additive `ALTER TABLE` and compatibility index steps into `db/schema.sql`;
- remove obsolete SQLite compatibility migration helpers and their upgrade-only tests;
- merge the core and episodic MongoDB migrators into one version-110 datastore baseline, removing legacy document backfills and field cleanup intended for older layouts;
- update test fixtures so every datastore starts from an empty database;
- add schema-version initialization and a clear reset-required error for incompatible existing layouts.

The reset requirement must be called out in release notes and deployment documentation before release.

“Squash all migrations” applies to schema owned by the selected primary datastore: core conversations/entries, attachments, outbox, tasks, checkpoints, and episodic memory collections/tables. Optional vector backends (`pgvector`, `sqlitevec`, Qdrant, and Infinispan) retain their independently registered migration plugins because they are separately selectable capabilities with their own lifecycle. SQLite FTS remains a conditional baseline applied only when FTS5 support is available; it must not contain compatibility upgrades for older core schemas.

### Index Strategy

Ancestry indexes support lineage discovery, but bounded entry queries also need datastore-specific indexes. Candidate SQL indexes are:

```sql
CREATE INDEX idx_entries_conversation_channel_order
    ON entries (conversation_id, channel, created_at, seq, id);

CREATE INDEX idx_entries_conversation_channel_seq_order
    ON entries (conversation_id, channel, seq, created_at, id)
    WHERE seq IS NOT NULL;

CREATE INDEX idx_entries_group_channel_order
    ON entries (conversation_group_id, channel, created_at, seq, id);
```

PostgreSQL partitioning and SQLite query planning differ, so indexes are added only after `EXPLAIN`/`EXPLAIN QUERY PLAN` confirms that an implemented query path uses them. Superseded narrower indexes should be removed from the squashed baseline.

## Design Decisions

### Closure is the source of truth

Persisting both direct-parent columns and ancestry records would make reads simpler during a rolling migration, but it creates two writable sources of truth. Because this change explicitly permits a reset, the design removes the duplicate conversation columns. SQL derives direct fork metadata from depth 1; MongoDB reads it from the authoritative ancestry document.

### Preserve `conversation_group_id`

The closure does not replace `conversation_group_id`. Groups remain the access-control, archive, eviction, partitioning, and `forks=all` boundary. Deriving a group through ancestry would complicate these common operations and weaken referential integrity.

### Preserve entry ownership columns

`entries.conversation_id` and `entries.conversation_group_id` remain. The former identifies the branch segment; the latter supports access-controlled group queries and PostgreSQL partitioning.

### Materialize boundaries, not ordering keys

The closure stores the boundary entry ID rather than duplicating `created_at`, `seq`, and entry ID ordering fields. Fork anchors are immutable and are only removed with their group, so resolving their ordering keys remains safe. If measurements show this join dominates entry listing, materialized boundary ordering keys can be considered separately.

## Security Considerations

- An ancestry row is not an authorization grant. Access continues to be checked through the descendant conversation's `conversation_group_id` and group membership before entries are queried.
- Fork creation must verify that the requested parent belongs to the resolved group and that a non-null anchor is visible in that parent's ancestry.
- Journal anchors remain client-scoped; a client may only fork at a journal entry it is authorized to observe.
- MongoDB aggregation pipelines must apply membership authorization before returning entry content.

## Testing

### Schema and invariant tests

- Every root and fork receives exactly one self row in its conversation group.
- Fork creation copies all parent closure rows with incremented depth.
- Only the copied parent self row receives direct-edge `forked_at_entry_id`; `before_entry_id` is placed on the segment that owns the anchor.
- A fork anchored in an inherited ancestor bounds the owning ancestor, excludes newer path segments, and retains the requested anchor on the depth-1 row.
- A sibling-branch or otherwise invisible anchor is rejected.
- MongoDB rejects a fork before writing when its encoded ancestry document would exceed the server's BSON document limit.
- Duplicate depths, duplicate ancestor rows, invalid self rows, ancestry rows whose conversations do not belong to the recorded group, and cross-group fork boundaries are rejected.
- Idempotent conversation creation returns fork metadata derived from the closure.
- Group eviction removes all ancestry rows.

### Behavioral BDD coverage

```gherkin
Feature: conversation ancestry closure

  Scenario: A nested fork exposes entries from each bounded ancestor
    Given a root conversation with history entries A, B, and C
    And a child fork before C with history entries D and E
    And a grandchild fork before E with history entry F
    When I list the grandchild history entries
    Then the entries are A, B, D, and F in global composite order

  Scenario: A blank-segment fork preserves earlier inherited ancestry
    Given a child that inherits root entries before C
    When I fork at the beginning of the child segment
    Then the new fork inherits the root entries before C
    And it inherits no entries owned by the child

  Scenario: A journal entry is an exclusive fork boundary
    Given a permitted journal entry between two history entries
    When I fork before the journal entry
    Then only history entries ordered before the journal boundary are inherited

  Scenario: A fork anchor can be inherited from an older ancestor
    Given a child conversation that inherits root entry B
    When I fork the child before inherited entry B
    Then the new conversation reports the child as forkedAtConversationId
    And it reports B as forkedAtEntryId
    And it inherits only entries ordered before B from the root
    And it inherits no entries owned by the child

  Scenario: A fork anchor from a sibling branch is rejected
    Given sibling conversations in the same conversation group
    When I fork one sibling using an entry visible only in the other sibling
    Then the request is rejected as invalid

  Scenario: Equal timestamps retain global ordering across segments
    Given visible ancestor and descendant entries with equal createdAt values
    When I list entries with a page limit
    Then entries are ordered by createdAt, seq nulls first, and id
    And following afterCursor returns each entry exactly once

  Scenario: fromSeq excludes entries without a sequence
    Given visible ancestry entries with null and non-null sequence values
    When I list entries from sequence 3
    Then only entries with sequence at least 3 are returned
    And they are ordered by sequence ascending

  Scenario: forks all remains group scoped
    Given a conversation group with sibling branches
    When I list entries with forks all and a page limit
    Then entries from the group are returned in global order
    And pagination does not load the complete group

  Scenario: MongoDB rejects an ancestry document that exceeds its server limit
    Given a fork chain whose next encoded ancestry document exceeds the MongoDB document limit
    When I create another fork
    Then the request is rejected as invalid
    And no conversation or ancestry document is published
```

Run equivalent coverage for PostgreSQL, SQLite, and MongoDB. Add targeted unit tests for query builders and MongoDB aggregation pipelines so BDD coverage does not depend on query-plan implementation details.

### Query-plan tests

Populate groups with many sibling branches and deep ancestry, then confirm:

- the number of loaded closure rows is proportional to ancestry depth;
- returned entry rows are bounded by page size plus cursor probes;
- no common path calls `listEntriesForGroup`/`loadEntriesForGroup`;
- expected ancestry and entry indexes appear in datastore query plans;
- latency and allocations do not scale with unrelated sibling entries.

## Tasks

- [x] Add the relational `conversation_ancestry` model, baseline tables, constraints, and indexes.
- [x] Add the MongoDB `conversation_ancestry` document model, collection, and indexes.
- [x] Remove persisted direct fork columns from conversations and MongoDB conversation documents.
- [x] Write self and inherited ancestry rows using the SQL transaction and MongoDB publish-last protocols.
- [x] Implement MongoDB's idempotent publish-last creation and orphan-ancestry cleanup protocol.
- [x] Hydrate public direct-parent fork metadata from the SQL depth-1 row or MongoDB ancestry document.
- [x] Replace fork-root, direct-fork-child, fork-metadata, and ancestry queries with closure predicates without changing started-conversation ancestry filters.
- [x] Replace full-group entry loads with globally ordered datastore queries for common non-`allForks` entry-listing paths. SQL uses ancestry joins for visible history, context, journal, all-channel, and latest context-cache reads; MongoDB builds ancestry-derived visibility filters for the same bounded reads.
- [x] Optimize `fromSeq`, forward, backward, tail, `forks=all`, and admin entry listing for known history/context/journal/all-channel combinations. (`fromSeq`, `upToEntryId`, forward, backward, tail, context epoch filters, latest context-cache reads, user entry listing, admin entry listing, and `forks=all` listing are bounded for those shapes.)
- [x] Preserve journal-anchor, context epoch, client, and agent visibility semantics.
- [x] Squash PostgreSQL and SQLite schemas into reset-only baselines.
- [x] Remove obsolete SQLite compatibility migration code and upgrade-only tests.
- [x] Merge and squash core plus episodic MongoDB migration/index initialization into the version-110 baseline.
- [x] Add reset detection and a clear incompatible-schema startup error.
- [x] Add and validate datastore schema version 110 metadata.
- [x] Add schema-invariant, BDD, pagination, and query-plan tests for all datastores. (SQLite REST BDD now covers direct-child fork listing, forward/backward/tail pagination across exclusive ancestry boundaries, ancestry `fromSeq`, history `upToEntryId`, context `upToEntryId` through ancestry, and `forks=all` history/context pagination; SQLite query-count coverage verifies bounded `forks=all` context materialization; PostgreSQL dry-run SQL coverage verifies ancestry joins; MongoDB filter-shape coverage verifies bounded ancestry/group predicates.)
- [x] Update database design, deployment/reset instructions, release notes, and module facts.

## Files to Modify

| File | Change |
| --- | --- |
| `internal/model/model.go` | Add relational ancestry model and stop persisting direct fork fields on conversations. |
| `internal/plugin/store/postgres/db/schema.sql` | Replace fork columns/indexes with the squashed ancestry baseline and entry indexes. |
| `internal/plugin/store/sqlite/db/schema.sql` | Replace fork columns/indexes with the squashed ancestry baseline and fold in additive migrations. |
| `internal/plugin/store/postgres/postgres.go` | Write/query closure rows, hydrate fork metadata, and use bounded datastore entry queries. |
| `internal/plugin/store/sqlite/sqlite.go` | Write/query closure rows, remove compatibility migrations, and use bounded datastore entry queries. |
| `internal/plugin/store/mongo/mongo.go` | Add ancestry documents/indexes, remove direct fork persistence, and use bounded aggregation queries. |
| `internal/plugin/store/mongo/episodic_store.go` | Remove the separate datastore migrator after folding its collections and indexes into the MongoDB baseline. |
| `internal/plugin/store/*/schema.go` and datastore migrators | Install/validate schema version 110 and reject pre-reset layouts. |
| `internal/plugin/store/*/pagination_test.go` | Cover closure visibility, global ordering, cursor boundaries, and query shape/materialization bounds. |
| `internal/bdd/testdata/features/*.feature` | Cover nested ancestry, journal anchors, `fromSeq`, `forks=all`, and pagination. |
| `docs/db-design.md` | Replace direct-parent conversation columns with the ancestry closure design. |
| `docs/design.md` | Update fork persistence and retrieval descriptions. |
| `docs/datastore-reset.md` | Document the schema-110 reset requirement for deployments. |
| `docs/release-notes.md` | Call out the reset-required schema squash and ancestry closure change. |
| `docs/enhancements/implemented/034-forked-entry-retrieval.md` | Mark the group-load retrieval design as superseded by this enhancement after implementation. |
| `AGENTS.md` and `internal/FACTS.md` | Record the new ancestry source of truth and reset requirement after implementation. |

## Verification

```bash
# Formatting and compilation
gofmt -w internal/model/model.go internal/plugin/store/postgres internal/plugin/store/sqlite internal/plugin/store/mongo
go build ./... > /tmp/ancestry-build.log 2>&1
rg -n "ERROR|FAIL|panic|undefined:" /tmp/ancestry-build.log

# Focused store tests
go test ./internal/plugin/store/sqlite -count=1 > /tmp/ancestry-sqlite.log 2>&1
go test ./internal/plugin/store/postgres -count=1 > /tmp/ancestry-postgres.log 2>&1
go test ./internal/plugin/store/mongo -count=1 > /tmp/ancestry-mongo.log 2>&1
rg -n "ERROR|FAIL|panic|--- FAIL:" /tmp/ancestry-*.log

# Integration/BDD tests must run sequentially in the devcontainer
wt exec -- go test ./internal/bdd -run '^TestFeaturesSQLite$' -count=1
wt exec -- go test ./internal/bdd -run '^TestFeatures$' -count=1
wt exec -- go test ./internal/bdd -run '^TestFeaturesMongo$' -count=1
```

## Non-Goals

- Changing public REST or gRPC fork request/response fields.
- Removing conversation groups or moving access control away from groups.
- Supporting arbitrary DAGs or multiple direct parents.
- Supporting individual fork-node hard deletion or reparenting.
- Preserving pre-enhancement datastore contents.
