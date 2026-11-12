---
status: implemented
---

# Enhancement 118: Conversation projections, membership export, and ClickHouse layouts

> **Status**: Implemented. Breaking change to the analytics exporter only.

## Summary

Replace the ClickHouse exporter's compatibility paths with one current export format and
one projection pipeline for conversations, entries, and memories. Add small layout options
and opt-in membership export. Existing analytics installations rebuild into a fresh
ClickHouse database with a fresh checkpoint; the operational datastore is unchanged.

## Motivation

Analytics consumers need to join conversation relationships with entry and memory data.
For example, a workflow dashboard may group activity by the conversation that started it,
compare forked conversations, or report on shared conversation groups. Full admin snapshots
already contain conversation lineage, but configurable projections currently accept only
entries and memories. Consumers must otherwise extract relationships from generic JSON
or reconstruct them from application events.

Previously, the processor duplicated generic payloads in `resources` and per-resource tables,
used separate live/backfill conversion paths, wrote lineage separately, and discovered purge
subjects through a resource catalog. The analytics-only compatibility break removes those
paths and their old-layout/checkpoint branches.

This replaces the export-format assumptions of
[Enhancement 404](../superseded/404-clickhouse-analytics-export.md) while retaining the useful
chain behavior of [Enhancement 117](117-clickhouse-projection-chains.md).
SQLite publishes committed outbox rows through an ordered relay outside the writer lock and retries failed publications.
The implementation includes the common pipeline, conversation projections, layout options,
and membership export for group-access reporting. Queries using conversation IDs
that have already passed a live access check do not require membership analytics.

## Scope and complexity decisions

| Area | New design and reason |
| --- | --- |
| Compatibility | Support one current schema, checkpoint shape, and manifest definition. No format-version dispatch, old readers, promotion state, registry inference, or old-digest preservation. |
| Rego | Every projection uses `input.record`, with `input.resource` identifying the kind. Remove flat aliases and implicit memory-attribute mapping. |
| Capture | All application tables use projections. Ship a metadata-only default set through the same engine; explicit projection paths replace that set. No generic payload duplication or legacy/custom capture switch. |
| Resource catalog | Remove `resources` and `purge_subjects`. Persist complete typed purge scope in the queue and filter tables directly by their processor-owned identities. |
| Purge progress | Keep only pending/complete. The intermediate ready state existed to separate payload cleanup from deleting saved subject lists, which no longer exist. |
| Snapshot ordering | Allocate a version per observation, not per batch. Remove repeated-resource scans and special early flushes. |
| Normalization | Convert current full resources or deletion summaries once into a typed observation, shared by bootstrap and live/replayed events. |
| Deletion markers | Write explicit fence rows in the sink. Remove the `deletion_fence_writer` materialized view and its duplicate write path. |
| Lineage | Project the source fork/started-by fields. Remove the dedicated lineage writer and physical `conversation_lineage` table. |
| Layout | Keep two sorting presets and one string dictionary-encoding option. Defer skip indexes and codec chains pending useful measurements. |
| Membership | Keep one fixed group/user table, complete snapshots/events, and a capability check. Membership is a known relationship, not another Rego resource. |
| Reliability controls | Keep leases, frozen retries, safe checkpoints, bounded batches, retention ownership, and purge modes. These protect current operation, not old formats. |
| Exceptional late writes | Add a real exporter-takeover test. Do not add another cleanup service without a demonstrated need. |

Arbitrary SQL/engines, new authorization rules, core datastore resets, automatic schema
adoption, and automatic deletion of an existing analytics database are outside scope.

## Design

### Clean analytics baseline and cutover

Initialize the current schema directly in an empty dedicated analytics database. Put
required registry layout fields, typed failure identities, fixed membership relations,
and complete purge request fields in that baseline. There is no numbered-format migration
or old checksum preservation. The `schema_versions` record for component `clickhouse-export`
contains only its current schema checksum and application time. Remove its numeric
`schema_version` field and the redundant per-row schema-version columns; only one layout
is supported. Keep `ingest_version`, which orders observations rather than schema formats.

Claim an empty database with that schema fingerprint before creating other owned objects.
Manage-mode retries may finish matching partial infrastructure initialization; projection
tables still require registry ownership as described below. Validate mode checks the
complete current schema without DDL. Reject an older, unknown, mismatched, or unowned
nonempty database before writing application data or recreating views. Do not upgrade,
adopt, drop, or reset it automatically. Validate the registry shape/checksum before other
DDL. Old binaries must reject the changed registry shape before recreating views; they
expect the removed numeric field. Test this rejection without adding an old-format reader.

Use the stable checkpoint content type
`application/vnd.memory-service.clickhouse-checkpoint+json`, with no version parameter or
body version field. Validate the one current checkpoint structure, including required
fields and rejection of unknown fields. Do not infer missing fields or read old shapes.
Startup validates a stored checkpoint before opening the ClickHouse sink, so a rejected
checkpoint never claims an empty database. A different content type means the client ID
belongs to another or previous exporter; the error asks for a new client ID and database.
Keep the existing manifest `apiVersion: memory-service/v1alpha1` label; introduce no new
manifest version. Manifests must use the current fields and record input. Rewrite bundled
expressions, regenerate digests, and update examples/queries. Source content-type and
memory-kind version strings are unaffected.

The operator cutover is:

1. Let the old exporter/cleanup owner finish pending deletion work, then stop the exporter.
   Keep its analytics database/checkpoint separate; never point it at the new database.
2. Prepare the new projection configuration, a fresh analytics database, and fresh exporter
   and checkpoint client identities. Grant the exporter's ClickHouse credentials access to
   that database. Update readers for typed projection columns and the new table definitions.
3. Bootstrap retained current source state, catch up through the durable outbox, verify the
   new tables, and switch readers. Retire the old analytics dataset separately when it is
   no longer needed, with its existing retention/deletion responsibilities honored.

A rebuild cannot recover source records or event history that no longer exists. Preserve
any required old analytics history separately; this enhancement provides no historical
row conversion or union-view compatibility layer. The primary PostgreSQL, SQLite, or
MongoDB data, attachments, source outbox, and unrelated processor checkpoints are not reset.
No dataset is erased merely by upgrading a binary. Rollback is a coordinated return to the
old exporter, database, checkpoint, and readers while its replay history remains usable.
Do not point an old exporter at the new schema.

### One projection capture model

All user-facing conversation, entry, and memory tables are ordinary projections registered
in `projection_registry`. They receive only their declared application columns plus the
common processor-owned identity, version, and deletion columns. Do not create the old
payload-bearing `resources` table or parallel generic resource tables.

With no `--projection-path`, load a packaged metadata-only projection set:

| Table | Declared metadata, in addition to common identity columns |
| --- | --- |
| `conversations` | Exactly `created_at`, `updated_at`, and `archived`. |
| `entries` | User/client/agent, channel, content type, creation/indexing times, epoch, and sequence. No content or indexed content. |
| `memories` | Namespace/key, kind, revision, creation/expiration times, and archival state. No value or attributes. |

These are normal manifests evaluated by the normal engine, not separate sink writers.
The default conversation projection persists no owner, client, agent, or lineage fields.
Those fields remain available in `input.record`. The opt-in
`deploy/clickhouse/projections/conversation-details.yaml` manifest writes them, together
with lifecycle fields, to the separate `conversation_details` table. Its distinct name
allows loading it alongside the default metadata manifests.
An explicit projection path replaces the entire packaged set. There is no implicit
per-resource fallback: include the supplied metadata manifests explicitly if those tables
are also wanted. Failing, disabling, or removing a projection never starts another capture
path. Removed tables remain registered for view recreation, retention, and deletion cleanup.

Projection table names such as `conversations`, `entries`, and `memories` are no longer
reserved built-in table slots. Ordinary immutable registry ownership applies to them.
Infrastructure names, fixed membership relations, and generated `_all`/`_current` suffixes
remain reserved. Reserve membership names from the start; there are no historical custom
projection ownership exceptions in the fresh analytics database.

Metadata payload mode limits projection input to the declared metadata fields. Explicit
metadata-only projections can also use this mode. Projected mode permits entry content and memory values/
attributes for explicit extraction. Full mode additionally permits conversation title and
entry indexed content, and still requires the decrypted-content acknowledgment. Neither
mode automatically writes a generic JSON copy. The default set remains metadata-only in
all modes; a full snapshot requires an explicit JSON column and Rego.

Remove the implicit memory `columns <- attributes` evaluator path: every chain root requires
`projectionRego`. Rename failure policy `continue-generic` to `record`, keeping `stop` as
the other choice for Rego and output validation errors. Missing source snapshots are recorded under both policies because deletion may already have removed the source during catch-up. `record` writes a payload-free failure row and continues; it never adds
an alternative payload capture. Do not keep the old policy name as an alias.

Direct lineage is available through opt-in projection columns, including the conversation
details example. Consumers join those columns or declare an ordinary lineage projection
when needed. Remove the special lineage rows, composite
lineage IDs, dedicated physical table, and `conversations:lineage` disable control. Disabling
projection output uses the existing projection control; there is no second lineage pipeline.

### Uniform projection routing and Rego input

Roots for `entry`, `memory`, and `conversation` may omit `selector` to receive every
observation of that resource. An entry or memory root may instead choose the existing
exact content-type/kind selector behavior. Conversation roots are unconditional.
An unconditional chain has one route: later manifests can add nullable columns or replace
its logic but cannot introduce selectors. Selected entry/memory chains keep their current
linear chain and Rego-reuse rules. Separate chains may project the same resources.

```yaml
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata:
  name: workflow_conversations
spec:
  resource: conversation
  table: workflow_conversations
  rows: one
  columns:
    started_by_conversation_id: {type: string, nullable: true}
  projectionRego: |
    package memoryservice.analytics
    output := {
      "started_by_conversation_id": object.get(input.record, "startedByConversationId", null),
    }
```

Every expression receives only `input.resource` and the closed `input.record` below.
Remove `input.content`, `input.attributes`, `input.value`, and other duplicated flat data
inputs. Update bundled Rego and tests accordingly. Fixed memberships have no Rego.

| Resource | Record fields |
| --- | --- |
| Conversation | `id`, `conversationGroupId`, `ownerUserId`, `clientId`, `agentId`, `createdAt`, `updatedAt`, `archived`, filtered `metadata`, `forkedAtConversationId`, `forkedAtEntryId`, `startedByConversationId`, `startedByEntryId`; `title` only in full mode. |
| Entry | `id`, `conversationId`, `conversationGroupId`, `userId`, `clientId`, `agentId`, `channel`, `contentType`, `createdAt`, `epoch`, `seq`, `indexedAt`; `content` in projected/full mode and `indexedContent` only in full mode. |
| Memory | `id`, `namespace`, `key`, `kind`, `revision`, `createdAt`, `expiresAt`, `archived`; `value` and `attributes` in projected/full mode. |

Use a resource-aware helper, not automatic protobuf serialization. Optional fields are
absent when unavailable. Conversation metadata uses the operator allowlist, empty by
default. Future fields must not become exportable implicitly. Exclude `logical_id_source`,
cursors, credentials, and batch metadata. Keep existing value/row/resource-size limits,
Rego cancellation, and nondeterministic-builtin rejection. Rego performs no database or
related-resource lookups.

Source timestamps are valid UTC RFC 3339 strings with source precision. Do not substitute
processing time or the Unix epoch for missing/malformed required values. Parse integer
source fields without a `float64` round trip and reject fractional/out-of-range values.
Expose archival state as a boolean, not an internally synthesized timestamp. Channel values
keep the normalized enum spelling. Started-by and fork relationships remain distinct.

Explicit JSON capture uses the same field policy:

```yaml
columns:
  payload_json: {type: json_string, nullable: false}
projectionRego: |
  package memoryservice.analytics
  output := {"payload_json": input.record}
```

`json_string` serializes the object; the existing `json` type requests native JSON. A JSON
column creates no other generic copy and grants no access beyond the record mapping.

### One observation pipeline and snapshot versions

Convert a full admin resource or the current documented deletion-summary shape into one
typed observation: kind, resource ID, conversation/group identity where applicable,
lifecycle action/change, source time/cursor, and optional normalized snapshot. Bootstrap
and live/replayed events feed that same path. Remove duplicated backfill/live payload
formatters, generic payload scrubbing, and guessed field/case aliases. Full resources and
source deletion summaries remain distinct supported shapes; they are not compatibility shims.

A summary without an available snapshot does not become a fabricated full record. Emit
only its valid lifecycle information, required deletion work, and projection-failure
information under the configured policy. Do not project summary JSON or invent source
creation times. All group-dependent outputs require authoritative group identity. Entry
identity must never fall back to treating its own ID as a conversation ID.

For each new source event or bootstrap snapshot, allocate
`ingest_version = lease_generation << 32 | observation_sequence`, not a batch-wide version.
All rows, row-count markers, failures,
fences, and purge cutoffs derived from one observation share its version. The next
observation gets a greater version even for another update to the same resource in the
same batch. A replayed deletion with an existing purge request reuses that request's
original deletion version, as specified below. Keep the existing lease-generation bounds
and stop before sequence wrap; a restart acquires a new generation. Remove `Batch.Version`, `BatchSequence`,
`hasPendingMultiRowSnapshot`, and repeated-resource early flushing. Before bootstrap, read the
exporter's highest stored version and refuse to write unless the current lease generation is
greater. A lost or reset checkpoint restarts at generation 1, and its rows would otherwise
lose to older rows in the current views without any error.

Batches are transport/retry units only: bounded by events, rows, bytes, and delay. Freeze
their rows, per-observation versions, batch ID, and deduplication tokens across retries.
Count fences and purges even when there are no projection rows. The safe cursor advances
only after all writes and required managed purges succeed. Replaying after a restart may
write newer observations; latest-snapshot views still produce one logical result.

### One current checkpoint and replay protocol

Checkpoints store the active projection digest/output settings, completed replay
ID, safe cursor, and bootstrap state. The observation sequence is process-local and never serialized, so buffered events do not trigger checkpoint writes. Require all current fields;
there is no legacy/custom capture field or inference for an older checkpoint.

For a replay, attach one pending target to the existing `scanning`, `catchup`, and `complete`
states: target projection/output settings and requested replay ID. Persist it before data
writes. Commit the target and completed replay ID only after scanning and durable catch-up
finish. Restart with the same target resumes it; incompatible changes during a pending
replay fail. Ordinary restarts resume the safe cursor.

A fresh checkpoint always bootstraps current state. A new projection, replacement logic,
or newly enabled/re-enabled output needs a new `--projection-replay-id` to fill retained
records. Disabling output stops new rows without a scan while cleanup remains active.
Permit replay for conversation-only sets and membership enablement without user projection
files. There is no special legacy restoration scan; returning to the packaged default set
is an ordinary projection-set change. Removing a projection creates no replacement capture.

### Small physical-layout options and registry

Expose only these sorting presets in the chain root's `spec.clickhouse.ordering`:

| Preset | Resources | `rows: one` | `rows: many` |
| --- | --- | --- | --- |
| `resource`, default | Entry, memory, conversation | `exporter_id, resource_id` | `exporter_id, resource_id, row_index` |
| `conversation` | Entry, conversation | `exporter_id, conversation_id, resource_id` | `exporter_id, conversation_id, resource_id, row_index` |

The processor fixes engine, partitioning, and replacement version. Mutable application
fields cannot enter the key because [ReplacingMergeTree replaces rows by sorting key](https://clickhouse.com/docs/engines/table-engines/mergetree-family/replacingmergetree).
Snapshots, empty-output markers, and tombstones use the same immutable conversation ID.
Missing identity is fatal under either failure policy, without checkpoint advancement.

For conversation ordering, include conversation ID in latest-resource and row window
partitions so that identity predicates can push below those windows. Mutable predicates
stay above latest-snapshot selection. Preserve empty/shrinking-output markers, surplus-row
handling, delete fences, and exporter isolation. Measure pruning with `EXPLAIN indexes = 1`
before recommending the optional preset.

Allow only `clickhouse.lowCardinality: true` on string columns, including `addColumns`,
rendering `LowCardinality(Nullable(String))`. Logical `nullable: false` still validates
active Rego output; physical nullability permits tombstones and empty-output markers.
Keep Go/Rego values unchanged. Dictionary encoding needs representative measurements;
[its benefit depends on data diversity](https://clickhouse.com/docs/sql-reference/data-types/lowcardinality).
Ordering belongs to the root, encoding to the declaring manifest. Changes to an existing
table's ordering or encoding require a new table and replay. Manifests
remain immutable, but historical manifest digests need not survive the clean break.

Require explicit `row_mode` and `ordering` in every registry entry, agreeing across all
manifests/exporters for a table. Selectors record the resource and either the exact selector
or unconditional route. Reject missing metadata; never infer it from an old sort key.
Manifests/digests define configured columns; `system.columns` and `system.tables` validate
physical types, encoding, engine/version argument, partitioning, and keys. Reuse current
chain-prefix and concurrent-registration checks. Do not store full schema JSON or SQL.

Configured-out tables need their registry ownership/layout and common identity columns to
recreate views and perform cleanup. Application columns stay in ClickHouse; no reconstructed
old schema is needed. Manage mode creates owned objects and supports current-format nullable
column additions. Validate mode changes no DDL. Neither mode silently rewrites mismatches
or adopts unregistered relations. An orphan left by interrupted initial CREATE needs
explicit operator recovery, with no automatic drop of data.

Validate manifests before connecting. Reject unknown/duplicate keys, unsupported definitions,
reserved names, invalid chains, and unsupported layouts with file/field errors. Inspection
shows effective projections, columns, ordering, encoding, and proposed DDL. Existing health
and replay reporting expose progress. Use bounded error/metric labels; never log payloads,
credentials, resource identities, or raw driver SQL.

Defer skip indexes and configurable codec chains. The motivating mutable parent-ID filter
must follow latest-snapshot selection, so a [skip index](https://clickhouse.com/docs/optimize/skipping-indexes)
may not prune the source scan. Add tuning options only after a useful plan or storage/CPU
measurement. Keep current leases, retention ownership, supported protocols, and retry limits.

### Direct deletion scopes and one fence writer

Remove the resource catalog and purge-subject discovery/persistence. Every projection row
already has processor-owned resource, conversation, and group IDs, and its registry entry
owns a resource kind. Lifecycle and failure rows must also include explicit resource kind/ID
and applicable group identity. Those identities must be present even when Rego emits no
application rows. Complete purge scope comes from the source event and queue, not from
payload data or an auxiliary catalog.

There are two purge scopes, always isolated by exporter:

| Scope | Table predicate |
| --- | --- |
| Resource | Exact resource kind and ID. Membership revokes also require `ingest_version <= cutoff_ingest_version` to preserve newer grants. Retired resource IDs remove all versions. Registry ownership supplies kind for projection tables. |
| Conversation group | Exact canonical group UUID on every group-dependent table, without a version cutoff, so higher-version replayed observations are also removed. |

Persist source event/purge identity, resource kind/ID, conversation/group identity, scope,
and a non-null `cutoff_ingest_version UInt64` in `purge_queue` and every status update.
Require group scope to come from authoritative group deletion, currently
`eventstream.ConversationDeletedEvents`; its per-conversation notifications are idempotent.
A bare conversation ID, archive, or membership revoke cannot imply deletion of a whole group.

Workers can rerun the same direct predicates after any partial cleanup, using only the
queue and registered table list. Do not enumerate dependent resource IDs or persist
`purge_subjects`. Add `resource_kind` to projection failures and use a consistent
`ingest_version` column there, removing the special untyped-ID and `version` cleanup path.
Never delete another kind's data merely because its text ID matches.

Use only `pending` and `complete` purge states. Remove the intermediate `ready` state and
its subject-list cleanup phase. Mark complete only after every required mutation succeeds;
a failed attempt can repeat the direct predicates. Retrying a pending write cannot replace
an already persisted complete status.

Reusing a purge ID reuses its persisted scope/cutoff rather than the retry's newly allocated
version; conflicting scope is fatal. Resolve that original deletion version before freezing
replayed deletion rows/fences, so replaying an old revoke cannot advance its fence past a
newer grant. Cutoff-bound membership cleanup preserves newer grants even if an external
worker executes an old request after they were exported. No null historical-cutoff branch
or old queue status reader is needed.

Use explicit batch fence rows and one sink insertion path. Remove the resource-table
materialized view and its separate explicit-resource fence writer. Write fences before
cleanup/checkpoint advancement, including for disabled outputs and configured-out tables:

- Identity fences retain the maximum version per `(exporter_id, resource_kind, resource_id)`.
  Current rows must exceed that version; a later valid incarnation can do so.
- Group fences use kind `conversation_group` and the canonical UUID as resource ID.
  Group UUIDs are never reused, so a group fence hides every dependent row at any version.

With membership export disabled, a revoke writes no identity fence because no membership
rows are written to hide. It queues a purge only when an earlier export left rows for that
identity. Membership IDs encode the user ID, so this keeps removed users out of fences and
the purge queue. Enabling export later requires a replay whose current-state scan omits
revoked members.

Current views select the latest observation before applying deletion rules. Archive state
is not deletion. A group purge directly covers entry/conversation projections, memberships,
lifecycle rows, and failures. Ungrouped memories have no group match. Test missing group
identity at every source path; it is fatal rather than a reason to skip dependent cleanup.

Keep identity/group fences independently of payload/tombstone TTL and normal purge. Remove
them only with explicit dataset retirement after writers and resumable checkpoints are
retired. They retain identity metadata, not application content; measure cardinality/storage.
No automatic fence reclamation or extra cleanup service is introduced.

Managed mode persists work, executes it synchronously, and checkpoints only on success.
Record-only persists work/fences for an external worker. External mode creates no queue
request and promises no managed deletion; its owner supplies a safe cleanup procedure and
preserves fences. Disabling output never loses the subscriptions required by its purge mode.

All application tables, including default metadata projections and fixed memberships, use
`ProjectionRetention` for active rows and `TombstoneRetention` for deleted rows on
`observed_at`. Retain lifecycle/failure retention controls. Remove `GenericPayloadRetention`
and the separate generic-resource TTL path. When tombstone retention is positive, require
positive active-row retention no longer than tombstone retention, including when only
packaged defaults or fixed memberships are enabled. Keep fences outside both TTLs.

A purge marked complete acknowledges its submitted mutations. Fences enforce current-view
exclusion, not physical deletion of arbitrary future INSERTs. Validate outstanding requests
across exporter takeover in tests; do not assume normal ordered replay reverses event order.

### Fixed membership export

Add `--export-memberships` and `MEMORY_SERVICE_CLICKHOUSE_EXPORT_MEMBERSHIPS`, default false.
It works in metadata mode without user projections. Reject `resource: membership` YAML.
Use the common output replay protocol when enabling/re-enabling it after initial bootstrap.

Create `conversation_memberships` and `conversation_memberships_current` in the current
baseline, even when export is off; empty tables need no separate schema component, lazy
ownership protocol, or name-collision compatibility exception. Output remains opt-in.

The fixed table has the common exporter/batch/event/cursor/ingestion-version/observation
columns plus `resource_id`, `conversation_group_id`, `user_id`,
`access_level LowCardinality(String)`, `created_at Nullable(DateTime64(9, 'UTC'))`, and
`is_deleted`. Use `ReplacingMergeTree(ingest_version)` ordered by
`(exporter_id, conversation_group_id, user_id)`. It has no conversation ID, raw JSON,
Rego, row index, or configurable layout.

Identity is the group/user pair, never the role. One helper defines `resource_id` as
unpadded base64url of the group's 16 UUID bytes followed by the exact UTF-8 user ID.
Use it in snapshots, events, fences, and purges; it is not anonymization. Active snapshots
require source creation time; revocations do not. Use the source access-level vocabulary.

The view selects the latest pair and applies common identity/group fences. Revocation
queues exact pair cleanup of the membership table, lifecycle, and failure data where
applicable; memberships produce no Rego failures. A re-share gets a newer observation
version. Group deletion filters the table directly by group ID. No catalog rows or separate
membership cleanup algorithm are needed.

Add bounded membership scans to PostgreSQL, SQLite, MongoDB, and store adapters, ascending
by stable `(group_id, user_id)` cursor, including owners. Use admin `initial_state=current`
and durable catch-up. A restarted scan may start from the beginning with stable identities.

Add `AnalyticsMembershipRecord` and an admin full resource containing `id`,
`conversationGroupId`, `userId`, `accessLevel`, and `createdAt`, identical across REST/gRPC.
Public membership responses and agent membership event shapes stay unchanged. A group
membership has no single conversation ID. Revocations retain summary identities and
normalize into tombstones when the row no longer exists.

Emit durable events in the mutation transaction for owner creation, copied memberships in
new groups, grants, updates, revokes, and both role changes in an ownership transfer. A
snapshot alone or an incidental conversation event cannot supply those changes reliably.
Ownership transfers also emit conversation updates for the owner fields they change. SQLite
publishes committed outbox rows in append order through a relay outside its process-local
writer lock. Routes suppress duplicate publication. Publication hooks are discarded on rollback.
PostgreSQL and MongoDB retain their existing durable relays.

Advertise `features.analytics_membership_snapshots` only when the active store and durable
stream support the baseline and all required mutations; absent means false. Enabled
exporters check before bootstrap. This is source capability detection, not an analytics
historical-format reader; it prevents silently incomplete exports against unsupported sources.

The exporter subscribes without conversation filters. Admin full/current requests selecting
`kinds=membership` with nonempty gRPC `conversation_ids` fail with `InvalidArgument`.
Omitted kinds with conversation filtering exclude memberships in snapshot/replay/live
phases. Preserve summary-stream behavior; introduce no conversation-to-group lookup/cache.
Update contracts/generated clients under `api-contract-change`, respecting reserved numbers.

Join memberships by exporter/group and avoid multiplying run counts. Analytics cannot
be the sole authorization decision because revocation is eventually exported; the BFF
continues to check live Memory Service access. No new authorization or user-erasure API
is introduced.

## Testing

Required acceptance coverage uses existing unit, ClickHouse, store, and event-stream runners:

- Clean break: unsupported schema/checkpoint shapes and manifest definitions fail clearly
  without changing their data. The fresh baseline initializes directly; matching partial initialization resumes.
  Real pre-enhancement binaries cannot use the new database in either schema mode.
- Defaults/capture: packaged metadata projections work without user paths in local/production
  examples. Explicit paths replace them entirely. No generic payload, catalog, special lineage
  table, or hidden capture fallback is created. Default tables and explicit JSON obey field policy.
  The default conversation manifest declares only three lifecycle columns and evaluates without
  owner/client fields. Supplying reporting fields does not export them. The optional conversation
  details example compiles alongside the defaults and exports the reporting fields from `input.record`.
- Rego: bundled manifests use only record input. Selected/unconditional chains, one/many rows,
  nullable additions, replacement/reuse, undefined output, and record/stop policies agree.
  Test malformed timestamps, numeric bounds/values above 2^53, Unicode identities, limits,
  cancellation, and privacy-safe errors.
- Ordering: many updates to the same resource share a transport batch but have distinct
  observation versions. Verify latest results before/after merges, empty/shrinking outputs,
  revoke/re-share in one batch, sequence overflow, restart, and frozen retry versions/tokens.
- Normalization: bootstrap and live/replayed full resources generate the same record and
  identities. Missing snapshots never become payloads; current deletion summaries still work.
- Replay/schema: pending target changes, disabled/re-enabled output, crashes during scan/catch-up,
  current-generation immutable digests, removed manifests, drift, concurrent registrations,
  and orphan relations fail/recover without losing cleanup coverage.
- Deletion: direct kind/ID/group predicates work with no resource catalog, including zero-row
  projections and configured-out tables. Interrupt cleanup between tables, restart using only
  the queue, and verify membership cutoffs, maximum fences, preservation of newer grants, and removal of group/resource rows replayed under a higher lease generation.
  Cross-kind matching IDs never cause cross-deletion. Cover all purge modes and TTL/merge behavior.
- Membership: all stores/transports and owner/grant/transfer/update/revoke/re-share/group-delete
  paths agree, including mutations during scans, unsupported source capability, and every
  snapshot/replay/live filtering phase. Disabled export leaves fixed tables empty, stores no
  fence or queue row for a revoke, and still purges rows left by prior enablement.
- Recovery: a stored checkpoint with another content type or invalid content fails before
  ClickHouse is opened, and bootstrap refuses a lease generation that is not above the stored rows.
  Insert deduplication settings are refused in validate mode, repaired in manage mode, and
  keep operator-tuned windows. The SQLite relay restarts after a recovered panic without
  dropping events.

Inject an old INSERT after purge to verify current-view fences. Separately test real
outstanding native/HTTP requests during lease loss and exporter takeover, followed by
ordered replay through deletion. Distinguish a lost acknowledgment from work still running
on the server; the [HTTP interface](https://clickhouse.com/docs/interfaces/http#using-clickhouse-sessions-in-the-http-protocol)
does not automatically stop requests on disconnect. Check raw tables and views without
claiming a fence physically erases a late write. If reproduced, document the physical
retention limit and assess a separate fix rather than adding speculative infrastructure.

```gherkin
Scenario: Explicit projections replace the default metadata set
  Given an exporter with only a custom conversation projection configured
  When it bootstraps current source state
  Then it creates only the configured application table
  And it writes no generic payload or resource catalog copy

Scenario: Several updates remain ordered in one batch
  Given two observations for the same conversation before a batch limit is reached
  When the batch is written and ClickHouse merges its parts
  Then the current view returns the later observation
  And both observations had distinct ingestion versions

Scenario: Purge recovery needs no discovery catalog
  Given a membership revoke with a persisted scope and cutoff
  And a newer grant for the same pair
  When cleanup restarts using only the purge queue and registered tables
  Then the newer grant remains current
  And the original revoke fence remains stored
```

Verify analytics joins against typed columns on forks, started child conversations,
missing optional lineage, updates, and deletes, scoped to one exporter. If lineage tests expose missing
source updates for inherited conversations, fix the producer paths with store tests.

## Tasks

- [x] Define the single current baseline/checkpoint shape and manifest contract; reject incompatible state.
- [x] Convert bundled metadata/domain projections to record input and update default loading.
- [x] Remove generic payload/catalog/lineage writers and implicit attribute mapping.
- [x] Share normalization and per-observation versions across bootstrap and live processing.
- [x] Add layout controls and required registry metadata without historical inference.
- [x] Implement direct persisted purge scopes, typed failure rows, and one explicit fence writer.
- [x] Simplify failure/retention options and retain existing durability/lease guarantees.
- [x] Verify current-format restart/replay, deletion, batching, privacy, and takeover behavior.
- [x] Add fixed membership rows, complete snapshots/outbox events, capability checks, and parity tests.
- [x] Regenerate contracts/clients and update deployment configs, examples, queries, and cutover docs.

## Files to modify

| Files | Changes |
| --- | --- |
| `internal/cmd/process/clickhouse/projection.go`, `projections_command.go` | Current manifests, record-only expressions, uniform routing, default projection set, layout validation/inspection. |
| `internal/cmd/process/clickhouse/processor.go`, `config.go`, `service.go`, `resource_record.go`, `types.go` | Typed observations, shared bootstrap/live conversion, snapshot versions, current checkpoint/replay, simplified failure/retention settings. |
| `internal/cmd/process/clickhouse/schema.go`, `projection_schema.go`, `sink.go`, `schema_validation.go`, `deduplication.go` | Current baseline, explicit registry metadata, no generic/catalog/subject-discovery tables, direct purges, one fence writer, fixed memberships. |
| `deploy/clickhouse/projections/`, packaged metadata projection assets, `compose.yaml`, Kustomize analytics configs | New manifest/input format, metadata defaults, renamed/removed settings, and fresh analytics identities. |
| `contracts/protobuf/memory/v1/memory_service.proto`, both OpenAPI contracts, generated Go/TypeScript/Python clients and Java contract generation | Membership admin snapshots/full events, capability/filtering contracts, and transport parity. |
| `internal/registry/store/event_snapshot.go`, stores/adapters, SQLite commit publication, `internal/service/eventstream/`, `internal/service/capabilities/`, mutation/event routes, `internal/grpc/server.go` | Bounded membership scans and complete transactional events; canonical full/summary identities. |
| ClickHouse/store/event tests, `internal/bdd/testdata/features-outbox/`, outbox runners, `internal/sitebdd/steps_clickhouse.go` | New baseline and pipeline acceptance coverage, source parity, and executable examples. |
| `site/src/pages/docs/analytics/` and event site docs | Record input, typed queries, defaults, new settings, membership behavior, cutover/history limitations, and recovery. |

## Verification

Implementation verification used the devcontainer workflow. The processor/testcontainers
race suites, membership REST/gRPC scenarios on all three stores, broad SQLite BDD suite,
site build and executable analytics queries, generated contracts, Go build/vet, both frontend
builds/lint, developer frontend tests, Python stub compilation, and full Java reactor compile
passed. PostgreSQL/MongoDB store tests and targeted transaction/rollback checks also ran;
the PostgreSQL deletion test now accounts for the newly durable owner/copy events.

```bash
task generate
go build ./...
go vet ./internal/cmd/process/... ./internal/service/eventstream ./internal/service/capabilities \
    ./internal/plugin/store/sqlite ./internal/plugin/store/postgres ./internal/plugin/store/mongo ./internal/grpc
go test -race ./internal/cmd/process/... ./internal/testutil/testclickhouse -count=1
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' \
    ./internal/bdd -run '^TestFeaturesSQLite$' -count=1
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' \
    ./internal/bdd -run '^TestFeatures(SQLite|Pg|Mongo)Outbox$/analytics-membership' -count=1
CGO_ENABLED=1 go test -race -tags='sqlite_fts5' \
    ./internal/plugin/store/sqlite ./internal/service/eventstream ./internal/service/capabilities -count=1
(cd site && npm run build)
GODOG_TAGS='@analytics' CGO_ENABLED=1 go test -race -tags='site_tests sqlite_fts5' -timeout 20m \
    ./internal/sitebdd/ -run TestSiteDocs -count=1
(cd frontends/chat-frontend && npm run lint && npm run build)
(cd frontends/developer && npm run lint && npm test && npm run build)
./java/mvnw -f java/pom.xml compile
```

`TestPreviousExporterRejectsCurrentBaseline` also ran with a real binary built from
pre-enhancement commit `5dcc36df2`, supplied through `MEMORY_SERVICE_TEST_PREVIOUS_BINARY`.
Both manage and validate modes refused the current database without changing its DDL.
The default test run skips that optional binary check when the environment variable is absent.

The broad outbox runners were exercised separately. Their two PostgreSQL turn-trace
session-ID assertions also fail on unchanged commit `5dcc36df2`; these existing failures
are outside this enhancement. The membership scenarios pass under the race detector on
PostgreSQL, SQLite, and MongoDB. Keep new source cases in the durable outbox runners.

Native and HTTP takeover tests observe a real unfinished INSERT before canceling its
writer and replaying through deletion. Both completed with zero physical/current rows
on pinned ClickHouse `26.8.8.8`. The separate injected-late-INSERT test leaves a physical
row while returning zero current rows, confirming the documented fence/cleanup boundary.
No additional cleanup service was introduced.

`TestClickHouseLayoutMeasurements` records query plans, read rows, latency, memory, disk
bytes, and batch ingestion time without hardware-dependent performance assertions. On the
32,768-entry local sample, the conversation preset read 16,384 rows versus 32,768 for resource
ordering, while both queries took 19 ms. The sample combining conversation ordering with
dictionary encoding used more disk than resource ordering with plain strings, so it does
not justify changing either default. Measure the actual workload before selecting layout options.
