---
status: proposed
---

# Enhancement 100: Enhanced Episodic Memory Search

> **Status**: Proposed.

## Summary

Enhance `POST /v1/memories/search` so clients can filter by safe policy attributes with a small operator language and page deterministically with opaque cursors. This gives cognition processors and agent applications a governed retrieval surface for durable and TTL-backed memory items under a shared namespace prefix.

## Motivation

The current episodic memory search API is optimized for simple namespace-prefix lookup with flat equality filters. Cognition processors need a stronger but still generic substrate retrieval primitive:

- search under a shared namespace prefix such as `["user", "alice", "cognition.v1"]`, whose child namespaces hold facts, preferences, procedures, and short-lived cache notes
- filter by safe plaintext policy attributes such as memory kind, runtime ID, confidence, freshness, and provenance IDs
- sort semantic and attribute search results deterministically
- page results with opaque cursors instead of offsets

Without these improvements, applications either over-fetch broad search results and filter them client-side, or a separate cognition-specific retrieval API has to wrap memory search. The better substrate boundary is to make `/v1/memories/search` expressive enough for these retrieval patterns while keeping memory data under the existing governance, archive, indexing, and encryption model.

[099](099-quarkus-cognition-processor.md) depends on this substrate enhancement for cognition-memory retrieval, but the API remains generic and is not coupled to that processor.

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
  "order": "relevance",
  "include_usage": true,
  "after_cursor": null
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
  ],
  "afterCursor": "opaque-cursor-or-null"
}
```

`SearchMemoriesRequest` becomes:

| Field | Required | Notes |
| --- | --- | --- |
| `namespace_prefix` | yes | Existing non-empty namespace prefix. It is validated against `EpisodicMaxDepth` and passed through OPA filter injection. Empty segments are invalid. |
| `query` | no | Free text semantic query. When present and an embedder is configured, search uses vector retrieval under the effective prefix. |
| `filter` | no | Attribute filter expression. Existing flat equality filters still work; operator form is added below. |
| `limit` | no | Default 10, maximum 100. |
| `order` | no | `relevance` (default when `query` is set), `createdAtDesc`, or `createdAtAsc`. `relevance` without `query` falls back to `createdAtDesc`. |
| `include_usage` | no | Includes usage metadata without incrementing usage counters, matching current search behavior. |
| `archived` | no | Existing `exclude|include|only` archive filter. |
| `after_cursor` | no | Opaque cursor for the selected order. `offset` is replaced with cursor pagination for deterministic paging. |

`SearchMemoriesResponse` adds nullable `afterCursor`. The cursor is opaque to clients. The server may encode a request hash, order keys, effective prefix position, and the last memory ID, but clients must only replay it with the same request fields except `after_cursor`. A cursor replayed with different search fields returns `400`.

### gRPC Contract

The gRPC `SearchMemoriesRequest` must stay semantically aligned with REST:

```protobuf
message SearchMemoriesRequest {
  repeated string namespace_prefix = 1;
  string query = 2;
  optional google.protobuf.Struct filter = 3;
  int32 limit = 4;
  bool include_usage = 5;
  ArchiveFilter archived = 6;
  string order = 7;
  optional string after_cursor = 8;
}

message SearchMemoriesResponse {
  repeated MemoryItem items = 1;
  optional string after_cursor = 2;
}
```

The old protobuf `offset` field is removed under the repo's pre-release no-compatibility rule.

### Filter Expression

The filter language stays intentionally small and maps to plaintext policy attributes, not encrypted memory values. A field may be:

- a scalar, equivalent to `$eq`
- an array, equivalent to `$in`
- an operator object with one of `$eq`, `$ne`, `$in`, `$nin`, `$exists`, `$gte`, `$lte`

Example:

```json
{
  "memoryKind": {"$in": ["procedure", "summary", "bridge"]},
  "runtimeId": "quarkus-reference-v1",
  "conversationIds": {"$in": ["uuid"]},
  "confidence": {"$in": ["medium", "high"]},
  "freshness": {"$ne": "stale"}
}
```

Unsupported operators return `400`. Type mismatches do not match rows. OPA filter injection receives the parsed filter and may narrow it, but may not broaden it.

### Cognition Attributes

The default and example memory policies should document how cognition deployments can extract these safe attributes:

| Attribute | Source | Purpose |
| --- | --- | --- |
| `memoryKind` | `value.kind` | Filter facts, preferences, procedures, decisions, summaries, bridge notes, and topic notes. |
| `runtimeId` | `value.runtime.id` | Isolate active, shadow, or benchmark processor outputs. |
| `runtimeVersion` | `value.runtime.version` | Debug and benchmark processor versions. |
| `confidence` | `value.confidence` | Filter weak or medium-confidence candidates. |
| `freshness` | `value.freshness` | Exclude stale or contradicted memories from retrieval. |
| `conversationIds` | `value.provenance.conversation_ids` | Retrieve items related to a conversation lineage. |
| `entryIds` | `value.provenance.entry_ids` | Audit and targeted debug lookups. |
| `sourceHash` | `value.provenance.source_hash` | Idempotent replay and debug lookup. |

These attributes are safe to expose as `MemoryItem.attributes`; they must not contain raw evidence text, `clientId`, provider prompts, or provider cache keys.

### Search Execution

The route executes a search under one authorized namespace prefix:

1. Validate the prefix and reject malformed requests with `400`.
2. Call `policy.InjectFilter` with the requested prefix and filter.
3. If policy narrows the prefix to no accessible namespace, return zero rows. Do not leak whether inaccessible rows exist.
4. Run semantic or attribute search for the effective prefix.
5. Sort the result set by the selected order. For `relevance`, sort by score descending, then `createdAt` descending, then `id` ascending for deterministic ties.
6. Return up to `limit` rows plus `afterCursor` when more rows are available.

Cursor state should include the request hash and enough ordering keys to resume deterministically under the same effective prefix.

## Design Decisions

### Keep Retrieval Generic

The API remains a generic memory retrieval surface, while cognition remains an external producer of better memory items. This avoids a separate cognition-specific retrieval endpoint for data that is already stored in `/v1/memories`.

### Use Opaque Cursors

Offset pagination is unstable when memory rows can be updated, archived, or inserted between pages. An opaque cursor can carry the request hash and ordering keys without exposing datastore-specific ordering details to clients.

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

  Scenario: Search pagination uses an opaque cursor
    Given more than 12 memories match query "deployment fix"
    When POST /v1/memories/search is called with limit 12
    Then the response body field "afterCursor" should not be null
    When POST /v1/memories/search is called again with the same request and that after_cursor
    Then the second page should not repeat memory ids from the first page

  Scenario: Search cursor is bound to the original request
    Given a search response returned afterCursor "cursor-1"
    When POST /v1/memories/search is called with after_cursor "cursor-1" and a different query
    Then the response status should be 400

  Scenario: Unsupported filter operators are rejected
    When POST /v1/memories/search filters memoryKind with {"$regex":"proc.*"}
    Then the response status should be 400
```

### Unit / Integration Tests

- Search rejects unsupported filter operators with `400`.
- Type mismatches in operator filters are treated as non-matches.
- Search returns deterministic order across repeated calls.
- OPA filter injection runs for the requested prefix and does not leak inaccessible rows.
- `include_usage` enriches results without incrementing usage counters.
- REST and gRPC request/response shapes remain semantically aligned.

## Tasks

- [ ] Add memory search filter operators `$eq`, `$ne`, `$in`, `$nin`, `$exists`, `$gte`, and `$lte`.
- [ ] Add deterministic result ordering and opaque `after_cursor` pagination.
- [ ] Align gRPC `SearchMemoriesRequest` and `SearchMemoriesResponse` with the REST contract.
- [ ] Update memory policy docs/examples so cognition memories can expose safe filter attributes.
- [ ] Regenerate REST and gRPC clients.
- [ ] Add BDD and store/route tests for authorization, operators, deterministic ordering, and cursor binding.

## Files to Modify

| File | Change |
| --- | --- |
| `docs/enhancements/100-enhanced-memory-search.md` | This enhancement doc |
| `contracts/openapi/openapi.yml` | Extend `POST /v1/memories/search` with richer filter/order options and cursor pagination |
| `contracts/protobuf/memory/v1/memory_service.proto` | Align `SearchMemoriesRequest`/`SearchMemoriesResponse` with filter/order options and cursor pagination |
| `internal/generated/api/` and generated clients | Regenerate from OpenAPI after memory search contract changes |
| `internal/generated/pb/` and generated gRPC clients | Regenerate from protobuf after `SearchMemories` contract changes |
| `internal/registry/episodic/plugin.go` | Update search request/store contracts for operator filters, order, and cursor semantics |
| `internal/plugin/route/memories/memories.go` | Parse enhanced search requests, apply OPA injection, sort deterministically, and emit `afterCursor` |
| `internal/plugin/store/postgres/episodic_store.go` | Support operator filters and deterministic ordered memory search |
| `internal/plugin/store/sqlite/episodic_store.go` | Support operator filters and deterministic ordered memory search |
| `internal/plugin/store/mongo/episodic_store.go` | Support operator filters and deterministic ordered memory search |
| `internal/episodic/policy.go` and configured `attributes.rego` examples | Document or extract safe cognition attributes from memory values/index payloads |
| `internal/bdd/testdata/features/memories-rest.feature` | Add REST coverage for operators, deterministic ordering, authorization, and cursors |
| `internal/bdd/testdata/features/memories-grpc.feature` | Add gRPC coverage for aligned search semantics |

## Verification

```bash
# Regenerate Go/OpenAPI/protobuf artifacts touched by contract changes
task generate:go

# Regenerate Java REST clients after OpenAPI changes
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus -am clean compile

# Build affected Go packages after search changes
go build ./internal/registry/episodic ./internal/plugin/route/memories ./internal/plugin/store/postgres ./internal/plugin/store/sqlite ./internal/plugin/store/mongo ./internal/cmd/serve

# Run focused Go tests for memory search behavior
go test ./internal/plugin/route/memories ./internal/plugin/store/postgres ./internal/plugin/store/sqlite ./internal/plugin/store/mongo > go-test.log 2>&1
# Search for failures using Grep tool on go-test.log
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
- Cursor payloads must be signed or otherwise tamper-evident if they encode server state client-side.
