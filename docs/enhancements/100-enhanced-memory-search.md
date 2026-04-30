---
status: proposed
---

# Enhancement 100: Enhanced Episodic Memory Search

> **Status**: Proposed.

## Summary

Enhance memory search in both REST and gRPC so clients can filter by safe policy attributes with a small operator language and receive deterministic bounded search results. This gives cognition processors and agent applications a governed retrieval surface for durable and TTL-backed memory items under a shared namespace prefix.

## Motivation

The current episodic memory search API is optimized for simple namespace-prefix lookup with flat equality filters plus the older unprefixed `in`/range operator shape documented by Enhancement 068. Cognition processors need a stronger but still generic substrate retrieval primitive:

- search under a shared namespace prefix such as `["user", "alice", "cognition.v1"]`, whose child namespaces hold facts, preferences, procedures, and short-lived cache notes
- filter by safe plaintext policy attributes such as memory kind, runtime ID, confidence, freshness, and provenance IDs
- sort semantic and attribute search results deterministically

Without these improvements, applications either over-fetch broad search results and filter them client-side, or a separate cognition-specific retrieval API has to wrap memory search. The better substrate boundary is to make `/v1/memories/search` expressive enough for these retrieval patterns while keeping memory data under the existing governance, archive, indexing, and encryption model.

[099](099-quarkus-cognition-processor.md) depends on this substrate enhancement for cognition-memory retrieval, and [101](101-grpc-api-parity-for-cognition.md) depends on this document for the memory-search portion of gRPC parity. The API remains generic and is not coupled to either processor.

## Design

### REST Contract

Keep the existing single `namespace_prefix` request field. Cognition processors should arrange their memory namespaces so one prefix can retrieve the relevant family of memory products, for example `["user", "alice", "cognition.v1"]`.

Example request:

```json
{
  "namespace_prefix": ["user", "alice", "cognition.v1"],
  "query": "help me continue the deployment fix",
  "filter": {
    "conversationIds": {"$in": ["uuid"]},
    "memoryKind": ["preference", "procedure", "summary", "bridge", "topic"],
    "runtimeId": "quarkus-reference-v1"
  },
  "limit": 12,
  "include_usage": true
}
```

Example response:

```json
{
  "items": [
    {
      "id": "uuid",
      "namespace": ["user", "alice", "cognition.v1", "procedures"],
      "key": "procedure:deployment-debugging",
      "attributes": {
        "memoryKind": "procedure",
        "runtimeId": "quarkus-reference-v1",
        "entryIds": ["uuid"]
      },
      "value": {
        "kind": "procedure",
        "statement": "User usually debugs deployments by checking logs, then environment drift, then rollout history.",
        "provenance": {
          "entry_ids": ["uuid"]
        },
        "runtime": {
          "id": "quarkus-reference-v1",
          "version": 1
        }
      },
      "score": 0.87
    }
  ]
}
```

`SearchMemoriesRequest` becomes:

| Field | Required | Notes |
| --- | --- | --- |
| `namespace_prefix` | yes | Existing non-empty namespace prefix. It is validated against `EpisodicMaxDepth` and passed through OPA filter injection. Empty segments are invalid. |
| `query` | no | Free text semantic query. When present and non-blank, requests vector retrieval under the effective prefix; unavailable semantic search fails as described below. |
| `filter` | no | Attribute filter expression. Existing flat equality filters still work; operator form is added below. |
| `limit` | no | Default 10, maximum 100. |
| `include_usage` | no | Includes usage metadata without incrementing usage counters, matching current search behavior. |
| `archived` | no | Existing `exclude|include|only` archive filter. |

The old REST `offset` request field is removed and rejected if sent. The experimental `order` request field is not added and is also rejected if sent. Search results are intentionally not pageable; callers request a bounded top-k result set with `limit`. Because generated JSON request structs normally ignore unknown fields, the route must use strict request-body validation or an explicit raw-body check for these obsolete fields instead of relying only on generated binding.

### gRPC Contract

The gRPC `SearchMemoriesRequest` must stay semantically aligned with REST. The same validation, defaults, OPA filter injection, safe attributes, ordering, archive filtering, TTL visibility, and usage metadata behavior apply to both transports:

```protobuf
message SearchMemoriesRequest {
  repeated string namespace_prefix = 1;
  string query = 2;
  optional google.protobuf.Struct filter = 3;
  int32 limit = 4;
  reserved 5; // old offset field
  bool include_usage = 6;
  ArchiveFilter archived = 7;
}

message SearchMemoriesResponse {
  repeated MemoryItem items = 1;
}
```

The old protobuf `offset` field is removed under the repo's pre-release no-compatibility rule, but field number 5 stays reserved to avoid accidental wire collisions while generated clients catch up. REST removes `offset` at the same time so both transports expose non-pageable bounded search.

### Implicit Search Mode and Ordering

Search mode is derived from `query`; clients do not choose an explicit sort mode.

When `query` is present and non-blank, the request is a semantic top-k search:

- The server requires an embedder and vector search backend. If semantic search is unavailable, REST returns `503` and gRPC returns `FAILED_PRECONDITION`.
- Results are ranked by vector score descending.
- Ties are broken by `createdAt` descending, then `id` ascending so repeated searches over the same indexed data return a stable order.
- `score` is populated on returned items.
- No semantic-hit fallback to attribute-only search is allowed. A semantic query with no matching vectors returns an empty `items` array.

When `query` is omitted or blank, the request is an attribute-only top-k search:

- Results are ordered by `createdAt` descending, then `id` descending, matching the existing newest-first memory search behavior.
- `score` is omitted.

This keeps the current useful behavior of query-driven relevance search while making the implicit ordering explicit and preventing a semantic query from silently broadening into a generic newest-first lookup.

### Filter Expression

The filter language stays intentionally small and maps to plaintext policy attributes, not encrypted memory values. The canonical request language uses Mongo-style `$` operators, limited to predicates that can be pushed down to the supported SQL, MongoDB, and Qdrant search paths. A field may be:

- a scalar, equivalent to `$eq`
- an array, equivalent to `$in`
- an operator object with one of `$eq`, `$in`, `$exists`, `$gte`, `$lte`

Example:

```json
{
  "memoryKind": {"$in": ["procedure", "summary", "bridge"]},
  "runtimeId": "quarkus-reference-v1",
  "conversationIds": {"$in": ["uuid"]},
  "confidence": {"$in": ["medium", "high"]},
  "freshness": {"$in": ["fresh", "stable"]}
}
```

Unsupported operators return `400`. Type mismatches do not match rows. OPA filter injection receives the parsed caller filter and may narrow it, but may not broaden it.

Existing scalar equality filters continue to work. Existing request array values become canonical `$in` filters. The older unprefixed operator objects from Enhancement 068 (`{"in": [...]}`, `{"gte": ...}`) are replaced by the `$` form under the repo's pre-release no-compatibility rule; site docs and policy examples must be updated in the same change. `$ne`, `$nin`, `$exists: false`, and other negative or missing-field predicates are intentionally not part of the API because they are not reliably pushdownable across vector stores. Filter validation runs after OPA filter injection so unsupported or malformed caller filters and policy-injected filters are rejected before they reach any store. Stores and vector backends must not silently ignore unknown operator objects.

#### Matching Rules

Filter fields are ANDed. Multiple operators on the same field are also ANDed.

Stored policy attributes may be scalars or arrays:

- `$eq` matches scalar equality, or membership when the stored attribute is an array.
- `$in` matches when a scalar attribute is in the requested set, or when a stored array overlaps the requested set.
- `$gte` and `$lte` require an existing numeric attribute or RFC 3339 timestamp string. They do not match arrays, missing fields, null values, or arbitrary strings. The normalizer must not mix numeric and timestamp bounds on the same field.
- `$exists` must be `true`. It matches present non-null attributes with at least one scalar value or one array element. `$exists: false` is rejected. Attribute extraction policies should omit unknown values instead of emitting nulls.

Missing and null fields never match. This keeps the API positive-only and avoids backend-specific missing-field behavior.

#### Caller and Policy Filter Composition

The caller filter and OPA-injected `attribute_filter` must be conjoined, not overwritten. If both sides constrain the same field, the implementation must intersect those constraints. An unsatisfiable intersection returns zero rows without revealing whether inaccessible rows exist. For example, caller `{"memoryKind": "procedure"}` plus policy `{"memoryKind": "summary"}` produces an empty result, not either side winning by map overwrite.

The policy injection API should return the effective namespace prefix and policy `attribute_filter` separately from the caller filter, or return a structured conjunction that preserves both. It should not merge filters into a plain map before normalization, because duplicate policy/caller keys can be lost. The normalizer may keep a simple per-field operator map when constraints can be represented directly, or it may compile to an internal predicate tree with explicit AND nodes. The store and vector interfaces should receive the normalized representation or a validated compiled form; they should not receive raw JSON maps where duplicate policy/caller constraints can be lost.

### Cognition Attributes

The built-in default policy continues to extract only the namespace guard attributes (`namespace`, `sub`). Cognition deployments need a configured `attributes.rego` example, or a packaged cognition policy bundle, that extracts these additional safe attributes:

| Attribute | Source | Purpose |
| --- | --- | --- |
| `memoryKind` | `value.kind` | Filter facts, preferences, procedures, decisions, summaries, bridge notes, and topic notes. |
| `runtimeId` | `value.runtime.id` | Isolate active, replay, or benchmark processor outputs. |
| `runtimeVersion` | `value.runtime.version` | Debug and benchmark processor versions. |
| `confidence` | `value.confidence` | Include only positively retrievable confidence levels such as medium or high. |
| `freshness` | `value.freshness` | Include only positively retrievable freshness states such as fresh or stable. |
| `conversationIds` | `value.provenance.conversation_ids` | Retrieve items related to a conversation lineage. |
| `entryIds` | `value.provenance.entry_ids` | Audit and targeted debug lookups. |
| `sourceHash` | `value.provenance.source_hash` | Idempotent replay and debug lookup. |

These attributes are safe to expose as `MemoryItem.attributes` when the configured policy extracts them; they must not contain raw evidence text, `clientId`, provider prompts, or provider cache keys.

### Search Execution

The route executes a search under one authorized namespace prefix:

1. Validate the prefix and reject malformed requests with `400`.
2. Call policy filter injection with the requested prefix and parsed caller filter, preserving caller and policy constraints separately.
3. If policy narrows the prefix to no accessible namespace, return zero rows. Do not leak whether inaccessible rows exist.
4. Normalize and validate the combined caller + policy filter.
5. Run semantic search when `query` is non-blank; otherwise run attribute-only search.
6. Apply the implicit ordering for the selected search mode.
7. Return up to `limit` rows. Search does not expose cursor or offset continuation.

Semantic search needs an explicit availability check or typed store error so the route can distinguish "semantic backend unavailable" from "backend available but no vector hits." Returning an empty vector result from a disabled local vector backend is not sufficient because this enhancement requires unavailable semantic search to fail with `503`/`FAILED_PRECONDITION`.

### Archive, TTL, and Usage Parity

Memory search behavior must be identical for REST and gRPC:

- omitted `archived` defaults to `exclude`
- `archived=exclude|include|only` maps to the same `ArchiveFilter` behavior in both transports
- TTL-backed memories are searchable before expiry and excluded after expiry
- TTL-backed memories expose `expires_at` in search results before expiry
- OPA attributes are extracted from TTL-backed memory values/index payloads the same way as durable memories
- `include_usage=true` returns usage metadata but does not increment usage counters
- search never exposes raw encrypted memory values outside the existing response shape or any policy-disallowed internal metadata

## Design Decisions

### Keep Retrieval Generic

The API remains a generic memory retrieval surface, while cognition remains an external producer of better memory items. This avoids a separate cognition-specific retrieval endpoint for data that is already stored in `/v1/memories`.

### Keep Search Non-Pageable

Memory search is a top-k retrieval primitive for agent context assembly, not a browsing API. Dropping pagination avoids pretending that approximate vector backends can resume a stable relevance-ordered result set. Callers that need broader recall can increase `limit` up to the configured maximum or narrow filters and retry.

### Keep Filters Pushdownable

All supported filter predicates must be expressible as backend payload/metadata filters in the SQL, MongoDB, and Qdrant paths. The API intentionally excludes negative predicates such as `$ne` and `$nin`, and missing-field predicates such as `$exists: false`, because those often require backend-specific post-filtering or unbounded overfetch to remain correct. Callers should model retrieval as positive allow-lists, for example `confidence: {"$in": ["medium", "high"]}` rather than `confidence != "low"`.

### Filter Policy Attributes Only

Filtering encrypted memory values directly would undermine the storage model and create datastore-specific behavior. Policy-extracted plaintext attributes are already the right boundary for governed filtering.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Enhanced episodic memory search
  Scenario: Prefix memory search applies authorization
    Given Alice can read ["user","alice","cognition.v1"]
    And Alice cannot read ["user","bob","cognition.v1"]
    When Alice searches namespace prefix ["user","alice","cognition.v1"]
    Then the response status should be 200
    And every returned item namespace should start with ["user","alice"]

  Scenario: Memory filters support operator expressions
    Given memories exist with memoryKind values "procedure", "bridge", and "decision"
    When POST /v1/memories/search filters memoryKind with {"$in":["procedure","bridge"]}
    Then the response status should be 200
    And no returned item should have memoryKind "decision"

  Scenario: Search rejects obsolete pagination fields
    When POST /v1/memories/search is called with offset 12
    Then the response status should be 400
    When POST /v1/memories/search is called with after_cursor "cursor-1"
    Then the response status should be 400

  Scenario: Search rejects unsupported ordering fields
    When POST /v1/memories/search is called with order "createdAtAsc"
    Then the response status should be 400

  Scenario: Semantic search with no vector hits returns no rows
    Given memories exist under ["user","alice","cognition.v1"] but no indexed memory matches "deployment cache"
    When POST /v1/memories/search is called with query "deployment cache"
    Then the response status should be 200
    And the response body field "items" should have size 0

  Scenario: Unsupported filter operators are rejected
    When POST /v1/memories/search filters memoryKind with {"$regex":"proc.*"}
    Then the response status should be 400

  Scenario: gRPC search matches REST archive filtering
    Given a memory is archived
    When SearchMemories is called over gRPC with archived ONLY
    Then the archived memory is returned
    When SearchMemories is called over gRPC with archived EXCLUDE
    Then the archived memory is not returned

  Scenario: TTL-backed memories are searchable before expiry
    Given a memory is written with ttl_seconds 3600 and index text "deployment cache"
    When SearchMemories is called over REST and gRPC with query "deployment cache"
    Then both responses include the memory with expires_at set
```

### Unit / Integration Tests

- Search rejects unsupported filter operators with `400`.
- Search rejects obsolete `offset`, `order`, and `after_cursor` fields with `400` instead of silently ignoring them.
- Type mismatches in operator filters are treated as non-matches.
- Search returns deterministic order across repeated calls.
- Semantic search does not fall back to attribute-only search when the vector result set is empty.
- Semantic search returns a failed-precondition style error when embeddings or vector search are unavailable.
- Multivalued policy attributes such as `conversationIds` and `entryIds` support array-membership filtering consistently across SQL, MongoDB, and Qdrant.
- OPA filter injection runs for the requested prefix and does not leak inaccessible rows.
- OPA-injected filters are validated with the same operator rules as caller filters.
- Caller and OPA filters on the same field are intersected; conflicting constraints return zero rows.
- `include_usage` enriches results without incrementing usage counters.
- REST and gRPC request/response shapes remain semantically aligned, including archive filters and TTL-backed memory visibility.

## Tasks

- [x] Add pushdownable memory search filter operators `$eq`, `$in`, `$exists: true`, `$gte`, and `$lte`.
- [x] Add a normalized filter representation and policy-injection API shape that preserve caller + OPA conjunctions and support multivalued policy attributes consistently.
- [x] Add deterministic implicit ordering for semantic and attribute-only bounded top-k search results.
- [x] Add semantic-search capability detection or a typed unavailable error for vector backends.
- [x] Align gRPC `SearchMemoriesRequest` and `SearchMemoriesResponse` with the REST contract.
- [x] Verify REST and gRPC search share archive-filter defaults, TTL visibility, safe attributes, and usage metadata behavior.
- [x] Update memory policy docs/examples so cognition memories can expose safe filter attributes.
- [x] Regenerate REST and gRPC clients.
- [x] Add BDD coverage for operator happy paths and vector-backend filter pushdown.

## Files to Modify

| File | Change |
| --- | --- |
| `docs/enhancements/100-enhanced-memory-search.md` | This enhancement doc |
| `contracts/openapi/openapi.yml` | Extend `POST /v1/memories/search` with richer filters, document implicit ordering, and remove offset pagination |
| `contracts/protobuf/memory/v1/memory_service.proto` | Align `SearchMemoriesRequest`/`SearchMemoriesResponse` with richer filters, implicit ordering, and removed offset pagination |
| `site/src/pages/docs/concepts/memories.md` | Update public memory-search docs from unprefixed operators and offset pagination to `$` operators and non-pageable bounded results |
| `internal/generated/api/` and generated clients | Regenerate from OpenAPI after memory search contract changes |
| `internal/generated/pb/` and generated gRPC clients | Regenerate from protobuf after `SearchMemories` contract changes |
| `internal/registry/episodic/plugin.go` | Update search request/store contracts for normalized operator filters, implicit ordering, semantic availability, and no pagination |
| `internal/plugin/route/memories/memories.go` | Parse enhanced search requests with strict obsolete-field rejection, conjoin caller and OPA filters, reject obsolete pagination/order fields, enforce semantic no-fallback behavior, and sort deterministically |
| `internal/episodic/policy.go` and configured `attributes.rego` examples | Return policy-injected filters without overwriting caller constraints, document or extract safe non-null cognition attributes from memory values/index payloads |
| `internal/plugin/store/postgres/episodic_store.go` | Support normalized operator filters, array policy attributes, and deterministic ordered memory search |
| `internal/plugin/store/sqlite/episodic_store.go` | Support normalized operator filters, array policy attributes, and deterministic ordered memory search |
| `internal/plugin/store/mongo/episodic_store.go` | Support normalized operator filters, array policy attributes, and deterministic ordered memory search |
| `internal/plugin/store/episodicqdrant/qdrant.go` | Support normalized operator filters and array policy attributes in Qdrant payload filters |
| `internal/bdd/testdata/features/memories-rest.feature` | Add REST coverage for operators, deterministic ordering, authorization, semantic no-fallback behavior, and obsolete pagination rejection |
| `internal/bdd/testdata/features-grpc/memories-grpc.feature` | Add gRPC coverage for aligned search semantics |

## Verification

```bash
# Regenerate Go/OpenAPI/protobuf artifacts touched by contract changes
go generate ./...

# Regenerate Java REST clients after OpenAPI changes
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus -am clean compile

# Compile Java gRPC stubs after protobuf changes
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-proto-quarkus -am clean compile

# Build affected Go packages after search changes
go build ./...

# Run focused Go tests for memory search behavior
go test ./internal/registry/episodic ./internal/episodic ./internal/plugin/route/memories ./internal/plugin/store/postgres ./internal/plugin/store/sqlite ./internal/plugin/store/mongo ./internal/plugin/store/episodicqdrant ./internal/grpc -count=1 > go-test.log 2>&1
# Search for failures using Grep tool on go-test.log

# Run REST/vector BDD coverage for the changed search semantics
go test ./internal/bdd -run '^TestFeaturesSQLite$' -count=1
go test ./internal/bdd -run '^TestFeaturesSQLiteVec$' -count=1

# Regenerate and verify the frontend OpenAPI client
cd frontends/chat-frontend && npm run generate && npm run lint && npm run build
```

## Non-Goals

- Adding a cognition-specific user-facing retrieval endpoint.
- Filtering encrypted memory value fields directly.
- Changing memory archive semantics.
- Adding general-purpose SQL/Mongo query passthrough to the memory API.
- Guaranteeing backward compatibility for the old `offset` request field.

## Security Considerations

- OPA filter injection is mandatory; enhanced search must not become a way to bypass namespace authorization.
- Inaccessible prefixes contribute zero rows and must not reveal whether matching rows exist.
- Search responses must not expose internal `clientId`, provider prompts, raw evidence text, or provider cache keys.
