status: implemented
---

# Enhancement 072: Memory Load Counts for Importance Signals

> **Status**: Implemented.

## Summary

Track how often episodic memories are fetched by direct read APIs, and persist durable
usage signals (`fetch_count`, `last_fetched_at`) so agent apps can
estimate memory usefulness/importance over time.

## Motivation

Today, episodic memories have no feedback signal from read traffic. We can write and search
memories, but we cannot answer basic questions like:

- Which memories are repeatedly retrieved and likely useful?
- Which memories are never loaded and likely stale?
- Should retrieval ranking boost memories with repeated successful retrieval history?

Usage counters give simple, backend-agnostic signals that can support:

- memory reinforcement and cleanup jobs
- ranking/boosting during retrieval
- operational reporting (hot namespaces/keys)

## Design

### 1. What Counts as a "Load"

A load is counted once per API request per unique memory key when a memory is returned to a caller.

Counted:

- `GET /v1/memories` when a memory is found
- gRPC `GetMemory` when a memory is found

Not counted:

- `POST /v1/memories/search` results
- gRPC `SearchMemories` results
- admin-only endpoints
- background jobs (indexer, TTL, eviction)
- not-found reads

### 2. Datastore Options and Recommendation

| Option | Pros | Cons | Fit |
|---|---|---|---|
| PostgreSQL table updates | Durable, transactional, strong queryability (`ORDER BY fetch_count`) | Write amplification under very hot keys | Good |
| MongoDB `$inc` updates | Durable, atomic increments, natural for document backend | Requires parity logic with Postgres implementation | Good |
| Redis/Infinispan counters | Very high write throughput, low latency | Not authoritative, requires flush/recovery logic, cache reset risk | Good only as optional buffer |
| Vector DB payload updates (PGVector/Qdrant) | Co-located with semantic retrieval stack | Wrong data model for mutable counters; expensive and awkward updates | Poor |

**Recommendation**: use the primary episodic datastore as the source of truth (Postgres or
Mongo, matching `--datastore-kind`) and store counters in a dedicated stats table/collection.
Do not store canonical counters in cache or vector stores.

Rationale:

- Counters are durable and survive process/cache restarts.
- Increment semantics are simple and atomic in both supported primary stores.
- We avoid mutating append-history rows in `memories` on every read.
- Querying top memories by count stays close to existing memory metadata.

### 3. Data Model

Use a dedicated logical store `memory_usage_stats` keyed by encoded namespace + key.

PostgreSQL:

```sql
CREATE TABLE memory_usage_stats (
    namespace       TEXT        NOT NULL, -- RS-encoded namespace
    key             TEXT        NOT NULL,
    fetch_count     BIGINT      NOT NULL DEFAULT 0,
    last_fetched_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (namespace, key)
);

CREATE INDEX memory_usage_stats_last_fetched_idx
    ON memory_usage_stats (last_fetched_at DESC);
CREATE INDEX memory_usage_stats_fetch_count_idx
    ON memory_usage_stats (fetch_count DESC);
```

MongoDB:

- Collection: `memory_usage_stats`
- Document shape:
  - `namespace: string`
  - `key: string`
  - `fetch_count: int64`
  - `last_fetched_at: date`
- Indexes:
  - unique `{namespace: 1, key: 1}`
  - `{fetch_count: -1}`
  - `{last_fetched_at: -1}`

### 4. Write Path for Counters

Add a batched store method:

```go
IncrementMemoryLoads(ctx context.Context, keys []MemoryKey, fetchedAt time.Time) error
```

Where each `MemoryKey` contains decoded namespace + key for a direct fetch result.

Behavior:

- Deduplicate repeated keys within one request before incrementing.
- Increment with a single bulk statement/write-batch where possible.
- Counter failures are non-fatal to read APIs: log warning and continue response.

PostgreSQL upsert shape:

```sql
INSERT INTO memory_usage_stats (namespace, key, fetch_count, last_fetched_at)
VALUES ...
ON CONFLICT (namespace, key) DO UPDATE
SET fetch_count = memory_usage_stats.fetch_count + EXCLUDED.fetch_count,
    last_fetched_at = GREATEST(memory_usage_stats.last_fetched_at, EXCLUDED.last_fetched_at);
```

MongoDB upsert shape (`bulkWrite`):

```json
{
  "updateOne": {
    "filter": {"namespace": "...", "key": "..."},
    "update": {
      "$inc": {"fetch_count": 1},
      "$max": {"last_fetched_at": "<fetchedAt>"}
    },
    "upsert": true
  }
}
```

### 5. API/Contract Design

Use a hybrid API strategy:

- integrate usage into existing agent read endpoints via opt-in flags
- add dedicated admin usage endpoints for reporting/ranking

No `totalCount` field is stored or returned.

#### 5.1 Agent REST API

1. Extend `GET /v1/memories` with query flag `include_usage` (default `false`).
2. Extend `POST /v1/memories/search` with request field `include_usage` (default `false`).
3. When enabled, responses include:

```json
"usage": {
  "fetchCount": 12,
  "lastFetchedAt": "2026-03-04T15:05:00Z"
}
```

When `include_usage=false` or omitted, behavior stays unchanged.

#### 5.2 Admin REST API

Add admin-only endpoints for direct usage retrieval and ranking:

- `GET /admin/v1/memories/usage?ns=...&key=...`
- `GET /admin/v1/memories/usage/top?prefix=...&sort=fetch_count|last_fetched_at&limit=...`

`top` is for analytics/reporting and does not change agent read-path behavior.

#### 5.3 gRPC Equivalents

Add matching fields/methods in `memory/v1/memory_service.proto`:

1. `message MemoryUsage` with:
   - `int64 fetch_count`
   - `google.protobuf.Timestamp last_fetched_at`
2. `GetMemoryRequest.include_usage` (bool)
3. `SearchMemoriesRequest.include_usage` (bool)
4. `MemoryItem.usage` (optional `MemoryUsage`)
5. Admin RPCs:
   - `GetMemoryUsage(GetMemoryUsageRequest) returns (MemoryUsage)`
   - `ListTopMemoryUsage(ListTopMemoryUsageRequest) returns (ListTopMemoryUsageResponse)`

#### 5.4 Ranking Follow-up

A later enhancement may consume `fetch_count` for retrieval boosting with a configurable weight.

## Testing

BDD feature scenarios (REST + gRPC):

```gherkin
Scenario: Get memory increments load count
  Given a memory exists in namespace ["user","alice","notes"] key "k1"
  When I GET "/v1/memories?ns=user&ns=alice&ns=notes&key=k1"
  Then fetch_count for namespace ["user","alice","notes"] key "k1" should be 1

Scenario: Search does not increment usage counters
  Given memories "k1" and "k2" exist under namespace prefix ["user","alice"]
  When I POST "/v1/memories/search" with query that returns k1 and k2
  Then fetch_count for "k1" should not change
  And fetch_count for "k2" should not change

Scenario: Counter write failure does not fail read request
  Given load-counter storage temporarily fails
  When I GET an existing memory
  Then the response status should be 200
  And a warning log should mention memory load counter failure

Scenario: Get memory returns usage when include_usage=true
  Given a memory exists and has usage stats
  When I GET "/v1/memories?ns=user&ns=alice&ns=notes&key=k1&include_usage=true"
  Then the response JSON at "$.usage.fetchCount" should be 12
  And the response JSON should not contain "$.usage.totalCount"

Scenario: Search memories returns usage per item when include_usage=true
  Given memories "k1" and "k2" exist with usage stats
  When I POST "/v1/memories/search" with include_usage=true
  Then each returned item should contain "usage"
  And no returned usage object should contain "totalCount"

Scenario: gRPC GetMemory returns usage when include_usage=true
  Given a memory exists and has usage stats
  When I invoke gRPC "GetMemory" with include_usage=true
  Then the gRPC response MemoryItem should include usage.fetch_count and usage.last_fetched_at

Scenario: gRPC SearchMemories returns usage when include_usage=true
  Given memories exist with usage stats
  When I invoke gRPC "SearchMemories" with include_usage=true
  Then each returned item should include usage

Scenario: Admin usage endpoints return direct usage and top usage rankings
  Given usage stats exist for multiple keys
  When I GET "/admin/v1/memories/usage?ns=user&ns=alice&ns=notes&key=k1"
  Then the response should include fetchCount and lastFetchedAt
  When I GET "/admin/v1/memories/usage/top?prefix=user&prefix=alice&sort=fetch_count&limit=2"
  Then the response should contain at most 2 ranked items
```

Unit tests:

- Postgres upsert increments and last-fetched timestamp
- Mongo bulk upsert increments and dedupe behavior
- Route/gRPC handlers call `IncrementMemoryLoads` only for direct fetch reads
- duplicate keys in one response increment once
- direct fetch path updates `last_fetched_at`

## Tasks

- [x] Add `MemoryKey` and `IncrementMemoryLoads(...)` to `registry/episodic.EpisodicStore`
- [x] Implement Postgres `memory_usage_stats` migration + increment query
- [x] Implement Mongo `memory_usage_stats` indexes + `bulkWrite` increment path
- [x] Update Agent OpenAPI (`openapi.yml`) with `include_usage` and `usage` response field
- [x] Update Admin OpenAPI (`openapi-admin.yml`) with memory usage endpoints
- [x] Update gRPC proto (`memory_service.proto`) with usage messages/fields and admin RPCs
- [x] Wire counters into REST direct memory reads (`getMemory`) only
- [x] Wire counters into gRPC direct memory reads (`GetMemory`) only
- [x] Implement admin REST handlers for usage lookup and top usage ranking
- [x] Implement gRPC admin handlers for usage lookup and top usage ranking
- [x] Add REST feature tests for `include_usage` and admin usage endpoints
- [x] Add gRPC feature tests for `include_usage` and admin usage RPCs
- [x] Run feature tests for both datastore backends
- [x] Add unit tests for datastore increment semantics and request dedupe

## Files to Modify

| File | Change |
|---|---|
| `memory-service-contracts/src/main/resources/openapi.yml` | Add `include_usage` and `usage` fields to memory agent endpoints |
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Add admin memory usage endpoints |
| `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto` | Add usage messages/fields and admin usage RPCs |
| `internal/registry/episodic/plugin.go` | Add load-counter method and key type to interface |
| `internal/plugin/store/postgres/db/schema.sql` | Add `memory_usage_stats` table and indexes |
| `internal/plugin/store/postgres/episodic_store.go` | Implement Postgres increment logic |
| `internal/plugin/store/mongo/episodic_store.go` | Create indexes and implement Mongo increment logic |
| `internal/plugin/route/memories/memories.go` | Increment counters on REST `getMemory` responses only and add admin usage endpoints under `/admin/v1/memories/usage*` |
| `internal/grpc/server.go` | Increment counters on gRPC `GetMemory` responses only |
| `internal/generated/api/*.go` | Regenerate REST models/handlers from updated OpenAPI |
| `internal/generated/admin/*.go` | Regenerate admin REST models/handlers from updated OpenAPI |
| `internal/generated/pb/memory/v1/*.go` | Regenerate gRPC stubs/messages from updated proto |
| `internal/bdd/testdata/features/memories-rest.feature` | Add end-to-end load-count scenarios |
| `internal/bdd/testdata/features/memories-grpc.feature` | Add gRPC memory usage scenarios |
| `internal/bdd/steps_*.go` | Add assertions/helpers for counter verification |

## Verification

```bash
# Start/ensure devcontainer context for build/test commands
wt up

# Compile affected Go modules
wt exec -- go build ./...

# Run episodic memory BDD suites (Postgres + Mongo)
wt exec -- go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1 > test-pg.log 2>&1
wt exec -- go test ./internal/bdd -run TestFeaturesMongoKeycloak -count=1 > test-mongo.log 2>&1
```

## Non-Goals

- Exactly-once global counting across client retries/network duplication
- Per-user or per-client segmented counters in phase 1
- Real-time analytics dashboards

## Design Decisions

1. **Primary datastore is canonical**: counters are durable and queryable, unlike cache-only counters.
2. **Separate stats table/collection**: avoids heavy update churn on append-history `memories` rows.
3. **Best-effort counter writes**: read APIs remain available even if counter persistence is degraded.

## Open Questions

- Should counters reset when a key is deleted and later recreated in the same namespace?
- Should semantic search ranking consume `fetch_count` in this enhancement, or in a follow-up?
- Do we need an admin endpoint to inspect/reset counters before ranking uses them?
