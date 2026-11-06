---
status: implemented
---

# Enhancement 117: ClickHouse projection chains

> **Status**: Implemented.

## Summary

Let ClickHouse analytics projection manifests form a linear chain, like database
migrations, so that several manifests write to one shared table. A later manifest can add a
content type, reuse or replace projection logic, or add nullable columns, and readers keep
querying the same table and views when a producer bumps its content-type version.

This implements [GitHub issue #760](https://github.com/chirino/memory-service/issues/760)
and extends the typed projections of
[Enhancement 404](404-clickhouse-analytics-export.md#typed-projections).

## Motivation

Before this change, each `AnalyticsProjection` wrote to its own table: `metadata.name` was
both the manifest identity and the physical table name, the manifest was immutable, and
its selector matched exactly one `contentType` or memory `kind`. When a producer bumped its
content-type version, a consumer had to add a newly named projection (and table), replay
it, and change every query to read the new table too, usually through a hand-maintained
`UNION ALL` view. Many version bumps only add record kinds or optional fields, so the
projected analytics fields stay the same. Until a projection existed for the new version and
every reader had been updated, new entries silently dropped out of analytics.

## Design

### The chain

- **First manifest:** an ordinary `AnalyticsProjection`. It may set `spec.table` to give the
  table a version-neutral name; otherwise the table is `metadata.name`, as before. It
  declares `columns` and `rows`.
- **Later manifests:** set `spec.previous` to the name of the manifest before them. They inherit the table,
  the columns, the row mode, and the resource. They must not declare `columns`, `rows`, or
  `table`, and their `spec.resource` must match the chain's.
- **Linear:** each later manifest names exactly one previous manifest. Two manifests naming
  the same previous manifest (a branch), a previous manifest that isn't configured, and a
  cycle are rejected at load time. Manifest documents can appear in any file and order; the
  chain is ordered by `spec.previous`.
- **Old manifests stay configured.** A fresh install builds the table by applying every
  manifest in order. Logic for content types that are no longer produced stays idle.
- **Unchanged for existing manifests:** a manifest without `spec.previous` is a chain of one.
  All new manifest fields are omitted from the digest when empty, so manifests written
  before chains keep their digests, tables, and views.

### What a later manifest can do

Each later manifest declares at least one of these actions:

| Action | Fields |
| --- | --- |
| Add a content type that uses existing logic | `selector` plus `projectionRegoFrom: <earlier manifest>` |
| Add a content type with its own logic | `selector` plus a complete `projectionRego` |
| Replace existing logic | `projectionRegoReplaces: <earlier manifest>` plus a complete `projectionRego`, then a projection replay |
| Add nullable columns | `addColumns: { ... }` |

```yaml
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata:
  name: support_ticket_v1_steps
spec:
  resource: entry
  table: support_ticket_steps
  selector:
    contentType: support-ticket/v1
  rows: many
  columns:
    step: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := [{"step": s.name} | some s in input.content]
---
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata:
  name: support_ticket_v2_steps
spec:
  resource: entry
  previous: support_ticket_v1_steps
  projectionRegoFrom: support_ticket_v1_steps
  selector:
    contentType: support-ticket/v2
---
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata:
  name: support_ticket_steps_duration
spec:
  resource: entry
  previous: support_ticket_v2_steps
  projectionRegoReplaces: support_ticket_v1_steps
  addColumns:
    duration_ms: {type: uint64, nullable: true}
  projectionRego: |
    package memoryservice.analytics
    output := [{"step": s.name, "duration_ms": object.get(s, "durationMs", null)} | some s in input.content]
```

The loader resolves each chain into a content-type-to-logic map:

- **Shared logic:** `projectionRegoFrom` adds the manifest's selector to the logic of an
  earlier manifest in the same chain. A reference to a manifest that itself uses
  `projectionRegoFrom` resolves to the manifest that defines the Rego. A reference to a
  manifest outside the chain, later in the chain, or without logic (one that only adds
  columns) is rejected. A manifest cannot set both `projectionRego` and
  `projectionRegoFrom`, and `projectionRegoFrom` requires a selector.
- **Replacing logic:** `projectionRegoReplaces: X` without a selector takes over every
  selector that X's logic currently projects, including selectors added through
  `projectionRegoFrom`. If X itself uses `projectionRegoFrom`, the replaced logic is the
  Rego X reuses. With a selector, it takes over only that selector, which must currently
  use X's logic. `projectionRegoReplaces` requires a complete `projectionRego`. Replacing
  logic that no longer projects anything is rejected.
- **No overlap:** a selector can be covered by only one logic within a chain. Covering it
  again requires `projectionRegoReplaces`. Different chains may still select the same content type,
  each writing its own table.
- **Columns:** the table's columns are the first manifest's columns plus every
  `addColumns`. Added columns must be nullable and must not already exist.

Rows and projection failures carry the name of the manifest whose logic produced them.
The memory attribute mapping of a first memory manifest without `projectionRego` counts as
that manifest's logic.

### Output checks

Projection output is checked against the table's combined columns, for chains and
standalone manifests alike: unknown keys fail with `column_set_mismatch`, an omitted
nullable column is written as `NULL`, and an omitted non-nullable column fails with
`column_set_mismatch`. Earlier logic therefore keeps working after a later manifest adds
columns.

### Schema management

For each chain, the processor:

1. Reads the registry rows of every exporter for the chain's manifests and table. The
   registered manifests must form the beginning of the configured chain with identical
   digests and tables. A registered manifest that is edited, reordered, renamed, or removed
   from the configuration is rejected. Each manifest's digest includes its `previous`,
   `projectionRegoReplaces`, and `projectionRegoFrom` references, so reordering changes digests.
2. Rejects pre-existing relations for a table with no registry rows, as before.
3. In `manage` mode, creates the table with the first manifest's columns, then adds each
   missing column with `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` in chain order. A fresh
   install and an upgraded table therefore have the same column order. Added columns are
   nullable, so `ADD COLUMN` only changes metadata. An added column that already exists
   must have the declared physical type; this catches a column left by an interrupted start
   whose manifest changed before it was registered. The server canonicalizes the declared
   type (`toTypeName(defaultValueOfTypeName(...))`) before the comparison, because
   `system.columns` rewrites type names, for example by omitting JSON parameters that equal
   ClickHouse's defaults.
4. In `validate` mode, changes nothing and fails with the list of missing columns, then
   requires every manifest to be registered. The error tells the operator to start the
   processor once in `manage` mode with a ClickHouse user that can alter tables, and shows
   the `ALTER TABLE` statements that run applies. Applying those statements by hand is not
   enough, because only `manage` mode registers the new manifests and recreates the views.
5. In `manage` mode, registers each unregistered manifest in chain order with its table and
   previous manifest, then re-reads the registry to detect a conflicting concurrent registration.
   If two exporters extend the same table with divergent manifests at the same time, each
   one that detects the conflict withdraws the rows it just registered and fails, so the
   losing configuration does not permanently block the table.

The generated `<table>_all` and `<table>_current` views are recreated on every `manage`
startup, so they include added columns. Columns added to existing rows stay `NULL` until a
manifest that produces them is in place and an operator runs a projection replay.

### Registry

`projection_registry` gains a `previous String DEFAULT ''` column. The processor adds it
with `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` in `manage` mode, outside the versioned
schema DDL, the same way insert deduplication settings are applied. Existing databases keep
schema V1 and its checksum, and processors from earlier releases can still share the
database because they write explicit column lists. `validate` mode fails with the `ALTER`
statement when the column is missing. The column is only required when projections are
enabled.

The ownership rule changes from "exactly one manifest digest per table" to "every
registered manifest for this table belongs to one chain". Registered manifests whose table
differs from their name are accepted, and manifests that only replace logic or add
columns register an empty selector expression (`entry:` or `memory:`).

Purge, retention, insert deduplication, and view creation read the registry once per
table rather than once per manifest. `projection_failures` is unchanged: it records the
projection name, and the registry maps that name to its table.

### Inspection

A chain's definition is spread across manifests, so a new command prints each table's
chain, combined columns with the manifest that declared each one, and the selector-to-logic
map, without connecting to ClickHouse:

```bash
memory-service process clickhouse-projections --projection-path /etc/memory-service/projections
memory-service process clickhouse-projections --projection-path ./projections --format json
```

The registry itself shows what an exporter has applied:

```sql
SELECT table_name, projection_name, previous, projection_digest
FROM memory_service.projection_registry FINAL
WHERE exporter_id = 'primary-analytics'
ORDER BY table_name, projection_name;
```

### Moving existing tables onto a chain

- **Keep the existing table:** add a manifest with `spec.previous` naming the existing
  projection. The table keeps its name and rows.
- **Use a version-neutral name:** start a new chain with `spec.table`, add one manifest per
  content type, and run a projection replay to fill it. Old tables keep working until
  readers switch. Tables of projections that are no longer configured stay registered, so
  deletion and retention still cover them, and operators drop them manually.

## Non-goals

- Changing a column's type, renaming or dropping columns, or changing the row mode in
  place. These still need a new table.
- Prefix or wildcard selectors. Every content type is opted in explicitly.
- Converting, renaming, or copying rows between tables.
- Branching chains.

## Design decisions

- **Registry column outside the schema version.** Bumping the ClickHouse schema to V2 for
  one additive column would make processors from earlier releases refuse the database. The
  column is additive and idempotent, so it follows the insert deduplication precedent
  instead.
- **Mixed-version exporters.** Processors from earlier releases allow exactly one manifest
  digest per table and require the table name to equal the manifest name. They refuse to
  start once a chain registers a second manifest for a table they also write, or a chain
  manifest under their own exporter ID. Upgrade every exporter that shares a database
  before extending a chain.
- **Uniform output checks.** Writing `NULL` for omitted nullable columns applies to
  standalone manifests too. It only turns former `column_set_mismatch` failures into rows,
  and it keeps one rule for every table.
- **Failure codes.** Unknown keys and omitted required columns keep the existing
  `column_set_mismatch` code so dashboards keyed on it keep working.

## Testing

Unit tests (`internal/cmd/process/clickhouse/projection_chain_test.go`,
`processor_test.go`, `projection_schema_test.go`):

- Bundled manifests keep their pre-chain digests and table names.
- A three-manifest chain shares one table with the expected column order, row mode, and
  selector-to-logic routing. `projectionRegoReplaces` without a selector replaces shared logic; with a
  selector it splits one content type off.
- `projectionRegoFrom` resolves through other `projectionRegoFrom` references.
- Loading rejects later manifests that declare `columns`, `rows`, or `table`; missing
  previous manifests; branches; cycles; overlapping selectors; `projectionRego` with
  `projectionRegoFrom`; references outside the chain or later in it; non-nullable or
  duplicate added columns; resource mismatches; and two chains sharing a table.
- Memory chains share an attribute-mapping root through `projectionRegoFrom`, add columns
  to it, and split a kind off with `projectionRegoReplaces` and a selector.
- Output checks for unknown keys, omitted nullable columns, and omitted required columns.
- Projection failures from chain logic are recorded under the manifest whose logic ran.
- The processor writes every content type of a chain, and its tombstones, to the shared
  table.
- The inspection command's text and JSON output.

ClickHouse integration tests (`projection_chain_integration_test.go`). The package starts
one ClickHouse container with `internal/testutil/testclickhouse` on first use, so these
tests run in CI through `task test:go`:

- A chain extends a projection created before chains: the table keeps its rows, gains the
  column, registers previous manifests, routes new content types, replays existing rows with the
  replacing logic, passes `validate` mode (including retention), rejects removed and
  edited manifests, and purges hard-deleted rows from the shared table.
- `validate` mode lists missing columns and makes no changes.
- An existing added column with a different type is rejected before registration, and
  added columns of every projection type, including JSON columns whose parameters equal
  ClickHouse's defaults, are accepted when reopened in `manage` and `validate` mode.
- Withdrawing registrations removes only the calling exporter's rows.
- A memory chain splits a kind off with `projectionRegoReplaces` and a selector, and a replay rewrites
  only that kind's rows.
- Every exporter's registry must be a prefix of the configured chain: a shorter chain,
  a reordered chain, and a registry that skips a manifest are rejected.
- When another exporter registers a divergent extension between this exporter's
  registration and its re-check, this exporter withdraws its rows and the other
  configuration still starts.

The site's analytics doc scenario (`GODOG_TAGS='@analytics'`) loads the bundled manifests
through `ProjectionSet.Match` and runs the documented registry query, including
`previous`.
- A fresh install with `spec.table` and `rows: many` applies the full chain in order, and a
  replay fills the table from entries of several content types.
- A registry without the `previous` column fails `validate` mode and is upgraded by
  `manage` mode without a schema checksum change.

## Tasks

- [x] Parse and validate `table`, `previous`, `projectionRegoReplaces`, `projectionRegoFrom`, and
      `addColumns` without changing existing digests.
- [x] Link manifests into chains and build selector routing.
- [x] Check output against the table's combined columns.
- [x] Create shared tables, add columns, and validate the registry prefix.
- [x] Add the registry `previous` column and accept chain registry rows.
- [x] Read registered projections once per table for views, retention, deduplication, and
      purge.
- [x] Add the `clickhouse-projections` inspection command.
- [x] Unit and ClickHouse integration tests.
- [x] Update Enhancement 404 and the site documentation.

## Files to Modify

| File | Change |
| --- | --- |
| `internal/cmd/process/clickhouse/projection.go` | Chain manifest fields, validation, linking, routing, and output checks. |
| `internal/cmd/process/clickhouse/projection_schema.go` | Per-table schema management, registry prefix check, column addition, registry column. |
| `internal/cmd/process/clickhouse/sink.go` | Keep the loaded projection tables. |
| `internal/cmd/process/clickhouse/processor.go` | Route resources through the chain's selector map. |
| `internal/cmd/process/clickhouse/projections_command.go` | `clickhouse-projections` inspection command. |
| `internal/cmd/process/command.go` | Register the inspection command. |
| `internal/cmd/process/clickhouse/*_test.go` | Unit and integration tests. |
| `internal/sitebdd/steps_clickhouse.go` | Route doc fixtures through `ProjectionSet.Match`; start ClickHouse with the shared test helper. |
| `internal/testutil/testclickhouse/` | Shared ClickHouse test container, pinned to the `compose.yaml` image. |
| `docs/enhancements/implemented/404-clickhouse-analytics-export.md` | Point the projection section at chains. |
| `site/src/pages/docs/analytics/projections.mdx` | Document chains and output checks. |
| `site/src/pages/docs/analytics/clickhouse/operations.mdx` | Schema management, inspection, and replay for chains. |
| `site/src/pages/docs/analytics/index.mdx` | Chains and `spec.table` in the relation overview. |
| `site/src/pages/docs/analytics/querying.mdx` | Registry query shows `previous`. |
| `site/src/pages/docs/analytics/clickhouse/configuration.mdx` | The `clickhouse-projections` command. |

## Verification

```bash
go build ./...
go vet ./internal/cmd/process/...
# Starts ClickHouse with testcontainers (Docker required).
go test ./internal/cmd/process/... ./internal/testutil/testclickhouse -count=1
(cd site && npm run build)
GODOG_TAGS='@analytics' CGO_ENABLED=1 go test -race -tags='site_tests sqlite_fts5' -timeout 20m \
  ./internal/sitebdd/ -run TestSiteDocs -count=1
```
