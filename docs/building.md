# Building Memory Service

## Quick Start

```bash
# Default build — all plugins included
go build -o memory-service .

# CGO-free build (excludes SQLite)
CGO_ENABLED=0 go build -tags nosqlite -o memory-service .
```

## Plugin Exclusion Tags

The binary includes all plugins by default. Use `no<name>` build tags to exclude optional plugins at compile time, reducing binary size, dependencies, and attack surface.

```bash
go build -tags '<tag1>,<tag2>,…' -o memory-service .
```

### Available Tags

| Tag | Plugins Excluded | Dependencies Removed |
|-----|-----------------|---------------------|
| `nopostgresql` | `store/postgres`, `vector/pgvector`, `attach/pgstore` | PostgreSQL driver, pgvector |
| `nosqlite` | `store/sqlite`, `vector/sqlitevec` | CGO sqlite bindings, sqlite-vec |
| `nomongo` | `store/mongo`, `attach/mongostore` | MongoDB driver |
| `noredis` | `cache/redis` | go-redis (unless infinispan is also enabled) |
| `noinfinispan` | `cache/infinispan` | Infinispan RESP support |
| `noqdrant` | `vector/qdrant` | Qdrant vector plugin |
| `nos3` | `attach/s3store` | AWS S3 SDK |
| `novault` | `encrypt/vault` | Vault SDK |
| `noawskms` | `encrypt/awskms` | AWS KMS SDK |
| `noopenai` | `embed/openai` | OpenAI client |
| `nomcp` | `mcp` subcommand | MCP server, generated OpenAPI client |
| `notcp` | TCP listener | TCP networking code |
| `nouds` | Unix domain socket listener | UDS networking code |

### Constraints

- **At least one store backend must remain.** Building with `nopostgresql,nosqlite,nomongo` simultaneously produces a compile error.
- **At least one listener must remain.** Building with `notcp,nouds` simultaneously produces a compile error.
- **Redis and Infinispan**: `noredis` removes redis CLI flags and plugin registration but keeps the redis implementation compiled if infinispan is enabled (since infinispan wraps redis). Use `noredis,noinfinispan` together to fully remove the go-redis dependency.

### Core Plugins (Not Excludable)

These lightweight plugins are always included:

- `cache/local`, `cache/noop` — in-process caching
- `encrypt/plain`, `encrypt/dek` — encryption backends
- `embed/disabled`, `embed/local` — embedding backends
- `attach/filesystem` — filesystem attachment store
- `route/system` — system routes

## Example Builds

```bash
# PostgreSQL-only (minimal)
go build -tags 'nosqlite,nomongo,noqdrant,nos3,novault,noawskms,noinfinispan,noredis,noopenai' -o memory-service .

# MongoDB-only (CGO-free)
CGO_ENABLED=0 go build -tags 'nopostgresql,nosqlite,noqdrant,nos3,novault,noawskms,noinfinispan,noredis,noopenai' -o memory-service .

# Everything except SQLite (CGO-free)
CGO_ENABLED=0 go build -tags nosqlite -o memory-service .

# PostgreSQL + Redis only
go build -tags 'nosqlite,nomongo,noqdrant,nos3,novault,noawskms,noinfinispan,noopenai' -o memory-service .

# Smallest SQLite + UDS-only binary (local-only agent use case)
go build -tags 'sqlite_fts5,sqlite_json,nopostgresql,nomongo,noqdrant,nos3,novault,noawskms,noinfinispan,noredis,noopenai,notcp' -o memory-service .

# TCP-only (no Unix domain socket support)
go build -tags nouds -o memory-service .

# UDS-only (no TCP support)
go build -tags notcp -o memory-service .
```

## How It Works

Excludable plugins register themselves via `init()` functions. Each plugin's Go files carry a `//go:build !no<name>` constraint. When a tag is set:

1. The plugin's source files are excluded by the compiler.
2. Its `init()` never runs, so it never registers with the plugin registry.
3. CLI flags contributed by the plugin are not added to `--help`.
4. Selector flags (e.g., `--db-kind`) automatically reflect only available backends.

Blank imports for excludable plugins live in per-plugin files under `internal/cmd/serve/plugin_*.go` and `internal/cmd/migrate/plugin_*.go`, each with the corresponding build constraint. Top-level subcommands (serve, migrate, mcp) register via `internal/cmd/commands/`, with optional commands guarded by build tags.
