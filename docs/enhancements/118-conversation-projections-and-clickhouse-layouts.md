---
status: proposed
---

# Enhancement 118: Conversation projections, membership export, and ClickHouse layouts

> **Status**: Proposed.

## Summary

Add configurable conversation projections and bounded ClickHouse layout options, followed
by an opt-in, fixed membership table. Preserve existing exports without conversation YAML;
with it, store only declared application fields and the required lifecycle metadata.

## Motivation

The Prince UI combines journal run analytics with conversation relationships. It currently
parses `startedByConversationId` from `conversations_current.payload_json` or reconstructs
it from OneRing's journal. Analytics conversation snapshots already carry authoritative
lineage, but projections currently accept only entries and memories.

Projection tables also have fixed resource-oriented sorting keys. They cannot request
conversation-oriented ordering, skip indexes, or dictionary encoding for repeated kinds
and statuses. Separately, membership changes already reach SSE and gRPC subscribers, but
there is no complete membership snapshot export or typed analytics table.

This extends [projection chains](implemented/117-clickhouse-projection-chains.md) and
[the analytics exporter](implemented/404-clickhouse-analytics-export.md). Phase 1 covers
conversation projections and layouts. Phase 2 adds fixed membership export. Journal run
summaries, arbitrary engines or SQL expressions, and a new authorization system are outside
this proposal.

## Design

### Conversation projections

Supported projection resources become `entry`, `memory`, and `conversation`. Memberships
use the fixed table described below, not a configurable projection. OneRing journals,
steps, and runs remain entry content, and conversation snapshots supply direct lineage
without another projection resource.

A conversation root omits `selector`, requires `projectionRego`, and receives every exported
conversation snapshot. It supports the existing `one` and `many` row modes. Separate chains
can select the same resource. Each conversation chain has one unconditional route; later
manifests may add nullable columns or replace earlier logic, but cannot introduce content-type
or memory-kind selectors. Do not confuse an unconditional root with a schema-only extension.

Proposed syntax, not supported by the current loader:

```yaml
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata:
  name: prince_ui_conversations_v1
spec:
  resource: conversation
  table: prince_ui_conversations
  rows: one
  columns:
    started_by_conversation_id: {type: string, nullable: true}
  projectionRego: |
    package memoryservice.analytics
    output := {
      "started_by_conversation_id": object.get(input.record, "startedByConversationId", null),
    }
```

The processor already owns `conversation_id` and `conversation_group_id`; do not project
another copy. Started-by and fork relationships remain distinct. Creation, updates,
backfill, replay, missing snapshots, empty outputs, deletion fences, hard-delete purges,
retention, and disabled outputs must cover conversation projections. Archive state does
not mean deletion. Tables absent from the current configuration still receive cleanup.

### A defined, deterministic Rego context

Add `input.record` without removing or changing existing flat inputs. Use a closed mapping
from normalized analytics snapshots, not automatic protobuf serialization or arbitrary
new fields from future source schemas. Optional fields are absent when unavailable.

| Resource | Record fields |
| --- | --- |
| Conversation | `id`, `conversationGroupId`, `ownerUserId`, `clientId`, `agentId`, `createdAt`, `updatedAt`, `archived`, filtered `metadata`, `forkedAtConversationId`, `forkedAtEntryId`, `startedByConversationId`, `startedByEntryId`; `title` only in `full` mode. |
| Entry | `id`, `conversationId`, `conversationGroupId`, `userId`, `clientId`, `agentId`, `channel`, `contentType`, `createdAt`, `content`, `epoch`, `seq`, `indexedAt`; `indexedContent` only in `full` mode. |
| Memory | `id`, `namespace`, `key`, `kind`, `revision`, `createdAt`, `expiresAt`, `archived`, `value`, `attributes`. `kind` maps to the normalized memory-kind field. |

Record channel values match the existing flat input. Source timestamps use UTC RFC 3339
strings; optional timestamps are omitted when absent. Expose archival state as a boolean,
not an invented archival timestamp. The current resource decoder sometimes synthesizes
`archived_at` from processing time; that value must not leak into this deterministic context.
Do not expose internal `logical_id_source`, cursors, credentials, or batch metadata.

Entry content and memory values and attributes remain available to explicit projections
in `projected` mode, as they are in the existing flat context. Conversation metadata keeps
the operator allowlist, empty by default. Preserve the existing title and full-content
export policy; a generic JSON column does not bypass it. Metadata mode still rejects
projection manifests. Full mode still requires the decrypted-content acknowledgment.

Share formatting helpers where the field policy is identical, but do not build the Rego
context by reusing a generic payload formatter that omits existing projection inputs.
Rego receives no database client or related-resource lookups. Keep existing value, row,
and resource-size limits, nondeterministic-builtin rejection, and cancellation behavior.
Tests cover both supported input paths to prevent the new object from widening access.

### Built-in fallback and explicit payload capture

Without conversation YAML, preserve the existing `conversations`, `conversations_current`,
`conversation_lineage`, and shared `resources` schemas, default payloads, and disable-selector
behavior. Entry or memory YAML alone does not change this fallback.

With conversation YAML, custom tables contain only declared application columns plus
mandatory processor-owned fields. Stop new built-in conversation payload writes; shared
`resources` keeps its fixed identity, version, timestamp, archival, and deletion fields
with `payload_json = '{}'`. Cleanup needs these identities, not the omitted JSON. Lineage
and lifecycle output controls remain independent. Existing entry and memory capture is
unchanged. Existing generic rows are not retroactively erased and retain their purge/TTL rules.

YAML-defined projections already have no automatic JSON column. Current duplication comes
from writing the same generic `ResourceRow` into both `resources` and a per-type table.
Explicitly requesting a custom JSON snapshot uses ordinary columns and Rego:

```yaml
columns:
  payload_json: {type: json_string, nullable: false}
projectionRego: |
  package memoryservice.analytics
  output := {"payload_json": input.record}
```

`json_string` serializes the returned object. `json` can request native JSON instead.
Neither choice creates another generic copy. For both a narrow table and retained JSON,
use two ordinary projections or declare both typed fields and a JSON column in one table.
There is no payload-retention flag or exception to manifest immutability. Receiving a source
snapshot for evaluation is not permission to persist undeclared application fields.

Custom projections use non-reserved names. They do not repurpose `conversations` or change
its schema. Failing or disabling a configured conversation projector cannot re-enable
legacy payload capture. Resolve the capture mode from the configured resource declarations,
not from successful evaluations or whether an enabled projector returned rows. An invalid
manifest fails startup; a disabled output remains disabled.

### Ordered resource snapshots

The current processor already flushes before a second pending multi-row snapshot of the
same resource, because one batch shares an ingestion version. Generalize that protection
to every repeated `(resource kind, resource ID)` in live events and bootstrap pages,
including single-row conversation projections and fixed memberships. Check catalog
identities even when generic payload output is suppressed. Flush the first snapshot before
allocating the next batch/version; preserve frozen values and versions across retries.

This avoids two role or conversation changes receiving the same version. Event IDs are
hashes, not a chronological tie-breaker, and equal-version physical merges cannot establish
the intended order. Extending the existing flush rule is simpler than adding another
ordering column or changing the version format. Test multiple updates, revoke/re-share,
and empty/nonempty output transitions before an ordinary batch-size flush, with actual
ClickHouse merges as well as current-view reads. Measure the extra flushes for hot resources.

### Durable deletion fences in Phase 1

The V2 compatibility generation uses one fence-lifetime rule across resource kinds,
not a special rule only for the Phase 2 membership table. Retain the maximum identity
deletion version per `(exporter_id, resource_type, resource_id)` independently of payload
and tombstone-row cleanup. In particular, conversation fences remain after a hard-delete
purge, even if its projections are configured out or capture later returns to fallback.
Retained fences are not subject to payload TTLs or ordinary purge deletion. New grants or
resource incarnations may exceed the identity fence; delayed older INSERTs may not.

Enable the group-fence rule described in Phase 2 as part of V2 activation, without requiring
membership export to be enabled. Every conversation/group-dependent canonical view,
including generic and entry-projection views, excludes rows under a retained deleted-group
fence. A memory with no conversation group has no group-fence match. This covers late
dependent writes after a group purge, not just the conversation row itself.

All V2 hard-delete requests carry a persisted `cutoff_ingest_version Nullable(UInt64)` in
`purge_queue`, `PurgeRow`, and retry state; new requests require a non-null value. Payload
cleanup is bounded by the cutoff and its resource/group scope, so a delayed purge cannot
erase a later incarnation sharing an ID. Use each table's actual version column for this
bound, including `version` for projection failures, and use registry resource ownership
to avoid cross-kind matches on an untyped text ID. Existing completed V1 purge records may
remain historical with a null cutoff; never rerun them as unbounded V2 cleanup.

The current sink's final DELETE against `deletion_fences` must therefore change for V2,
not only for membership purges. Keep one maximum marker per deleted resource identity
and one per deleted group until explicit dataset retirement after writers and resumable
checkpoints are disposed of. This retains identity metadata, not application payloads;
document and measure the resulting catalog growth. The unchanged V1-only fallback retains
its existing behavior. V2 fixtures must verify payload removal while fences remain,
instead of reusing the existing assertion that every cleanup table becomes empty.

ClickHouse [mutations](https://clickhouse.com/docs/reference/statements/alter#mutations)
do not block INSERTs or affect data inserted after the mutation was submitted. Waiting
for purge completion therefore cannot replace a retained read fence.

Add a Phase 1 regression that completes a conversation purge and then accepts an old
projection INSERT. The current view must keep it hidden before and after restart,
physical merges, TTL work, and removal of its manifest. Apply the same delayed-writer
test to an entry under a deleted group and to other V2 identity-fence consumers.

### Mode transitions, replay, and migration

Track `legacy` or `custom` conversation capture in checkpoint state. A checkpoint without
this field is legacy. Separate the committed mode from a pending transition and its replay
ID; do not commit a new mode merely by loading configuration. Persist a pending transition
before writes and commit it only after the snapshot scan and durable catch-up complete.
A restart resumes the same pending replay rather than requiring a fresh ID on every attempt.

Switching either direction with an existing checkpoint requires a new
`--projection-replay-id`. Extend its validation to allow legacy restoration without
projection files. A fresh exporter performs normal bootstrap. A mode change without a
requested replay fails before writes with the required command and reason. Configuration
changes during a pending transition fail unless they match that transition; operators
finish it before requesting another. Ordinary restarts with the committed mode need no replay.

Restoring fallback after removing the last conversation YAML is an explicit broader-capture
transition, not permission to resume stale built-in readers immediately. Initial replay
fills only retained source records; neither the registry nor `resources` recreates source
content that has already been deleted. Show the selected mode, pending mode, and replay
progress without logging payloads.

For an online migration while built-in readers stay live, use two upgraded exporters with
distinct exporter/client IDs and checkpoints: one stays on fallback, one bootstraps the
custom projections. This uses existing dataset isolation and avoids a special payload-copy
switch. Queries must select one exporter ID throughout both conversation and journal
joins; the existing UI query must be changed before it sees both datasets. Retention
settings must agree because the tables are shared. Validate equivalence against retained
source data, switch the UI, and retire the old exporter in a separate operation. A single
exporter instead requires a coordinated cutover while its new tables bootstrap.

Upgrade all exporters sharing the database before enabling new layouts or conversation
registry entries. Quiesce old schema managers during activation; a version fence cannot stop an
old startup that already passed its version check. Before feature DDL or registration,
drain accepted legacy purge mutations and unfinished purge requests while old workers
are quiesced; their requests lack the cutoff required by V2. Fail activation with an
actionable recovery instruction if unfinished legacy work remains. Then
promote the `schema_versions` entry for component `clickhouse-export` from V1 to V2,
preserving the verified baseline checksum value. Insert the V2 entry, synchronously remove
the active V1 entry, and verify the resulting registry state before activating any feature.
Recover an interrupted promotion before proceeding; no existing application table is reset.
This is a compatibility generation, not a resource-row format change. Split those constants
so resource-row `schema_version` values retain their existing meaning.

New binaries accept verified V1 for legacy-only operation and V2 for activated features,
and reject unknown generations. An old manage-mode binary rejects the V2 entry before
recreating views; an old validate-mode binary also fails because the required active V1
entry is absent. Keeping both versions active would leave that second route unprotected.
This mechanically blocks later old-exporter restarts from treating custom ordering as a
single-row layout or deleting the new fence types through old cleanup code.

Promote only when enabling a conversation projection, non-default layout, or fixed
membership export; unchanged legacy deployments remain V1. A V2 database remains supported
by new binaries even after feature configuration is removed. Never automatically downgrade
after a failed activation or removal of feature configuration. Explain that an old binary's
reset suggestion means upgrade the binary, not reset the database. Test real pre-enhancement
binaries in both manage and validate modes, including interrupted promotion and restart.

Existing table data and schemas stay intact; no automatic drop, reorder, or reset occurs. Application rollback
uses an upgraded exporter binary and the restoration scan, not an older binary. Retire
old tables only after readers and exporters stop using them.

### Safe physical ordering

Expose `spec.clickhouse.ordering` as two presets, never arbitrary columns or SQL:

| Preset | Resources | `rows: one` | `rows: many` |
| --- | --- | --- | --- |
| `resource`, default | Entry, memory, conversation | `exporter_id, resource_id` | `exporter_id, resource_id, row_index` |
| `conversation` | Entry, conversation | `exporter_id, conversation_id, resource_id` | `exporter_id, conversation_id, resource_id, row_index` |

```yaml
spec:
  clickhouse:
    ordering: conversation
```

The processor fixes the engine, partitioning, replacement version, and identity suffix.
Memory projections cannot choose the conversation preset. Projected fields such as status,
run ID, timestamp, or parent ID cannot enter the key. This protects
[ReplacingMergeTree's sorting-key replacement identity](https://clickhouse.com/docs/engines/table-engines/mergetree-family/replacingmergetree).

Use resource-specific identity extraction. An entry event must supply its conversation
ID explicitly; the generic `firstString(..., "id")` fallback in the current event path must
not mistake the entry's ID for its conversation. Snapshots, markers for empty or shrinking
outputs, and tombstones must use the same invariant conversation ID. Missing identity
is fatal even under `continue-generic`; leave the batch uncheckpointed. Test all stores,
backfill, replay, and deletion paths before enabling this preset. Configuration cannot
prove a source producer never supplies a different, nonempty ID.

Keep latest-resource-snapshot semantics independent of physical ordering. For the
conversation preset, add the invariant conversation ID to both resource-version and row
window partition keys so conversation predicates can safely push below the windows.
Mutable predicates stay above snapshot selection. Preserve row-count markers, deletion
fences, exporter isolation, and handling of surplus indexes from older outputs. Validate
pruning with `EXPLAIN indexes = 1`; a new sort key alone does not prove an optimization.

### Structured indexes and encodings

Add root-level `spec.clickhouse.indexes`. Each index names one existing column, with no SQL
expression. Initially allow these bounded settings:

| Field | Initial rule |
| --- | --- |
| `name`, `column` | Validated identifiers; unique index name and an existing column. |
| Index count | At most eight indexes per table. |
| `granularity` | Integer 1 through 64; default 1. |
| `type: minmax` | Numeric, boolean, or timestamp columns; no parameters. |
| `type: set` | String, integer, or boolean columns; required `maxValues` from 1 through 4096. |
| `type: bloom_filter` | String columns; `falsePositiveRate` from 0.001 through 0.25, default 0.025. |

Supported nullable/dictionary wrappers must be checked against the pinned ClickHouse
server in integration tests. Arrays, JSON, Variant, and unsupported combinations are
rejected in this first version. For example:

```yaml
spec:
  clickhouse:
    indexes:
      - name: parent_lookup
        column: started_by_conversation_id
        type: bloom_filter
        falsePositiveRate: 0.01
        granularity: 1
```

[Skip indexes](https://clickhouse.com/docs/optimize/skipping-indexes) eliminate granules;
they are not point-lookup indexes. A mutable parent-ID predicate cannot move below latest
snapshot selection, so that index may provide no pruning through the current view. It is
an opt-in candidate, not a promised default optimization.

Extend the existing per-column `clickhouse` object:

```yaml
columns:
  kind:
    type: string
    nullable: false
    clickhouse:
      lowCardinality: true
  recorded_at_ms:
    type: uint64
    nullable: false
    clickhouse:
      codecs:
        - name: delta
        - name: zstd
          level: 1
```

Allow `lowCardinality` only on strings, rendering nullable strings as
`LowCardinality(Nullable(String))`. Keep Go/Rego value types unchanged. Allow `lz4` without
parameters, `zstd` with level 1 through 9 and default 1, and an optional single `delta`
transform on integer/timestamp columns before exactly one compressor. Reject misplaced,
repeated, incompatible, or unknown settings. Preserve the existing native-JSON bounds;
these options are not unrestricted physical-type overrides. References:
[LowCardinality](https://clickhouse.com/docs/sql-reference/data-types/lowcardinality) and
[compression](https://clickhouse.com/docs/data-compression/compression-in-clickhouse).

Ordering and indexes belong to the chain root. Column encodings belong to the manifest
that declares the column, including `addColumns`. Later manifests cannot alter an existing
layout; changes require a new table and replay. New options are omitted from digests when
absent, preserving existing digests, table definitions, and defaults.

### Registry, schema checks, and failures

Add `row_mode` and versioned `layout_json` metadata to the registry with an additive,
idempotent migration outside the existing schema-version checksum. New registrations
record row mode and the cumulative physical descriptor at that chain position: columns
and logical/physical types, ordering, encodings, index definitions, and view-layout version.
The descriptor has no resource values or Rego input. Compare the longest compatible
registered chain across exporters against the actual table. Reject divergent descriptors
and recheck concurrent registrations as Enhancement 117 does.

Treat stored descriptors as untrusted metadata: validate their version, identifiers,
types, and allowed options before generating statements. Never execute stored DDL.

Configured-out tables still need view recreation, retention, and purge. Read their row
mode and descriptor from the registry, not an exact sort-key string. Legacy empty metadata
can be recovered from configured manifests and verified physical schema. Without manifests,
accept only the two historical layouts after checking required lifecycle columns and the
engine. That fallback validates lifecycle compatibility, not an unknown original projected
schema. Never infer a custom layout. Registry selectors gain the `conversation:` prefix;
fixed memberships do not enter the projection registry.

Validate all manifests before connecting to ClickHouse. Preflight ownership and existing
definitions before layout DDL. `manage` creates missing owned objects and applies approved
additive migrations; it never silently rewrites a conflicting engine, key, type, codec, or
index. `validate` performs no DDL. Repairing a missing index on a populated table reports
that historical parts need explicit `MATERIALIZE INDEX`; do not start a large mutation
silently. DDL is nontransactional, so retries must recognize valid partial work without
registering an incompatible manifest or starting ingestion.

| Stage | Rejected condition | Outcome |
| --- | --- | --- |
| Manifest load | Unknown/duplicate keys, unsupported preset/resource, invalid chains, reserved columns, incompatible codecs, bad index names/types/parameters, or conflicting inherited options. | Nonzero exit with manifest and field path, invalid setting, and supported alternatives; no connection required. |
| Startup | Registry conflicts, missing required metadata, unsupported source capability, physical mismatch, or an unrequested capture-mode transition. | Fatal error before ingestion, readiness, and checkpoint advancement. Explain migration or replay required. |
| Resource processing | Missing required identity on a snapshot, empty marker, or tombstone. | Stop processing, fail readiness, and retain the frozen batch for retry; no checkpoint advancement. |

Strict decoding applies to `AnalyticsProjection`, not unrelated YAML document kinds.
Previously ignored unknown projection settings now fail preflight. There is no unsafe
flag, custom engine/primary-key escape hatch, or fallback to broader capture. Ordinary
Rego failures keep the configured failure policy and payload-free failure rows. They do
not bypass schema, registry, or identity checks. Partial writes can be visible until retry;
canonical views must still deliver latest-snapshot results.

Errors and metrics use fixed failure codes and bounded labels, never raw driver SQL,
source identities, metadata, payloads, or credentials. Inspection reports schemas,
indexes, capture mode, and proposed DDL; runtime health reports replay and failure status.
Log or audit mode transitions and registration outcomes without resource data.

### Phase 2: Fixed membership export

Membership is a small built-in table, not `resource: membership` YAML. Reject that resource
in the projection loader. Add `--export-memberships` and
`MEMORY_SERVICE_CLICKHOUSE_EXPORT_MEMBERSHIPS`, default false. Existing exporters and
checkpoints do not start collecting memberships on upgrade. The flag enables fixed
metadata export without requiring projection files, including in metadata payload mode.

Create reserved `conversation_memberships` and `conversation_memberships_current` relations
only after explicit enablement or when existing rows/metadata require lifecycle management.
Track their fixed DDL under a separate `clickhouse-memberships` schema component rather
than changing the existing baseline checksum. A preexisting projection may already own
the proposed table name because it is not currently reserved. When membership export is
off, keep that valid legacy projection working. When enabling membership export, reject
any such ownership collision before writes; never take over or reinterpret its table.
Use a different database or migrate that custom projection before retrying. Validate
initialization ownership, exact fixed schemas, and partial-startup recovery explicitly.
The fixed table uses generic-resource common columns `exporter_id`, `batch_id`, `event_id`,
`source_cursor`, `ingest_version`, `observed_at`, and `schema_version`, plus `resource_id`,
`conversation_group_id`, `user_id`, `access_level LowCardinality(String)`,
`created_at Nullable(DateTime64(9, 'UTC'))`, and `is_deleted`. It has no `conversation_id`,
raw JSON, user-extensible columns, row indexes, or configurable layout. Use
`ReplacingMergeTree(ingest_version)` ordered by
`(exporter_id, conversation_group_id, user_id)`.

Membership identity is the immutable group/user pair, never the role. Define `resource_id`
as unpadded base64url of the group's 16 UUID bytes followed by the exact UTF-8 user ID.
A single shared helper produces it for snapshots, events, tombstones, catalog rows, and
purges. This is an unambiguous encoding, not anonymization. The current view selects the
latest pair by ingestion version/event ID, then applies membership and group deletion fences.
A revoke needs no creation timestamp; active full snapshots must contain their source time.
The access level uses the source model's owner/manager/writer/reader vocabulary.

Revocation is a source hard delete, not merely a logical tombstone. Before advancing its
checkpoint, persist purge work for that exact group/user identity together with its
deletion-version cutoff. Use Phase 1's persisted cutoff and retained-fence contract;
membership revocations also require a non-null cutoff.
Pair-specific payload cleanup in `conversation_memberships`, `resources`, and
`lifecycle_events` must include `ingest_version <= cutoff_ingest_version` as well
as exporter and identity. Scope shared catalog/event tables to resource type
`membership`; a matching text ID in another resource type is not a deletion target.
Fixed memberships do not create Rego projection-failure rows and must not delete another
resource's failure records through an untyped ID match.
Never use the existing unbounded identity-only purge for a membership revocation.
The purge must not remove its membership identity fence, even through a generic final
fence-cleanup step. The cutoff protects newer grants from deletion; the retained fence
independently rejects an older snapshot inserted after payload cleanup has completed.

A re-share keeps the same identity but receives a greater ingestion version. An old purge,
including one retried after a restart, must retain the newer grant and the maximum revoke fence.
Persist the original cutoff with the purge request; never recompute it from worker time
or the current row. Reusing a purge ID must reuse its stored cutoff rather than replacing
it with a later value. Use explicit column lists on all purge-queue writes and retain the
cutoff across status updates. Process and checkpoint the revoke consistently with the existing
frozen-batch and synchronous managed-purge contract. Test delayed purge after re-share,
repeated revoke/re-share cycles, and crashes before and after purge completion.

The existing membership notifications already supply group/user/role for changes, and
retained outbox cursors support replay. They do not cover a complete baseline. Add bounded
membership snapshot scans in PostgreSQL, SQLite, MongoDB, and store adapters, with a stable
ascending `(group_id, user_id)` cursor and one row per membership, including owners. Use
the existing admin `initial_state=current` boundary, then catch up before readiness. A
restart may rescan from the beginning with stable identities, as existing bootstrap does.

Add an `AnalyticsMembershipRecord` and an admin full-event membership shape containing
`id`, `conversationGroupId`, `userId`, `accessLevel`, and `createdAt`. Keep the public
membership response unchanged; it deliberately lacks the internal group key. Admin REST
and gRPC streams emit the same full shape. Agent membership events remain their current
summary shape even under `detail=full`; document this exception rather than invent a
single conversation ID for a group membership. Revocations keep their summary identifiers
and normalize directly into tombstones when the source row no longer exists.

Membership export is group-scoped and the fixed exporter subscribes without conversation
filters. For new admin full-event or current-state requests that explicitly select
`kinds=membership`, reject a nonempty gRPC `conversation_ids` filter with `InvalidArgument`;
there is no single conversation ID to match. Preserve existing summary-stream behavior.
When kinds are omitted and a conversation filter is present, exclude memberships from
the snapshot, replay, and live phases consistently with the existing conversation-scoped
filter. Do not resolve conversation IDs into groups or add a lookup/cache in this phase.
Document the rejection and exclusion rules in the event contracts and test all phases.

Extend the existing capabilities response with
`features.analytics_membership_snapshots`, false when absent. Advertise it only when
bounded membership snapshots and required mutation events are supported by the active
store and durable event stream. An enabled membership exporter must check it before
bootstrap; an older source server must fail clearly rather than appear to export an empty
membership dataset. Disabled exporters do not require the capability. Update both API
contracts and generated clients without reusing reserved protobuf numbers or names.

Existing share/update/revoke routes emit membership events, but creation and ownership
transfer also change memberships. Wire initial owner creation, copied membership creation
for a new group, transfers, grants, updates, and revocations into the durable outbox in
the mutation transaction. Do not rely on an incidental conversation update or a live-only
publish. Transfers must deliver both changed roles. Snapshot-to-live tests cover mutations
before and during scans, duplicate delivery, crashes, revocation, and re-sharing.

A membership applies to its group and branches. Deleting a whole group must purge all
its membership rows; repeated per-conversation deletion notifications from that operation
must be idempotent. Represent the group fence in the existing `deletion_fences` table as
`(exporter_id, resource_type='conversation_group', resource_id=<canonical group UUID>)`,
with the same group in `conversation_group_id` and the deletion event's ingestion version.
Insert it before dependent cleanup or checkpoint advancement through an explicit batch
fence record, not a fabricated source resource.

Only the source's authoritative group-deletion flow, currently
`eventstream.ConversationDeletedEvents`, may trigger this fence. Its per-conversation
notifications carry the deleted group ID. A bare conversation ID, archival event, or
membership revocation cannot mean group deletion. Missing required group identity is
fatal whenever V2 group cleanup is active, including Phase 1 without membership export.
Duplicate notifications update the same fence.

The membership current view first selects the latest pair. It then requires the row to
be newer than any `resource_type='membership'` identity fence and excludes it if any
`conversation_group` fence exists for its group. Deleted group UUIDs are not reused, so
a greater membership ingestion version cannot override a group fence. This prevents an
in-flight old snapshot from exposing a deleted group again.

Both membership identity fences and group fences have no automatic TTL and are excluded
from normal pair, per-resource, and group-purge fence deletion. Keep the maximum revoke
version per `(exporter_id, resource_type='membership', resource_id)` and one marker per
deleted group for the dataset's lifetime. A later valid grant can exceed the pair fence;
no member write can override a group fence. Physical tombstone-row cleanup does not remove
these read fences.

This retains revocation identity metadata independently of the deleted membership payload.
Expose fence cardinality and storage growth in operations guidance. Fences may be removed
only as part of explicit dataset retirement, after dependent cleanup and disposal of all
writers and resumable checkpoints. Lease cancellation alone is not a database barrier
against an INSERT already accepted by ClickHouse. A future fence-reclamation feature must
prove stale-write exclusion before weakening this retention rule. See ClickHouse's
[delete-marker retention example](https://clickhouse.com/docs/reference/engines/table-engines/mergetree-family/replacingmergetree#is_deleted).

Preserve managed cleanup subscriptions when membership output is later disabled, and
retain minimal catalog rows needed to discover old memberships.
Do not claim a new user-erasure API: existing source revocations and group-deletion flows
must produce the events required to clean analytics. Retention uses existing generic-active
and tombstone durations, `GenericPayloadRetention` and `TombstoneRetention`, on
`observed_at`, with the same duration constraints and fence requirements as other
current-state exports.

Persist membership enablement and pending replay in checkpoint outputs. Enabling after
running without it requires a new replay ID, allowed without projection files. Disabling
stops new active rows but not cleanup; re-enabling requires another baseline. Validate
all restart and disable transitions before reporting readiness. Preserve existing purge
mode semantics: managed mode executes queued cleanup; record-only mode persists work
and fences for the operator; external mode delegates cleanup and cannot promise managed
deletion coverage. No layout option changes those policies. Managed cleanup subscriptions
must remain active after disabling new membership output. Test membership and group-fence
handling separately for each purge mode instead of silently treating every mode as managed.
Record-only and external cleanup consumers must honor the persisted cutoff for every V2
purge and reject a request without it. They must also preserve identity and group fences
independently of payload cleanup. Upgrade those consumers before V2 activation, including
Phase 1. The processor cannot verify arbitrary external cleanup code; delegated
modes do not acquire managed-deletion guarantees from the new configuration checks.

Join memberships to conversations on exporter and group IDs. Indexes or Rego are not
needed for this first fixed schema. This enables membership reporting and user-scoped
analytics; it cannot be the sole authorization decision for private data because
revocation is eventually exported. The BFF continues to validate live access through
Memory Service. The existing run query needs no membership table if its input conversation
IDs are already authorized.

## Testing

Required regression coverage includes:

- Original manifests retain digests and schemas. No conversation YAML, including configs
  with entry/memory YAML, preserves current built-in writes and defaults.
- Explicit JSON columns store only Rego output in their table. Custom conversation capture
  creates no generic payload duplicate. Failed/disabled projectors do not broaden capture.
- Capture-mode and membership enable/disable transitions survive crashes before/after
  snapshot completion and checkpoint saves. Missing replay IDs fail before writes.
- Repeated single- and multi-row resource mutations in one would-be batch receive ordered
  versions. The last change wins before and after physical merges, including revoke/re-share.
- Registry upgrade, concurrent registration, configured-out tables, unfamiliar layouts,
  physical drift, and partial DDL fail or recover without losing cleanup coverage.
- Feature activation completes compatibility promotion before new DDL. Real pre-enhancement
  binaries must fail in both schema modes before writing data, replacing views, or purging
  new resource types. An interrupted promotion resumes without resetting application tables.
- Every invalid preset, column type/codec combination, index bound, and unknown/duplicate
  YAML setting fails offline. Required-identity failures stop even under `continue-generic`.
- Conversation ordering handles replay, duplicates, missing IDs, shrinking/empty outputs,
  tombstones, group deletes, and the current generic `id` fallback hazard.
- Phase 1 conversation deletion retains its identity fence after payload purge. A delayed
  old conversation INSERT and an old entry under a deleted group stay hidden across
  restart, physical merges, TTL work, and configured-out tables. V2 activation rejects
  unfinished legacy purge work rather than running it without a cutoff.
- Latest-snapshot views never return an older row that matches a mutable indexed predicate.
  Query plans establish any promised pruning; unchanged plans are reported honestly.
- Membership defaults stay off; an old source capability fails when enabled. Both admin
  event transports, all stores, initial owners, forks/groups, transfers, role changes,
  revocation, group deletion, re-sharing, and disabled-output cleanup agree. Existing
  projections with the new built-in table name work while membership export is off;
  enabling it rejects the collision without changing their data.
- A revoke followed by re-share survives delayed, retried, and repeated cutoff-bound
  cleanup. After a revoke and completed purge, a delayed pre-revoke INSERT remains hidden
  through the retained pair fence. Repeat across exporter lease loss, restart, physical
  merges, TTL work, and subsequent valid re-shares. A group fence hides all group memberships
  until dataset retirement, survives
  ordinary purge/TTL work, and cannot be overridden by a later in-flight snapshot.
- Explicit membership selection plus conversation filtering fails for new admin full
  exports. Omitted kinds with conversation filtering excludes memberships consistently
  during snapshots, replay, and live delivery. Unfiltered membership export remains complete.

Example executable documentation scenarios:

```gherkin
Scenario: Conversation capture remains explicit
  Given an exporter using the built-in conversation fallback
  When it switches to a conversation projection with a new replay ID
  Then only its declared application fields are written after the transition
  And its catalog records still support hard-delete cleanup

Scenario: Invalid conversation identity cannot be mistaken for an entry ID
  Given a conversation-ordered entry projection
  When a deletion event contains an entry ID but no conversation ID
  Then processing fails without advancing the checkpoint

Scenario: Membership ownership changes remain current
  Given fixed membership export is enabled and its snapshot is complete
  When ownership transfers between two users
  Then the current membership table reflects both changed roles
  And a later group deletion removes both memberships
```

Compare UI queries on forks, started children, mixed journal versions, absent journal
origins, updates, and deletes. Scope every validation query to its intended exporter ID
and deduplicate group membership joins so they do not multiply run counts. Test non-ASCII
identities, malformed timestamps, source-size limits, cancellation, index materialization,
and privacy-safe errors. No new product UI is required; CLI errors and recovery commands
are the operator interface.

## Tasks

- [ ] Add conversation routing, the closed Rego record context, and explicit capture rules.
- [ ] Add checkpointed capture transitions and the documented two-exporter migration.
- [ ] Implement common V2 fence retention and cutoff-bound payload cleanup in Phase 1.
- [ ] Extend repeated-resource snapshot flushing to single-row exports and membership changes.
- [ ] Implement bounded layout options, identity checks, registry descriptors, and inspection.
- [ ] Complete and test V2 compatibility promotion before activating new feature schemas.
- [ ] Cover fallback compatibility, replay, lifecycle, races, partial failures, and privacy.
- [ ] Benchmark canonical-view queries and storage before recommending layout defaults.
- [ ] Add opt-in fixed membership export, source capability checks, and additive schema setup.
- [ ] Add membership snapshots and transactional events for every source mutation path.
- [ ] Implement membership fences, purges, retention, and disabled-output recovery.
- [ ] Persist revocation cutoffs and define group-fence lifetime and filtered-stream behavior.
- [ ] Retain pair revoke fences independently of payload purges and test delayed-old-writer recovery.
- [ ] Regenerate affected contracts/clients and add REST/gRPC/store parity coverage.
- [ ] Port Prince projections and UI queries in follow-up changes, retaining rollback data.

## Files to modify

| Files | Changes |
| --- | --- |
| `internal/cmd/process/clickhouse/projection.go`, `projections_command.go` | Conversation routing, closed context validation, layout options, compatible digests, and inspection. |
| `internal/cmd/process/clickhouse/processor.go`, `config.go`, `service.go`, `resource_record.go` | Capture transitions, context normalization, replay, membership flag, and source capability checks. |
| `internal/cmd/process/clickhouse/projection_schema.go`, `schema.go`, `sink.go`, `types.go`, `identity.go` | Compatibility promotion, registry descriptors, membership schema, physical validation, typed writes, cutoff-bearing purge requests, group fences, and cleanup. |
| `contracts/protobuf/memory/v1/memory_service.proto`, `contracts/openapi/openapi.yml`, `contracts/openapi/openapi-admin.yml` | Membership admin snapshots/full events and capability contracts. |
| `internal/registry/store/event_snapshot.go`, store snapshot implementations, metrics adapter | Bounded membership scans with a stable composite cursor. |
| `internal/service/eventstream/`, `internal/service/capabilities/`, admin/agent event routes, `internal/grpc/server.go` | Full admin membership shape, capabilities, snapshot delivery, and transport parity. |
| Conversation, membership, and transfer mutation paths in all stores and route adapters | Transactional membership events, including initial/copy creation and transfers. |
| ClickHouse unit/integration tests, event-stream tests, store tests, and `internal/bdd/` | Compatibility, lifecycle, transition, concurrency, and transport regression coverage. |
| `internal/sitebdd/steps_clickhouse.go`, analytics and event site docs | Executable examples, layout restrictions, migration, fixed memberships, and operator recovery. |
| Generated clients and affected hand-written adapters | Regenerate contracts and preserve changed event/capability options across transports. |

The membership phase follows `api-contract-change`. Agent membership response schemas stay
unchanged; document the admin-only full shape and agent summary behavior explicitly.
If lineage tests expose missing inherited-conversation updates, include source event fixes
and store regression tests before Phase 1 is complete.

## Verification

These are implementation gates, not runtime checks already run for this proposal:

```bash
go build ./...
go vet ./internal/cmd/process/...
# Starts ClickHouse with testcontainers; requires Docker.
go test ./internal/cmd/process/... ./internal/testutil/testclickhouse -count=1
(cd site && npm run build)
GODOG_TAGS='@analytics' CGO_ENABLED=1 go test -race -tags='site_tests sqlite_fts5' -timeout 20m \
    ./internal/sitebdd/ -run TestSiteDocs -count=1
```

For membership source/contracts, also run:

```bash
task generate
go build ./...
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' \
    ./internal/bdd -run '^TestFeaturesSQLite$' -count=1
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' \
    ./internal/bdd -run '^TestFeaturesAuthFixtures(Postgres|Mongo)$' -count=1
./java/mvnw -f java/pom.xml compile
```

Use the project's devcontainer workflow where required. Record read rows/bytes, latency,
memory, storage, and ingestion overhead against the same representative retained data.
Run additional store verifiers for the source mutation paths touched. Performance results
must retain canonical deduplication and deletion handling.
