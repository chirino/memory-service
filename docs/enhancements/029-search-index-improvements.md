---
status: partial
superseded-by:
  - 030-index-design.md
---

# Search and Index API Improvements

> **Status**: Partially implemented. Index model further redesigned to per-entry indexing by
> [030](030-index-design.md).

## Motivation

The search API needed improvements to better support UI use cases and provide a more consistent developer experience:

1. **Missing conversation context**: Search results only contained the matched entry, requiring additional API calls to get conversation details for display.

2. **No pagination**: The API used a `topK` limit but didn't support cursor-based pagination for browsing through large result sets.

3. **Response size**: Every search result included the full entry content, even when only metadata was needed for a search results list.

4. **Unused parameters**: The `conversationIds` filter and `before` temporal filter were implemented but never used in practice.

5. **Admin indexing**: Administrators and automated systems needed a way to index conversations without requiring an agent API key.

This enhancement improves the search API with cursor-based pagination, conversation metadata in results, and an optional entry inclusion flag. It also adds an admin index endpoint for privileged indexing operations.

## Dependencies

- **Enhancement 014 (Admin Access)**: Uses the admin role infrastructure for the new admin index endpoint.

## Design Decisions

### Search Result Improvements

Each search result now includes conversation metadata at the top level:

| Field | Description |
|-------|-------------|
| `conversationId` | UUID of the conversation containing the matched entry |
| `conversationTitle` | Decrypted title of the conversation |
| `score` | Relevance score (currently 1.0 for keyword search) |
| `highlights` | Highlighted matching text (reserved for future use) |
| `entry` | The matched entry (optional, controlled by `includeEntry`) |

### Cursor-Based Pagination

Following the pattern established by the conversations list API, search results now support cursor-based pagination:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `limit` | Maximum number of results to return | 20 |
| `after` | Cursor for pagination (entry UUID from previous page) | null |

Response includes:
```json
{
  "data": [...],
  "nextCursor": "entry-uuid-for-next-page"
}
```

The `nextCursor` is the entry ID of the last result, or null if there are no more results.

### Include Entry Option

The `includeEntry` parameter controls whether the full entry content is included:

| Value | Behavior |
|-------|----------|
| `true` (default) | Entry included in each result |
| `false` | Entry omitted, only metadata returned |

This reduces response size when building search result lists that link to conversations.

### Removed Parameters

The following parameters were removed as they were not used in practice:

| Removed Parameter | Reason |
|-------------------|--------|
| `conversationIds` | Never used; search is intended to be cross-conversation |
| `before` | Never used; temporal filtering not needed for semantic search |
| `topK` | Replaced by `limit` for consistency with other endpoints |

### Admin Index Endpoint

A new admin endpoint allows privileged users to index conversations without an agent API key:

**Endpoint**: `POST /v1/admin/conversations/index`

**Authorization**: Requires `indexer` or `admin` role

**Key differences from regular index endpoint**:

| Aspect | Regular `/v1/conversations/index` | Admin `/v1/admin/conversations/index` |
|--------|-----------------------------------|---------------------------------------|
| Authorization | Agent API key required | Indexer or admin role |
| Conversation access | Only conversations accessible to the agent | Any conversation |
| Client ID | From API key | Set to "admin" |
| Audit logging | No | No (intentionally not logged) |

### Indexer Role

A new `indexer` role is introduced for users/clients that need to index conversations but don't need full admin access:

```properties
# OIDC role mapping (no default - must be explicitly configured if using OIDC)
memory-service.roles.indexer.oidc.role=indexer

# User-based assignment
memory-service.roles.indexer.users=indexer-user-1,indexer-user-2

# Client-based assignment (for API key access)
memory-service.roles.indexer.clients=indexer-service
```

The role hierarchy is: `admin` > `indexer` (admin implies indexer access).

## API Changes

### REST API - Search

**Before:**
```yaml
SearchConversationsRequest:
  properties:
    query: string
    topK: integer
    conversationIds: array[uuid]
    before: string (ISO 8601 datetime)

SearchResult:
  properties:
    entry: Entry
    score: float
    highlights: string
```

**After:**
```yaml
SearchConversationsRequest:
  properties:
    query: string
    after: string (cursor)
    limit: integer (default 20)
    includeEntry: boolean (default true)

SearchResult:
  properties:
    conversationId: uuid
    conversationTitle: string
    score: float
    highlights: string
    entry: Entry (optional)

Response:
  properties:
    data: array[SearchResult]
    nextCursor: string (nullable)
```

### REST API - Admin Search

Same changes as regular search, plus admin-specific filters:

```yaml
AdminSearchEntriesRequest:
  properties:
    query: string
    after: string (cursor)
    limit: integer (default 20)
    userId: string (filter by owner)
    includeDeleted: boolean (default false)
    includeEntry: boolean (default true)
```

### REST API - Admin Index

```yaml
POST /v1/admin/conversations/index:
  requestBody:
    schema:
      $ref: '#/components/schemas/IndexTranscriptRequest'
  responses:
    '201':
      description: Index entry created
      schema:
        $ref: '#/components/schemas/Entry'
    '403':
      description: Indexer or admin role required
    '404':
      description: Conversation not found
```

### gRPC API

The protobuf definitions were updated similarly:

```protobuf
message SearchEntriesRequest {
  string query = 1;
  string after = 2;
  int32 limit = 3;
  optional bool include_entry = 4;
}

message SearchResult {
  bytes conversation_id = 1;
  string conversation_title = 2;
  float score = 3;
  string highlights = 4;
  Entry entry = 5;
}

message SearchEntriesResponse {
  repeated SearchResult results = 1;
  string next_cursor = 2;
}
```

## Scope of Changes

### 1. OpenAPI Contracts

**Files:**
- `memory-service-contracts/src/main/resources/openapi.yml`
- `memory-service-contracts/src/main/resources/openapi-admin.yml`

Updated schemas for search request/response and added admin index endpoint.

### 2. gRPC Contracts

**File:** `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`

Updated message definitions for search with pagination and conversation metadata.

### 3. DTO Classes

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/SearchEntriesRequest.java`
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/SearchResultDto.java`
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/SearchResultsDto.java` (new)
- `memory-service/src/main/java/io/github/chirino/memory/model/AdminSearchQuery.java`

Updated DTOs to match new API contracts.

### 4. Memory Store Interface and Implementations

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java`
- `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`
- `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java`

Changed `searchEntries` and `adminSearchEntries` return types to `SearchResultsDto` with pagination support.

### 5. REST Resource

**File:** `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java`

Added `adminIndexConversationTranscript` endpoint and updated `searchConversations` for new response format.

### 6. gRPC Service

**File:** `memory-service/src/main/java/io/github/chirino/memory/grpc/SearchGrpcService.java`

Updated to handle new pagination and `includeEntry` flag.

### 7. Role Resolver

**File:** `memory-service/src/main/java/io/github/chirino/memory/security/AdminRoleResolver.java`

Added `indexer` role with `hasIndexerRole()` and `requireIndexer()` methods.

### 8. Test Configuration

**File:** `memory-service/src/test/resources/application.properties`

Added indexer role configuration for test user "dave".

### 9. Cucumber Tests

**Files:**
- `memory-service/src/test/resources/features/index-rest.feature`
- `memory-service/src/test/resources/features/index-grpc.feature`
- `memory-service/src/test/resources/features/admin-rest.feature`
- `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java`

Added tests for pagination, new response format, and admin index endpoint.

## Implementation Plan

### Phase 1: Search API Updates

1. Update `SearchEntriesRequest` DTO (remove conversationIds/before, add limit/after/includeEntry)
2. Create `SearchResultsDto` wrapper with results list and nextCursor
3. Update `SearchResultDto` to include conversationId and conversationTitle
4. Update `PostgresMemoryStore.searchEntries()` with pagination logic
5. Update `MongoMemoryStore.searchEntries()` with pagination logic
6. Update REST endpoint response mapping
7. Update gRPC service and DTO mapper

### Phase 2: Admin Search API Updates

1. Update `AdminSearchQuery` model (replace topK with limit, remove conversationIds/before)
2. Update `openapi-admin.yml` with new schema
3. Change `adminSearchEntries` return type to `SearchResultsDto`
4. Update `PostgresMemoryStore.adminSearchEntries()` implementation
5. Update `MongoMemoryStore.adminSearchEntries()` implementation
6. Update `AdminResource.searchConversations()` response mapping

### Phase 3: Admin Index Endpoint

1. Add `IndexTranscriptRequest` schema to `openapi-admin.yml`
2. Add `/v1/admin/conversations/index` endpoint to `openapi-admin.yml`
3. Add indexer role to `AdminRoleResolver`
4. Add `adminIndexConversationTranscript()` to `AdminResource`
5. Add indexer role test configuration

### Phase 4: Testing

1. Update search pagination tests
2. Add tests for includeEntry=false
3. Add tests for conversationId/conversationTitle in results
4. Add admin index endpoint tests (success, role checks, validation)
5. Compile and verify all changes

## Verification

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# Run specific test scenarios
./mvnw test -Dcucumber.filter.tags="@search or @admin-index"

# Test search pagination manually
curl -X POST /v1/conversations/search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": "test", "limit": 2}'

# Test admin index
curl -X POST /v1/admin/conversations/index \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "conversationId": "...",
    "transcript": "...",
    "untilEntryId": "..."
  }'
```

## Files Modified (Complete List)

| File | Change Type |
|------|-------------|
| `memory-service-contracts/src/main/resources/openapi.yml` | Modified |
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Modified |
| `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/SearchEntriesRequest.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/SearchResultDto.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/SearchResultsDto.java` | New |
| `memory-service/src/main/java/io/github/chirino/memory/model/AdminSearchQuery.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/grpc/SearchGrpcService.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/grpc/GrpcDtoMapper.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/security/AdminRoleResolver.java` | Modified |
| `memory-service/src/test/resources/application.properties` | Modified |
| `memory-service/src/test/resources/features/index-rest.feature` | Modified |
| `memory-service/src/test/resources/features/index-grpc.feature` | Modified |
| `memory-service/src/test/resources/features/admin-rest.feature` | Modified |
| `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java` | Modified |

## Backward Compatibility

This enhancement includes breaking changes to the search API:

| Change | Impact |
|--------|--------|
| Removed `topK` parameter | Use `limit` instead |
| Removed `conversationIds` parameter | No direct replacement (search is cross-conversation) |
| Removed `before` parameter | No direct replacement |
| Response includes `conversationId`/`conversationTitle` | Additive, no impact |
| Response includes `nextCursor` | Additive, no impact |

Clients using the removed parameters will need to update their requests. Since this is a pre-release API, backward compatibility is not required.

## Future Considerations

- **Vector search integration**: The search infrastructure is ready for vector similarity search once embeddings are enabled.
- **Highlights**: The `highlights` field is reserved for returning matching text snippets.
- **Search analytics**: Audit logging of search queries for analytics purposes.
- **Search filters**: Additional filters like date ranges, channels, or content types.
