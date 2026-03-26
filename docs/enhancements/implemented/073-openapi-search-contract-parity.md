---
status: implemented
---

# Enhancement 073: Go Search OpenAPI Contract Parity

> **Status**: Implemented.
>
> **Current Contract Note**: Admin conversation search archive filtering now follows [094](./094-archive-operations.md). References below to `includeArchived` are historical; the current contract uses `archived=exclude|include|only`.

## Summary

Align the Go search handlers with the fields and behaviors documented in OpenAPI for agent and admin search, and extend agent `searchType` to support requesting multiple backends in one request (for example semantic + fulltext). This closes current request/response contract gaps for `searchType`, `groupByConversation`, `afterCursor`, and admin search pagination/deleted filtering.

## Motivation

The Go API currently accepts only a subset of documented search request fields and ignores others without warning.

Current parity gaps:

| Endpoint | OpenAPI field/behavior | Current Go behavior |
|---|---|---|
| `POST /v1/conversations/search` | `searchType` backend selection | ignored; route always does implicit semantic-then-fulltext fallback |
| `POST /v1/conversations/search` | `groupByConversation` (default `true`) | ignored |
| `POST /v1/conversations/search` | `afterCursor` request pagination | ignored on request; semantic path always returns `afterCursor: null` |
| `POST /v1/conversations/search` | `501 SearchTypeUnavailable` for unavailable requested type | currently returns fallback result or `503 search_disabled` |
| `POST /v1/admin/conversations/search` | `afterCursor` | ignored |
| `POST /v1/admin/conversations/search` | `includeArchived` | ignored |
| `POST /v1/admin/conversations/search` | response `afterCursor` | not returned |

This mismatch causes client confusion and breaks frontend expectations for safe pagination/search mode selection.

## Design

### 1. Agent Search Request Parity + Multi-Select `searchType`

Update `searchConversations` request binding to include all documented fields:

```go
type searchRequest struct {
    Query               string  `json:"query" binding:"required"`
    SearchType          any     `json:"searchType"` // string or []string, normalized after bind
    AfterCursor         *string `json:"afterCursor"`
    Limit               *int    `json:"limit"`
    IncludeEntry        *bool   `json:"includeEntry"`
    GroupByConversation *bool   `json:"groupByConversation"`
}
```

Behavior:

| Field | Default | Validation |
|---|---|---|
| `searchType` | `["auto"]` | accepts string or non-empty array of strings; each value must be known search type |
| `limit` | `20` | `1 <= limit <= 200` |
| `includeEntry` | `true` | boolean |
| `groupByConversation` | `true` | boolean |
| `afterCursor` | `nil` | opaque cursor token |

Invalid values return `400` with `code=validation_error`.

### 2. Agent SearchType Execution Semantics

Implement explicit mode selection with support for requesting multiple concrete search types.

Normalized selection behavior:

| Request form | Normalized execution plan |
|---|---|
| omitted | `["auto"]` |
| `"auto"` | `["auto"]` |
| `"semantic"` | `["semantic"]` |
| `["semantic","fulltext"]` | execute semantic and fulltext independently |

Execution semantics:

| Normalized plan | Behavior |
|---|---|
| `["auto"]` | Try semantic first; if empty/unavailable, try fulltext |
| concrete type list (no `auto`) | Execute each requested type independently; combine results |

If multiple concrete types are requested, `limit` applies per type.  
Example: `searchType=["semantic","fulltext"]` and `limit=10` returns up to 20 results.

Each backend contributes results tagged with its `kind` field (`qdrant`/`pgvector`/`postgres`/`mongo`).

`501` response body matches OpenAPI `SearchTypeUnavailableError`:

```json
{
  "error": "search_type_unavailable",
  "message": "One or more requested search types are not available on this server.",
  "availableTypes": ["fulltext"]
}
```

### 3. Agent `groupByConversation` + `afterCursor`

#### Fulltext path (single-type or multi-type component)

Extend `SearchEntries` store API to accept:
- `afterCursor` (entry id cursor)
- `groupByConversation`

Postgres strategy:
- keep `ORDER BY score DESC, entry_id ASC` for deterministic pagination
- when grouped: rank rows per conversation (`ROW_NUMBER() OVER (PARTITION BY conversation_id ORDER BY score DESC, entry_id ASC)`) and keep `row_number = 1`
- apply per-type cursor and `limit+1` pagination on the final ordered result set

Mongo strategy:
- grouped mode uses aggregation pipeline (`$sort` by score, `$group` by `conversation_id`, first doc wins)
- apply per-type cursor filtering and `limit+1` pagination after grouping

#### Semantic path (single-type or multi-type component)

Add post-processing parity in route-level semantic execution:
- fetch vector candidates with bounded overfetch (internal `candidateLimit`, for example `min(limit*3+1, 1000)` when grouped)
- enrich, sort deterministically by `score DESC, entry_id ASC`
- optionally dedupe by conversation (`groupByConversation=true`)
- apply per-type cursor and `limit+1` pagination in memory

#### Combined pagination cursor

When multiple concrete types are requested, response pagination uses a composite opaque cursor that stores per-type cursor state.

Example decoded structure:

```json
{
  "types": ["semantic", "fulltext"],
  "cursors": {
    "semantic": "entry-uuid-or-opaque-subcursor",
    "fulltext": "entry-uuid-or-opaque-subcursor"
  }
}
```

`afterCursor` is base64url-encoded JSON for transport.

### 4. Admin Search Parity

Extend admin request binding and store query model:

```go
type AdminSearchQuery struct {
    Query          string
    UserID         *string
    Limit          int
    IncludeEntry   bool
    IncludeArchived bool
    AfterCursor    *string
}
```

Behavior changes:
- honor `includeArchived` in admin search filters
- honor `afterCursor` with deterministic ordering and `limit+1`
- include response `afterCursor` for paging
- validate `1 <= limit <= 1000`

### 5. Contract/Implementation Drift Guard

Add BDD coverage for the documented fields so future drift is caught quickly.

Required scenarios:

```gherkin
Scenario: Agent searchType semantic returns 501 when semantic search is disabled
  When I search conversations with request:
  """
  { "query": "test", "searchType": "semantic" }
  """
  Then the response status should be 501
  And the response body "error" should be "search_type_unavailable"

Scenario: Agent search with semantic+fulltext applies limit per type
  When I search conversations with request:
  """
  { "query": "design", "searchType": ["semantic","fulltext"], "limit": 10 }
  """
  Then the response status should be 200
  And the search response should contain at most 20 results

Scenario: Agent search honors groupByConversation default true
  Given multiple matching entries in the same conversation
  When I search conversations with request:
  """
  { "query": "design" }
  """
  Then only the best-scoring entry per conversation is returned

Scenario: Admin search includeArchived false excludes deleted conversations
Scenario: Admin search includeArchived true includes deleted conversations
Scenario: Admin search returns afterCursor when more results exist
```

## Testing

### Unit/Store Tests

- Postgres fulltext grouped query returns one row per conversation in score order.
- Postgres cursor pagination remains stable under tie scores (`entry_id` tie-breaker).
- Mongo grouped pipeline preserves deterministic ordering for pagination.
- Admin search filters archived rows based on `includeArchived`.

### BDD Coverage

- Extend existing search and validation features for:
  - `searchType` behavior (single type, multi-type, `auto`)
  - unsupported mode returning `400`
  - unavailable explicit mode returning `501`
  - per-type limit behavior for multi-type requests
  - `groupByConversation` true/false behavior
  - request `afterCursor` paging behavior
- Extend admin REST feature for `includeArchived` and admin search pagination.
- Run both Postgres and Mongo/Qdrant BDD suites.

## Tasks

- [x] Update `searchType` request contract to accept string or array of search types.
- [x] Update agent search route request model to bind `searchType`, `afterCursor`, `groupByConversation`.
- [x] Add `searchType` normalization/validation and explicit execution logic (including multi-type requests) with OpenAPI-compatible `501` errors.
- [x] Extend store search interfaces to carry `afterCursor` and `groupByConversation`.
- [x] Implement grouped and paginated fulltext search in Postgres store.
- [x] Implement grouped and paginated fulltext search in Mongo store.
- [x] Add semantic result grouping and per-type cursor pagination in Go route path.
- [x] Implement multi-type result composition with per-type `limit` and composite cursor encoding/decoding.
- [x] Extend admin search query model with `includeArchived` and `afterCursor`.
- [x] Implement admin search deleted filtering + pagination in Postgres and Mongo stores.
- [x] Return `afterCursor` from `POST /v1/admin/conversations/search` responses.
- [x] Add/adjust BDD scenarios for all newly enforced OpenAPI behaviors.
- [x] Update docs/examples that rely on old implicit behavior if needed.

## Files to Modify

| File | Change |
|---|---|
| `memory-service-contracts/src/main/resources/openapi.yml` | Update `searchType` schema to support multi-value selection and adjust cursor description/constraints |
| `internal/plugin/route/search/search.go` | Parse missing request fields; implement `searchType` dispatch, grouping/cursor semantics, and `501` behavior |
| `internal/registry/store/plugin.go` | Extend search query method signatures and `AdminSearchQuery` fields |
| `internal/plugin/store/postgres/postgres.go` | Add grouped search + cursor support for user and admin search; include deleted filtering for admin |
| `internal/plugin/store/mongo/mongo.go` | Add grouped search + cursor support for user and admin search; include deleted filtering for admin |
| `internal/plugin/route/admin/admin.go` | Parse `includeArchived` + `afterCursor` for admin search and return response cursor |
| `internal/bdd/testdata/features/index-rest.feature` | Add agent search parity scenarios (`groupByConversation`, pagination cursor) |
| `internal/bdd/testdata/features/validation-rest.feature` | Add invalid `searchType` and out-of-range limit validation scenarios |
| `internal/bdd/testdata/features/admin-rest.feature` | Add admin `includeArchived` and `afterCursor` scenarios |
| `internal/bdd/testdata/features-qdrant/search-mongo-qdrant.feature` | Add semantic mode and pagination/grouping checks for Mongo+Qdrant |
| `site/src/pages/docs/*search*.mdx` | Update docs to reflect enforced semantics and error behavior |

## Verification

```bash
# Ensure devcontainer is running for build/test commands.
wt up

# Compile affected Go code.
wt exec -- go build ./internal/...

# Run Postgres BDD suite.
wt exec -- sh -lc 'go test ./internal/bdd -run TestFeatures -count=1 > bdd-pg.log 2>&1'
rg -n "FAIL|panic:|--- FAIL|Error:" bdd-pg.log

# Run Mongo + Qdrant BDD suite.
wt exec -- sh -lc 'go test ./internal/bdd -run TestFeaturesMongo -count=1 > bdd-mongo.log 2>&1'
rg -n "FAIL|panic:|--- FAIL|Error:" bdd-mongo.log
```

## Non-Goals

- Adding a new public `topK` request field. `limit` remains the public result-size control.
- Reworking gRPC search contracts in this enhancement.
- Changing datastore schemas solely for this parity work.

## Design Decisions

1. Keep `limit` as the only public result-size parameter, applied per requested search type; use internal overfetch only when grouping/cursoring semantic results.
2. Use deterministic ordering (`score DESC`, tie-break `entry_id ASC`) for stable cursor pagination.
3. Use opaque composite cursors so pagination remains well-defined for multi-type searches.
4. Avoid adding a synthetic `both` mode; explicit type lists scale better as new search backends are added.
5. Favor route/store parity with existing OpenAPI fields over introducing new contract fields.

## Open Questions

1. Should admin search remain fulltext-only (current Go behavior) or also adopt vector/semantic execution in this enhancement?
2. Should mixed-backend combined results be returned grouped by backend execution order, or interleaved by a normalization strategy across backend scores?
