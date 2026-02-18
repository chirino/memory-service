---
status: proposed
---

# Enhancement 061: Consistent Pagination Across All List Endpoints

> **Status**: Proposed.

## Summary

Standardize cursor-based pagination across every list endpoint in both the Agent API and Admin API. Adopt a unified naming convention (`afterCursor` for both input and output), add missing cursor fields, and introduce pagination to list endpoints that currently return unbounded results. This also prepares the API for future reverse paging (`beforeCursor`).

## Motivation

The current pagination implementation has grown organically and contains several inconsistencies:

1. **Inconsistent naming across endpoints**:
   - Most endpoints use `after` (request) and `nextCursor` (response) — two different names for the same concept.
   - `GET /v1/conversations/unindexed` uses `cursor` for both request and response — a third convention.
   - Developers must memorize which name to use where.
2. **Ambiguity with timestamp filters**: The admin conversations endpoint has both `after` (pagination cursor) and `deletedAfter` (timestamp filter) as query parameters. The similar names make it easy to confuse a cursor with a date filter.
3. **Missing cursor in admin list conversations**: `GET /v1/admin/conversations` accepts `after` and `limit` parameters but does not include a cursor in the response schema, so clients cannot reliably page through results.
4. **Unbounded list endpoints**: Several endpoints return all results with no pagination support:
   - `GET /v1/conversations/{id}/memberships`
   - `GET /v1/conversations/{id}/forks`
   - `GET /v1/ownership-transfers`
   - `GET /v1/admin/conversations/{id}/memberships`
   - `GET /v1/admin/conversations/{id}/forks`
5. **Search pagination in body vs query**: The search endpoints (`POST /v1/conversations/search`, `POST /v1/admin/conversations/search`) accept `after` and `limit` in the request body rather than as query parameters. These endpoints are designed to be safely proxied to frontend apps, so POST is correct (see Design Decisions), but the field should be renamed for consistency.

### Risks Addressed

- **Silent data loss**: Without a cursor in admin conversation listings, clients may believe they've received all results when more exist.
- **Unbounded responses**: Endpoints without pagination could return thousands of items, causing memory pressure and slow responses.
- **Developer confusion**: Three different naming conventions (`after`/`nextCursor`, `cursor`/`cursor`, body `after`/`nextCursor`) increases integration friction.
- **Future-proofing**: Inconsistent naming now would make adding reverse paging (`beforeCursor`) harder later.

## Design

### Naming Convention: `afterCursor`

Use **`afterCursor`** as both the input parameter name and the output response field name across all paginated endpoints.

**Why same name for input and output:**
- Zero cognitive overhead — copy the value from the response and pass it in the next request under the same key.
- No mapping between different field names (`after` → `nextCursor` → `after`).
- Self-documenting: the `Cursor` suffix makes it unambiguous (can't be confused with timestamp filters like `deletedAfter`).

**Why `afterCursor` over alternatives:**
- As an **output field**, `afterCursor` means "use this cursor to get items after this page" — clear.
- As an **input parameter**, `afterCursor` means "give me items after this cursor" — equally clear.
- Extends naturally to reverse paging: `beforeCursor` (output = "use this to go backward", input = "give me items before this cursor").
- Disambiguates from `deletedAfter`/`deletedBefore` timestamp filters on admin endpoints.

**Comparison of approaches considered:**

| Approach | Input | Output | Same name? | Reverse paging |
|----------|-------|--------|------------|----------------|
| Current (majority) | `after` | `nextCursor` | No | Add `before`/`prevCursor` (still mismatched) |
| Current (unindexed) | `cursor` | `cursor` | Yes | Need `direction` param or split names |
| **Proposed** | **`afterCursor`** | **`afterCursor`** | **Yes** | **Add `beforeCursor`/`beforeCursor`** |

### Uniform Response Shape

Every list endpoint returns:

```json
{
  "data": [ ... ],
  "afterCursor": "550e8400-e29b-41d4-a716-446655440000"
}
```

When there are no more results, `afterCursor` is `null`.

### Changes Required

#### 1. Rename Existing Cursor Fields

All existing paginated endpoints must be updated:

| Endpoint | Old Input | New Input | Old Output | New Output |
|----------|-----------|-----------|------------|------------|
| `GET /v1/conversations` | `after` | `afterCursor` | `nextCursor` | `afterCursor` |
| `GET /v1/conversations/{id}/entries` | `after` | `afterCursor` | `nextCursor` | `afterCursor` |
| `POST /v1/conversations/search` | `after` (body) | `afterCursor` (body) | `nextCursor` | `afterCursor` |
| `GET /v1/conversations/unindexed` | `cursor` | `afterCursor` | `cursor` | `afterCursor` |
| `GET /v1/admin/conversations` | `after` | `afterCursor` | *(missing)* | `afterCursor` |
| `GET /v1/admin/conversations/{id}/entries` | `after` | `afterCursor` | `nextCursor` | `afterCursor` |
| `POST /v1/admin/conversations/search` | `after` (body) | `afterCursor` (body) | `nextCursor` | `afterCursor` |
| `GET /v1/admin/attachments` | `after` | `afterCursor` | `nextCursor` | `afterCursor` |

#### 2. Example: `GET /v1/conversations` (After Changes)

**OpenAPI**:
```yaml
parameters:
  - name: afterCursor
    in: query
    required: false
    description: Pagination cursor; returns conversations after this position.
    schema:
      type: string
      format: uuid
      nullable: true
  - name: limit
    in: query
    required: false
    description: Maximum number of conversations to return.
    schema:
      type: integer
      default: 20
      minimum: 1
      maximum: 200
responses:
  '200':
    schema:
      type: object
      properties:
        data:
          type: array
          items:
            $ref: '#/components/schemas/ConversationSummary'
        afterCursor:
          type: string
          nullable: true
          description: Cursor for the next page. Null when no more results.
```

**Usage**:
```bash
# First page
curl "http://localhost:8080/v1/conversations?limit=2" \
  -H "Authorization: Bearer <token>"
# Response: { "data": [...], "afterCursor": "660e8400-..." }

# Next page — pass afterCursor back as afterCursor
curl "http://localhost:8080/v1/conversations?limit=2&afterCursor=660e8400-..." \
  -H "Authorization: Bearer <token>"
# Response: { "data": [...], "afterCursor": null }  ← no more pages
```

#### 3. Example: `POST /v1/conversations/search` (After Changes)

For search endpoints, `afterCursor` lives in the request body:

```bash
# First page
curl -X POST "http://localhost:8080/v1/conversations/search" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{ "query": "design decisions", "limit": 2 }'
# Response: { "data": [...], "afterCursor": "eyJ..." }

# Next page
curl -X POST "http://localhost:8080/v1/conversations/search" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{ "query": "design decisions", "limit": 2, "afterCursor": "eyJ..." }'
```

#### 4. Add Pagination to Unbounded List Endpoints

Add `afterCursor` and `limit` query parameters plus `afterCursor` in the response to all currently unbounded endpoints.

##### `GET /v1/conversations/{id}/memberships`

| Parameter | Type | Default | Max | Description |
|-----------|------|---------|-----|-------------|
| `afterCursor` | string (UUID) | — | — | Cursor: membership user ID |
| `limit` | integer | 50 | 200 | Max items per page |

##### `GET /v1/conversations/{id}/forks`

| Parameter | Type | Default | Max | Description |
|-----------|------|---------|-----|-------------|
| `afterCursor` | string (UUID) | — | — | Cursor: fork conversation ID |
| `limit` | integer | 50 | 200 | Max items per page |

##### `GET /v1/ownership-transfers`

| Parameter | Type | Default | Max | Description |
|-----------|------|---------|-----|-------------|
| `afterCursor` | string (UUID) | — | — | Cursor: transfer ID |
| `limit` | integer | 50 | 200 | Max items per page |

##### `GET /v1/admin/conversations/{id}/memberships`

| Parameter | Type | Default | Max | Description |
|-----------|------|---------|-----|-------------|
| `afterCursor` | string (UUID) | — | — | Cursor: membership user ID |
| `limit` | integer | 50 | 1000 | Max items per page |

##### `GET /v1/admin/conversations/{id}/forks`

| Parameter | Type | Default | Max | Description |
|-----------|------|---------|-----|-------------|
| `afterCursor` | string (UUID) | — | — | Cursor: fork conversation ID |
| `limit` | integer | 50 | 1000 | Max items per page |

### Pagination Limits Summary (After Changes)

#### Agent API

| Endpoint | Default | Max |
|----------|---------|-----|
| `GET /v1/conversations` | 20 | 200 |
| `GET /v1/conversations/{id}/entries` | 50 | 200 |
| `GET /v1/conversations/{id}/memberships` | 50 | 200 |
| `GET /v1/conversations/{id}/forks` | 50 | 200 |
| `POST /v1/conversations/search` | 20 | 200 |
| `GET /v1/conversations/unindexed` | 100 | 200 |
| `GET /v1/ownership-transfers` | 50 | 200 |

#### Admin API

| Endpoint | Default | Max |
|----------|---------|-----|
| `GET /v1/admin/conversations` | 100 | 1000 |
| `GET /v1/admin/conversations/{id}/entries` | 50 | 1000 |
| `GET /v1/admin/conversations/{id}/memberships` | 50 | 1000 |
| `GET /v1/admin/conversations/{id}/forks` | 50 | 1000 |
| `POST /v1/admin/conversations/search` | 20 | 1000 |
| `GET /v1/admin/attachments` | 50 | 1000 |

### Future: Reverse Paging

This naming convention is designed to extend naturally to reverse paging when needed:

```json
// Response with bidirectional cursors
{
  "data": [ ... ],
  "afterCursor": "abc123",
  "beforeCursor": "def456"
}

// Forward: pass afterCursor back as afterCursor
GET ?afterCursor=abc123&limit=20

// Backward: pass beforeCursor back as beforeCursor
GET ?beforeCursor=def456&limit=20
```

Reverse paging is out of scope for this enhancement but the naming is ready for it.

### gRPC Alignment

The gRPC API already uses consistent `PageRequest` / `PageInfo` messages. Ensure the following gRPC services also support pagination for any list RPCs that currently lack it:

- `ListConversationMemberships` — add `PageRequest page` field
- `ListConversationForks` — add `PageRequest page` field

The gRPC `page_token` / `next_page_token` naming follows Google AIP conventions and does not need to match the REST naming.

### Backward Compatibility

Since the project is pre-release and backward compatibility is not required:

- All `after` query parameters are renamed to `afterCursor` without a deprecation period.
- All `nextCursor` response fields are renamed to `afterCursor`.
- The `cursor` parameter and response field on `/v1/conversations/unindexed` are renamed to `afterCursor`.
- Search request body fields `after` are renamed to `afterCursor`.
- Existing unbounded endpoints gain optional pagination parameters; omitting them still returns data (using defaults), so existing clients continue to work.

## Testing

### Cucumber Scenarios

Add pagination tests for each newly paginated endpoint:

```gherkin
Scenario: List memberships with pagination
  Given I have a conversation shared with 5 users
  When I list memberships with limit 2
  Then the response status should be 200
  And the response should contain 2 memberships
  And the response should have an afterCursor
  When I list memberships with limit 2 and afterCursor from previous response
  Then the response status should be 200
  And the response should contain at least 1 membership

Scenario: List forks with pagination
  Given I have a conversation with 3 forks
  When I list forks with limit 2
  Then the response status should be 200
  And the response should contain 2 forks
  And the response should have an afterCursor

Scenario: List ownership transfers with pagination
  Given there are 3 pending ownership transfers
  When I list ownership transfers with limit 2
  Then the response status should be 200
  And the response should contain 2 transfers
  And the response should have an afterCursor

Scenario: Unindexed entries uses afterCursor parameter
  Given there are 5 unindexed entries
  When I list unindexed entries with limit 2
  Then the response status should be 200
  And the response should contain 2 entries
  And the response should have an afterCursor
  When I list unindexed entries with limit 2 and afterCursor from previous response
  Then the response status should be 200
  And the response should contain at least 1 entry

Scenario: Admin list conversations returns afterCursor
  Given there are 3 conversations
  When I admin-list conversations with limit 2
  Then the response status should be 200
  And the response should contain 2 conversations
  And the response should have an afterCursor
```

### Update Existing Tests

All existing tests that reference `after`, `nextCursor`, or `cursor` pagination fields must be updated to use `afterCursor`. Search the test codebase for:
- `"after"` query parameter usage
- `"nextCursor"` response field assertions
- `"cursor"` parameter/field on unindexed endpoint

## Tasks

- [ ] Rename `after` → `afterCursor` and `nextCursor` → `afterCursor` on all agent API endpoints in `openapi.yml`
- [ ] Rename `cursor` → `afterCursor` on `GET /v1/conversations/unindexed` in `openapi.yml`
- [ ] Rename `after` → `afterCursor` and `nextCursor` → `afterCursor` on all admin API endpoints in `openapi-admin.yml`
- [ ] Add `afterCursor` to `GET /v1/admin/conversations` response in `openapi-admin.yml`
- [ ] Add `afterCursor`/`limit` to `GET /v1/conversations/{id}/memberships` in `openapi.yml`
- [ ] Add `afterCursor`/`limit` to `GET /v1/conversations/{id}/forks` in `openapi.yml`
- [ ] Add `afterCursor`/`limit` to `GET /v1/ownership-transfers` in `openapi.yml`
- [ ] Add `afterCursor`/`limit` to admin memberships and forks endpoints in `openapi-admin.yml`
- [ ] Update `SearchConversationsRequest` schema: `after` → `afterCursor`
- [ ] Update `AdminSearchRequest` schema: `after` → `afterCursor`
- [ ] Update `UnindexedEntriesResponse` schema: `cursor` → `afterCursor`
- [ ] Update all Java resource classes to use `afterCursor` parameter name
- [ ] Update store layer methods for renamed parameter
- [ ] Implement pagination in `SharingResource.java` (memberships)
- [ ] Implement pagination in `ConversationsResource.java` (forks)
- [ ] Implement pagination in `OwnershipTransferResource.java`
- [ ] Implement pagination in `AdminResource.java` (memberships, forks, fix conversations cursor)
- [ ] Update store layer methods to accept pagination parameters for new endpoints
- [ ] Add `PageRequest` to gRPC `ListConversationMemberships` and `ListConversationForks`
- [ ] Update gRPC service implementations
- [ ] Update all existing Cucumber tests for renamed fields
- [ ] Add Cucumber tests for all newly paginated endpoints
- [ ] Regenerate REST clients (`./mvnw -pl quarkus/memory-service-rest-quarkus clean compile`)
- [ ] Regenerate TypeScript client (`cd frontends/chat-frontend && npm run generate`)
- [ ] Update pagination concept page in `site/src/pages/docs/concepts/pagination.md`

## Files to Modify

| File | Change |
|------|--------|
| `memory-service-contracts/.../openapi.yml` | Rename all `after`→`afterCursor`, `nextCursor`→`afterCursor`, `cursor`→`afterCursor`; add pagination to memberships, forks, transfers |
| `memory-service-contracts/.../openapi-admin.yml` | Rename all `after`→`afterCursor`, `nextCursor`→`afterCursor`; add `afterCursor` to admin conversations response; add pagination to admin memberships, forks |
| `memory-service-contracts/.../memory_service.proto` | Add `PageRequest` to membership and fork list RPCs |
| `memory-service/.../api/ConversationsResource.java` | Rename `after`→`afterCursor`; add pagination to forks endpoint; rename response field |
| `memory-service/.../api/SharingResource.java` | Add pagination to memberships endpoint |
| `memory-service/.../api/OwnershipTransferResource.java` | Add pagination to transfers endpoint |
| `memory-service/.../api/AdminResource.java` | Rename params; fix conversations cursor; add pagination to admin memberships/forks |
| `memory-service/.../api/SearchResource.java` | Rename `cursor`→`afterCursor` on unindexed endpoint |
| `memory-service/.../api/dto/SearchEntriesRequest.java` | Rename `after`→`afterCursor` |
| `memory-service/.../store/MemoryStore.java` | Update method signatures |
| `memory-service/.../store/impl/PostgresMemoryStore.java` | Rename params; implement pagination for new endpoints |
| `memory-service/.../store/impl/MongoMemoryStore.java` | Rename params; implement pagination for new endpoints |
| `memory-service/.../grpc/MembershipsGrpcService.java` | Add pagination support |
| `memory-service/.../grpc/ConversationsGrpcService.java` | Add pagination to forks RPC |
| `memory-service/src/test/resources/features/*.feature` | Update existing pagination tests; add new scenarios |
| `memory-service/src/test/resources/features-grpc/*.feature` | Update existing pagination tests; add new gRPC scenarios |
| `memory-service/src/test/java/.../cucumber/StepDefinitions.java` | Update step definitions for `afterCursor` |
| `frontends/chat-frontend/src/**` | Update any pagination logic after client regeneration |
| `site/src/pages/docs/concepts/pagination.md` | Update documentation |

## Verification

```bash
# Compile
./mvnw compile

# Regenerate clients
./mvnw -pl quarkus/memory-service-rest-quarkus clean compile
cd frontends/chat-frontend && npm run generate && npm run lint && npm run build

# Run all tests
./mvnw test -pl memory-service > test.log 2>&1
# Search for failures using Grep tool on test.log
```

## Design Decisions

1. **Same name for input and output (`afterCursor`)**: Eliminates the cognitive overhead of mapping between `after` (input) and `nextCursor` (output). Copy the value from the response field directly into the next request parameter.
2. **`afterCursor` over `cursor`**: The `after` prefix makes the direction explicit and avoids collision with generic "cursor" concepts. It also disambiguates from timestamp filter parameters like `deletedAfter`.
3. **`afterCursor` over `nextCursor`**: As an input parameter, `afterCursor` reads naturally ("give me items after this cursor") while `nextCursor` would be awkward ("give me items... next cursor?").
4. **Future-proof for `beforeCursor`**: The naming pattern extends cleanly to reverse paging without any renaming of existing fields.
5. **Search endpoints stay POST**: The search endpoints are designed to be safely proxied to frontend SPAs. With GET, search queries would leak through browser history, `Referer` headers to third-party domains, and infrastructure access logs (CDNs, reverse proxies, load balancers). POST keeps the query in the request body, which is not logged by default infrastructure and never leaks through referrer headers. The trade-off is that `afterCursor` and `limit` live in the body for search — a justified exception for frontend safety.
6. **gRPC keeps its own convention**: The gRPC `page_token`/`next_page_token` naming follows Google AIP standards and is well-understood in the gRPC ecosystem. No need to force REST naming conventions onto gRPC.
