---
status: complete
---

# Enhancement 065: Port memory-service to Go

> **Status**: Complete — Gate 1: 325 scenarios on postgresql+pgvector. Gate 2: 327 scenarios on mongodb+qdrant (REST + gRPC + encrypted + task-queue). All phases done, 0 failures, 0 skipped.

## Summary

Replace the Quarkus/Java `memory-service` implementation with an idiomatic Go service that exposes the same REST (Agent + Admin) and gRPC APIs, uses the same PostgreSQL schema, and maintains feature parity with the existing Java implementation across all supported datastores.

## Motivation

- **Smaller footprint**: Go produces a single static binary (~15–30 MB) vs. a Quarkus JVM image (~300+ MB). The native Quarkus build is closer, but still requires GraalVM tooling.
- **Faster cold start**: Go services start in milliseconds, important for serverless/container-per-request deployments.
- **Simpler deployment**: No JVM, no GraalVM, no Maven. `go build` → ship the binary.
- **Go ecosystem alignment**: The project already has Python and Java integrations; Go is a common language for infrastructure and platform teams. A Go service enables a Go SDK/integration.
- **Lower operational complexity**: Fewer moving parts (no Quarkus extensions, CDI, Panache, etc.).
- **Pre-release freedom**: Datastores are reset frequently; no backward compatibility required.

Current implementation (`memory-service/`) is ~177 Java source files across 14 packages with heavy framework dependencies (Quarkus, Panache, Hibernate, LangChain4j).

## Non-Goals

- No feature additions beyond parity with the Java implementation.
- The Java `memory-service` may be kept alongside the Go port until validated; deletion is a separate step.
- No MiniLM local embedding parity in Go: LangChain4j ONNX runtime path is out of scope; use OpenAI or disabled embeddings.

## Non-Functional Requirements

These constraints apply across all phases and must not be violated during implementation.

### Streaming I/O — no full loads into memory

File attachments and gRPC byte streams must never be fully buffered in memory:

| Operation | Requirement |
|-----------|-------------|
| HTTP attachment upload (`POST /v1/attachments`, multipart) | Read `multipart/form-data` as a stream and enforce max size during copy/store; backend may spool to temp file (for seekable retry-safe S3 uploads) but must not buffer the full payload in memory |
| HTTP attachment download (`GET /v1/attachments/download/...`) | Stream bytes from the attachment store reader to the HTTP response writer via `io.Copy`; for DB-backed stores, temp-file spool is allowed to release DB cursor/connection resources before client drain |
| HTTP source URL attachment ingest (`POST /v1/attachments`, JSON `sourceUrl`) | Download into a bounded temp file and then store from a rewinded stream; do not `io.ReadAll` remote bodies |
| gRPC `AttachmentsService.UploadAttachment` (client-streaming) | Receive `UploadAttachmentRequest` chunks one at a time; write each chunk to the store incrementally; do not collect all chunks before writing |
| gRPC `AttachmentsService.DownloadAttachment` (server-streaming) | Read the attachment store in fixed-size chunks; send one `DownloadAttachmentResponse` per chunk; do not buffer the full file before sending |
| gRPC `ResponseRecorderService.Record` / `Replay` | Relay streaming LLM tokens chunk-by-chunk through Redis; never accumulate the full response before forwarding |
| MongoDB document reads with large `content` fields | Use projection to exclude `content` in list queries; only fetch full content when explicitly requested |

Violating any of these will cause memory spikes proportional to attachment size and must be caught in code review.

### Bounded request bodies

The HTTP server must enforce a configurable max body size (default 10 MB, mirrors `quarkus.http.limits.max-body-size`) to prevent resource exhaustion. Apply at the gin middleware layer before handler dispatch. Exception: streaming multipart uploads on `POST /v1/attachments` are exempt from the global wrapper because that route streams request bytes directly to the attachment store and enforces its own bounded size during store writes.

### Graceful shutdown

On `SIGTERM` / `SIGINT`:
1. Stop accepting new connections.
2. Wait for in-flight HTTP and gRPC requests to complete (with a configurable drain timeout, default 30 s).
3. Flush and close background goroutines (indexing task, eviction task).
4. Close DB and Redis connections.

### Database connection pooling

The GORM + pgx Postgres pool and the MongoDB client pool must be bounded. Expose the pool size as CLI flags. The Prometheus `memory_service_db_pool_open_connections` gauge must reflect live pool utilization.

### Partition-aware Postgres queries

All queries against the `entries` table must include `conversation_group_id` in the `WHERE` clause or join condition to enable partition pruning. Any query that omits it must be treated as a bug.

### No silent error swallowing

All `error` returns must be either returned to the caller, wrapped with context (`fmt.Errorf("...: %w", err)`), or logged at `ERROR` level with a full stack trace. `_ = err` assignments are forbidden except in `defer` cleanup paths where the error is genuinely unactionable.

### Data integrity invariants

- Every conversation must belong to exactly one `conversation_group`; `conversation_group_id` must be set on every conversation row. [001]
- Forking is only permitted on `HISTORY` channel entries originating from a `USER` role. Forks on other channels or roles are rejected with 422. [001]
- Individual entries cannot be deleted; they are only removed via cascade when the parent conversation/group is deleted. [001]
- Entries are immutable once created — no update path. New content requires a new entry. [001, 021]
- All user-facing read queries must include `WHERE deleted_at IS NULL`. Soft-deleted rows must be invisible to the Agent API and return 404. [013]
- Hard deletes (eviction) must rely on `ON DELETE CASCADE` at the Postgres level, or explicit ordered deletion (entries → conversations → memberships → transfers → groups) for MongoDB. [016]
- Vector store index entries for a conversation must be queued for cleanup **before** the conversation group is hard-deleted, not after. [015, 016]

### Field-size and validation constraints

All inputs are validated on entry; violations return HTTP 400 with field-level constraint details. [056]

| Field | Constraint |
|-------|-----------|
| Conversation title | Max 500 characters |
| `content_type` | Max 127 characters; must match MIME type pattern |
| `indexed_content` | Max 100 000 characters |
| Conversation `metadata` | Max 50 keys; total serialized size ≤ 16 KB |
| `user_id` / `client_id` | Max 255 characters each |
| Search query string | Max 1 000 characters |
| List endpoint `limit` | Max 200 for Agent API, configurable up to 1 000 for Admin API |

### Access control invariants

- Four access levels in ascending order: `READER < WRITER < MANAGER < OWNER`. Operations are gated at the minimum required level. [001, 024]
- Access control is enforced at the `conversation_group` level — all conversations in a fork tree share one permission set. [001]
- Return **404** (not 403) for resources the authenticated user cannot see. Return 403 only for explicit action-denial (e.g. attempting a write with READER access). [013, 014]
- `MEMORY` channel entries are scoped per `client_id`; each agent only receives its own memory entries when listing. [007]
- Agent-originated entries must have `client_id` set; user-originated entries have `client_id = null`. [007]

### Admin audit logging

Every call to an Admin API endpoint must be written to the admin audit log with: caller identity, resolved role, HTTP method + path, target resource, and optional justification string. When `require-justification` is enabled, requests without a justification field return 400. API keys must never appear in audit logs. [014]

### Pagination invariants

- All list endpoints use opaque cursor-based pagination (base64 or UUID cursors). Offset-based pagination is not used. [061]
- The same cursor value must return the same page regardless of concurrent modifications. [061]
- Results are ordered by `created_at ASC` for determinism unless otherwise specified. [061]
- `afterCursor: null` in responses is valid; clients must tolerate additive pagination fields without erroring. [CLAUDE.md]

### gRPC status code mapping

gRPC services must map domain errors to gRPC status codes consistently: [003]

| HTTP equivalent | gRPC status |
|---|---|
| 400 Bad Request | `INVALID_ARGUMENT` |
| 401 Unauthorized | `UNAUTHENTICATED` |
| 403 Forbidden | `PERMISSION_DENIED` |
| 404 Not Found | `NOT_FOUND` |
| 409 Conflict | `ALREADY_EXISTS` or `FAILED_PRECONDITION` |
| 422 Unprocessable | `FAILED_PRECONDITION` |
| 500 Internal | `INTERNAL` |

### Background task queue invariants

- Tasks are claimed with `FOR UPDATE SKIP LOCKED` (Postgres) or `findOneAndUpdate` with a `processingAt` timestamp (MongoDB) so multiple replicas can safely process concurrently without a distributed lock. [015]
- Singleton tasks are identified by a non-null `task_name`; re-inserting a task with the same name is a no-op (idempotent). [015]
- Failed tasks are retried after a configurable delay (default 10 minutes). [015]
- Vector store cleanup is always enqueued as an async task, never executed synchronously during a delete operation. [015, 016]

### Eviction / hard-delete behaviour

- Eviction processes soft-deleted records in configurable batches (default 1 000 rows) with a configurable inter-batch delay (default 100 ms) to reduce lock contention. [016]
- Eviction and normal user queries operate on disjoint row sets (`deleted_at IS NULL` vs `deleted_at IS NOT NULL`) — eviction cannot block reads. [016]
- The `POST /v1/admin/evict` endpoint supports `Accept: text/event-stream` for SSE progress streaming on long-running evictions. [016]

### Encryption at rest

The following fields must be encrypted at the storage layer (transparent to the API layer): [002, 013]
- Conversation `title`
- Entry `content`

`indexed_content` (full-text search content) is intentionally stored in plaintext so indexing/search can work.
`SUMMARY` channel content is no longer part of the model.

Encryption keys must not be logged or exposed in API responses.

### Response resumer invariants

- Replay always streams from offset 0; partial resume is not supported. [010]
- Cancel is idempotent — cancelling a non-existent or already-cancelled recording returns success. [004]
- The response resumer advertised address for multi-instance routing follows the priority: explicit config > request metadata header > local hostname. [005]

## Design

### Module layout

The Go module lives at the project root so users can `go install github.com/chirino/memory-service`.

```
./
├── main.go                    # entrypoint, wires everything together
├── go.mod
├── go.sum
├── Dockerfile
├── internal/
│   ├── pom.xml                # Maven wrapper that builds ../main.go via exec-maven-plugin
│   ├── cmd/
│   │   ├── serve/             # serve sub-command
│   │   ├── migrate/           # migrate sub-command
│   │   └── generate/          # //go:generate entry point (protoc + oapi-codegen)
│   │       └── main.go        # orchestrates all codegen; run via go generate ./...
│   ├── generated/             # GENERATED — do not edit by hand
│   │   ├── api/               # oapi-codegen output from openapi.yml (Agent API)
│   │   │   └── api.gen.go     # types + StrictServerInterface
│   │   ├── admin/             # oapi-codegen output from openapi-admin.yml (Admin API)
│   │   │   └── admin.gen.go   # types + StrictServerInterface
│   │   └── pb/                # protoc output from memory_service.proto
│   │       └── *.pb.go / *_grpc.pb.go
│   ├── registry/              # LEAF packages — plugin interfaces + Register()/Names()/Select()
│   │   ├── route/             # RouterLoader plugin registry (gin.Engine → error)
│   │   ├── migrate/           # Migrator plugin registry (runs migrations from all active plugins)
│   │   ├── store/             # MemoryStore plugin registry
│   │   ├── cache/             # Cache plugin registry
│   │   ├── attach/            # AttachmentStore plugin registry
│   │   ├── embed/             # Embedder plugin registry
│   │   ├── vector/            # VectorStore plugin registry
│   │   └── resumer/           # ResponseResumerStore plugin registry
│   ├── plugin/                # Implementations — each calls registry.Register() in init()
│   │   ├── route/
│   │   │   ├── conversations.go  # PLUGIN: implements gen/api StrictServerInterface (conversation routes)
│   │   │   ├── entries.go        # PLUGIN: implements gen/api StrictServerInterface (entry routes)
│   │   │   ├── memberships.go    # PLUGIN: Agent API membership routes
│   │   │   ├── transfers.go      # PLUGIN: Agent API ownership-transfer routes
│   │   │   ├── search.go         # PLUGIN: Agent API search routes
│   │   │   ├── attachments.go    # PLUGIN: Agent API attachment routes
│   │   │   ├── admin.go          # PLUGIN: implements gen/admin StrictServerInterface
│   │   │   └── system.go         # PLUGIN: /v1/health + /metrics routes
│   │   ├── store/
│   │   │   ├── postgres.go    # PLUGIN: Postgres MemoryStore; also registers Migrator (embeds schema.sql)
│   │   │   └── mongo.go       # PLUGIN: MongoDB MemoryStore; also registers Migrator (creates collections/indexes)
│   │   ├── cache/
│   │   │   ├── redis.go       # PLUGIN: Redis cache
│   │   │   └── noop.go        # PLUGIN: no-op cache
│   │   ├── attach/
│   │   │   ├── postgres.go    # PLUGIN: Postgres attachment store
│   │   │   ├── s3.go          # PLUGIN: S3 attachment store
│   │   │   └── encrypt.go     # PLUGIN: AES-GCM encryption wrapper
│   │   ├── embed/
│   │   │   ├── openai.go      # PLUGIN: OpenAI embedder
│   │   │   └── disabled.go    # PLUGIN: noop embedder
│   │   ├── vector/
│   │   │   ├── pgvector.go    # PLUGIN: pgvector VectorStore; also registers Migrator (embeds pgvector-schema.sql)
│   │   │   └── qdrant.go      # PLUGIN: Qdrant VectorStore; also registers Migrator (creates collection + index)
│   │   └── resumer/
│   │       ├── redis.go       # PLUGIN: Redis resumer store
│   │       ├── memory.go      # PLUGIN: in-memory resumer store (tests/dev)
│   │       └── noop.go        # PLUGIN: noop resumer store
│   ├── grpc/                  # gRPC service implementations (use gen/pb/ types)
│   ├── security/              # Auth gin middleware (OIDC JWT + API key)
│   ├── config/                # Config struct + env loading
│   ├── tempfiles/             # Temp-file helpers (create + delete-on-close readers)
│   ├── service/               # Background jobs (eviction, indexing)
│   └── model/                 # Shared domain types (Conversation, Entry, etc.)
```

### Plugin pattern

All swappable backends use the init-registration plugin pattern described in [docs/go-plugin-pattern.md](../go-plugin-pattern.md). Each pluggable layer has a **registry** (leaf package defining the interface + `Register()`/`Loaders()`), one or more **plugins** (implementations that call `Register()` in `init()`), and a **consumer** (the `serve` command) that blank-imports the desired plugin packages to activate them.

The eight pluggable layers and their registries:

| Registry package | Interface | Plugins |
|---|---|---|
| `internal/registry/route` | `RouterLoader` (`func(*gin.Engine) error`) | `plugin/route/conversations`, `entries`, `memberships`, `transfers`, `search`, `attachments`, `admin`, `system` |
| `internal/registry/migrate` | `Migrator` (`Migrate(ctx) error`) | registered by store and vector plugins in their own `init()` |
| `internal/registry/store` | `MemoryStore` | `plugin/store/postgres`, `plugin/store/mongo` |
| `internal/registry/cache` | `ConversationCache` / `MemoryEntriesCache` | `plugin/cache/redis`, `plugin/cache/infinispan`, `plugin/cache/noop` |
| `internal/registry/attach` | `AttachmentStore` | `plugin/attach/postgres`, `plugin/attach/s3`, `plugin/attach/encrypt` |
| `internal/registry/embed` | langchaingo `Embedder` | `plugin/embed/openai`, `plugin/embed/disabled` |
| `internal/registry/vector` | langchaingo `VectorStore` | `plugin/vector/pgvector`, `plugin/vector/qdrant` |
| `internal/registry/resumer` | `ResponseResumerStore` | `plugin/resumer/redis`, `plugin/resumer/memory`, `plugin/resumer/noop` |

**Migrations are pluggable.** Each store or vector plugin that owns schema registers a `Migrator` in the same `init()` call alongside its primary interface:
- `plugin/store/postgres` — embeds `db/schema.sql`, runs `golang-migrate` against Postgres
- `plugin/store/mongo` — creates collections and indexes via `go.mongodb.org/mongo-driver/v2`
- `plugin/vector/pgvector` — embeds `db/pgvector-schema.sql`, runs as a second `golang-migrate` source on Postgres
- `plugin/vector/qdrant` — creates the Qdrant collection and HNSW index if absent

The `migrate` sub-command blank-imports the same plugin set as `serve` and calls `registry/migrate.RunAll(ctx)`. No central migration file — each plugin owns its schema.

Backend plugins also register with a string `Name`. The registry exposes `Names()` — used to:
- **Discover valid choices** for config validation (`"unknown store; valid: postgres, mongo"`)
- **Populate `--help` text** with the live `strings.Join(registry.Names(), "|")` list
- **Select at runtime** by matching configured backend names (for example `MEMORY_SERVICE_DB_KIND`, `MEMORY_SERVICE_CACHE_KIND`, `MEMORY_SERVICE_ATTACHMENTS_KIND`) against registered names

Route plugins register with an `Order int` for deterministic mount sequence. The `serve` command calls `registry/route.RouteLoaders()` and iterates — exactly as shown in [docs/go-plugin-pattern.md](../go-plugin-pattern.md).

Adding a new API group or backend = one new plugin package + one blank import; no changes to registries or consumer core.

### Code generation

All generated code lives under `internal/generated/` and is committed to the repository. A single entry point regenerates everything:

```go
//go:generate go run ./internal/cmd/generate
```

Place this directive at the top of `main.go`. Run with:

```bash
go generate ./...
```

`internal/cmd/generate/main.go` orchestrates two code generation steps in order:

**1. OpenAPI types + server interfaces** (`oapi-codegen`):

```bash
oapi-codegen --config=internal/generated/api/cfg.yaml \
    memory-service-contracts/src/main/resources/openapi.yml

oapi-codegen --config=internal/generated/admin/cfg.yaml \
    memory-service-contracts/src/main/resources/openapi-admin.yml
```

Each `cfg.yaml` uses the `strict-server` and `gin` output targets:

```yaml
# internal/generated/api/cfg.yaml
package: api
generate:
  - strict-server   # generates StrictServerInterface with one method per operation
  - gin-server      # generates gin router registration shim
  - types           # generates request/response structs from OpenAPI schemas
  - spec            # embeds the raw spec (for /openapi.json endpoint)
output: internal/generated/api/api.gen.go
```

Route plugins (e.g. `internal/plugin/route/conversations.go`) implement the generated `api.StrictServerInterface`. The gin registration shim is wired once in `internal/cmd/serve/` — adding a new operation to `openapi.yml` and running `go generate` propagates the new method signature to every plugin that embeds the interface.

**2. gRPC stubs** (`protoc`):

```bash
protoc \
  --go_out=internal/generated/pb --go_opt=paths=source_relative \
  --go-grpc_out=internal/generated/pb --go-grpc_opt=paths=source_relative \
  memory-service-contracts/src/main/resources/memory/v1/memory_service.proto
```

`internal/grpc/` service implementations use the types from `internal/generated/pb/`.

**Tool installation** (`internal/cmd/generate/main.go` can install tools if absent):

```go
// Ensure tools are installed before generating
exec("go", "install", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest")
exec("go", "install", "google.golang.org/protobuf/cmd/protoc-gen-go@latest")
exec("go", "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest")
```

`internal/generated/` is committed — CI does not need codegen tools installed; contributors regenerate locally when the contracts change.

### Technology choices

| Concern | Library | Rationale |
|---------|---------|-----------|
| OpenAPI codegen | `github.com/oapi-codegen/oapi-codegen/v2` | Contract-first: generates Go types + `StrictServerInterface` + gin router shim from `openapi.yml`; run via `go generate ./...` |
| CLI / commands | `github.com/urfave/cli/v3` | Sub-command structure (`serve`, `migrate`, etc.) |
| HTTP router | `github.com/gin-gonic/gin` | Built-in binding/validation, rich middleware, less boilerplate across 47+ endpoints |
| ORM / queries | `gorm.io/gorm` + `gorm.io/driver/postgres` | GORM for Postgres model mapping and query generation |
| MongoDB | `go.mongodb.org/mongo-driver/v2` | Direct Mongo driver for MongoDB backend (no GORM abstraction) |
| OIDC / JWT | `github.com/coreos/go-oidc/v3` | Verify Bearer JWTs against an OIDC provider; extract `sub` as user ID |
| DB migrations | `github.com/golang-migrate/migrate/v4` | Runs existing SQL migration files unchanged |
| gRPC | `google.golang.org/grpc` | Standard; generate from existing `.proto` |
| Protobuf codegen | `google.golang.org/protobuf` + `protoc-gen-go` | Standard toolchain |
| Redis | `github.com/redis/go-redis/v9` | Cache + resumer location store |
| Embeddings + vector stores | `github.com/tmc/langchaingo` | Pluggable `Embedder` interface (OpenAI, noop) + `VectorStore` interface with pgvector and Qdrant backends; wrapped by `registry/embed` and `registry/vector` |
| Qdrant client | `github.com/qdrant/go-client/qdrant` | Underlying Qdrant gRPC client used by langchaingo's Qdrant vector store |
| pgvector | `github.com/pgvector/pgvector-go` | Postgres vector type support (used by langchaingo's pgvector store) |
| S3 | `github.com/aws/aws-sdk-go-v2` | Attachment file store |
| Metrics | `github.com/prometheus/client_golang` | Prometheus exposition; mirrors Java metrics |
| Validation | `github.com/go-playground/validator/v10` | Field constraints matching OpenAPI (also used by gin) |
| Logging | `github.com/charmbracelet/log` + `github.com/charmbracelet/lipgloss` + `github.com/charmbracelet/x/term` | Structured, styled logging with terminal detection |
| Crypto | stdlib `crypto/aes`, `crypto/sha256` | Attachment encryption (AES-GCM) |
| Testing | `github.com/stretchr/testify` | Assertions + test suites |
| Postgres (test) | `github.com/testcontainers/testcontainers-go/modules/postgres` | Spin up disposable Postgres containers (`pgvector/pgvector:pg18`) for unit/integration tests |
| Redis / Qdrant (test) | `github.com/testcontainers/testcontainers-go` | Spin up Redis and Qdrant as Docker containers in tests |
| Cucumber (test) | `github.com/cucumber/godog v0.15.1` + `github.com/itchyny/gojq` + `github.com/evanphx/json-patch` | Godog BDD runner executing Java `.feature` files directly; gojq for JSON field selection; json-patch for partial JSON matching |

### Configuration

`urfave/cli/v3` flags define the full configuration surface. Every flag binds to both an environment variable and a field in the `internal/config` struct, so the service can be configured by flag or env var interchangeably. The `--help` output lists all flags, their env var names, defaults, and the live `Names()` list for enum-like choices.

```
Flag                       Env var                          Default
--db-url                   MEMORY_SERVICE_DB_URL            (required)
--redis-url                MEMORY_SERVICE_REDIS_HOSTS       (optional)
--db-kind                  MEMORY_SERVICE_DB_KIND           postgres | mongo
--cache-kind               MEMORY_SERVICE_CACHE_KIND        redis | infinispan | none
--attach-kind              MEMORY_SERVICE_ATTACHMENTS_KIND  postgres | s3
--temp-dir                 MEMORY_SERVICE_TEMP_DIR          os.TempDir()
--vector-kind              MEMORY_SERVICE_VECTOR_KIND       pgvector | qdrant | (disabled)
--qdrant-host              MEMORY_SERVICE_VECTOR_QDRANT_HOST localhost:6334
--embed-kind               MEMORY_SERVICE_EMBEDDING_KIND    openai | disabled
--openai-api-key           MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY (required for openai)
--oidc-issuer              MEMORY_SERVICE_OIDC_ISSUER       (optional; enables OIDC auth)
--prometheus-url           MEMORY_SERVICE_PROMETHEUS_URL    (optional; enables Prometheus-backed admin stats)
--s3-bucket                MEMORY_SERVICE_ATTACHMENTS_S3_BUCKET (required for s3 attach)
--http-port                MEMORY_SERVICE_HTTP_PORT         8080
--single-port-plaintext    MEMORY_SERVICE_SINGLE_PORT_PLAINTEXT true
--single-port-tls          MEMORY_SERVICE_SINGLE_PORT_TLS   true
--tls-cert-file            MEMORY_SERVICE_TLS_CERT_FILE     (optional)
--tls-key-file             MEMORY_SERVICE_TLS_KEY_FILE      (optional)
--read-header-timeout-seconds MEMORY_SERVICE_READ_HEADER_TIMEOUT_SECONDS 5
--admin-api-key            MEMORY_SERVICE_ADMIN_API_KEY     (required)
--roles-admin-oidc-role    MEMORY_SERVICE_ROLES_ADMIN_OIDC_ROLE admin
--roles-auditor-oidc-role  MEMORY_SERVICE_ROLES_AUDITOR_OIDC_ROLE auditor
--roles-admin-users        MEMORY_SERVICE_ROLES_ADMIN_USERS (optional CSV)
--roles-auditor-users      MEMORY_SERVICE_ROLES_AUDITOR_USERS (optional CSV)
--roles-indexer-users      MEMORY_SERVICE_ROLES_INDEXER_USERS (optional CSV)
--roles-admin-clients      MEMORY_SERVICE_ROLES_ADMIN_CLIENTS (optional CSV)
--roles-auditor-clients    MEMORY_SERVICE_ROLES_AUDITOR_CLIENTS (optional CSV)
--roles-indexer-clients    MEMORY_SERVICE_ROLES_INDEXER_CLIENTS (optional CSV)
--encryption-key           MEMORY_SERVICE_ENCRYPTION_DEK_KEY (required for encrypt attach)
```

The `--help` description for enum flags like `--store` and `--vector-store` is generated from the live registry: `"backend type ("+strings.Join(registrystore.Names(), "|")+")"` so it always reflects actually compiled-in plugins.

### Single-port transport

The Go service now supports the single-port transport design in [go-single-port.md](../go-single-port.md):

- A single TCP listener can accept HTTP and gRPC traffic.
- Plaintext mode supports HTTP/1.1 and h2c (including plaintext gRPC).
- TLS mode supports HTTP/1.1 + HTTP/2 + gRPC over ALPN.
- Startup/shutdown is centralized through a single `StartSinglePortHTTPAndGRPC(...)` path in `internal/cmd/serve/singleport.go`.

### API parity

The Go service must implement all endpoints in `memory-service-contracts/src/main/resources/openapi.yml` (Agent API) and `openapi-admin.yml` (Admin API):

**Agent API** (`/v1/...`):
- Conversations: CRUD, list, forks
- Entries: list, append, sync
- Memberships: list, share, update, delete
- Ownership transfers: list, get, create, accept, delete
- Search: semantic + full-text
- Attachments: upload, get, delete, download-url, download

**Admin API** (`/v1/admin/...`):
- Conversation admin: list (with deleted), get, delete (soft), restore, entries, memberships, forks
- Search admin: cross-user search
- Eviction: trigger cache eviction
- Attachments admin: list, get, delete, content, download-url
- Stats: request-rate, error-rate, latency-p95, cache-hit-rate, db-pool-utilization, store-latency-p95, store-throughput

**gRPC** (`memory/v1/memory_service.proto`):
- `SystemService.GetHealth`
- `ConversationsService` (5 RPCs)
- `ConversationMembershipsService` (4 RPCs)
- `OwnershipTransfersService` (5 RPCs)
- `EntriesService` (3 RPCs)
- `SearchService` (3 RPCs)
- `ResponseRecorderService` (5 RPCs, includes bidirectional streaming)
- `AttachmentsService` (3 RPCs, includes client/server streaming)

### Database

Migrations are owned by the plugins that need them, not by a central runner:
- `plugin/store/postgres` embeds `db/schema.sql` and runs it via `golang-migrate` on startup
- `plugin/vector/pgvector` embeds `db/pgvector-schema.sql` and applies it as a second migration source
- `plugin/vector/qdrant` creates the Qdrant collection and HNSW index programmatically
- `plugin/store/mongo` creates MongoDB collections and indexes via the driver

The `migrate` sub-command calls `registry/migrate.RunAll(ctx)` which invokes every registered `Migrator` in registration order.

GORM is used for the Postgres model mapping and query generation; raw SQL via `gorm.Raw`/`gorm.Exec` is used for partition-aware `entries` queries (must include `conversation_group_id` in `WHERE` clauses for the 16-partition table). MongoDB uses `go.mongodb.org/mongo-driver/v2` directly.

### Security

Two authentication mechanisms are supported on all requests via `Authorization: Bearer <token>`:

**API key auth** (primary): If `MEMORY_SERVICE_OIDC_ISSUER` is not set, or if the token does not parse as a JWT, the token is treated as an opaque API key and mapped directly to `user_id` in gin context. For admin routes, API-key client role mapping is applied when `X-Client-ID` matches configured `MEMORY_SERVICE_ROLES_ADMIN_CLIENTS` / `MEMORY_SERVICE_ROLES_AUDITOR_CLIENTS`.

**OIDC / JWT auth** (required when OIDC is configured): When `MEMORY_SERVICE_OIDC_ISSUER` is set, `github.com/coreos/go-oidc/v3` is used to verify the Bearer JWT signature and expiry against the provider's JWKS. The JWT `sub` claim is extracted as the `user_id`. No local user table — the external identity is trusted directly, mirroring the Java `quarkus-oidc` integration.

Admin API authorization is role-based (auditor/admin), resolved from user lists, OIDC roles, and API-key client mappings.

CORS is handled via `github.com/gin-contrib/cors` middleware.

### Response resumer

The `ResponseRecorderService` gRPC streaming service stores in-progress LLM response chunks in Redis so they can be replayed on reconnect. The Go implementation uses a Redis stream per `(conversation_id, entry_id)` key, mirroring the Java logic.

### Logging

Use `charmbracelet/log` for all structured logging. It auto-detects TTY vs. non-TTY and switches between styled (terminal) and JSON (pipe/file) output. Integrate as gin middleware for access logging.

```go
import "github.com/charmbracelet/log"

log.Info("Server started", "port", cfg.HTTPPort)
log.Error("Store operation failed", "err", err, "op", "AppendEntry")
```

### Metrics

Export Prometheus metrics at `/metrics` (HTTP) mirroring existing Java Micrometer gauges:
- `memory_service_requests_total` (counter, labeled by method/status)
- `memory_service_request_duration_seconds` (histogram)
- `memory_service_store_latency_seconds` (histogram, labeled by operation)
- `memory_service_cache_hits_total` / `memory_service_cache_misses_total`
- `memory_service_db_pool_open_connections` (gauge)

### Build integration

`internal/pom.xml` hooks into the Maven build via `exec-maven-plugin` to run `go build` and `go test` from the project root. The test execution uses `go test -json` piped to `gotestfmt` so long-running suites show per-test progress with a readable formatter.

```xml
<!-- internal/pom.xml sketch -->
<plugin>
  <groupId>org.codehaus.mojo</groupId>
  <artifactId>exec-maven-plugin</artifactId>
  <executions>
    <execution>
      <id>go-build</id>
      <phase>compile</phase>
      <goals><goal>exec</goal></goals>
      <configuration>
        <executable>go</executable>
        <workingDirectory>${project.basedir}/..</workingDirectory>
        <arguments><argument>build</argument><argument>./...</argument></arguments>
      </configuration>
    </execution>
    <execution>
      <id>go-test</id>
      <phase>test</phase>
      <goals><goal>exec</goal></goals>
      <configuration>
        <executable>bash</executable>
        <workingDirectory>${project.basedir}/..</workingDirectory>
        <arguments>
          <argument>-lc</argument>
          <argument>set -o pipefail; go test -json ./... | go run github.com/gotesttools/gotestfmt/v2/cmd/gotestfmt@latest</argument>
        </arguments>
      </configuration>
    </execution>
  </executions>
</plugin>
```

### Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /memory-service .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /memory-service /memory-service
EXPOSE 8080 9000
ENTRYPOINT ["/memory-service"]
```

## Parity Review (2026-02-24)

### Verification Snapshot

- `wt exec -- go build ./...` passes.
- Postgres-backed Go tests now use Testcontainers (`pgvector/pgvector:pg18`) instead of `go-pgembed`, removing the prior arm64 linker blocker and aligning container image family with Java tests.
- Go `serve` now supports single-port HTTP+gRPC multiplexing per `docs/go-single-port.md` (cmux + h2c + TLS/plaintext dispatch).
- Go BDD parity suite has been migrated from hand-rolled Go test functions to **godog** (`v0.15.1`), executing the original Java `.feature` files directly from `memory-service/src/test/resources/features/`.
- Original Java BDD baseline: `241` Cucumber scenarios in 19 `.feature` files. Go step definitions are being implemented to match Java step patterns exactly so the feature files require no modifications.

### Findings

1. Coverage parity gap closed by migrating to godog: instead of hand-porting Java scenarios to Go test functions, Go now executes the original Java `.feature` files directly via godog. This eliminates drift risk and ensures 1:1 scenario coverage as step definitions are completed.

## Tasks

### Phase 1: Scaffold & infrastructure

- [x] Set up `go.mod` at project root (`module github.com/chirino/memory-service`)
- [x] Implement `internal/cmd/generate/main.go` — orchestrates protoc codegen; `//go:generate go run ./internal/cmd/generate` in `main.go`
- [x] Run protoc to produce `internal/generated/pb/memory/v1/*.go`; committed generated files
- [x] Define `internal/config/` — `Config` struct with all configuration fields + `WithContext`/`FromContext`
- [x] Implement CLI entry point in `main.go` using `urfave/cli/v3` with `serve` and `migrate` sub-commands
- [x] Implement single-port HTTP+gRPC transport in `internal/cmd/serve/singleport.go` following `docs/go-single-port.md`
- [x] Create all eight **registry** packages following plugin pattern
- [x] Implement `internal/plugin/store/postgres/` — GORM + Postgres `MemoryStore` with embedded `schema.sql`; plugin name `"postgres"`
- [x] Implement `internal/plugin/store/mongo/mongo.go` — MongoDB `MemoryStore` with collection indexes, AES-GCM encryption, regex search
- [x] Add `internal/pom.xml` with `exec-maven-plugin` for `go build` / `go test`
- [x] Add `Dockerfile.golang`

### Phase 2: Agent REST API — core

- [x] Set up gin router in `internal/cmd/serve/serve.go`; mount all route plugins
- [x] Implement `internal/security/auth.go` — gin middleware (API key + OIDC JWT via `go-oidc/v3`)
- [x] Implement `internal/plugin/route/system/` — `/v1/health` + `/metrics`; Order 0
- [x] Implement `internal/plugin/route/conversations/` — Agent API CRUD + list + forks
- [x] Implement `internal/plugin/route/entries/` — list, append, sync
- [x] Implement `internal/plugin/route/memberships/` — list, share, update, delete
- [x] Implement `internal/plugin/route/transfers/` — ownership transfers CRUD + accept
- [x] All Agent OpenAPI paths are implemented, and parity test coverage has now been added for the previously uncovered endpoints (`DELETE /v1/conversations/{conversationId}/response`, `POST /v1/conversations/{conversationId}/entries/sync`, `POST /v1/conversations/search`, `POST /v1/conversations/index`, `GET /v1/conversations/unindexed`, `PATCH /v1/conversations/{conversationId}/memberships/{userId}`, `GET /v1/ownership-transfers/{transferId}`, `DELETE /v1/ownership-transfers/{transferId}`, `DELETE /v1/attachments/{id}`, `GET /v1/attachments/download/{token}/{filename}`).

### Phase 3: Admin REST API

- [x] Implement `internal/plugin/route/admin/` — all Admin API routes with role-based guard (`admin`/`auditor`)
- [x] Stats endpoints backed by Prometheus placeholder (wired to real counters/histograms)

### Phase 4: gRPC services

- [x] Run `protoc` codegen into `internal/generated/pb/memory/v1/`
- [x] Implement `internal/grpc/server.go` — all gRPC services in `memory_service.proto` (including `AttachmentsService`)
- [x] Register all gRPC services in `serve` command

### Phase 5: Search

- [x] Implement `internal/plugin/vector/pgvector/` — pgvector VectorStore + Migrator; plugin name `"pgvector"`
- [x] Implement `internal/plugin/vector/qdrant/` — Qdrant VectorStore + Migrator; plugin name `"qdrant"`
- [x] Implement `internal/plugin/embed/openai/` — OpenAI Embedder; plugin name `"openai"`
- [x] Implement `internal/plugin/embed/disabled/` — noop Embedder; plugin name `"disabled"` (default)
- [x] Search route supports full-text (`tsvector`) search
- [x] Wire vector search + embedder in serve command
- [x] Background indexing task in `internal/service/indexer.go`

### Phase 6: Attachments

- [x] Implement `internal/plugin/attach/pgstore/` — BYTEA-backed AttachmentStore; plugin `"postgres"`
- [x] Implement `internal/plugin/attach/s3store/` — S3-backed AttachmentStore; plugin `"s3"`
- [x] Implement `internal/plugin/attach/encrypt/` — AES-GCM wrapper; plugin `"encrypt"`
- [x] Implement `internal/plugin/route/attachments/` — Agent API attachment routes (contract path parity)
- [x] Wire attachment store plugins in serve command (postgres/s3/encrypt)

### Phase 7: Caching

- [x] Implement `internal/plugin/cache/redis/` — Redis-backed ConversationCache + MemoryEntriesCache; plugin `"redis"`
- [x] Implement `internal/plugin/cache/noop/` — no-op cache; plugin `"noop"` (default)
- [x] Prometheus cache hit/miss counters in `internal/security/metrics.go`

### Phase 8: Response resumer

- [x] Implement `internal/plugin/resumer/redis/` — Redis stream-backed resumer; plugin `"redis"`
- [x] Implement `internal/plugin/resumer/noop/` — no-op resumer; plugin `"noop"` (default)
- [x] Wire via `registry/resumer.Select()` in serve command; injected into gRPC ResponseRecorderServer

### Phase 9: Background tasks & eviction

- [x] Implement `internal/service/eviction.go` — periodic soft-delete cleanup
- [x] Implement `internal/service/indexer.go` — background embedding indexer (goroutine + ticker)
- [x] `POST /v1/admin/evict` endpoint supports both sync and SSE streaming modes

### Phase 10: Observability & hardening

- [x] Add `internal/security/logging.go` — charmbracelet access logging middleware + admin audit middleware
- [x] Add `internal/security/metrics.go` — Prometheus request counter/histogram middleware
- [x] Expose Prometheus `/metrics` endpoint via `promhttp.Handler()`
- [x] `/v1/health` liveness check

### Phase 11: Testing

- [x] Store layer unit tests using `testify` + Testcontainers Postgres (`store_test.go` — retained as store-contract tests)
- [x] REST endpoint integration tests — superseded by BDD; retained: `serve_test.go` (middleware unit), `attachments_test.go` (sourceUrl async)
- [x] gRPC service integration tests — superseded by 80 gRPC BDD scenarios; old `server_test.go` deleted
- [x] MongoDB store plugin — full `MemoryStore` implementation using `go.mongodb.org/mongo-driver/v2`
- [x] Godog BDD migration — 325 scenarios across 28 feature files (see Phase 13)

### Phase 12: Parity remediation TODOs (from 2026-02-24 review)

- [x] Implement and register gRPC `AttachmentsService` (`UploadAttachment`, `GetAttachment`, `DownloadAttachment`).
- [x] Align Agent attachment route paths and response shapes with `openapi.yml` (`/v1/attachments...`).
- [x] Implement Admin attachment endpoints from `openapi-admin.yml` (`/v1/admin/attachments...`).
- [x] Fix attachment authorization checks so attachment reads/deletes are constrained to the validated conversation/group scope.
- [x] Remove `ListAttachments` cross-conversation leakage and return only attachments in the target conversation scope.
- [x] Restore Java parity for `MEMORY` scoping in list APIs (`X-Client-ID`/`clientId` behavior, store-side filtering, and gRPC list fallback behavior).
- [x] Add auth + authorization checks to gRPC response recorder methods (`Record`, `Replay`, `Cancel`, `CheckRecordings`).
- [x] Implement REST cancel endpoint behavior to actually request cancellation and return contract-compliant response (`200`).
- [x] Wire `AdminAuditMiddleware` into routes and enforce `require-justification` when enabled.
- [x] Fix `migrate` command plugin imports and context/config wiring so all migrators run.
- [x] Enforce membership requirement on ownership-transfer recipient creation.
- [x] Access-denied parity for explicit conversation write denials now returns HTTP `403` (`forbidden`) in affected agent flows (previously mapped to `404`).
- [x] Stop exposing `conversationGroupId` in public Agent API responses.
- [x] Implement effective conversation title query filtering parity.
- [x] Port fork-history listing semantics for agent/admin entry listing (`fork ancestry` vs `forks=all`) across Postgres and Mongo stores.
- [x] Import and activate the full runtime plugin set in `serve` (redis cache/resumer, openai embedder, pgvector/qdrant, s3/encrypt).
- [x] Align admin restore/list-entry parity details: restore returns `200` with conversation payload, returns `409` for already-active conversations, and admin entries honor `forks=none|all`.
- [x] Converge admin endpoint auth to role-based access: `/v1/admin/*` now uses authenticated users plus resolved `admin`/`auditor` roles (auditor read-only, admin write-capable), and forbidden role failures return `403`.
- [x] Ported additional admin scenarios from Java features into Go tests: auditor/non-role access on admin routes, admin/auditor restrictions for delete/evict, `/v1/admin/evict` sync `204` + `?async=true` SSE behavior, and stats endpoint metric/unit response shape checks.
- [x] Auth-model parity completed: Go admin role resolution now matches Java behavior across user-role mapping, OIDC role mapping, and API-key client mapping (`roles-admin-clients` / `roles-auditor-clients`) for admin endpoint authorization.
- [x] Prometheus parity completed: admin stats endpoints now execute live PromQL `/api/v1/query_range` calls using `MEMORY_SERVICE_PROMETHEUS_URL`, support default/custom `start`/`end`/`step`, return contract `TimeSeriesResponse`/`MultiSeriesResponse` payloads, and mirror Java failure semantics (`501 prometheus_not_configured`, `503 prometheus_unavailable`).
- [x] Implement URL-based attachment creation (`POST /v1/attachments` with JSON `sourceUrl`) per OpenAPI contract, including async background fetch and status transitions (`downloading` -> `ready`/`failed`), plus status-aware `GET /v1/attachments/{id}` and `GET /v1/attachments/{id}/download-url` behavior.
- [x] Remove `io.ReadAll` usage from attachment storage paths and switch to bounded stream-copy implementations in pgstore/s3/encrypt wrappers.
- [x] Implement Java temp-file parity for attachment non-functional behavior: configurable `MEMORY_SERVICE_TEMP_DIR` (default OS temp), S3 upload temp-file buffering for seekable retries, sourceUrl ingest temp-file staging, and pg/mongo retrieval temp-file spooling to release DB resources before client download completion.
- [x] Scope max-body-size middleware exemptions to true request-streaming flows only: multipart `POST /v1/attachments` is excluded; non-streaming JSON/admin endpoints remain bounded.
- [x] Harden Testcontainers Postgres bootstrap reliability: `internal/testutil/testpg.StartPostgres` now waits for port readiness plus an explicit `pgx` ping loop before returning DSN (fixes intermittent `connection refused` during migrations/tests).
- [x] Port missing Agent API endpoint scenarios for parity coverage: response-cancel endpoint behavior (resumer-disabled path), memory sync (`X-Client-ID` + dedupe), search/index/unindexed flow, membership access-level update, transfer get/delete, and attachment token-download + delete flows.
- [x] Port Prometheus-backed admin stats scenarios: request/error/latency/cache/db-pool/store metrics via mocked Prometheus responses, plus `501` not-configured and `503` unavailable behaviors.
- [x] Port admin auth-role client mapping scenarios: API-key + `X-Client-ID`-driven admin/auditor role assignment parity for admin route guards.
- [x] Port an additional Java scenario batch to Go BDD parity tests for `conversations-rest.feature`, `sharing-rest.feature`, `ownership-transfers-rest.feature`, `update-conversation-rest.feature`, and `validation-rest.feature` (status/behavior parity assertions).
- [x] Fix membership response-shape parity in agent routes: membership create/list/update now return `conversationId` and do not expose `conversationGroupId`.
- [x] Fix delete-conversation parity behavior in Postgres store: deleting a conversation now soft-deletes the conversation/group tree and hard-deletes memberships, entries, and pending ownership transfers for that group.
- [x] ~~Continue porting remaining Java scenarios (not yet fully covered)~~ — **superseded by Phase 13 godog migration**.

### Phase 13: Godog BDD migration (replaces hand-rolled test porting)

> **Status**: Gate 1 complete — 325 scenarios passing across 28 feature files.

**Motivation**: Instead of manually porting each Java Cucumber scenario into Go test functions (error-prone, drift-prone, ~241 scenarios), use `godog` to execute the original Java `.feature` files directly from `memory-service/src/test/resources/features/`. This guarantees 1:1 scenario coverage with zero feature-file modifications.

#### Architecture

```
internal/testutil/cucumber/           # Generic godog framework
├── cucumber.go                       # TestSuite, TestScenario, TestSession, variable resolution, pipe functions (uuid_to_hex_string, json, json_escape, string)
├── http_request.go                   # Generic HTTP steps (GET/POST/PATCH/DELETE with body)
├── http_response.go                  # Response code, JSON match/contain, header, field selection
└── scenario.go                       # LOCK/UNLOCK, sleep steps

internal/bdd/                         # Memory-service godog tests (replaces hand-rolled tests)
├── cucumber_test.go                  # Test runner: Postgres setup, HTTP+gRPC server boot, feature file discovery
├── mock_prometheus.go                # Mock Prometheus server for admin stats tests
├── steps_admin.go                    # Admin API: list/get/delete/restore conversations
├── steps_admin_stats.go              # Admin stats: Prometheus-backed metrics endpoints
├── steps_attachments.go              # Attachment upload/download/delete (REST)
├── steps_auth.go                     # Auth steps: user/admin/agent/auditor/indexer authentication
├── steps_cache.go                    # Cache metric recording and assertions
├── steps_cleanup.go                  # Database cleanup between scenarios
├── steps_conversations.go            # Conversation CRUD: create, get, list, delete, update, fork
├── steps_domain_assertions.go        # Domain-specific response assertions (status, body fields, etc.)
├── steps_entries.go                  # Entry management: append, list, sync, memory entries
├── steps_eviction.go                 # Admin eviction: sync 204, async SSE, concurrent safety
├── steps_grpc.go                     # gRPC steps: generic proto dispatcher, UUID↔bytes handling, streaming
├── steps_memberships.go              # Sharing: share, list, update, delete memberships
├── steps_sql.go                      # SQL execution and result assertions
└── steps_taskqueue.go                # Background task queue processing steps
```

#### Key design decisions

1. **Variable resolution bridge**: Java features use `${response.body.id}` while godog framework uses `${response.id}`. The `Resolve()` method in `cucumber.go` transparently strips the `body.` prefix, so `${response.body.data[0].id}` resolves as `${response.data[0].id}` via gojq — no feature file changes needed.

2. **Dual step pattern registration**: Java features use `I call GET "/path"` but we also support `I GET path "/path"`. Both patterns are registered so existing feature files work without modification.  

3. **Auth model**: The Go server uses Bearer token = user ID for API key auth. Step definitions set Authorization headers and X-Client-ID accordingly:
   - `I am authenticated as user "alice"` → `Bearer alice`
   - `I am authenticated as admin user "alice"` → `Bearer alice` + config `AdminUsers: "alice"`
   - `I am authenticated as agent with API key "key"` → keeps current user as bearer, sets `X-Client-ID: key`
   - `I am authenticated as auditor user "alice"` → `Bearer alice` + config `AuditorUsers: "alice"`

4. **Parallel execution**: Feature files run as `t.Run()` subtests. Each scenario gets isolated variable scope and per-user HTTP sessions. Shared DB access is inherently isolated by per-scenario conversation creation.

5. **Test infrastructure**: Single Testcontainers Postgres (`pgvector/pgvector:pg18`), full Go HTTP+gRPC server boot on random ports, per-scenario database cleanup via `steps_cleanup.go`.

6. **gRPC UUID↔bytes bridge**: Proto `bytes` fields (like `conversation_id`) use 16-byte binary UUIDs, but feature files pass human-readable UUID strings. The `unmarshalTextProto` function uses proto reflection to identify bytes fields, then injects `|uuid_to_hex_string` pipe into variable references before expansion. For response assertions, `convertBase64UUIDs` post-processes protojson output to decode base64-encoded 16-byte values back to UUID strings.

7. **Pipe functions**: Variable resolution supports `${var|pipe}` syntax. The `uuid_to_hex_string` pipe converts a UUID string (e.g. `550e8400-...`) to prototext escaped-byte format (`\x55\x0e\x84...`). Other pipes: `json`, `json_escape`, `string`.

#### Implementation tasks

- [x] **Framework**: Created `internal/testutil/cucumber/` package 
  - `cucumber.go` — TestSuite/TestScenario/TestSession, variable resolution with `response.body.` bridge, JSON match/contain, pipe functions (`uuid_to_hex_string`, `json`, `json_escape`, `string`)
  - `http_request.go` — generic HTTP steps (`I call GET/POST/PATCH/DELETE "path" with body:`), event stream support, polling/wait steps, header setting
  - `http_response.go` — response code, JSON match/contain, field selection via gojq, variable storage from response, header assertions
  - `scenario.go` — LOCK/UNLOCK (RWMutex), sleep step

- [x] **Test runner**: Created `internal/bdd/cucumber_test.go`
  - Testcontainers Postgres setup (reuse `testpg.StartPostgres`)
  - Boot full Go HTTP + gRPC servers on random ports
  - Configure admin/auditor/indexer user roles and agent API keys
  - Walk `features/`, `features-grpc/`, `features-encrypted/` subdirectories, create `t.Run()` per feature
  - Initialize godog `TestSuite` per feature file with shared Postgres, HTTP, and gRPC infrastructure

- [x] **Step definitions — Phase A (core REST, ~130 scenarios)**:
  - `steps_auth.go` — 6 auth patterns (user, admin, agent, auditor, indexer, re-auth)
  - `steps_conversations.go` — create, get, list, delete, update, fork (~15 patterns)
  - `steps_entries.go` — append, list, sync, memory entries (~15 patterns)
  - `steps_memberships.go` — share, list, update, delete (~10 patterns)
  - `steps_domain_assertions.go` — status code, body field assertions, error codes, contains (~30 patterns)
  - Features covered: `conversations-rest`, `entries-rest`, `sharing-rest`, `ownership-transfers-rest`, `update-conversation-rest`, `validation-rest`, `forking-rest`, `multi-agent-memory-rest`

- [x] **Step definitions — Phase B (admin + search, ~50 scenarios)**:
  - `steps_admin.go` — admin list/get/delete/restore
  - `steps_eviction.go` — eviction sync/async/concurrent/role-gated
  - `steps_admin_stats.go` — Prometheus-backed stats, 501/503 error behaviors
  - `steps_sql.go` — SQL execution and result assertions
  - `steps_taskqueue.go` — background task queue processing
  - Features covered: `admin-rest`, `index-rest`, `eviction-rest`, `task-queue`, `admin-stats-rest`

- [x] **Step definitions — Phase C (attachments + infrastructure, ~60 scenarios)**:
  - `steps_attachments.go` — upload, download, delete, token-based download
  - `steps_cache.go` — cache metric recording/assertions
  - `mock_prometheus.go` — configurable Prometheus mock server
  - Features covered: `attachments-rest`, `forked-attachments-rest`, `fork-attachment-deletion-rest`, `admin-attachments-rest`, `memory-cache-rest`, `response-resumer-grpc`

- [x] **Step definitions — Phase D (gRPC, 80 scenarios)**:
  - `steps_grpc.go` — generic proto dispatcher for all gRPC services, UUID↔bytes handling via proto reflection + pipe functions, streaming Record/Replay/Cancel steps, attachment upload/download steps
  - Features covered: `conversations-grpc`, `entries-grpc`, `sharing-grpc`, `forking-grpc`, `ownership-transfers-grpc`, `update-conversation-grpc`, `index-grpc`, `attachments-grpc`

- [x] **Step definitions — Phase E (encrypted, 4 scenarios)**:
  - Reuses existing attachment steps with encryption-enabled file store
  - Features covered: `encrypted-file-store-rest`

- [x] **Cleanup**: Deleted hand-rolled integration tests superseded by BDD suite (`grpc/server_test.go`, `route/conversations/conversations_test.go`, `route/admin/admin_test.go`, `route/entries/entries_test.go`, `route/search/search_test.go`). Retained unit/contract tests not covered by BDD: `cmd/serve/serve_test.go` (middleware), `plugin/store/postgres/store_test.go` (store layer), `plugin/route/attachments/attachments_test.go` (sourceUrl async).

#### Results

| Directory | Feature files | Scenarios |
|-----------|--------------|-----------|
| `features/` | 19 | 241 |
| `features-grpc/` | 8 | 80 |
| `features-encrypted/` | 1 | 4 |
| **Total** | **28** | **325** |

Deferred: `features-qdrant/search-mongo-qdrant.feature` (2 scenarios) — requires MongoDB + Qdrant backends (Gate 2).

#### Gate 2 MongoDB store fixes applied

Bug fixes to `internal/plugin/store/mongo/mongo.go` to align with Postgres/Java behavior:

- **403 vs 404 access checks**: `requireAccess` errors were wrapped as `NotFoundError` instead of propagating `ForbiddenError` — fixed in 8 methods
- **Sync/epoch logic**: `SyncAgentEntry` fully rewritten to match Postgres epoch tracking, auto-create conversation, content diffing, and no-op detection
- **Fork entry resolution**: `forkedAtEntryId` now resolves to the entry BEFORE the fork point (Java parity)
- **ListForks**: Removed incorrect `forked_at_conversation_id $exists` filter — lists all conversations in the group
- **latest-fork list mode**: Implemented `ListModeLatestFork` for both agent and admin list endpoints
- **Delete cascade**: `DeleteConversation` now hard-deletes memberships/entries/transfers (Java/Postgres parity)
- **Transfer bugs**: Added `resolveConversationID` for transfer DTOs, `ForbiddenError` for accept/delete access, transfer cleanup on membership deletion, duplicate transfer detection with `existingTransferId` in conflict response
- **Index validation**: `IndexEntries` returns 404 when entry not found (was silently skipping)
- **Title derivation**: `AppendEntries` derives conversation title from first history entry content
- **Memory epoch auto-assign**: `AppendEntries` auto-assigns `epoch=1` for memory entries
- **Channel filtering**: `GetEntries` now handles "all channels" mode for agents (channel=nil, clientID set)
- **MongoDB attachment store**: Created `internal/plugin/attach/mongostore/` for file blob storage
- **Collection cleanup**: BDD test cleanup uses `DeleteMany` instead of `Drop` to preserve indexes
- **SQL verification skip**: `ExecSQL` returns nil for MongoDB (Java parity — SQL assertions are skipped for non-Postgres backends)
- **UpdateAttachment missing EntryID**: Added `entry_id` field handling to `UpdateAttachment` — fixes `status=linked` admin filter and cross-group attachment validation
- **Search pagination**: `SearchEntries` now uses limit+1 pattern to detect more results and return `afterCursor`
- **GetAttachment entry fallback**: When linked entry is hard-deleted (conversation deletion), falls back to ownership check (Postgres parity)
- **gRPC + encrypted features**: Added gRPC server and feature discovery for `features-grpc/` (80 scenarios) and `features-encrypted/` (4 scenarios) to MongoDB test runner — all passed on first run
- **Task queue on MongoDB**: Refactored `steps_taskqueue.go` to use `TestDB` interface instead of direct Postgres SQL. Added task queue methods (`DeleteAllTasks`, `CreateTask`, `CreateFailedTask`, `ClaimReadyTasks`, `DeleteTask`, `FailTask`, `GetTask`, `CountTasks`) to both `PostgresTestDB` and `MongoTestDB`. Removed task-queue skip from MongoDB runner.

#### Rollout gates

| Gate | Configuration | Criteria | Status |
|------|--------------|----------|--------|
| Gate 1 | `postgresql+redis+pgvector` | All feature files pass via godog | **Done** — 325 scenarios, 0 failures |
| Gate 2 | `mongodb+redis+qdrant` | All applicable feature files pass on Mongo backend (REST + gRPC + encrypted + task-queue) | **Done** — 327 scenarios, 0 failures, 0 skipped |

## Files to Modify / Create

| File/Path | Action | Description |
|-----------|--------|-------------|
| `main.go` | Create | CLI entrypoint (`urfave/cli/v3`); `//go:generate go run ./internal/cmd/generate` directive |
| `go.mod` / `go.sum` | Create | Go module at project root |
| `Dockerfile` | Create | Multi-stage Go build |
| `internal/pom.xml` | Create | Maven wrapper for `go build`/`go test` |
| `internal/cmd/generate/main.go` | Create | Codegen orchestrator: runs `oapi-codegen` + `protoc`; installs tools if absent |
| `internal/generated/api/cfg.yaml` | Create | `oapi-codegen` config for Agent API (types + strict-server + gin-server) |
| `internal/generated/api/api.gen.go` | Generated | Types + `StrictServerInterface` from `openapi.yml` — do not edit |
| `internal/generated/admin/cfg.yaml` | Create | `oapi-codegen` config for Admin API |
| `internal/generated/admin/admin.gen.go` | Generated | Types + `StrictServerInterface` from `openapi-admin.yml` — do not edit |
| `internal/generated/pb/` | Generated | gRPC stubs from `memory_service.proto` — do not edit |
| `internal/cmd/serve/` | Create | `serve` sub-command implementation |
| `internal/cmd/migrate/` | Create | `migrate` sub-command implementation |
| `internal/config/config.go` | Create | Env var config |
| `internal/model/` | Create | Domain types mirroring Java model package |
| `internal/registry/` | Create | Eight registry packages (route, migrate, store, cache, attach, embed, vector, resumer) |
| `internal/plugin/route/` | Create | Route group plugins (8 files); implement `gen/api.StrictServerInterface` |
| `internal/plugin/store/` | Create | `postgres.go` + `mongo.go` (each registers MemoryStore + Migrator) |
| `internal/plugin/cache/` | Create | `redis.go` + `noop.go` |
| `internal/plugin/attach/` | Create | `postgres.go`, `s3.go`, `encrypt.go` |
| `internal/plugin/embed/` | Create | `openai.go`, `disabled.go` |
| `internal/plugin/vector/` | Create | `pgvector.go` + `qdrant.go` (each registers VectorStore + Migrator) |
| `internal/plugin/resumer/` | Create | `redis.go` + `noop.go` |
| `internal/grpc/` | Create | gRPC service impls (use `gen/pb/` types) |
| `internal/security/` | Create | Auth gin middleware (OIDC JWT + API key) |
| `internal/service/` | Create | Background jobs (eviction, indexing) |
| `pom.xml` (root) | Modify | Add `internal` as a Maven module (for Go build) |
| `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto` | Read-only | Source of truth for gRPC; regenerate via `go generate ./...` |
| `memory-service-contracts/src/main/resources/openapi.yml` | Read-only | Source of truth for Agent API; regenerate via `go generate ./...` |
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Read-only | Source of truth for Admin API; regenerate via `go generate ./...` |
| `memory-service/src/main/resources/db/schema.sql` | Read-only | Reused verbatim by `golang-migrate` |

## Verification

```bash
# Ensure devcontainer is running
wt up

# Regenerate all code (OpenAPI types + server interfaces, gRPC stubs)
wt exec -- go generate ./...

# Build Go service (output binary named memory-service)
wt exec -- go build -o memory-service .

# Run Go unit tests (Postgres/Redis/Qdrant via Testcontainers)
wt exec -- go test ./... > /tmp/065-go-test.log 2>&1

# Inspect test failures
rg -n "FAIL|ERROR" /tmp/065-go-test.log

# Build via Maven wrapper
wt exec -- ./mvnw -f internal/pom.xml compile

# Run tests via Maven wrapper
wt exec -- ./mvnw -f internal/pom.xml test

# Start full stack (Postgres + Redis) and run integration tests
wt exec -- ./mvnw -f internal/pom.xml verify

# Confirm generated code + gRPC stubs compile
wt exec -- go vet ./internal/generated/... ./internal/grpc/...
```

## Design Decisions

### Init-registration plugin pattern for routes and all swappable backends

All seven pluggable layers use the init-registration pattern from [docs/go-plugin-pattern.md](../go-plugin-pattern.md):

**Route plugins** (`registry/route`) use `RouterLoader` (`func(*gin.Engine) error`) with an `Order` field. The `serve` command calls `RouteLoaders()` and mounts them in order — auth middleware at 0, API groups at 100, admin at 200. Adding a new API feature group requires only a new plugin file and one blank import.

**Backend plugins** (`registry/store`, `cache`, `attach`, `embed`, `vector`, `resumer`) register with a string `Name`. `Names()` drives config validation and CLI `--help` text; `Select(name)` picks the active backend — no switch statements in the consumer.

**Migration plugins** (`registry/migrate`) are registered by store and vector plugins in their own `init()`. `RunAll(ctx)` is called by the `migrate` sub-command. Each plugin owns its own schema files (`//go:embed`) or programmatic setup — there is no central migration directory.

The `serve` command blank-imports the full set; test binaries blank-import only what they need (e.g. `plugin/store/postgres` + `plugin/cache/noop`).

### GORM for Postgres, native driver for MongoDB

GORM is used for the Postgres backend: it handles model mapping, migrations bootstrapping, and standard CRUD queries. Raw SQL via `gorm.Raw`/`gorm.Exec` is used for partition-aware `entries` queries (`WHERE conversation_group_id = $1`). The MongoDB backend uses `go.mongodb.org/mongo-driver/v2` directly — GORM's community Mongo driver lacks coverage of the aggregation pipeline patterns used in `MongoMemoryStore` (e.g., `$lookup`, `$unwind`).

### gin over chi

gin complements GORM's batteries-included philosophy. Built-in `ShouldBindJSON`, `ShouldBindQuery`, and `ShouldBindUri` eliminate manual deserialization boilerplate across 47+ endpoints. gin's `*gin.Context` carries the user principal cleanly through middleware chains. chi is simpler but requires more glue code for binding and validation at this endpoint count.

### urfave/cli for commands and configuration

`urfave/cli/v3` provides two things: sub-command dispatch (`serve`, `migrate`) and the full configuration surface via flags. Every configuration value is a named CLI flag with an `EnvVars` binding — no separate config loader needed. Flags populate `internal/config.Config` struct fields directly. The `--help` output for enum flags (e.g. `--store`, `--vector-store`) is generated from `registry.Names()`, so it always reflects compiled-in plugins rather than a hardcoded string.

### charmbracelet/log for logging

Auto-detects TTY and switches between styled human-readable output (development) and structured key=value output (CI/containers). `lipgloss` provides the style definitions; `charmbracelet/x/term` handles TTY detection. All 500-level errors log a full stack trace via `log.Error("...", "err", err)`.

### langchaingo for embeddings and vector stores

`github.com/tmc/langchaingo` provides a clean `Embedder` interface (OpenAI, noop) and a `VectorStore` interface with native pgvector and Qdrant backends (`github.com/qdrant/go-client/qdrant` is the underlying Qdrant gRPC client). This eliminates writing the embedding + vector search abstraction layer by hand. The library is pre-1.0 but actively maintained (8700+ stars, v0.1.14, Oct 2025) and directly covers the two vector backends this service needs. Pre-1.0 instability risk is acceptable given the project's pre-release stance.

### Testcontainers for Postgres, Redis, and Qdrant

Postgres-backed tests use `github.com/testcontainers/testcontainers-go/modules/postgres` with `pgvector/pgvector:pg18`. Redis and Qdrant tests use `testcontainers-go` containers as well. This keeps test infrastructure consistent across architectures and avoids platform-specific embedded Postgres linker constraints.

### Single binary, single process

The Java service separates concerns via Quarkus extensions and CDI. In Go, we wire everything explicitly in `main.go` via `urfave/cli`. All optional backends (Redis, S3, Qdrant, MongoDB) are included in the binary and activated by config at runtime.

### Shared schema with Java service

The Go service reuses `db/schema.sql` without modification. During transition, both services can run against the same database simultaneously (useful for validation). Schema evolution will be driven by the Go service once it is the primary implementation.

### Contract-first code generation with oapi-codegen

The OpenAPI specs in `memory-service-contracts/` are the source of truth. Route handler implementations must not diverge from the spec — this is enforced by the generated `StrictServerInterface`.

**Why `oapi-codegen`**: The `github.com/oapi-codegen/oapi-codegen/v2` library is the de-facto standard for contract-first Go APIs (5000+ stars, maintained by a neutral OSS org, not a vendor). Key reasons over alternatives:

- **gin integration**: generates a native gin router registration shim (`gin-server` target) — no adapter layer.
- **`strict-server` target**: generates an interface with one method per OpenAPI operation. The Go compiler enforces that handler plugins satisfy the full interface — if a new operation is added to `openapi.yml` and `go generate` is run, plugin files fail to compile until the new method is implemented. This makes drift impossible.
- **`types` target**: generates Go structs for all request bodies, responses, and parameters from the OpenAPI `#/components/schemas`. Eliminates hand-written model structs for the API layer.
- **`spec` target**: embeds the raw spec so the service can serve `/openapi.json`.
- **Predictable output**: generated file is a single `.gen.go` file; easy to `git diff` after spec changes.

**Why not `ogen`**: `ogen` (`github.com/ogen-go/ogen`) generates a complete, self-contained HTTP server and does not integrate with gin. Switching would mean replacing the router, losing the gin middleware ecosystem, and rewriting all auth/logging middleware.

**Workflow when the spec changes**:
1. Edit `openapi.yml` or `openapi-admin.yml` in `memory-service-contracts/`.
2. Run `go generate ./...` — regenerates `internal/generated/api/api.gen.go` and/or `internal/generated/admin/admin.gen.go`.
3. Fix any compilation errors in `internal/plugin/route/` where new interface methods need implementation.
4. Commit both the spec change and the regenerated file together.

**gRPC codegen** follows the same pattern: edit `memory_service.proto`, run `go generate ./...`, fix compilation errors in `internal/grpc/`, commit together.

`internal/generated/` is committed so CI does not need `oapi-codegen` or `protoc` installed — only `go build` is required in CI.

## Open Questions

None currently.
