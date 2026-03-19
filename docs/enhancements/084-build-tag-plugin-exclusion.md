---
status: implemented
---

# Enhancement 084: Build-Tag Plugin Exclusion

> **Status**: Implemented.

## Summary

Use Go build tags to optionally exclude plugins from the binary at compile time (e.g., `-tags nosqlite`). Plugins that are excluded must also remove their exclusive CLI flags and env vars so that `--help` output stays clean and invalid flag combinations are impossible.

## Motivation

The current binary statically links every plugin via blank imports in `internal/cmd/serve/serve.go`. This causes several problems:

1. **Binary size**: Plugins like SQLite (with CGO) and MongoDB pull in large dependency trees. Deployments targeting only PostgreSQL pay the cost of all other store backends.
2. **CGO requirement**: The `sqlite` and `sqlitevec` plugins require CGO and `libsqlite3-dev`. Excluding them would allow fully static, CGO-free builds for container-only deployments.
3. **Attack surface**: Including unused plugins increases the attack surface. A production deployment using only PostgreSQL + Redis has no reason to ship MongoDB or Infinispan client code.
4. **CLI clutter**: Flags like `--infinispan-host`, `--vector-qdrant-host`, and `--attachments-s3-bucket` appear in `--help` even when the corresponding plugins will never be used.

Today, flags are defined monolithically in the `flags()` function in `serve.go`. Plugins have no way to contribute their own flags — all flag definitions live outside the plugin boundary. This means excluding a plugin still leaves its orphaned flags behind.

## Design

### Build Tag Convention

Each excludable plugin gets a `no<name>` build tag. The plugin's Go files use a `//go:build !no<name>` constraint so the compiler drops them when the tag is set.

| Tag | Excludes | Dependencies Removed |
|-----|----------|---------------------|
| `nopostgresql` | `store/postgres`, `vector/pgvector`, `attach/pgstore` | PostgreSQL driver, pgvector |
| `nosqlite` | `store/sqlite`, `vector/sqlitevec` | CGO sqlite bindings, sqlite-vec |
| `nomongo` | `store/mongo`, `attach/mongostore` | MongoDB driver |
| `noredis` | `cache/redis` (registration only) | go-redis (unless infinispan also excluded) |
| `noinfinispan` | `cache/infinispan` | (none — reuses redis impl) |
| `noqdrant` | `vector/qdrant` | Qdrant vector plugin registration |
| `nos3` | `attach/s3store` | AWS S3 SDK |
| `novault` | `encrypt/vault` | Vault SDK |
| `noawskms` | `encrypt/awskms` | AWS KMS SDK |
| `noopenai` | `embed/openai` | OpenAI client |
| `notcp` | TCP listener | TCP networking code |
| `nouds` | Unix domain socket listener | UDS networking code |

At least one store plugin must remain enabled. Similarly, `notcp` and `nouds` are mutually exclusive — at least one listener type must remain. For example, a MongoDB-only deployment could use `-tags 'nopostgresql,nosqlite'`, while a PostgreSQL-only deployment could use `-tags 'nosqlite,nomongo'`.

### Compile-Time Store Guard

A sentinel file in `internal/registry/store/` ensures the build fails if all store backends are excluded:

`internal/registry/store/nostore.go`:
```go
//go:build nopostgresql && nosqlite && nomongo

package store

// This file is only compiled when every store backend is excluded.
// The undefined reference below produces a build error on purpose.
var _ = at_least_one_store_backend_must_be_enabled
```

When all three `no*` store tags are set, the compiler includes this file and fails with:

```
internal/registry/store/nostore.go:7:9: undefined: at_least_one_store_backend_must_be_enabled
```

As long as at least one store tag is absent, the build constraint is not satisfied and the file is ignored.

### Cross-Plugin Dependencies

Two cases require special handling:

**Redis / Infinispan**: The `cache/infinispan` plugin wraps the `cache/redis` implementation. The redis package is split into two files:
- `redis.go` (implementation): `//go:build !noredis || !noinfinispan` — compiled unless **both** are excluded
- `plugin.go` (registration + CLI flags): `//go:build !noredis` — only registers when redis is explicitly enabled

This means `noredis` alone removes redis CLI flags and plugin registration, but keeps the implementation available for infinispan. Only `noredis,noinfinispan` together fully removes the go-redis dependency.

**Episodic Qdrant**: The `store/episodicqdrant` package is a shared utility imported by postgres, mongo, and sqlite episodic stores. It has **no build tag** — it is always compiled. The `noqdrant` tag only controls the `vector/qdrant` plugin registration. The episodic qdrant client code remains in the binary but is only used at runtime if `vector-kind=qdrant` is configured.

Plugins that are always lightweight and have no heavy external dependencies (e.g., `cache/local`, `cache/noop`, `encrypt/plain`, `encrypt/dek`, `embed/disabled`, `embed/local`, `attach/filesystem`, `route/system`) are **not excludable** — they form the core set.

### Plugin-Contributed Flags

Extend the registry `Plugin` struct to include optional CLI flags and a post-parse hook:

```go
// In each registry package (store, cache, vector, embed, attach, encrypt, etc.)
type Plugin struct {
    Name   string
    Loader Loader
    Flags  func(cfg *config.Config) []cli.Flag  // CLI flags contributed by this plugin
    Apply  func(*config.Config)                 // Called after flag parsing to apply flag values to config
}
```

When a plugin is excluded via build tag, its `init()` never runs, so its flags are never registered and its `Apply` hook never fires.

The `flags()` function in `serve.go` changes from a hardcoded list to a two-part assembly:

```go
func flags(cfg *config.Config, ...) []cli.Flag {
    result := []cli.Flag{
        // Core flags (server, listener, db-kind, db-url, etc.)
        ...
    }
    // Append plugin-contributed flags
    result = append(result, registrystore.PluginFlags(cfg)...)
    result = append(result, registrycache.PluginFlags(cfg)...)
    result = append(result, registryvector.PluginFlags(cfg)...)
    result = append(result, registryembed.PluginFlags(cfg)...)
    result = append(result, registryattach.PluginFlags(cfg)...)
    result = append(result, encrypt.PluginFlags(cfg)...)
    return result
}
```

Each registry gains `PluginFlags()` and `ApplyAll()` functions that collect flags and apply hooks from all registered plugins:

```go
// In internal/registry/store/plugin.go
func PluginFlags(cfg *config.Config) []cli.Flag {
    var flags []cli.Flag
    for _, p := range plugins {
        if p.Flags != nil {
            flags = append(flags, p.Flags(cfg)...)
        }
    }
    return flags
}

func ApplyAll(cfg *config.Config) {
    for _, p := range plugins {
        if p.Apply != nil {
            p.Apply(cfg)
        }
    }
}
```

### File Structure Per Plugin

Each plugin already has its own package. Add a build constraint to every `.go` file in excludable plugin packages:

```go
//go:build !nosqlite

package sqlite
```

For the blank import in `serve.go`, use conditional import files:

**Before** (monolithic `serve.go`):
```go
import (
    _ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
    _ "github.com/chirino/memory-service/internal/plugin/store/mongo"
    // ...
)
```

**After** (split into per-plugin files):

`internal/cmd/serve/plugin_sqlite.go`:
```go
//go:build !nosqlite

package serve

import _ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
import _ "github.com/chirino/memory-service/internal/plugin/vector/sqlitevec"
```

`internal/cmd/serve/plugin_mongo.go`:
```go
//go:build !nomongo

package serve

import _ "github.com/chirino/memory-service/internal/plugin/store/mongo"
import _ "github.com/chirino/memory-service/internal/plugin/attach/mongostore"
```

Core plugins remain in `serve.go` with no build constraints.

### Flag Migration Example: Redis

**Before** — flags defined in `serve.go`:
```go
&cli.StringFlag{
    Name:        "redis-hosts",
    Category:    "Cache:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_REDIS_HOSTS"),
    Destination: &cfg.RedisURL,
    Usage:       "Redis connection URL",
},
```

**After** — flags defined in the redis plugin's `init()`:

```go
//go:build !noredis

package redis

import (
    "context"
    "github.com/chirino/memory-service/internal/config"
    registrycache "github.com/chirino/memory-service/internal/registry/cache"
    "github.com/urfave/cli/v3"
)

func init() {
    registrycache.Register(registrycache.Plugin{
        Name:   "redis",
        Loader: load,
        Flags: func(cfg *config.Config) []cli.Flag {
            return []cli.Flag{
                &cli.StringFlag{
                    Name:        "redis-hosts",
                    Category:    "Cache:",
                    Sources:     cli.EnvVars("MEMORY_SERVICE_REDIS_HOSTS"),
                    Destination: &cfg.RedisURL,
                    Usage:       "Redis connection URL",
                },
            }
        },
    })
}
```

Note: The redis package is split into `redis.go` (implementation, `!noredis || !noinfinispan`) and `plugin.go` (registration, `!noredis`). The implementation is shared with infinispan.

### Flag Ownership Mapping

Flags that move from `serve.go` into plugins:

| Flag(s) | Moves To Plugin |
|---------|----------------|
| `redis-hosts` | `cache/redis` |
| `infinispan-host`, `infinispan-username`, `infinispan-password` | `cache/infinispan` |
| `attachments-s3-bucket`, `attachments-s3-use-path-style` | `attach/s3store` |
| `vector-qdrant-host` | `vector/qdrant` |
| `embedding-openai-api-key` | `embed/openai` |
| `encryption-vault-transit-key`, `encryption-vault-addr`, `encryption-vault-token` | `encrypt/vault` |
| `encryption-kms-key-id`, `encryption-kms-aws-region`, `encryption-kms-aws-access-key-id`, `encryption-kms-aws-secret-access-key` | `encrypt/awskms` |

| `db-url`, `db-max-open-conns`, `db-max-idle-conns` | `store/postgres` (when PostgreSQL-specific; shared DB flags stay core) |

Flags that stay in `serve.go` (core):
- All Server, Network Listener, Authorization, Monitoring flags
- `db-kind`, `cache-kind`, `attachments-kind`, `encryption-kind`, `vector-kind`, `embedding-kind` (selector flags)
- `cache-local-*` (local cache is core)
- `attachments-fs-dir`, `attachments-allow-private-source-urls` (filesystem is core)
- `encryption-dek-key`, `encryption-db-disabled`, `encryption-attachments-disabled` (DEK is core)
- `vector-indexer-batch-size` (core vector config)
- All episodic memory flags

Note: `db-url` is used by multiple store backends (postgres, sqlite, mongo) so it remains a core flag.

### Apply Hook Pattern

The `Apply` callback solves the problem of flag values needing to reach `config.Config`. Two approaches are viable:

**Option A — Direct destination**: If the plugin can import `config`, flags use `Destination: &cfg.Field` as today. The `Apply` hook is only needed for post-parse transformations (e.g., setting env vars from flag values, as Vault/KMS do today).

**Option B — Plugin-local state**: The plugin stores flag values in package-level vars and reads them in its `Loader`. This avoids plugins importing `config` but requires the `Loader` context to already carry the parsed flag values.

Option A is simpler and matches the existing pattern. The `Apply` hook handles edge cases like the Vault/KMS `os.Setenv` calls.

### Registry `Names()` Accuracy

The `Names()` function already only returns registered plugins. When a plugin is excluded by build tag, it never registers, so `--help` output for selector flags (e.g., `db-kind (postgres|mongo|sqlite)`) automatically reflects only the available plugins.

## Testing

### Build Verification

```bash
# Default build — all plugins
go build -o memory-service .

# Exclude SQLite (CGO-free build)
CGO_ENABLED=0 go build -tags 'nosqlite' -o memory-service .

# PostgreSQL-only, exclude everything else
go build -tags 'nosqlite,nomongo,noqdrant,nos3,novault,noawskms' -o memory-service .

# MongoDB-only build
go build -tags 'nopostgresql,nosqlite,noqdrant,nos3,novault,noawskms' -o memory-service .

# Verify excluded flags are absent
./memory-service serve --help | grep -c redis   # 0 when noredis
./memory-service serve --help | grep -c sqlite  # 0 when nosqlite
```

### Cucumber Scenarios

```gherkin
Feature: Build tag plugin exclusion

  Scenario: SQLite plugin is unavailable when built with nosqlite
    Given the server is built with tags "nosqlite"
    When I set db-kind to "sqlite"
    Then the server fails with "unknown store \"sqlite\""

  Scenario: Redis flags are hidden when built with noredis
    Given the server is built with tags "noredis"
    When I request --help output
    Then the output does not contain "redis-hosts"

  Scenario: Default build includes all plugins
    Given the server is built with no exclusion tags
    When I request the registered store names
    Then the list contains "postgres", "mongo", "sqlite"
```

### Unit Tests

```go
// internal/registry/store/plugin_test.go
func TestNamesReflectsRegisteredPlugins(t *testing.T) {
    // After init(), Names() should return only what was registered.
    names := store.Names()
    // Exact set depends on build tags — just verify non-empty and no duplicates.
    seen := map[string]bool{}
    for _, n := range names {
        require.False(t, seen[n], "duplicate plugin name: %s", n)
        seen[n] = true
    }
}
```

## Tasks

- [x] Add `Flags` and `Apply` fields to each registry `Plugin` struct (store, cache, vector, embed, attach, encrypt)
- [x] Add `PluginFlags()` and `ApplyAll()` functions to each registry package
- [x] Migrate plugin-specific flags from `serve.go` into their respective plugin `init()` registrations
- [x] Update `flags()` in `serve.go` to assemble core flags + plugin-contributed flags
- [x] Wire `ApplyAll()` into the serve command's `Action` (after flag parsing, before `run()`)
- [x] Add `//go:build !no<name>` constraints to all `.go` files in excludable plugin packages
- [x] Split `cache/redis` into implementation (`!noredis || !noinfinispan`) and registration (`!noredis`)
- [x] Create per-plugin import files (`internal/cmd/serve/plugin_*.go`) with matching build constraints
- [x] Create per-plugin import files (`internal/cmd/migrate/plugin_*.go`) with matching build constraints
- [x] Remove blank imports of excludable plugins from `serve.go` and `migrate.go`
- [x] Add `internal/registry/store/nostore.go` compile-time guard
- [x] Verify `go build -tags 'nopostgresql,nosqlite,nomongo' .` fails at compile time
- [x] Verify all individual exclusion tags build successfully
- [x] Verify minimal build (`nomongo,noredis,noinfinispan,noqdrant,nos3,novault,noawskms,noopenai`) succeeds
- [ ] Update `Dockerfile` to accept a `GO_BUILD_TAGS` build arg
- [ ] Update `CLAUDE.md` with build tag documentation

## Files to Modify

| File | Change |
|------|--------|
| `internal/registry/store/plugin.go` | Add `Flags`, `Apply` to `Plugin`; add `Flags()`, `ApplyAll()` |
| `internal/registry/store/nostore.go` | **New**: compile-time guard — fails build when all store backends excluded |
| `internal/registry/cache/plugin.go` | Same |
| `internal/registry/vector/plugin.go` | Same |
| `internal/registry/embed/plugin.go` | Same |
| `internal/registry/attach/plugin.go` | Same |
| `internal/registry/encrypt/plugin.go` | Same |
| `internal/cmd/serve/serve.go` | Remove plugin-specific flags; assemble from registries; call `ApplyAll()` |
| `internal/cmd/serve/plugin_postgresql.go` | **New**: conditional import for postgres + pgvector + pgstore |
| `internal/cmd/serve/plugin_sqlite.go` | **New**: conditional import for sqlite + sqlitevec |
| `internal/cmd/serve/plugin_mongo.go` | **New**: conditional import for mongo + mongostore |
| `internal/cmd/serve/plugin_redis.go` | **New**: conditional import for redis |
| `internal/cmd/serve/plugin_infinispan.go` | **New**: conditional import for infinispan |
| `internal/cmd/serve/plugin_qdrant.go` | **New**: conditional import for qdrant + episodicqdrant |
| `internal/cmd/serve/plugin_s3.go` | **New**: conditional import for s3store |
| `internal/cmd/serve/plugin_vault.go` | **New**: conditional import for vault |
| `internal/cmd/serve/plugin_awskms.go` | **New**: conditional import for awskms |
| `internal/cmd/serve/plugin_openai.go` | **New**: conditional import for openai |
| `internal/plugin/store/postgres/*.go` | Add `//go:build !nopostgresql` |
| `internal/plugin/store/sqlite/*.go` | Add `//go:build !nosqlite` |
| `internal/plugin/store/mongo/*.go` | Add `//go:build !nomongo` |
| `internal/plugin/cache/redis/redis.go` | Add `//go:build !noredis \|\| !noinfinispan` (implementation) |
| `internal/plugin/cache/redis/plugin.go` | **New**: `//go:build !noredis` (registration + flags) |
| `internal/plugin/cache/infinispan/*.go` | Add `//go:build !noinfinispan` |
| `internal/plugin/vector/pgvector/*.go` | Add `//go:build !nopostgresql` |
| `internal/plugin/vector/qdrant/*.go` | Add `//go:build !noqdrant` |
| `internal/plugin/vector/sqlitevec/*.go` | Add `//go:build !nosqlite` |
| `internal/plugin/attach/s3store/*.go` | Add `//go:build !nos3` |
| `internal/plugin/attach/pgstore/*.go` | Add `//go:build !nopostgresql` |
| `internal/plugin/attach/mongostore/*.go` | Add `//go:build !nomongo` |
| `internal/plugin/encrypt/vault/*.go` | Add `//go:build !novault` |
| `internal/plugin/encrypt/awskms/*.go` | Add `//go:build !noawskms` |
| `internal/plugin/embed/openai/*.go` | Add `//go:build !noopenai` |
| `internal/plugin/store/episodicqdrant/*.go` | No build tag (always compiled — shared by store episodic impls) |
| Each plugin `init()` above | Move relevant flags from `serve.go` into `Plugin.Flags` |
| `Dockerfile` | Add `GO_BUILD_TAGS` build arg |
| `CLAUDE.md` | Document build tags |

## Verification

```bash
# Full build (all plugins)
go build -o memory-service .

# Minimal PostgreSQL-only build
go build -tags 'nosqlite,nomongo,noqdrant,nos3,novault,noawskms,noinfinispan,noredis,noopenai' -o memory-service .

# Minimal MongoDB-only build (CGO-free)
CGO_ENABLED=0 go build -tags 'nopostgresql,nosqlite,noqdrant,nos3,novault,noawskms,noinfinispan,noredis,noopenai' -o memory-service .

# Run Go tests (default tags — all plugins present)
go test ./internal/... -count=1

# Verify excluded plugin not selectable
./memory-service serve --db-kind sqlite 2>&1 | grep 'unknown store'
```

## Design Decisions

1. **`no` prefix convention**: Using `nosqlite` rather than `sqlite` means the default `go build` includes everything. This follows the Go standard library pattern (e.g., `nethttpomithttp2`) and avoids surprising users who build without tags.
2. **Plugin-contributed flags over flag removal**: Rather than conditionally removing flags from a master list, plugins contribute their own flags during registration. This is more maintainable — adding a new plugin automatically includes its flags.
3. **Grouped tags**: `nosqlite` excludes both `store/sqlite` and `vector/sqlitevec` because they share the CGO dependency. Similarly, `nomongo` covers `store/mongo` and `attach/mongostore`, and `nopostgresql` covers `store/postgres`, `vector/pgvector`, and `attach/pgstore`. Users don't need to know internal plugin topology.
4. **All store backends are excludable**: Unlike the original design, no store backend is mandatory. This allows MongoDB-only or SQLite-only builds. The constraint is that at least one store plugin must remain — the server fails at startup if none are registered. Lightweight, dependency-free plugins (`cache/local`, `cache/noop`, `encrypt/plain`, `encrypt/dek`, `embed/disabled`, `embed/local`, `attach/filesystem`, `route/system`) form the non-excludable core.
5. **No runtime disable**: This is compile-time only. Runtime disabling (e.g., config flag to ignore a plugin) is a separate concern and not addressed here. Build tags give the strongest guarantees — excluded code is not in the binary at all.
