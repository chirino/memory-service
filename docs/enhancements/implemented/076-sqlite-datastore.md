---
status: implemented
---

# Enhancement 076: SQLite Datastore and Vector Store

> **Status**: Implemented.

## Summary

Add a SQLite datastore implementation for the Go memory-service, using the same GORM-oriented architecture as PostgreSQL wherever the query logic is portable, while keeping schema, migration, FTS, and vector details SQLite-specific.

SQLite mode will default to a filesystem-backed attachment store exposed as `attachments-kind=fs`, and will support both conversation semantic search and episodic memory vector search using `vector-kind=sqlite`.

## Motivation

PostgreSQL is the only SQL datastore in the Go server today. That is the right default for multi-user production deployments, but it is heavier than necessary for:

1. Local development and demos.
2. Single-node embedded deployments.
3. CI/test setups that do not need a separate database container.
4. Users who want a simple file-backed persistence option without introducing MongoDB.

The current Go implementation already has a large amount of SQL-domain logic that should not be rewritten twice:

1. Conversation, entry, membership, transfer, eviction, and task-queue flows are already expressed through GORM-friendly models and query composition.
2. The episodic store has a SQL-backed PostgreSQL implementation whose write semantics should remain aligned with any SQL sibling backend.
3. The BDD harness is already organized around “one backend runner + one `TestDB` adapter”, which is a good fit for adding SQLite parity coverage.

At the same time, SQLite is not “Postgres in a file”. It needs its own design for:

1. Schema DDL and migrations.
2. Full-text search.
3. Vector search.
4. Attachment blob storage.
5. Runtime/build tooling if we use a CGO-backed driver.

## Design

### Scope

This enhancement adds a new SQL backend, not a separate product tier. SQLite should implement the same externally visible Memory Service behavior as the PostgreSQL backend for:

1. REST and gRPC APIs for conversations, entries, memberships, forking, transfers, admin APIs, attachments metadata, eviction, and task queue.
2. Conversation full-text search.
3. Conversation semantic search.
4. Episodic memory storage, semantic search, TTL, and usage counters.

SQLite-specific feature coverage in this enhancement focuses on REST scenarios. Dedicated SQLite gRPC BDD scenarios are not required.

### Architecture Overview

Add the following new plugins and supporting packages:

| Area | New package(s) | Purpose |
|------|----------------|---------|
| Datastore | `internal/plugin/store/sqlite` | SQLite `MemoryStore` + `EpisodicStore` plugins |
| Shared SQL/GORM layer | `internal/plugin/store/sqlgorm` | Portable SQL-store helpers shared by PostgreSQL and SQLite |
| Attachment store | `internal/plugin/attach/filesystem` | Filesystem-backed blob store used by SQLite deployments |
| Vector store | `internal/plugin/vector/sqlitevec` | SQLite-backed conversation vector store |
| Test helpers | `internal/testutil/testsqlite` | Temp DB/bootstrap helpers for unit + BDD tests |

The intended split is:

1. `sqlgorm` owns shared GORM-heavy business logic that is dialect-portable:
   - model loading/mapping
   - access-control joins
   - pagination/cursor helpers
   - common transactional flows
   - encryption/cache plumbing used by SQL stores
2. `postgres` and `sqlite` each own:
   - connection setup
   - dialect-specific raw SQL
   - schema SQL and migration registration
   - FTS/vector implementation details
   - backend-specific performance tuning

This should be an extraction, not a forced abstraction. If a query becomes less clear when generalized, keep it in the backend package.

### SQLite Engine Selection

Use:

1. `gorm.io/driver/sqlite` as the GORM dialector.
2. `github.com/mattn/go-sqlite3` as the actual SQLite driver.
3. `sqlite-vec` for vector functions/search.

This means runtime Go builds must support CGO.

#### Why this choice

1. The service already uses GORM heavily on the PostgreSQL path, so the SQLite backend should stay in the same ORM/tooling family.
2. The official GORM SQLite driver is the natural fit for sharing code with the PostgreSQL GORM store.
3. `go-sqlite3` already exposes SQLite build tags such as `fts5`, which we need for full-text search.
4. `sqlite-vec` provides official Go bindings for the `mattn/go-sqlite3` driver.

#### Packaging rule

Pin exact versions of the SQLite driver and `sqlite-vec` bindings in `go.mod`, and wrap extension registration behind a small internal package. `sqlite-vec` Go bindings are still pre-v1, so we should keep the dependency surface narrow.

### Configuration Surface

Add/clarify the following configuration:

| Setting | Example | Notes |
|--------|---------|------|
| `MEMORY_SERVICE_DB_KIND` | `sqlite` | Selects the SQLite datastore |
| `MEMORY_SERVICE_DB_URL` | `file:/tmp/memory-service.sqlite` | SQLite DSN/file path |
| `MEMORY_SERVICE_ATTACHMENTS_KIND` | `fs` | SQLite defaults to the filesystem attachment store when omitted |
| `MEMORY_SERVICE_ATTACHMENTS_FS_DIR` | `/tmp/memory-service-attachments` | Optional override for the attachment root |
| `MEMORY_SERVICE_VECTOR_KIND` | `sqlite` | Enables SQLite-backed semantic search |

Behavioral rules:

1. When `db-kind=sqlite` and `attachments-kind` is omitted, the server should default the attachment store to `fs`.
2. When `db-kind=sqlite` and `attachments-fs-dir` is omitted, the server should derive the attachment directory from the DB path. For example, `path/to/example.db` should use `path/to/example.db.attachments`.
3. DB-path derivation must follow the `go-sqlite3` DSN rules:
   - for plain filenames, use the substring before the first `?`
   - for `file:` URIs, parse the SQLite URI and extract the underlying local file path
4. If DB-path derivation fails, or if the DB URL is not file-backed (for example `:memory:`, `file::memory:`, or `mode=memory`) and no explicit `attachments-fs-dir` override is set, startup must fail with a clear error.
5. The derived attachment directory must be created automatically on startup if it does not already exist.
6. When `db-kind=sqlite` and `attachments-kind=db`, `postgres`, `mongo`, or `filesystem`, the server should fail startup with a clear error that SQLite does not support DB-backed attachment storage and recommends `fs`.
7. `vector-kind=sqlite` is only valid with `db-kind=sqlite`.
8. In SQLite mode, `vector-kind=sqlite` enables both conversation semantic search and episodic-memory semantic search.
9. In SQLite mode, when `vector-kind` is empty or `none`, both conversation semantic search and episodic-memory semantic search are disabled.

### Runtime and Build Changes

SQLite support requires CGO at runtime, not only in tests.

Implementation work includes:

1. Switching the repo `Dockerfile` build stage from `CGO_ENABLED=0` to `CGO_ENABLED=1`.
2. Switching `deploy/dev/air.toml` from `CGO_ENABLED=0` to `CGO_ENABLED=1`.
3. Ensuring CI and local dev images include a compiler toolchain. The devcontainer already installs `build-essential`; runtime/container paths need equivalent support.

### Shared SQLite DB Handle Ownership

The SQLite process should open the database once and share that opened handle across:

1. the SQLite `MemoryStore`
2. the SQLite `EpisodicStore`
3. the `sqlite-vec` conversation vector store

This should be implemented as a shared SQLite DB provider in the SQL/GORM layer rather than having each plugin call `gorm.Open(...)` independently.

Why:

1. It gives the process one authoritative place to apply PRAGMAs and connection policy.
2. It avoids plugin-by-plugin pool drift against the same database file.
3. It makes transaction-scope and write-serialization behavior enforceable across the whole SQLite-backed stack.

The vector store should share this opened handle, but it does not need to join the `MemoryStore` / `EpisodicStore` tx-scope interfaces. Current vector-store call paths already occur outside store-method workflows (search handlers, background indexing, task processing), so vector operations should continue to manage their own local read/write transactions on the shared SQLite handle as needed.

### SQLite Connection Defaults

The SQLite store should set connection/session defaults appropriate for a single-file service:

1. `PRAGMA foreign_keys = ON`
2. `PRAGMA journal_mode = WAL`
3. `PRAGMA busy_timeout` to avoid immediate `database is locked` failures
4. Conservative pool sizing so the service does not open unnecessary concurrent writers

These settings belong in the SQLite plugin, not in a cross-dialect shared layer.

### Transaction Scopes and Write Serialization

SQLite cannot support arbitrary concurrent writers reliably. WAL improves read/write concurrency, but SQLite still allows only one writer at a time, and deferred transactions can fail when they try to upgrade from read to write under contention.

To make SQLite dependable, this enhancement should introduce an internal SQL-store transaction-scope layer in `sqlgorm`:

1. `InReadTx(ctx, fn)`
2. `InWriteTx(ctx, fn)`

These must not remain implementation-only helpers. They should be added to the store interfaces used by handlers so endpoint workflows can declare read or write intent before the first DB statement:

1. `registrystore.MemoryStore`
2. `registryepisodic.EpisodicStore`

That keeps the transaction boundary available at the route/service layer without exposing raw `*gorm.DB` handles.

Design rules:

1. All callers of SQLite `MemoryStore` and SQLite `EpisodicStore` methods must enter either `InReadTx` or `InWriteTx` before invoking store methods.
2. A workflow that may write must declare write intent before its first database statement.
3. SQLite write scopes should start with immediate write intent on the shared handle, using `BEGIN IMMEDIATE` semantics or the equivalent driver behavior.
4. SQLite read scopes should use the normal relaxed read path and must not silently upgrade into writes.
5. Nested `write inside read` must fail deterministically.
6. Nested `read inside write` and `write inside write` should reuse the outer write scope.
7. SQLite store implementations should assert that a transaction-scoped context is present on entry to store methods, so missing-scope bugs fail fast during development and testing.
8. This assertion requirement applies to SQLite `MemoryStore` and `EpisodicStore` methods, not to the separate `VectorStore` interface.

This is intentionally broader than SQLite-only plumbing. The same transaction-intent abstraction should be designed so PostgreSQL can later route read scopes to replicas and write scopes to the primary, but PostgreSQL read-replica routing is not required to ship SQLite support.

### Shared GORM Code Boundaries

The first implementation pass should extract only the code that is truly portable between PostgreSQL and SQLite.

Likely shared:

1. Conversation CRUD flows.
2. Membership and ownership transfer CRUD.
3. Entry append/list/query orchestration.
4. Admin list/get flows.
5. Attachment metadata CRUD.
6. Task queue CRUD and eviction orchestration.
7. Shared episodic-memory write/read semantics outside of search SQL.

Remain backend-specific:

1. Schema SQL and migrators.
2. Full-text search SQL.
3. Vector search SQL.
4. Any query that depends on PostgreSQL JSONB, tsvector, partitioning, or special operators.
5. Any query that depends on SQLite FTS5 virtual tables, JSON1 functions, or PRAGMA behavior.

### SQLite Schema

Create a dedicated embedded schema at `internal/plugin/store/sqlite/db/schema.sql`.

The schema should preserve the same logical entities as PostgreSQL, but use SQLite-native types and features:

1. Store UUIDs as `TEXT`.
2. Store encrypted payloads as `BLOB`.
3. Store JSON documents as `TEXT` using JSON1 functions where needed.
4. Use normal tables for `entries`, `attachments`, `tasks`, `memories`, and `memory_usage`.
5. Do not attempt PostgreSQL-style partitioning.
6. Do not attempt PostgreSQL large objects or chunk tables for attachments.

Migrations should be explicit SQL, not `AutoMigrate`, because the SQLite path needs dialect-specific DDL for:

1. FTS virtual tables and triggers.
2. `sqlite-vec` extension setup and vector tables.
3. Schema changes that would otherwise rely on GORM’s SQLite table-rebuild behavior.

### Full-Text Search

Use SQLite FTS5 for conversation search.

Design:

1. Keep `entries.indexed_content` as the canonical application column.
2. Add an `entries_fts` FTS5 table using `indexed_content` as the indexed document.
3. Maintain `entries_fts` with insert/update/delete triggers on `entries`.
4. Execute conversation search by querying the FTS table, then joining back to `entries`/`conversations`/membership filters.
5. Preserve highlight generation so API responses continue returning highlighted snippets.

This keeps the public search API unchanged while making the indexing mechanism explicitly SQLite-native.

### Vector Store Design

#### Conversation vectors

Add a new SQLite vector plugin, registered as `sqlite` and implemented with `sqlite-vec`, for conversation semantic search:

1. Schema lives in `internal/plugin/vector/sqlitevec/db/schema.sql`.
2. The plugin stores vectors in an `entry_embeddings` table in the same SQLite database file.
3. Rows keep `entry_id`, `conversation_id`, `conversation_group_id`, `embedding`, `model`, and `created_at`.
4. Upserts use SQLite `ON CONFLICT`.

#### Episodic memory vectors

The SQLite episodic store also stores memory vectors in the same DB file, mirroring the PostgreSQL `memory_vectors` concept with SQLite types.

#### Regular-table vectors, not `vec0`

Use regular SQLite tables plus `sqlite-vec` scalar/distance functions for the first implementation instead of `vec0` virtual tables.

Reason:

1. Conversation search already needs flexible ACL filtering with `conversation_group_id IN (...)`.
2. Episodic search needs namespace-prefix and attribute filtering before/alongside vector scoring.
3. `sqlite-vec`’s own documentation says regular-table queries are more flexible, while `vec0` is optimized but less flexible.

That flexibility is more important than ANN optimization in the first SQLite implementation.

Representative search pattern:

```sql
SELECT entry_id,
       conversation_id,
       1 - vec_distance_cosine(embedding, :query_embedding) AS score
FROM entry_embeddings
WHERE conversation_group_id IN (...)
ORDER BY vec_distance_cosine(embedding, :query_embedding)
LIMIT :limit;
```

The same principle applies to `memory_vectors`, with namespace and attribute filters included in the `WHERE` clause.

### Filesystem Attachment Store

Add `internal/plugin/attach/filesystem` with plugin name `fs` for SQLite attachment storage.

Design requirements:

1. Blob metadata remains in the normal `attachments` table; the filesystem store only owns bytes on disk.
2. Each attachment is written atomically by streaming into a temp file and renaming into place.
3. Storage keys are opaque and path-safe.
4. When `attachments-fs-dir` is unset in SQLite mode, the root directory is derived from the resolved DB file path by appending `.attachments`.
5. If the DB URL cannot be resolved to a file path and no explicit `attachments-fs-dir` is set, startup fails.
6. The root directory is created automatically on startup if it does not exist.
7. Files are stored under the root directory with sharding to avoid huge flat directories.
8. `GetSignedURL` returns unsupported/nil so download URLs continue to proxy through the service token route.
9. The existing attachment encryption wrapper continues to work unchanged on top of the filesystem store.

Non-goal: storing attachment bytes in SQLite BLOB columns.

### Migration Registration and Command Wiring

Implementation should register migrators in this order:

1. SQLite base schema migrator.
2. SQLite vector schema migrator.

`serve` and `migrate` commands must import:

1. `internal/plugin/store/sqlite`
2. `internal/plugin/vector/sqlitevec`
3. `internal/plugin/attach/filesystem`

### Testing

#### 1. Shared SQL-store unit tests

Add table-driven tests around the extracted `sqlgorm` layer so the same behavioral assertions can run against PostgreSQL and SQLite for portable logic:

1. conversation creation/forking
2. membership access checks
3. append/list entry flows
4. task queue CRUD
5. attachment metadata lifecycle

The goal is to keep “portable logic drift” between PostgreSQL and SQLite visible without running the full BDD suite for every small change.

#### 2. SQLite-specific unit/integration tests

Add focused tests for SQLite-only behavior:

1. schema migration creates the expected tables, FTS tables, and triggers
2. `PRAGMA foreign_keys` and WAL mode are enabled
3. `entries_fts` stays in sync on insert/update/delete
4. `sqlite-vec` extension registration succeeds before vector queries run
5. vector upsert/search/delete works for `entry_embeddings`
6. episodic `memory_vectors` search respects namespace-prefix and attribute filters
7. filesystem attachment store streams, hashes, deletes, and proxies downloads correctly
8. encrypted filesystem attachments round-trip correctly
9. concurrent write workflows use explicit write scopes and do not regress into lock-upgrade flakes
10. `write inside read` scope violations fail deterministically
11. SQLite store methods fail fast when called without a transaction-scoped context
12. derived attachment directories are created automatically next to the DB file when no override is set
13. startup fails clearly when the SQLite DB URL cannot be resolved to a file path for attachment-root derivation and no explicit override is set

#### 3. BDD harness

Add a SQLite BDD runner family that mirrors the existing backend-specific runners:

| Test | Purpose |
|------|---------|
| `TestFeaturesSQLite` | Shared REST feature suite against SQLite + filesystem attachments |
| `TestFeaturesSQLiteVec` | Semantic conversation and episodic-memory search against SQLite + the `sqlite` vector backend |
| `TestFeaturesSQLiteEncrypted` | Encrypted attachment/file-store scenarios |
Harness rules:

1. Each runner creates an isolated temp root.
2. The SQLite DB file and filesystem attachment directory live under that temp root.
3. The runner injects a new `SQLiteTestDB` into the Godog suite, mirroring `PostgresTestDB` and `MongoTestDB`.
4. SQLite runners should use `cache-kind=none` by default; cache integration remains a separate test matrix.
5. Shared feature directories remain the default source of truth:
   - `internal/bdd/testdata/features`
   - `internal/bdd/testdata/features-encrypted`
6. Add a new SQLite vector-specific feature directory, for example `internal/bdd/testdata/features-sqlite`, derived from the existing `features-qdrant` coverage.

#### 4. SQL assertion compatibility in BDD

Shared feature files already contain SQL assertions. Rewriting the full suite into backend-specific SQL would create avoidable duplication.

`SQLiteTestDB.ExecSQL` should therefore support a small compatibility layer for the SQL actually used in shared feature files, for example:

1. translate simple PostgreSQL JSON extraction (`task_body->>'conversationGroupId'`) into SQLite JSON1 (`json_extract(...)`)
2. normalize timestamp formatting to RFC3339 for assertion parity
3. return plain scalar values in the same map shape expected by existing step helpers

This compatibility layer should stay intentionally small. If shared feature SQL starts diverging materially, create SQLite-specific feature files instead of building a generic SQL transpiler.

#### 5. Semantic-search BDD coverage

Add SQLite-native semantic-search scenarios that prove:

1. indexing a conversation entry produces a searchable vector row
2. combined `["semantic","fulltext"]` search works with per-type limits
3. ACL filtering is applied inside SQLite vector queries
4. vector delete tasks remove conversation vectors after eviction
5. episodic memory search does not leak across adjacent namespaces

The existing Mongo+Qdrant semantic features are a good baseline, but the SQLite suite must verify the SQLite implementation directly rather than assuming parity from another backend.

#### 6. Test matrix expectations

The implementation is not complete until the SQLite backend passes:

1. the shared REST feature set
2. encrypted attachment feature coverage
3. SQLite vector-specific BDD scenarios
4. targeted unit/integration tests for FTS, vector, migrations, and filesystem attachments

## Design Decisions

### Decision: Use CGO-backed SQLite

Accepted.

Reason:

1. It aligns with the official GORM SQLite path.
2. It gives straightforward access to SQLite build options such as FTS5.
3. It integrates with the official Go bindings published for `sqlite-vec`.

### Decision: Keep schema and migration SQL fully dialect-specific

Accepted.

Reason:

1. PostgreSQL and SQLite diverge materially on DDL, FTS, vector support, and blob storage.
2. Sharing migrations would either force lowest-common-denominator SQL or hide too much behavior in application code.

### Decision: Use explicit read/write transaction scopes in the shared SQL layer

Accepted.

Reason:

1. SQLite write workloads need write intent declared before the first statement to avoid deferred lock-upgrade failures.
2. A shared transaction-scope abstraction can later support PostgreSQL primary/replica routing without changing handler-level workflow code again.
3. This keeps SQLite concurrency policy enforceable even though the store, episodic store, and vector store share one opened DB handle.
4. Putting the scope API on the store interfaces allows endpoint handlers and other callers to define the outer workflow boundary before the first store call.
5. Requiring SQLite store methods to assert tx-scoped contexts prevents accidental unscoped access paths from reintroducing lock-upgrade bugs.
6. The separate vector-store interface should keep local transaction handling because its current call paths are not nested under store-method workflows.

### Decision: Use a filesystem attachment store for SQLite

Accepted.

Reason:

1. It avoids large BLOB writes in the SQLite database file.
2. It keeps the DB file smaller and operationally simpler.
3. It matches the user expectation that SQLite mode is file-based without making the DB itself hold attachment payloads.

## Non-Goals

1. Matching PostgreSQL write concurrency or scale characteristics.
2. Storing attachment bytes inside SQLite.
3. Implementing an ANN-optimized `vec0` path in the first SQLite release.
4. Providing a non-CGO SQLite runtime in the first iteration.
5. Eliminating all backend-specific SQL from the codebase.
6. Adding SQLite-specific gRPC feature scenarios in this enhancement.

## Security Considerations

1. Filesystem attachments must use opaque storage keys and path sanitization so user input never controls filesystem paths.
2. The existing encrypted attachment wrapper must remain supported in SQLite mode.
3. SQLite download URLs should continue using signed token-proxy routes rather than exposing raw file paths.
4. ACL filtering for semantic search must happen in the SQLite query itself; do not fetch unfiltered vector results and trim them in application code.

## Open Questions

1. Should SQLite expose an admin/debug metric for current WAL/checkpoint state, or is normal logging/metrics enough for the first release?

## Tasks

Current implementation note: the SQLite store package now covers both REST and gRPC transaction-scoped access paths, `internal/cmd/serve/serve.go` imports the SQLite datastore plugin, the SQLite BDD runners pass, and the site docs now document SQLite datastore/vector/attachment configuration.

- [x] Add SQLite datastore plugin under `internal/plugin/store/sqlite`.
- [x] Keep cross-cutting SQLite transaction-scope and shared-handle logic in reusable helpers without forcing a large premature `sqlgorm` extraction. The standalone `sqlgorm` package was deferred in the implemented slice.
- [x] Add shared SQL transaction scopes (`InReadTx` / `InWriteTx`) to `registrystore.MemoryStore` and `registryepisodic.EpisodicStore`, and use them to declare SQLite write intent before the first statement.
- [x] Add SQLite episodic store support in the same plugin family.
- [x] Add SQLite schema SQL and migrator registration.
- [x] Add SQLite FTS5 tables/triggers and search queries.
- [x] Add `sqlite-vec` conversation vector plugin and schema.
- [x] Add SQLite `memory_vectors` support for episodic semantic search.
- [x] Add filesystem attachment store plugin and config.
- [x] Default SQLite to `attachments-kind=fs` when the setting is omitted.
- [x] Parse SQLite DB URLs according to `go-sqlite3` DSN rules so file-backed DB paths can be resolved reliably.
- [x] Derive the SQLite filesystem attachment directory from the resolved DB path when `attachments-fs-dir` is omitted, and auto-create it on startup.
- [x] Fail startup when SQLite attachment-root derivation is requested but the DB URL cannot be resolved to a file path.
- [x] Validate at startup that explicit `attachments-kind` values other than `fs` are rejected for SQLite with an actionable error message recommending `fs`.
- [x] Update `serve` and `migrate` command imports/validation for SQLite plugins.
- [x] Update runtime build paths (`Dockerfile`, `deploy/dev/air.toml`) for CGO.
- [x] Ensure the SQLite store, episodic store, and vector store share one opened DB handle per process.
- [x] Update REST callers of `MemoryStore` and `EpisodicStore` to enter explicit read/write scopes before invoking SQLite-backed store methods. First implementation slice: conversations, memberships, transfers, memories, search, entries, attachments, and admin REST handlers.
- [x] Make SQLite store methods assert that a transaction-scoped context is present.
- [x] Keep `VectorStore` transaction handling local to the vector plugin while sharing the same opened SQLite handle.
- [x] Add SQLite unit/integration tests for migrations, FTS, vectors, and attachments.
- [x] Add SQLite BDD runners and `SQLiteTestDB`.
- [x] Add SQLite vector-specific feature coverage.
- [x] Document SQLite configuration and operational caveats in the site docs.

## Files to Modify

| File | Purpose |
|------|---------|
| `docs/enhancements/implemented/076-sqlite-datastore.md` | This enhancement plan |
| `go.mod` | Add SQLite driver and `sqlite-vec` dependencies |
| `go.sum` | Dependency lockfile updates |
| `internal/plugin/store/sqlgorm/*` | New shared SQL/GORM layer |
| `internal/plugin/store/sqlgorm/tx*` | Shared read/write transaction scope support |
| `internal/plugin/store/postgres/postgres.go` | Extract shared logic into `sqlgorm` |
| `internal/plugin/store/postgres/episodic_store.go` | Extract shared episodic SQL logic where appropriate |
| `internal/plugin/store/sqlite/sqlite.go` | New SQLite `MemoryStore` plugin |
| `internal/plugin/store/sqlite/episodic_store.go` | New SQLite episodic store plugin |
| `internal/plugin/store/sqlite/db/schema.sql` | SQLite base schema |
| `internal/plugin/vector/sqlitevec/sqlitevec.go` | New SQLite vector plugin |
| `internal/plugin/vector/sqlitevec/db/schema.sql` | SQLite vector schema |
| `internal/plugin/vector/sqlitevec/sqlitevec_test.go` | SQLite vector plugin tests |
| `internal/plugin/attach/filesystem/filesystem.go` | New filesystem attachment store |
| `internal/plugin/attach/filesystem/filesystem_test.go` | Filesystem attachment store tests |
| `internal/txscope/txscope.go` | Shared transaction-intent context marker |
| `internal/plugin/route/routetx/routetx.go` | Gin helpers that apply read/write store scopes |
| `internal/config/config.go` | New config fields/defaults as needed |
| `internal/config/compat.go` | Env-var compatibility wiring for filesystem attachment settings |
| `internal/cmd/serve/serve.go` | Plugin imports, config flags, validation |
| `internal/cmd/migrate/migrate.go` | Plugin imports and SQLite migration support |
| `internal/plugin/store/metrics/metrics.go` | Forward transaction-scope methods through store wrappers |
| `internal/plugin/route/**` | Enter read/write scopes around REST workflows that call store methods |
| `Dockerfile` | Enable CGO in production image builds |
| `deploy/dev/air.toml` | Enable CGO in local live-reload builds |
| `internal/testutil/testsqlite/*` | SQLite test bootstrap helpers |
| `internal/bdd/testdb_sqlite.go` | SQLite `TestDB` adapter for BDD |
| `internal/bdd/cucumber_sqlite_test.go` | Main SQLite BDD runner |
| `internal/bdd/cucumber_sqlite_encrypted_test.go` | SQLite encrypted attachment runner |
| `internal/bdd/cucumber_sqlite_vec_test.go` | SQLite vector-search runner |
| `internal/bdd/testdata/features-sqlite/*` | SQLite semantic-search BDD scenarios |
| `site/src/pages/docs/configuration.mdx` | SQLite datastore and filesystem attachment docs |
| `internal/FACTS.md` | Record SQLite/CGO and BDD harness facts as implementation lands |

## Verification

```bash
# Compile the Go implementation, including CGO-backed SQLite code.
go build ./...

# Run targeted unit/integration coverage for SQLite packages.
go test ./internal/plugin/store/sqlite/... ./internal/plugin/vector/sqlitevec/... ./internal/plugin/attach/filesystem/... -count=1

# Validate CGO-backed runtime build paths that SQLite depends on.
docker build -t memory-service-sqlite-test -f Dockerfile .
CGO_ENABLED=1 go build -o ./bin/memory-service .

# Run shared/backend BDD coverage.
go test ./internal/bdd -run 'TestFeaturesSQLite|TestFeaturesSQLiteVec|TestFeaturesSQLiteEncrypted' -count=1 > sqlite-bdd.log 2>&1

# Inspect failures without losing earlier context.
rg -n "FAIL|panic:|--- FAIL|Error:" sqlite-bdd.log
```
