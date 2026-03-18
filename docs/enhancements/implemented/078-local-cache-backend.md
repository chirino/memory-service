---
status: implemented
---

# Enhancement 078: Local Cache Backend

> **Status**: Implemented.

## Summary

Added a new process-local cache backend, `cache-kind=local`, for single-instance deployments such as local agent use cases. The implementation provides TTL-based expiry, bounded memory usage, concurrent access support, and process-local response-recording support without requiring Redis or Infinispan.

## Motivation

The Go service already supported `none`, `redis`, and `infinispan`, but local and embedded deployments still had to choose between no cache and an external cache service. That made SQLite and single-process setups miss both cached memory-entry reads and response-recording support.

## Design

### Implemented Backend

The memory entries cache is implemented in `internal/plugin/cache/local/` using [`github.com/dgraph-io/ristretto/v2`](https://github.com/dgraph-io/ristretto).

Implemented behavior:

1. `cache-kind=local` registers as a normal cache plugin through `internal/registry/cache`.
2. Cached values are stored as JSON bytes of `registrycache.CachedMemoryEntries`.
3. The cache cost is the serialized byte length of each entry list.
4. `MEMORY_SERVICE_CACHE_EPOCH_TTL` continues to control cache entry TTL.
5. `Set` and `Remove` call `cache.Wait()` so the cache is immediately observable by the request path and tests.

### Config Surface

Implemented config:

| Setting | Type | Default | Purpose |
|---------|------|---------|---------|
| `MEMORY_SERVICE_CACHE_KIND` / `--cache-kind` | `local`, `redis`, `infinispan`, `none` | `none` | Cache backend selection |
| `MEMORY_SERVICE_CACHE_EPOCH_TTL` | duration | `PT10M` | TTL for memory-entry cache entries |
| `MEMORY_SERVICE_CACHE_LOCAL_MAX_BYTES` / `--cache-local-max-bytes` | memory size | `64M` | Process-local cache budget |
| `MEMORY_SERVICE_CACHE_LOCAL_NUM_COUNTERS` / `--cache-local-num-counters` | integer | `100000` | Ristretto counter count |
| `MEMORY_SERVICE_CACHE_LOCAL_BUFFER_ITEMS` / `--cache-local-buffer-items` | integer | `64` | Ristretto get-buffer size |

### Response Recording

`cache-kind=local` also enables response recording and resumption for single-instance deployments.

Implemented behavior:

1. `internal/resumer/locator_store.go` now supports `cache-kind=local`.
2. The locator store uses a process-local `sync.RWMutex`-protected map with TTL pruning.
3. This backend is intentionally process-local only; it does not provide cross-replica replay or cancel routing.

### Documentation Correction

The Go cache implementations refresh TTL on writes (`Set`), not on reads (`Get`). Configuration docs were updated to describe TTL accurately instead of calling it a sliding read/write TTL.

## Testing

Implemented test coverage:

1. Unit tests for the Ristretto-backed cache cover round-trip serialization, TTL expiry, removal, oversized entries, and concurrent access.
2. Unit tests for the memory locator store cover lifecycle and TTL expiry.
3. Added a dedicated SQLite BDD runner, `internal/bdd/cucumber_sqlite_memory_test.go`, that exercises:
   - `memory-cache-rest.feature`
   - `response-recorder-grpc.feature`

## Tasks

- [x] Add `github.com/dgraph-io/ristretto/v2` to `go.mod`
- [x] Add `cache-kind=local` to the Go config and CLI surface
- [x] Implement `internal/plugin/cache/local/` using Ristretto
- [x] Add process-local `LocatorStore` support for `cache-kind=local`
- [x] Add unit tests for TTL, eviction, serialization, and concurrency
- [x] Add a SQLite BDD runner for `cache-kind=local`
- [x] Update configuration docs and examples to document the new backend and its single-instance limitation
- [x] Correct docs that described cache TTL as sliding on reads

## Files Modified

| File | Change |
|------|--------|
| `go.mod` | Promoted `ristretto/v2` usage into the implementation dependency set |
| `internal/config/config.go` | Added process-local cache tuning fields and defaults |
| `internal/config/compat.go` | Added env parsing for `MEMORY_SERVICE_CACHE_LOCAL_*` and exported memory-size parsing |
| `internal/cmd/serve/serve.go` | Added cache plugin import and CLI flags for the process-local cache |
| `internal/plugin/cache/local/local.go` | Added Ristretto-backed `MemoryEntriesCache` implementation |
| `internal/plugin/cache/local/local_test.go` | Added unit tests for local-cache behavior |
| `internal/resumer/locator_store.go` | Added process-local locator-store support for `cache-kind=local` |
| `internal/resumer/locator_store_test.go` | Added memory locator-store tests |
| `internal/bdd/cucumber_sqlite_local_test.go` | Added SQLite BDD runner for the local-cache matrix |
| `site/src/pages/docs/configuration.mdx` | Documented `memory` cache backend and corrected TTL wording |
| `site/src/pages/docs/faq.mdx` | Listed the in-memory cache backend |
| `README.md` | Updated cache support overview |

## Verification

```bash
# Unit tests
go test ./internal/plugin/cache/local ./internal/resumer ./internal/config > unit.log 2>&1

# Build
go build ./... > build.log 2>&1

# SQLite local-cache BDD coverage
CGO_ENABLED=1 go test -tags 'sqlite_fts5 sqlite_json' ./internal/bdd -run TestFeaturesSQLiteLocal -count=1 > bdd.log 2>&1
```

## Non-Goals

1. Cross-replica cache coherence for `cache-kind=local`.
2. Replacing Redis or Infinispan for multi-instance deployments.
3. Extending the cache abstraction beyond the current memory-entries and response-recording use cases.
