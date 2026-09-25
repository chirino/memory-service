---
status: implemented
---

# Enhancement 115: Immutable Memory Schema Versions and Online Migration

> **Status**: Implemented. Core schema version, migration, sort APIs, and BDD coverage are complete. Schema admin gRPC parity, typed sort, selector filtering, non-empty kind invariants, admin list kind+filter, policy kind restriction (IntersectKindSelectors, input.kind in authz/filter.rego), and transaction audit are live. This breaking baseline requires datastores to be reset.

## Summary

Add immutable `MemoryKindVersion` resources that define how an episodic memory value is projected into plaintext filter attributes. Each version has one canonical name such as `customer-profile/v2`, and every memory row stores that exact name, so different schema families and versions can coexist in the same datastore. Administrators create a new version rather than modify an existing one and run a durable background migration that moves matching memories online one row at a time.

This replaces the mutable process-local policy bundle and the datastore-wide `episodic_policy_bundle` fingerprint. Global authorization and search scoping remain immutable deployment configuration; only the replayable attribute projection becomes a stored, per-memory schema resource. Existing version-1 datastores are intentionally not migrated and must be reset before this baseline starts.

## Motivation

The current implementation has three correctness problems:

- `PUT /admin/v1/memory-policies` mutates one process immediately while existing memory rows, vector payloads, and other replicas can continue using different policy sources.
- One global policy version assumes every memory has the same attribute shape and makes any attribute change a datastore-wide maintenance event.
- `attributes.rego` receives transient caller context, including JWT claims, that cannot be reconstructed for a later projection rewrite.

A singleton datastore fingerprint and offline full rebuild solve consistency by stopping the service, but are too coarse when applications intentionally store different kinds of memory. A per-memory immutable version reference provides a stable interpretation for each projection and makes migration an ordinary online reconciliation process.

## Goals

1. Let multiple memory schema families and immutable versions coexist.
2. Make the exact canonical schema name used for every memory and vector observable.
3. Make attribute projection replayable from persisted memory inputs.
4. Move memories online with atomic per-row cutover and optimistic concurrency.
5. Reject stale vector metadata instead of depending on cross-store transactions.
6. Establish a migrated persisted-data baseline with explicit non-empty kinds.
7. Reuse the existing task processor and episodic indexer where practical.

## Non-Goals

- Kubernetes API compatibility or general-purpose CRDs.
- JSON Schema validation of the encrypted memory value.
- Versioned authorization policies or per-schema authorization decisions.
- Conversion webhooks, served/storage-version negotiation, or automatic version chains.
- Per-schema embedding providers, vector dimensions, ANN settings, or indexed-content rules.
- Automatic per-schema SQL expression indexes or Qdrant payload-index creation.
- Zero-gap semantic search for the individual row being cut over.
- Automatic migration when a new version is created.
- Deleting schema versions or automatically rolling back a partial migration.
- Per-memory migration checkpoint records or distributed migration locks.

## Breaking Changes

- ✅ **Done.** Remove `GET /admin/v1/memory-policies` and `PUT /admin/v1/memory-policies` from `contracts/openapi/openapi-admin.yml` and the `MemoryPolicyBundle` schema. The current admin REST handlers were `HandleAdminGetMemoryPolicies` and `HandleAdminPutMemoryPolicies` in `internal/plugin/route/memories/memories.go`; the admin gRPC service had no policy RPC, so no gRPC removal was needed.
- ✅ **Done (PolicyEngine).** Remove attribute extraction (`attributes.rego`, `attrExtract` query, `ExtractAttributes` method, and the `Attributes` field from the `ReplaceBundle` path) from the process-wide `PolicyEngine` in `internal/episodic/policy.go`. The remaining `authz.rego` and `filter.rego` programs are loaded once from deployment configuration and cannot be changed through an API. All residual `policy.ExtractAttributes(...)` call-sites in `memories.go` and `grpc/server.go` have been replaced with `resolveKindProjection` / `grpcResolveKindProjection`; the build passes cleanly.
- ✅ **Done.** Remove `input.context` from the attribute projection contract. User-created schema versions may read only persisted namespace, key, value, and index inputs.
- ✅ **Done.** Require every user-created schema version to declare the type of each projected attribute. Projection output and typed filters are rejected when they do not match the declaration.
- ✅ **Done.** Add an exact `kind` string to memory write results, memory items, and memory events. Add an optional `kind` top-level field to write and search requests (REST and gRPC).
- ✅ **Done (version 2).** The reserved `legacy/v1` constant, seed, NULL/empty-as-legacy fallbacks, COALESCE/legacy matching, and legacy exceptions have been removed. Fresh rows carry a non-empty canonical kind and DB schema columns are `NOT NULL`; version-1 datastores are rejected with reset guidance.
- ✅ **Done.** `AdminListMemories` REST and gRPC accept optional `kind` (kind selector) and `filter` (projected-attribute filter) parameters. All three stores implement them.
- ✅ **Done.** Policy kind restriction: `filter.rego` receives `input.kind` (caller's top-level kind selector); it may output a narrowing `kind` field. `IntersectKindSelectors` computes the effective kind (policy never broadens). Agent search and admin-as-user search apply the intersection. `EvaluateAuthz` receives `input.kind` for write operations (resolved before authz). The intersection result is `KindIntersection{Selector, Empty}` — a disjoint result returns 200 empty without a store query.
- ✅ **Done.** Transaction audit: every REST/gRPC kind-version/migration and admin-list/search store operation is wrapped in `routetx.EpisodicRead/Write` or `withEpisodicRead/Write`. Filter/sort type validation helpers are also inside a read tx to avoid SQLite `dbFor(ctx)` panics outside a scope.
- ✅ **Done.** Existence non-disclosure: `GetMemory`/`UpdateMemory` REST and gRPC run `EvaluateAuthz` before checking row presence (using the stored kind or `""`) so unauthorized callers cannot probe memory existence via response code differences.
- ✅ **Done.** Memory-kind definitions use `attributes` and `projectionRego` in REST and YAML manifests, with `projectionRegoFile` for file references; gRPC uses `attributes` and `projection_rego`. Persisted `attribute_types` and `attributes_rego` columns remain unchanged for data compatibility.

The public API and persisted schema changes are compatibility breaking. Older datastores must be reset before starting this baseline.

## Design

### Separation of Concerns

| Concern | Source of truth | Versioning |
|---|---|---|
| Operation authorization | Deployment `authz.rego` | One immutable program per process start |
| Search/list scoping | Deployment `filter.rego` | One immutable program per process start |
| Plaintext attribute projection | Stored `MemoryKindVersion.projectionRego` | Exact immutable version per memory |
| Indexed text | `PutMemoryRequest.index` persisted as `indexed_content` (JSONB column on `memories`) | Per memory revision; unchanged by schema migration |
| Embedding and ANN configuration | Service/store configuration | Outside this enhancement |

`MemoryKindVersion` deliberately owns only attribute projection. Keeping authorization global prevents a memory-selected schema from weakening access control. Keeping indexed content and the embedding model outside the schema makes semantic scores comparable across schema versions and allows vector payloads to be updated without calling the embedder.

The global `filter.rego` program may inject an attribute filter, but that filter has the same meaning for every selected schema. Operators must keep any security-relevant injected attribute fields present and semantically consistent across the schema versions that can be searched together. The built-in filter scopes every public caller to its own user namespace even when the principal also has an admin role. Dedicated admin memory APIs bypass this user filter; admin search evaluates it only when `as_user_id` explicitly requests the target user's view.

`MEMORY_SERVICE_POLICY_IMPORT_PATH` (mapped to `PolicyImportPath` in `internal/config/config.go`) is the shared startup source for all deployment-provided policy types. It accepts a comma-separated list of files and directories. Optional `authz.rego` and `filter.rego` files replace the global programs and must be supplied together. A configured directory exposes these files only at its root, or either file can be listed explicitly. When neither is present, the built-in authorization and scoping programs are used. Other Rego files remain available as assets referenced by manifest-based policy types and are not loaded as global programs. No datastore fingerprint or startup gate is needed for the global programs because they do not alter persisted projections.

Each configured directory is searched recursively for `*.yaml` and `*.yml` files. Explicit files are examined regardless of their extension. The importer first reads only the top-level `kind` discriminator, ignores documents whose kind is absent or not `memory-kind`, then strictly decodes matching manifests. A memory-kind manifest declares `attributes` and may provide inline `projectionRego` or reference `projectionRegoFile` relative to its own directory. Startup inserts an absent canonical version, logs an identical stored version as already present, and logs a same-name content conflict without overwriting the immutable database record. The database remains authoritative after import. Future policy document types use the same scan and their own `kind` discriminator. The distributed container image copies the complete `deploy/episodic-policies/` tree to `/etc/memory-service/policies/` and sets that parent directory as the default import path; the cognition bundle remains in its `cognition/` subdirectory. Independently, the built-in default bundle's manifest plus `authz.rego`, `projection.rego`, and `filter.rego` sources are compiled into the binary from `internal/episodic/default-v1/` using Go embedding, so they are available outside the container image too. The authz/filter programs remain global across all kinds; only the projection program belongs to the immutable `default/v1` kind.

### Memory Schema Version

One canonical string combines the schema family and version, analogous to Kubernetes group/version identifiers:

```json
"customer-profile/v2"
```

The canonical name contains exactly one `/`. Its family and version components are lowercase DNS-label-like values: 1–63 characters, beginning with a letter and containing only letters, digits, and hyphens. The complete string is the resource's unique identifier and is stored directly on memory rows and vectors; there is no separate public version field or internal schema UUID.

The immutable resource is:

```json
{
  "name": "customer-profile/v2",
  "attributes": {
    "kind": "string",
    "observedAt": "timestamp",
    "score": "number",
    "active": "boolean",
    "tags": "string[]"
  },
  "projectionRego": "package memories.attributes\nattributes := {\"kind\": input.value.kind}",
  "createdAt": "2026-08-14T18:00:00Z"
}
```

Creation parses the family/version components, validates the attribute type map, compiles and validates the Rego source, and then stores the canonical name, types, and source. Repeating a create request for an existing name with the same validated type map and byte-for-byte identical source returns the existing resource; any difference returns `409 Conflict`. There is no update or delete operation and no persisted fingerprint. Direct database modification is unsupported and database administrators are already inside the datastore trust boundary, so a second hash would not enforce immutability.

### Replayable Projection Contract

`projectionRego` receives exactly:

```json
{
  "namespace": ["user", "alice"],
  "key": "preference",
  "value": {"theme": "dark"},
  "index": {"summary": "prefers a dark theme"}
}
```

The compiled AST may reference only `input.namespace`, `input.key`, `input.value`, and `input.index`. Validation rejects OPA builtins for which `Builtin.IsNondeterministic()` is true. Source is limited to 256 KiB, evaluation observes request/job cancellation, and the result must be a JSON object.

Every evaluation replaces the complete `policy_attributes` object. It never merge-patches an old projection, so attributes removed in a new schema version are removed from plaintext storage and vector payloads when a memory migrates.

### Typed Attributes

`attributes` is a required map for user-created versions. It declares every top-level field the Rego program may return:

| Type | Stored value | Supported filters | Sort order |
|---|---|---|---|
| `string` | JSON string | `$eq`, `$in`, `$exists` | Binary Unicode/UTF-8 lexical order |
| `number` | Finite IEEE-754 double encoded as a JSON number | `$eq`, `$in`, `$exists`, `$gte`, `$lte` | Numeric order |
| `boolean` | JSON boolean | `$eq`, `$in`, `$exists` | `false`, then `true` |
| `timestamp` | Canonical UTC RFC 3339 string | `$eq`, `$in`, `$exists`, `$gte`, `$lte` | Chronological order |
| `string[]` | JSON array of strings | membership `$eq`/`$in`, `$exists` | Not sortable |

Attribute names are 1–63 characters and match `^[A-Za-z][A-Za-z0-9_-]*$`; dots and `$` prefixes are disallowed so PostgreSQL, SQLite, MongoDB, and Qdrant cannot interpret one logical field as different paths. Projected fields are optional, but any returned field must be declared and must match its type. Explicit nulls, non-finite numbers, mixed arrays, nested objects, and undeclared fields are rejected. Timestamps are parsed and rewritten as UTC `YYYY-MM-DDTHH:MM:SS.nnnnnnnnnZ` before being stored, so every backend and API response observes one representation.

Filter values are validated and normalized from the selected schema declarations instead of inferred from request JSON. A filter field must be declared by at least one selected regular schema; selected versions that do not declare it cannot match. If multiple selected regular versions declare the field with different types, the request returns `400 Bad Request` and must select an exact version.

Compiled schema programs are cached in `internal/episodic/kind.go` by Rego source text using a process-wide `sync.Map`. Because schema versions are immutable, entries are never evicted and the cache is always correct. No reload mutex, file watcher, or replica broadcast is needed.

### Write Resolution

`PutMemoryRequest` gains an optional `kind` string:

- `customer-profile/v2` selects that exact writable version.
- `customer-profile` is rejected because writes require an exact version.
- Omission always resolves to the fixed built-in `default/v1`.

The built-in replayable schema is registered as `default/v1` from Go-embedded manifest and Rego files. Every write result contains the resolved exact reference. A write to an existing key creates its normal new memory revision under the newly resolved schema; historical rows retain their own schema references. There is no mutable per-family default state.

### Search Semantics

Search requests gain an optional `kind` string:

- `customer-profile/v2` searches one exact version.
- `customer-profile` searches every version in that family, including versions concurrently being migrated.
- Omission searches every schema.

The same caller filter is applied to all selected versions. A field absent from a version does not match a condition on that field. Applications that change a field's meaning between versions should search exact versions separately; conversion between attribute layouts is out of scope.

Attribute-only searches may include one typed sort:

```json
{"sort":{"field":"observedAt","direction":"desc"}}
```

The field must be declared by at least one selected schema, must have one consistent sortable type, and must not be `string[]`. Missing and undeclared values sort last in both directions. Ties use `created_at DESC, id DESC` for deterministic results. String sorting explicitly uses PostgreSQL `C`, SQLite `BINARY`, and MongoDB simple binary collation so backend locale settings cannot change ordering.

A sort is rejected when `query` or `queries` requests semantic search; semantic results remain ordered by similarity score. Only one attribute sort is supported initially. Dynamic physical indexes can be added later if measured workloads require them.

Attribute-only search reads the schema reference and attributes from the same primary row, so each result is wholly old or wholly new. Semantic search remains comparable across versions because schema migration does not change `indexed_content` or the configured embedder.

### Admin API

The admin REST and gRPC surfaces provide equivalent operations:

| Operation | REST |
|---|---|
| Create immutable version | `POST /admin/v1/memory-kinds` |
| List versions | `GET /admin/v1/memory-kinds` |
| Get version | `GET /admin/v1/memory-kinds/{family}/{version}` |
| Create migration | `POST /admin/v1/memory-kind-migrations` |
| List migrations | `GET /admin/v1/memory-kind-migrations` |
| Get migration | `GET /admin/v1/memory-kind-migrations/{id}` |
| Request cancellation | `DELETE /admin/v1/memory-kind-migrations/{id}` |

All mutation operations are administrator-only and emit the normal admin audit record. Schema source is returned only on the administrator-only definition endpoints; memory responses expose only the canonical schema name.

Memory-kind REST fields use lower camel case. The principal request bodies are:

```json
{"name":"profile/v2","attributes":{"observedAt":"timestamp"},"projectionRego":"package memories.attributes\nattributes := {\"observedAt\": input.value.observedAt}"}
```

```json
{
  "source": "profile/v1",
  "target": "profile/v2",
  "namespace_prefix": ["user","alice"]
}
```

REST and protobuf request/response messages use `kind`; datastore columns and vector payloads use `memory_kind`.

### Storage Model

PostgreSQL illustrates the authoritative relational shape:

```sql
CREATE TABLE memory_kind_versions (
    name            TEXT PRIMARY KEY,
    attribute_types JSONB NOT NULL,
    attributes_rego TEXT,
    writable        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE memory_kind_migrations (
    id                      UUID PRIMARY KEY,
    source                  TEXT NOT NULL REFERENCES memory_kind_versions(name),
    target                  TEXT NOT NULL REFERENCES memory_kind_versions(name),
    namespace_prefix        JSONB,    -- serialized namespace array (NULL = no prefix restriction)
    state                   TEXT NOT NULL,
    cancel_requested        BOOLEAN NOT NULL DEFAULT FALSE,
    migrated_count          BIGINT NOT NULL DEFAULT 0,
    skipped_tombstone_count BIGINT NOT NULL DEFAULT 0,
    vector_pending_count    BIGINT NOT NULL DEFAULT 0,
    retry_count             INT NOT NULL DEFAULT 0,
    last_error_code         TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at              TIMESTAMPTZ,
    completed_at            TIMESTAMPTZ,
    CHECK (source <> target),
    CHECK (state IN ('queued', 'running', 'canceling', 'succeeded', 'failed', 'canceled'))
);

CREATE UNIQUE INDEX memory_kind_migrations_active_source_idx
    ON memory_kind_migrations (source)
    WHERE state IN ('queued', 'running', 'canceling');

-- Fresh-only baseline: memory_kind is NOT NULL on all fresh rows.
ALTER TABLE memories
    ADD COLUMN memory_kind TEXT NOT NULL DEFAULT 'default/v1'
        REFERENCES memory_kind_versions(name);

CREATE INDEX memories_kind_cursor_idx
    ON memories (memory_kind, created_at, id);

ALTER TABLE memory_vectors
    ADD COLUMN memory_kind TEXT NOT NULL,
    ADD COLUMN memory_revision BIGINT NOT NULL CHECK (memory_revision > 0);
```

Every row in a fresh version-2 datastore carries a non-empty canonical kind. Version-1 datastores are rejected rather than backfilled, and runtime NULL fallback is not implemented.

MongoDB adds equivalent `memory_kind_versions` and `memory_kind_migrations` collections and canonical-name reference fields. Qdrant payloads add `memory_kind` and `memory_revision`; they do not store Rego source. Startup removes the obsolete `memory_kind_defaults` SQL table or MongoDB collection left by earlier version-2 builds.

The migration resource stores only job-level state:

```text
id, source, target, namespace_prefix,
state, cancel_requested, migrated_count, skipped_tombstone_count,
vector_pending_count, retry_count, last_error_code, created_at, started_at,
completed_at
```

States are `queued`, `running`, `canceling`, `succeeded`, `failed`, and `canceled`. No per-memory migration table or cursor is needed because a migrated row no longer matches the source-version scan.

### Online Migration

A migration request names an exact source and target plus an optional namespace prefix. The target must be writable and must differ from the source. Both source and target must be in the same family (regular migrations) or the caller may choose any two distinct kinds. The API rejects a second active migration for the same source version with `409 Conflict`, avoiding duplicate work without a distributed lock.

Before starting a migration, operators should update writers to select the target's exact canonical name. Explicit writes to the source remain permitted; completion means no eligible source row existed at the final scan, not that the source version is retired.

Creation persists the migration resource and its uniquely named initial `memory_kind_migration` task atomically in the episodic datastore. Continuation tasks use fresh identities. PostgreSQL and SQLite use their SQL transaction; MongoDB uses a session transaction, so no committed queued migration can be left without its initial task. MongoDB deployments must therefore use a replica set or mongos; standalone MongoDB servers do not support these transactions.

For each bounded batch, the worker:

1. Selects up to `migrationBatchSize` rows whose effective schema is the source, optionally filtered by namespace prefix, scanning by the monotonic `(created_at, id)` cursor. Tombstones (nil encrypted value) are included in the scan; they advance the cursor but are not decrypted or counted here.
2. For each tombstone row, advances the cursor without incrementing any counter (tombstone counts are computed once during the final sweep, not cumulatively per batch). For each row with a recoverable value, decrypts the value, evaluates the target projection from persisted namespace, key, value, and indexed content, and validates the result against the declared types.
3. Uses a primary-datastore compare-and-swap on memory ID, source schema, and revision to atomically replace all attributes, set the target schema, increment revision, and set `indexed_at = NULL`. The CAS write and `migrated_count` increment share the same SQL or MongoDB session transaction. A revision mismatch stops the batch immediately; the task retries and the row re-appears in the next scan if it still uses the source schema.
4. After processing all rows, enqueues a continuation task with the updated cursor and deletes the current task. When the scan returns empty (cursor exhausted), runs a full paginated verification sweep across all remaining source rows:
   - Counts tombstones to obtain the final absolute `skipped_tombstone_count`.
   - If any replayable (non-tombstone) row is found during the sweep, schedules a full restart from an empty cursor (the absolute tombstone count is not persisted at this point; it will be re-counted on the next sweep).
   - If no replayable rows remain, persists `state=succeeded` with the absolute tombstone count. The sweep and final state write execute in one SQL or MongoDB session transaction.

The compare-and-swap prevents an in-flight memory update, archive, expiry, or competing migration from being overwritten. A conflicting row is reread on a later scan if it still uses the source. Migration increments revision as a lifecycle/projection transition so stale vector/indexer CAS operations cannot complete; it does not create a memory event because the encrypted value is unchanged.

Superseded, explicitly archived, and active rows with recoverable values are eligible so their event/search projections remain coherent. Value-less tombstones cannot be replayed; they retain their original schema reference and cleared attributes and increment `skipped_tombstone_count`. Schema versions are never deleted, so this retained reference is safe.

`DELETE /admin/v1/memory-kind-migrations/{id}` sets both `cancel_requested = true` and `state = "canceling"` atomically; the task worker checks either condition and completes `canceled` after the current row operation. Already migrated rows remain on the target. A failed deterministic projection or decryption marks the job `failed`; transient primary-datastore errors retain the task for retry. Retrying is naturally idempotent because only source-version rows are selected. Operators resume canceled or failed work by creating another migration with the same source and target.

`succeeded` means all replayable primary rows observed by the final source scan were cut over. It does not mean every pending vector has converged; the migration response reports `vector_pending_count`, and the existing memory-index status remains the authoritative convergence signal.

Rollback is the same operation with source and target reversed. It recomputes the old immutable projection; it does not restore a snapshot of old plaintext attributes.

### Vector Consistency

The primary memory row is authoritative. Every vector carries the memory's canonical schema name and revision:

- PostgreSQL, SQLite, and MongoDB vector searches require the vector metadata to match the joined primary row.
- Qdrant results return the canonical schema name, revision, and archive state, and the service discards candidates that do not match the fetched primary memory row.

After the primary cutover and before payload patching, an old vector is stale and cannot be returned. This creates a brief semantic-search false negative for that memory, not a false positive or mixed projection. Attribute-only search remains continuously available.

All vector backends apply exact/family kind filtering before similarity ranking; primary-row validation still rejects stale metadata. The existing indexer repairs an interrupted cutover because `indexed_at` remains null. Its vector upsert includes the canonical schema name and positive revision, and `SetMemoryKindIndexedAtCAS` is a compare-and-swap so an obsolete indexing attempt cannot mark a changed row complete.

### Reset Baseline and Compatibility

This implementation advances the datastore baseline from schema version 1 to version 2 as an intentionally breaking reset. PostgreSQL, SQLite, and MongoDB reject version-1, unknown, and unversioned existing layouts with reset guidance; no legacy rows or vectors are transformed. No legacy NULL fallback or `legacy/v1` synthetic kind exists.

- `memory_kind` columns are `NOT NULL` on all fresh schemas (PostgreSQL, SQLite, Mongo/Qdrant require non-empty kind on upsert).
- The `default/v1` built-in schema is registered on first boot from Go-embedded manifest and Rego files; any write that omits `kind` resolves to `default/v1`.
- Schema versions and migration state persist in newly initialized version-2 datastores.

## Failure, Recovery, and Operations

- Each task claim processes one bounded batch shorter than the existing task lease. More work is rescheduled as a successful continuation, not represented as a failure.
- A replica crash before primary cutover leaves the source row unchanged. A crash after cutover leaves `indexed_at = NULL`, allowing the ordinary indexer to repair vector metadata.
- A Qdrant outage delays vector synchronization and may reduce semantic results, but does not block primary attribute cutover or attribute search. After bounded in-process payload retries, the row remains pending for the ordinary indexer.
- Concurrent writes and lifecycle transitions are handled by compare-and-swap, not locks held across decryption or external calls.
- `GET /admin/v1/memory-kind-migrations/{id}` reports durable state and counters. Aggregate metrics report jobs by state and migration throughput without canonical-schema-name labels that would create unbounded cardinality.
- Task retry records and migration `last_error_code` store only a stable sanitized classification; raw Rego, decryption, datastore, and provider errors are not persisted in task bodies or returned by the admin API.
- Each claimed batch emits one `job.memory_kind_migration` start and terminal canonical event with task ID, retry attempt, work count, and failure count. A continuation terminates successfully; a returned transient failure terminates as `retrying`; shutdown terminates as `canceled`.
- Canonical events never contain schema source, canonical schema names, namespace, key, value, indexed content, projected attributes, or raw provider errors. Bounded diagnostic point logs may contain migration UUID, canonical schema name, and memory UUID.

## Security and Privacy

- Schema source is executable configuration and can expose data into plaintext attributes. Only administrators may create or read it.
- `policy_attributes` remains plaintext. Definition authors must project only values approved for plaintext filtering.
- Global authorization runs before schema selection and cannot be replaced by a memory schema.
- Immutable API/storage interfaces, restricted input roots, declared output types, and rejection of nondeterministic builtins make projection replayable. Direct datastore mutation remains outside the supported trust boundary.
- Full-object replacement removes fields dropped by a target schema from primary rows and vector payloads.
- Source size, request size, batch size, cancellation, and existing rate limits bound administrator-triggered resource use.
- Audit records identify schema and migration mutations by migration UUID and canonical schema name but never include Rego source or memory content.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Immutable memory schema versions

  Scenario: A schema version cannot be changed
    Given an administrator created memory schema "profile/v1"
    When the administrator creates "profile/v1" with different Rego source
    Then the response status is 409

  Scenario: Different memory schemas coexist
    Given "profile/v1" and "preference/v1" are writable schema versions
    When memories are stored with each schema
    Then each memory response contains its exact schema reference
    And searching either schema family returns only matching memories

  Scenario: Timestamp attributes sort chronologically
    Given "event/v1" declares "observedAt" as a timestamp
    And memories contain different timezone representations of "observedAt"
    When I search without a semantic query and sort "observedAt" ascending
    Then the memories are returned in chronological order
    And memories without "observedAt" are last

  Scenario: A migration remains available online
    Given memories exist under "profile/v1"
    And writers select "profile/v2" for new memories
    When an administrator starts a migration from "profile/v1" to "profile/v2"
    Then reads and attribute searches continue during migration
    And each returned memory has attributes matching its reported schema version
    And the migration eventually succeeds
```

### Focused Tests

- Creation validates canonical names, declared types, source size, package/query contract, input roots, deterministic builtins, and result type.
- Identical type-map/source create retries are idempotent; any different definition under the same canonical name conflicts.
- Projection rejects undeclared, null, nested, non-finite, malformed timestamp, and wrongly typed values; timestamps are stored in canonical UTC form.
- Writes omit `kind` to select `default/v1`, accept exact writable canonical names, and reject family-only selectors.
- New writes, updates, list results, events, admin results, REST, and gRPC expose the exact schema reference.
- Exact, family, and all-schema searches apply the documented selector behavior.
- Attribute filters use declared types; conflicting cross-version field types are rejected rather than inferred from request values.
- Attribute-only sorting is type-correct and deterministic for strings, numbers, booleans, timestamps, missing values, and ties on every primary datastore; arrays and semantic-sort combinations are rejected.
- Version-1, unknown, and unversioned populated datastores are rejected with reset guidance; a fresh version-2 datastore has no null/missing kind fallback.
- Migration replaces the complete projection, increments memory revision, and does not call the embedder.
- Concurrent put, archive, expiry, and migration attempts cannot overwrite newer state.
- Active, superseded, and recoverable archived rows migrate; value-less tombstones remain safely on their source and are counted.
- A crash after primary cutover leaves the row pending, and the ordinary indexer repairs vector metadata.
- Vector queries filter by exact/family kind before ranking, and primary validation never returns a stale schema/revision/lifecycle candidate.
- Multiple replicas cannot claim the same migration batch, and overlapping active jobs for one source are rejected.
- Cancellation and deterministic failure preserve already migrated rows and permit a later idempotent job.
- Canonical job events classify continuation, retry, cancellation, panic, and success correctly without sensitive fields.

## Tasks

- [x] Replace the mutable policy-bundle admin API with memory-schema-version and migration REST contracts (`contracts/openapi/openapi-admin.yml`, `openapi.yml`).
- [x] Add equivalent admin gRPC operations and add schema references/selectors to agent and admin REST/gRPC memory contracts (`contracts/protobuf/memory/v1/memory_service.proto`).
- [x] Regenerate Go, Java, Python, and TypeScript contract artifacts (`task generate` completed).
- [x] Split global authorization/search scoping from schema-owned attribute projection; remove `ExtractAttributes`, `Reload`, and `ReplaceBundle` from `PolicyEngine`; remove `attributes.rego` loading; reject `attributes.rego` in a configured policy dir (`internal/episodic/policy.go`). Fixed missing `"strings"` import and replaced stale `ExtractAttributes` call-sites in `memories.go` and `grpc/server.go` with `resolveKindProjection`/`grpcResolveKindProjection` helpers.
- [x] Implement canonical-name parsing, attribute-type and projection validation, immutable datastore loading, and compiled-program caching (`internal/episodic/kind.go`).
- [x] Add `EpisodicKindStore` interface methods and `ErrMemoryKindVersionConflict`/`ErrMemoryKindMigrationActiveForSource` errors to `internal/registry/episodic/plugin.go`; add `MemoryKind` field to `PutMemoryRequest`, `MemoryWriteResult`, and `MemoryItem`; add `SetMemoryKindIndexedAtCAS` to `EpisodicStore` interface.
- [x] Implement `EpisodicKindStore` CRUD for PostgreSQL (`internal/plugin/store/postgres/episodic_kind.go`), SQLite (`internal/plugin/store/sqlite/episodic_kind.go`), and MongoDB (`internal/plugin/store/mongo/episodic_kind.go`).
- [x] Add schema admin REST handlers for immutable versions and migrations in `internal/plugin/route/memories/memories.go` and register them in `internal/cmd/serve/wrapper_routes.go`.
- [x] Fix build-breaking residual call-sites: replaced `policy.ExtractAttributes(...)` in `memories.go` and `grpc/server.go` with `resolveKindProjection` / `grpcResolveKindProjection`; added missing `"strings"` import to `policy.go`; removed dead `persistPolicyBundle` function and `adminMemoryPolicyContext` helpers.
- [x] Add DB schema tables and columns: `memory_kind_versions` and `memory_kind_migrations` added to Postgres, SQLite, MongoDB; `memory_kind NOT NULL` column on `memories`; `memory_kind NOT NULL`/`memory_revision` columns on `memory_vectors`. Seeded `default/v1` from Go-embedded manifest/Rego assets (namespace/sub backward-compat projection). Updated `MemoryKindMigration.NamespacePrefix` GORM tag from `text[]` to `jsonb`. Removed deleted policy-bundle BDD scenario.
- [x] Add `memory_kind NOT NULL` and `memory_revision` columns to vector backends; `MemoryVectorUpsert` and `PendingMemory` structs carry schema name and memory revision. Qdrant payloads store `memory_kind` and `memory_revision` in point payloads. Stores reject upsert of vectors with empty `MemoryKind`; search skips vector candidates with empty/missing kind.
- [x] Implement schema-aware memory writes/results/events/searches in all three stores (`postgres/episodic_store.go`, `sqlite/episodic_store.go`, `mongo/episodic_store.go`): `ResolveKindForWrite`, evaluate kind projection via `CompileKindProjection`/`EvaluateKindProjection`, persist `memory_kind`, populate `MemoryKind` on reads and write results.
- [x] Implement durable migration creation, batching, continuation, cancellation, counters, and retry classification using the existing task queue (`internal/service/taskprocessor.go` — `memory_kind_migration` task type added; `FindMemoriesToMigrateByKind` + `MigrateOneMemoryKindCAS` added to all three stores).
- [x] Add compare-and-swap projection cutover — `MigrateOneMemoryKindCAS` in postgres, sqlite, and mongo stores; `indexed_at` cleared on cutover so the ordinary indexer re-syncs vector metadata.
- [x] Make the episodic indexer use `SetMemoryKindIndexedAtCAS` and carry schema metadata on vector upserts (`internal/service/episodic_indexer.go`). Fixed MongoDB `SetMemoryKindIndexedAtCAS` filter bug: `archived_at: nil` / `deleted_reason: nil` do not match documents without those fields; replaced with `$exists: false` to correctly match active memory documents in MongoDB.
- [x] Add `AdminMemoryKindServer` (`AdminMemoryKindServiceServer`) gRPC service registration in `internal/grpc/server.go` and `internal/cmd/serve/server.go`; schema-aware write handling in existing gRPC `PutMemory` paths via `grpcResolveKindProjection`.
- [x] Add schema-aware filter validation and one-field typed sorting to agent/admin REST and gRPC attribute searches. Schema selector (`memory_kind`) and one-field typed `sort` added to `SearchMemoriesRequest`/`AdminSearchMemoriesRequest` in OpenAPI, proto, all three store implementations, REST handlers, and gRPC handlers. BDD scenarios cover ascending sort with missing values last and sort+semantic rejection.
- [x] Version-2 reset invariant: `memory_kind NOT NULL` in all stores; version-1 and unversioned existing datastores are rejected; `legacy/v1` seed and constant removed; migration source/target must both exist and differ; stores reject empty kind.
- [x] Add Cucumber coverage for schema and migration admin endpoints, fixed `default/v1` resolution, family-only write rejection, Rego list redaction, and populated migration completion (`internal/bdd/testdata/features/memory-kind-rest.feature`).
- [x] Update repository knowledge with schema/migration invariants and new entries for `GetMemoryRowKind` (archived-aware, 4-arg signature), `KindIntersection` typed result, `AdminList kind+filter`, authz-before-404, SQLite tx scope, and filter validation tx scope.
- [x] Add `GetMemoryRowKind(ctx, namespace, key string, archived ArchiveFilter) (kind string, found bool, err error)` to `EpisodicStore` interface and all three stores; use it in REST/gRPC `GetMemory`/`UpdateMemory` for kind-aware authz inside a single write tx (no content before authz, no race). `ArchiveFilterExclude` used on update paths so archived rows return not-found.
- [x] Replace `KindSelectorDisjoint` sentinel string with `KindIntersection{Selector string; Empty bool}` typed struct; `ValidateKindSelector` validates format-only before intersection; agent/admin search (REST + gRPC) return 200 empty / empty gRPC response on `ki.Empty=true` without a store query. `AdminList` uses `ki.Empty` check from direct validation only (no user policy intersection on admin list paths).
- [x] `AdminListMemories` REST and gRPC: kind selector and attribute filter now applied; all three stores implement them; BDD scenarios for exact, family, omitted, and admin search parity.
- [x] Policy kind restriction: `IntersectKindSelectors`, `InjectFilterPartsWithKind`, `EvaluateAuthz` + `input.kind`; unit tests for all 15 intersection cases, 2 inject cases, 3 policy-output cases, default policy test.
- [x] Typed intersection result: replaced `KindSelectorDisjoint` sentinel string with `KindIntersection{Selector string; Empty bool}` struct; `ValidateKindSelector` validates format-only before intersection; all four Search handlers (agent/admin REST+gRPC) return 200 empty / empty gRPC response on `ki.Empty=true` before entering a store transaction.
- [x] Policy kind output validation: `InjectFilterPartsWithKind` validates the `kind` field from `filter.rego` output (must be non-empty string, pass `ValidateKindSelector`); malformed output is a policy error; policy can only narrow, never broaden.
- [x] Authz before 404 (existence non-disclosure): REST `getMemoryWithParams` and gRPC `GetMemory`/`UpdateMemory` now run `EvaluateAuthz` with the row's stored kind (or `""` for missing rows) before returning 404 — unauthorized callers cannot probe existence via differing status codes.
- [x] `GetMemoryRowKind` archived-aware: 4th parameter `archived ArchiveFilter`; all three stores implement it; `ArchiveFilterExclude` used for update paths.
- [x] PG episodic store tx scope fix: `GetMemoryRowKind`, `GetMemory`, `PutMemory`, `ArchiveMemory` in `postgres/episodic_store.go` now use `e.s.dbFor(ctx)` / `e.s.writeDBFor(ctx, op)` instead of the unscoped `e.db` field.
- [x] Filter validation tx scope fix: `searchMemories`, `HandleAdminSearchMemories`, `HandleAdminListMemories` moved `validateAndNormalizeCallerFilter` and `resolveKindSortFieldType` inside a `routetx.EpisodicRead` to prevent SQLite `dbFor(ctx)` panic outside a tx scope.
- [x] Vector invariants: Qdrant `UpsertMemoryVectors` rejects empty `MemoryKind`; `SearchMemoryVectors` skips Qdrant candidates with empty `memory_kind` payload; Mongo `SearchMemoryVectors` skips vectors with empty `MemoryKind`.
- [x] PG schema cleanup: removed duplicate `ALTER TABLE memories ADD COLUMN memory_kind` at ~line 506 and conditional `memory_vectors` migration block at ~lines 592–615 from `internal/plugin/store/postgres/db/schema.sql`.
- [x] Policy BDD (dedicated SQLite runner `TestFeaturesSQLitePolicy`, `auth_testfixtures` build tag): custom `authz.rego` (authz-custom namespace requires `authz/v1`, all other namespaces require `default/v1`, denied-ns always blocked — policy actively uses `input.kind`); custom `filter.rego` (ordinary callers → `kind: "default/v1"`, `filter-malformed-test` prefix → `kind: 42` integer to exercise malformed-output error path); scenarios cover kind-dependent authz allow/deny, authz uses actual row kind on read, malformed filter output → 500/INTERNAL, narrowing, disjoint.
- [x] gRPC `GetMemory` status propagation fix: the outer `episodicInternalError` call now checks `status.FromError(err)` first and passes through gRPC status errors (e.g. `PermissionDenied` from authz check) without re-wrapping them as `Internal`.
- [x] Update site documentation (`site/src/pages/docs/concepts/memories.md`, `site/src/pages/docs/concepts/admin-apis.mdx`): added Memory Schema Versions section (names, built-in default, write-time selection, schema selector + sort in search), updated Access Control section (removed `attributes.rego`, updated default policy listing), removed obsolete `memory-policies` admin route. Updated `admin-apis.mdx`: replaced single memory admin table with subsections for CRUD/search and Schema Versions + Migrations (full lifecycle examples, attribute types table, migration workflow, endpoint reference). Removed `legacy/v1` and `memory_kind` field references; fixed request field name to `kind`.
- [x] Update this enhancement to reflect implemented design.

## Files to Modify

| File | Change |
|---|---|
| `contracts/openapi/openapi.yml` | Add canonical schema names, typed filter validation, and attribute sort |
| `contracts/openapi/openapi-admin.yml` | Replace policy bundle endpoints; add schema/migration resources and typed sort |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add agent/admin parity for canonical names, definitions, migrations, and typed sort |
| `internal/generated/` and language client modules | Regenerate contract artifacts |
| `internal/episodic/policy.go` and tests | Retain immutable global authz/filter evaluation; move projection to schema programs |
| `internal/config/` and `internal/cmd/serve/` | Enforce the two-file global-policy directory contract and wire schema services |
| `internal/model/model.go` | Add schema-version and migration models |
| `internal/registry/episodic/plugin.go` | Add schema-aware request/result, migration, vector metadata, CAS, and search contracts |
| `internal/plugin/store/postgres/db/schema.sql` | Add schema/migration tables and memory schema reference |
| `internal/plugin/vector/pgvector/db/pgvector-schema.sql` | Add vector canonical schema name and memory revision |
| `internal/plugin/store/sqlite/db/schema.sql` | Add equivalent SQLite structures |
| `internal/plugin/store/postgres/episodic_store.go` | Implement schema persistence, typed filters/sort, CAS migration, selectors, and vector validation |
| `internal/plugin/store/sqlite/episodic_store.go` | Implement equivalent SQLite behavior with binary string collation |
| `internal/plugin/store/mongo/mongo.go` and `episodic_store.go` | Add collections, schema persistence, typed filters/sort, migration, and validation |
| `internal/plugin/store/episodicqdrant/qdrant.go` | Store, patch, filter, and return canonical schema name/revision payload metadata |
| `internal/service/episodic_indexer.go` | Carry schema metadata and CAS indexed completion |
| `internal/service/taskprocessor.go` | Add migration task execution and successful batch continuation |
| `internal/plugin/route/memories/memories.go` | Resolve schemas and expose schema-aware REST behavior |
| `internal/plugin/route/memories/memories.go` (admin handlers) | Add schema/migration admin handlers registered via `internal/cmd/serve/wrapper_routes.go` |
| `internal/grpc/server.go` | Add matching agent/admin gRPC behavior |
| `internal/bdd/testdata/` | Cover immutable versions, selectors, migration, compatibility, and failures |
| `site/src/pages/docs/concepts/memories.md` | Document schema versions, fixed write resolution, searches, and online migration |
| `site/src/pages/docs/concepts/admin-apis.mdx` | Replace mutable policy-bundle documentation |
| Repository knowledge (tests, code comments, skills, `AGENTS.md`) | Record the implemented schema and migration invariants |

## Verification

```bash
# Regenerate contracts and formatting outputs
task generate

# Focused schema, migration, indexer, and datastore tests
go test -race ./internal/episodic ./internal/registry/episodic ./internal/service \
  ./internal/plugin/store/postgres ./internal/plugin/store/sqlite \
  ./internal/plugin/store/mongo ./internal/plugin/store/episodicqdrant \
  -count=1 > schema-test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" schema-test.log

# REST/gRPC behavior and data-compatibility scenarios
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' \
  ./internal/bdd -run '^TestFeaturesSQLite$' -count=1 > bdd-test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" bdd-test.log

# Full supported Go matrix and build
task test:go > go-test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" go-test.log
go build ./... > build.log 2>&1
rg -n "ERROR|FAIL|panic|undefined:" build.log

# Generated Java, Python, and TypeScript consumers
./java/mvnw -f java/pom.xml compile > java-compile.log 2>&1
rg -n "FAILURE|ERROR|Compilation failure" java-compile.log
task verify:python > python-verify.log 2>&1
rg -n "FAIL|ERROR|Traceback" python-verify.log
cd typescript/vercelai && npm ci && npm run build && cd ../..

# Site contract/documentation checks; run after the Go/Java work above
task test:site > site-test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" site-test.log
```
