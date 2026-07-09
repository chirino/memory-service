---
status: implemented
---

# Enhancement 106: Multi-Query Semantic Memory Search

> **Status**: Implemented.

## Summary

Add a multi-query semantic search capability to all four memory search surfaces — user REST, admin REST, user gRPC (`MemoriesService`), and admin gRPC (`AdminMemoriesService`) — so an LLM can decompose a prompt into focused sub-queries, run vector search independently per query, and receive a single deduplicated, rank-fused result list with query attribution.

## Motivation

Agents currently issue one vector search using the full user prompt as the query embedding. A single embedding blends all entities, constraints, subtasks, and context contained in a multi-intent prompt into one averaged semantic target. This causes relevant memories to be missed when the relevant topics sit in different parts of the embedding space.

For example, a prompt like "continue the release — the Docker build broke and we excluded the Python packages last sprint" contains three distinct retrieval intents:

- overall release plan context
- container build failure context
- Python packaging decisions

A single embedding query ranks results for one blended intent and misses memories that are clearly relevant to the other two. Multi-query search lets the LLM or agent app decompose the prompt into focused strings and receive the union of matching memories, deduplicated and ranked by Reciprocal Rank Fusion (RRF).

The change touches:
- `POST /v1/memories/search` (user REST)
- `POST /admin/v1/memories/search` (admin REST)
- `MemoriesService.SearchMemories` (user gRPC)
- `AdminMemoriesService.SearchMemories` (admin gRPC)

All four surfaces gain the same multi-query shape while keeping full backward compatibility with single-query requests.

## Design

### Core Data Shapes

A query item carries the text string and an optional human-readable purpose label for attribution:

```json
{ "text": "release plan", "purpose": "overall context" }
```

The result item gains a `matchedQueries` field listing the purposes (or query texts when `purpose` is absent) of every query that matched the item:

```json
{
  "id": "...",
  "key": "...",
  "score": 0.91,
  "matchedQueries": ["overall context", "Docker image build issue"]
}
```

`matchedQueries` contains at least one element for every returned item. Items matched by more queries rank higher via RRF.

### REST Contract — User API (`POST /v1/memories/search`)

`SearchMemoriesRequest` gains two new optional fields:

| Field | Type | Notes |
|---|---|---|
| `queries` | array of `MemorySearchQuery` | Batch of semantic search strings. Mutually exclusive with the existing `query` field. |
| `per_query_limit` | integer [1–100] | Per-query vector search budget. Defaults to `limit`. The final merged list is capped at `limit`. |

`MemorySearchQuery`:

| Field | Type | Notes |
|---|---|---|
| `text` | string (required) | The search string for this query. Must be non-empty after trimming. |
| `purpose` | string (optional) | Human-readable label used as the attribution value in `matchedQueries`. Defaults to `text` if absent. |

`SearchMemoriesResponse` items gain `matchedQueries`:

| Field | Type | Notes |
|---|---|---|
| `matchedQueries` | array of string | Attribution: purposes (or texts) of all queries that matched this item. Present on all items in a multi-query response; absent in single-query responses. |

Validation rules:
- `query` and `queries` are mutually exclusive; sending both returns `400`.
- Each `queries[].text` must be non-empty after trimming; otherwise `400`.
- `queries` must contain at least one item when present; an empty array returns `400`.
- `per_query_limit` must be within [1, 100] when present.
- When `queries` is present and the embedder is unavailable, return `503`.
- All other existing validations (namespace, filter, archived, limit) remain unchanged.

Example request:

```json
{
  "namespace_prefix": ["user", "alice", "cognition.v1"],
  "queries": [
    { "text": "release plan", "purpose": "overall context" },
    { "text": "Docker image build issue", "purpose": "container build context" },
    { "text": "Python packages excluded from release", "purpose": "package scope" }
  ],
  "per_query_limit": 5,
  "limit": 12,
  "filter": { "memoryKind": { "$in": ["procedure", "summary", "bridge"] } }
}
```

Example response:

```json
{
  "items": [
    {
      "id": "...",
      "namespace": ["user", "alice", "cognition.v1", "procedures"],
      "key": "procedure:deployment-debugging",
      "score": 0.91,
      "matchedQueries": ["overall context", "container build context"],
      "value": { "kind": "procedure", "statement": "..." }
    },
    {
      "id": "...",
      "namespace": ["user", "alice", "cognition.v1", "decisions"],
      "key": "decision:python-scope",
      "score": 0.78,
      "matchedQueries": ["package scope"]
    }
  ]
}
```

### REST Contract — Admin API (`POST /admin/v1/memories/search`)

`AdminSearchMemoriesRequest` gains the same two fields (`queries`, `per_query_limit`) and the same mutual-exclusivity rule as the user API. `AdminSearchMemoriesResponse` items gain `matchedQueries` on `AdminMemoryItem`. All existing admin fields (`key_prefix`, `as_user_id`, `justification`, `include_usage`) are unaffected.

### gRPC Contract — `MemoriesService.SearchMemories`

`SearchMemoriesRequest` gains:

```protobuf
message MemorySearchQuery {
  string text = 1;
  optional string purpose = 2;
}

message SearchMemoriesRequest {
  repeated string namespace_prefix = 1;
  string query = 2;                        // existing single-query field; mutually exclusive with queries
  optional google.protobuf.Struct filter = 3;
  int32 limit = 4;
  reserved 5;
  bool include_usage = 6;
  ArchiveFilter archived = 7;
  optional RequestActor actor = 9;
  repeated MemorySearchQuery queries = 10; // new: multi-query
  int32 per_query_limit = 11;              // new: per-query vector budget
}
```

`MemoryItem` gains:

```protobuf
message MemoryItem {
  // ... existing fields ...
  repeated string matched_queries = 12;   // new: attribution
}
```

`SearchMemoriesResponse` is unchanged; attribution is on each item.

### gRPC Contract — `AdminMemoriesService.SearchMemories`

`AdminSearchMemoriesRequest` gains the same `queries` and `per_query_limit` fields (field numbers 10 and 11). `AdminMemoryItem` gains `repeated string matched_queries = 13`.

### Rank Fusion Strategy

Multi-query results are merged using **Reciprocal Rank Fusion (RRF)**:

```
rrf_score(item) = Σ_q  1 / (k + rank_q(item))
```

where `k = 60` (standard constant), `rank_q(item)` is the 1-based rank of the item in query `q`'s result list, and items not returned by query `q` are excluded from that query's term. Items matched by more queries naturally accumulate higher scores.

The merged list is sorted descending by RRF score. Ties among items with equal RRF scores are broken by the highest raw vector similarity score across all matching queries, then by first-seen order in the per-query result stream.

The `score` field on the returned item is set to the RRF score (not the raw vector similarity). This keeps the field semantically consistent across single- and multi-query responses.

### Search Execution — Multi-Query Path

1. Validate the request (namespace, filter, limit, per_query_limit, mutual exclusivity).
2. Call OPA filter injection once for the shared namespace/filter (same as single-query).
3. Normalize the combined filter.
4. Embed all query texts in a single batched `EmbedTexts` call.
5. For each query, run `SearchMemoryVectors` with `per_query_limit` (or `limit` if absent), collecting `(memoryID, rank, rawScore, purpose)` per result.
6. Build a per-item RRF accumulator across all queries.
7. Sort by RRF score descending, break ties as described above.
8. Trim to `limit`.
9. Fetch full memory items for the top IDs via `GetMemoriesByIDs`.
10. Attach `matchedQueries` from the accumulated attribution map.
11. Enrich with usage if `include_usage` is set.

The embedder batch call in step 4 means network round-trips to the embedding model stay at 1 regardless of the number of queries.

### Single-Query Backward Compatibility

When only the existing `query` field is set (and `queries` is absent), the handler follows the existing code path unchanged. The `matchedQueries` field is absent from response items.

### `MemoryItem.score` semantics for multi-query

| Mode | `score` value |
|---|---|
| Single `query` | Raw vector similarity score |
| `queries` (multi) | RRF score across all queries |
| Attribute-only | absent |

## Design Decisions

### Batch-embed all queries in one call

Embedding N queries in a single batched call avoids N serial round-trips to the embedding provider. The `registryembed.Embedder.EmbedTexts` interface already accepts a slice, so no interface changes are needed.

### RRF over simple score merging

Raw vector similarity scores are not comparable across different queries or different embedding runs. RRF uses only rank order and is robust to score distribution differences between queries. The `k=60` constant is widely established as a good default.

### `matchedQueries` uses purpose label, not raw text

Purposes are shorter, human-readable labels that agents can use directly in prompts or UI without surfacing full query strings (which may be verbose or internal). When `purpose` is absent, the query text is used as the fallback so the field is always meaningful.

### No new endpoint

Multi-query is an extension of the same search operation. Adding `queries` as an alternative to `query` keeps the surface area minimal and avoids duplicating authorization, filter injection, and archive logic.

### Admin API gets the same extension

Admin search is used for auditing and debugging workflows that benefit equally from multi-intent decomposed searches. The admin context already has `as_user_id` to scope results.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Multi-query semantic memory search

  Background:
    Given I am authenticated as user "alice"

  Scenario: Multi-query search deduplicates results and attributes matched queries
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "mq-test"],
      "key": "release-plan",
      "value": { "text": "release plan for v2" },
      "index": { "default": "release plan v2 deployment" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "mq-test"],
      "key": "docker-issue",
      "value": { "text": "docker build broke in CI" },
      "index": { "default": "docker image build CI failure" }
    }
    """
    And memory indexing has run
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "mq-test"],
      "queries": [
        { "text": "release plan", "purpose": "release" },
        { "text": "docker build issue", "purpose": "docker" }
      ],
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 1 items
    And every item in "items" should have field "matchedQueries"

  Scenario: Multi-query and single query are mutually exclusive
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "query": "release plan",
      "queries": [{ "text": "release plan" }],
      "limit": 5
    }
    """
    Then the response status should be 400

  Scenario: Multi-query rejects empty queries array
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "queries": [],
      "limit": 5
    }
    """
    Then the response status should be 400

  Scenario: Multi-query rejects blank query text
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "queries": [{ "text": "  " }],
      "limit": 5
    }
    """
    Then the response status should be 400

  Scenario: Multi-query overall limit is respected
    Given I call PUT "/v1/memories" with body:
    """
    { "namespace": ["user", "alice", "mq-limit"], "key": "k1",
      "value": { "t": 1 }, "index": { "default": "alpha one" } }
    """
    And I call PUT "/v1/memories" with body:
    """
    { "namespace": ["user", "alice", "mq-limit"], "key": "k2",
      "value": { "t": 2 }, "index": { "default": "beta two" } }
    """
    And I call PUT "/v1/memories" with body:
    """
    { "namespace": ["user", "alice", "mq-limit"], "key": "k3",
      "value": { "t": 3 }, "index": { "default": "gamma three" } }
    """
    And memory indexing has run
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "mq-limit"],
      "queries": [
        { "text": "alpha" },
        { "text": "beta" },
        { "text": "gamma" }
      ],
      "per_query_limit": 3,
      "limit": 2
    }
    """
    Then the response status should be 200
    And the response body "items" should have at most 2 items

  Scenario: Multi-query search cannot return another user's memories
    Given I am authenticated as user "bob"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "mq-auth"],
      "key": "secret",
      "value": { "text": "bob secret" },
      "index": { "default": "bob private data" }
    }
    """
    And memory indexing has run
    And I am authenticated as user "erin"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "bob", "mq-auth"],
      "queries": [{ "text": "bob private data" }],
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at most 0 items

  Scenario: Admin multi-query search returns results with matchedQueries
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "mq-admin"],
      "key": "admin-note",
      "value": { "text": "admin multi query note" },
      "index": { "default": "admin retrieval test" }
    }
    """
    And memory indexing has run
    And I am authenticated as admin user "alice"
    When I call POST "/admin/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "mq-admin"],
      "queries": [
        { "text": "admin retrieval test", "purpose": "admin-context" }
      ],
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 1 items
    And the response body field "items.0.matchedQueries.0" should be "admin-context"
```

### Unit / Integration Tests

- Multi-query with two distinct queries returns deduplicated results; IDs appear at most once in the response.
- An item matched by two queries has both purposes in `matchedQueries`.
- An item matched by only one query has exactly one element in `matchedQueries`.
- `per_query_limit` caps each individual vector search; the final list respects `limit`.
- RRF ranking: an item ranked first by two queries scores higher than an item ranked first by only one query.
- `query` and `queries` together returns `400` for both REST (user and admin) and gRPC transports.
- Empty `queries` array returns `400`.
- Blank (whitespace-only) `text` in a query item returns `400`.
- Single `query` (existing path) still returns items without `matchedQueries`.
- Authorization: multi-query search respects OPA namespace filter injection; cross-user queries return zero rows without error.
- Archive filter is applied per-query before merging; archived items excluded by default.
- `include_usage` enriches merged results without incrementing counters.
- `matchedQueries` uses `purpose` when provided, falls back to `text` when absent.

## Tasks

- [x] Add `MemorySearchQuery`, `queries`, `per_query_limit` to `SearchMemoriesRequest` in `contracts/openapi/openapi.yml` and add `matchedQueries` to `MemoryItem`.
- [x] Add same fields to `AdminSearchMemoriesRequest` in `contracts/openapi/openapi-admin.yml` and add `matchedQueries` to `AdminMemoryItem`.
- [x] Add `MemorySearchQuery` message and `queries`/`per_query_limit` fields to `SearchMemoriesRequest` and `AdminSearchMemoriesRequest` in `contracts/protobuf/memory/v1/memory_service.proto`; add `matched_queries` to `MemoryItem` and `AdminMemoryItem`.
- [x] Regenerate Go OpenAPI clients (`go generate .`) and gRPC stubs (`go generate .`).
- [x] Regenerate/validate Java generated clients through targeted Quarkus REST/proto compile.
- [x] Add `MatchedQueries []string` to `registryepisodic.MemoryItem`.
- [x] Implement `multiQuerySemanticSearch` helper in `internal/plugin/route/memories/memories.go` with batched embed, per-query vector search, RRF merge, and query attribution.
- [x] Wire `queries`/`per_query_limit` in `searchMemories` (user REST) and `HandleAdminSearchMemories` (admin REST) in `memories.go`.
- [x] Wire `queries`/`per_query_limit` in the gRPC `MemoriesService.SearchMemories` handler in `internal/grpc/server.go`.
- [x] Wire `queries`/`per_query_limit` in the gRPC `AdminMemoriesService.SearchMemories` handler in `internal/grpc/server.go`.
- [x] Propagate `matchedQueries` through `toAPIMemoryItem`, `toAdminMemoryItem`, and their gRPC equivalents.
- [x] Add BDD scenarios to `internal/bdd/testdata/features/memories-rest.feature` covering validation behavior.
- [x] Add vector-backed BDD scenario to `internal/bdd/testdata/features-sqlite/memories-sqlite.feature` covering attribution and merged user results.
- [x] Add admin REST vector-backed BDD scenario to `internal/bdd/testdata/features-sqlite/memories-sqlite.feature`.
- [x] Verify Go build, BDD coverage, aggregate tests, and site tests.

## Files to Modify

| File | Change |
|---|---|
| `docs/enhancements/implemented/106-multi-query-semantic-memory-search.md` | This enhancement doc |
| `contracts/openapi/openapi.yml` | Add `MemorySearchQuery` schema; add `queries`, `per_query_limit` to `SearchMemoriesRequest`; add `matchedQueries` to `MemoryItem` |
| `contracts/openapi/openapi-admin.yml` | Add `queries`, `per_query_limit` to `AdminSearchMemoriesRequest`; add `matchedQueries` to `AdminMemoryItem` |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add `MemorySearchQuery` message; add fields 10–11 to `SearchMemoriesRequest` and `AdminSearchMemoriesRequest`; add field 12 to `MemoryItem`; add field 13 to `AdminMemoryItem` |
| `internal/generated/api/api.gen.go` | Regenerated |
| `internal/generated/admin/admin.gen.go` | Regenerated |
| `internal/generated/pb/memory/v1/memory_service.pb.go` | Regenerated |
| `internal/generated/apiclient/apiclient.gen.go` | Regenerated |
| `internal/registry/episodic/plugin.go` | Add `MatchedQueries []string` to `MemoryItem` |
| `internal/plugin/route/memories/memories.go` | Add `multiQuerySemanticSearch`; wire into user and admin REST search handlers |
| `internal/grpc/server.go` | Wire multi-query fields into `MemoriesService.SearchMemories` and `AdminMemoriesService.SearchMemories` |
| `internal/bdd/testdata/features/memories-rest.feature` | Add multi-query validation BDD scenarios |
| `internal/bdd/testdata/features-sqlite/memories-sqlite.feature` | Add vector-backed user and admin multi-query BDD scenarios |

## Verification

```bash
# Regenerate Go/OpenAPI/protobuf artifacts
go generate .

# Regenerate Java REST and gRPC clients after contract changes
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus,quarkus/memory-service-proto-quarkus -am clean compile

# Build all Go packages
go build ./...

# Run focused unit tests for the memories route and episodic registry
go test ./internal/plugin/route/memories/... ./internal/registry/episodic/... ./internal/grpc/... -count=1

# Run BDD suite covering the changed feature file
go test ./internal/bdd -run '^TestFeaturesSQLite$' -count=1 > test.log 2>&1
# Search test.log for failures:
grep -E 'FAIL|Error|panic' test.log

# Run vector-backed BDD suite for semantic search coverage
go test ./internal/bdd -run '^TestFeaturesSQLiteVec$' -count=1 >> test.log 2>&1
grep -E 'FAIL|Error|panic' test.log
```

## Non-Goals

- Adding a new endpoint for multi-query search (it is an extension of the existing search operation).
- Changing single-query behavior or response shape.
- Supporting multi-query in the non-semantic (attribute-only) path.
- Streaming results per query as they arrive.
- Storing or persisting decomposed queries.
- Cross-namespace multi-query (each request still targets one `namespace_prefix`).

## Security Considerations

- OPA filter injection is called once and applies equally to all per-query vector searches. Multi-query does not bypass namespace authorization.
- Cross-user results are prevented by the namespace filter injected by OPA, identical to the single-query path.
- `matchedQueries` exposes only the `purpose` label (or query text) provided by the caller. It never exposes internal metadata, other users' data, or policy-injected values.
