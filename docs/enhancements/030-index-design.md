# Index Endpoint Redesign

## Motivation

The current indexing API has several issues:

### 1. Confusing Dual-Endpoint Design

Two endpoints exist with unclear separation:

- **`/v1/conversations/index`** (in `openapi.yml`): Requires an agent API key
- **`/v1/admin/conversations/index`** (in `openapi-admin.yml`): Requires indexer or admin role

The admin endpoint doesn't behave like other admin endpoints:
- No audit logging
- No admin role requirement (indexer role is sufficient)
- No special access to deleted resources

### 2. Conversation-Level Indexing is Too Coarse

The current design indexes a single transcript per conversation with an `untilEntryId` marker. This has limitations:

- Search results point to conversations, not specific entries
- Re-indexing requires re-processing the entire transcript
- Cannot associate search hits with the entry that contains the match

### 3. Entry-Level Indexing is More Useful

Indexing text per entry enables:

- Search results that link directly to the matching entry
- Incremental indexing (only index new entries)
- More accurate relevance scoring
- Better highlights showing exactly where the match occurred

## Dependencies

- **Enhancement 029 (Search Index Improvements)**: Introduced the indexer role infrastructure.
- **Enhancement 015 (Background Task Queue)**: Used for vector store retry logic with singleton tasks.

## Design Decisions

### Single Index Endpoint with Per-Entry Text

The indexing functionality will be consolidated into a single endpoint that accepts searchable text per entry:

**Endpoint**: `POST /v1/conversations/index`

**Authorization**: Requires `indexer` or `admin` role

**Request Body** (array of entries to index):
```json
[
  {
    "conversationId": "550e8400-e29b-41d4-a716-446655440000",
    "entryId": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
    "indexedContent": "The searchable text for this entry"
  },
  {
    "conversationId": "550e8400-e29b-41d4-a716-446655440000",
    "entryId": "7ca8c921-0ebe-22e2-91c5-11d05ge541d9",
    "indexedContent": "The searchable text for another entry"
  },
  {
    "conversationId": "661f9511-f30c-52e5-b827-557766551111",
    "entryId": "8db9d032-1fcf-33f3-a2d6-22e16hf652ea",
    "indexedContent": "Another conversation's entry text"
  }
]
```

**Key characteristics**:

| Aspect | Current Design | New Design |
|--------|----------------|------------|
| Authorization | Agent API key or indexer role | Indexer or admin role only |
| Granularity | Single transcript per conversation | Text per entry |
| Request format | Nested conversations with entries | Flat array of entries |
| Search results | Point to conversation | Point to specific entry |
| Incremental updates | Re-index entire transcript | Index only new entries |
| API contract | Split across two files | Single endpoint in openapi.yml |

### Removed Endpoints

| Endpoint | Reason for Removal |
|----------|-------------------|
| `POST /v1/conversations/index` (old) | Replaced with new per-entry design |
| `POST /v1/admin/conversations/index` | Consolidated into `/v1/conversations/index` |
| gRPC `IndexConversationTranscript` (old) | Replaced with new per-entry design |

### New gRPC Endpoints

New gRPC endpoints will mirror the REST endpoint behavior:

```protobuf
rpc IndexConversations(IndexConversationsRequest) returns (IndexConversationsResponse);
rpc ListUnindexedEntries(ListUnindexedEntriesRequest) returns (ListUnindexedEntriesResponse);
```

Both gRPC endpoints require the `indexer` role, matching the REST behavior. The `ListUnindexedEntries` RPC supports cursor-based pagination and returns entries sorted by `createdAt`.

### List Unindexed Entries Endpoint

A batch indexing job needs to discover which entries require indexing. This endpoint returns entries from the `history` channel that have not yet had their index content generated (where `indexedContent` is null).

**Endpoint**: `GET /v1/conversations/unindexed`

**Authorization**: Requires `indexer` or `admin` role

**Workflow**:
```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Batch Job      │     │  Text Processing│     │  Memory Service │
│                 │     │  (optional)     │     │                 │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                       │
         │  GET /v1/conversations/unindexed              │
         │──────────────────────────────────────────────>│
         │                       │                       │
         │  Paginated list of entries (with conversationId)
         │<──────────────────────────────────────────────│
         │                       │                       │
         │  Process entry content (summarize, redact, etc)
         │──────────────────────>│                       │
         │                       │                       │
         │  Processed text       │                       │
         │<──────────────────────│                       │
         │                       │                       │
         │  POST /v1/conversations/index                 │
         │──────────────────────────────────────────────>│
         │                       │                       │
         │  {"indexed": N}       │                       │
         │<──────────────────────────────────────────────│
         │                       │                       │
         │  (repeat with next page cursor until empty)   │
         └                       └                       └
```

**Response includes**:
- Paginated array of entries with their conversation ID
- Entries sorted by `createdAt` for consistent ordering
- Pagination cursor for fetching next batch

**Pagination**: Results are paginated using cursor-based pagination. The `indexed_content` index enables efficient queries for entries where `indexed_content IS NULL`, sorted by `createdAt`.

**Query parameters**:

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `limit` | integer | 100 | Maximum number of entries to return |
| `cursor` | string | null | Pagination cursor from previous response |

**Response format**:
```json
{
  "data": [
    {
      "conversationId": "550e8400-e29b-41d4-a716-446655440000",
      "entry": {
        "id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
        "channel": "history",
        "contentType": "message",
        "content": [...],
        "createdAt": "2025-01-10T14:40:12Z"
      }
    }
  ],
  "cursor": "eyJjcmVhdGVkQXQiOiIyMDI1LTAxLTEwVDE0OjQwOjEyWiJ9"
}
```

The index endpoint uses upsert semantics, so re-indexing an entry is safe if entries are processed multiple times. When an entry is indexed, the `indexedContent` is set, removing it from future unindexed queries.

### Inline Indexing During Entry Creation

Entries can be indexed at creation time by providing an `indexedContent` field in the request body. This avoids the need for a separate indexing step for most entries.

**Endpoint**: `POST /v1/conversations/{conversationId}/entries`

**Request Body with indexedContent**:
```json
{
  "userId": "user_1234",
  "channel": "history",
  "contentType": "message",
  "content": [
    {
      "type": "text",
      "text": "Based on your past chats, here are three possible approaches…"
    }
  ],
  "indexedContent": "Based on your past chats, here are three possible approaches…"
}
```

**Indexing Flow**:

```
┌─────────────────────────────────────────────────────────────┐
│  POST /v1/conversations/{id}/entries                        │
│  with indexedContent field                                  │
└─────────────────────────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│  1. Store entry in datastore                                │
│     - indexedContent = provided value                       │
│     - indexedAt = null                                      │
└─────────────────────────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│  2. Attempt synchronous vector store indexing               │
└─────────────────────────────────────────────────────────────┘
                    ╱         ╲
               success       failure
                  │             │
                  ▼             ▼
┌─────────────────────┐  ┌─────────────────────────────────┐
│ Update entry:       │  │ Log warning                     │
│ indexedAt = now()   │  │ Leave indexedAt = null          │
│ Return success      │  │ Create singleton retry task     │
└─────────────────────┘  │ Return success                  │
                         └─────────────────────────────────┘
                                  │
                                  ▼
                    ┌─────────────────────────────┐
                    │ Task processor picks up     │
                    │ "vector_store_index_retry"  │
                    │ task and runs batch job     │
                    └─────────────────────────────┘
```

**Key behaviors**:

| Scenario | Behavior |
|----------|----------|
| `indexedContent` provided, vector store succeeds | Entry stored with `indexedAt` set |
| `indexedContent` provided, vector store fails | Entry stored with `indexedAt = null`, singleton retry task created |
| `indexedContent` not provided | Entry stored with `indexedContent = null`, `indexedAt = null`, not searchable |
| Non-history channel with `indexedContent` | Returns 400 Bad Request - only history channel supports indexing |

The gRPC `AppendEntry` endpoint supports the same flow with an `indexed_content` field in the request message.

### Batch Index Endpoint Behavior

The `POST /v1/conversations/index` endpoint follows the same pattern:

1. **Update entries**: Set `indexedContent` for each entry in the request
2. **Attempt vector store indexing**: Try to index all entries synchronously
3. **Handle failures**: For any entries that fail vector store indexing:
   - Leave `indexedAt = null`
   - Create a singleton retry task (if not already exists)
4. **Return success**: Report number of entries where `indexedContent` was stored

**Important**: The response indicates how many entries had their indexed content stored, not how many are immediately searchable. If vector store indexing fails, entries will become searchable asynchronously when the retry task completes.

### Vector Store Retry Task

When vector store indexing fails, instead of creating a task per failed entry, we create a **singleton task** with a unique name. This ensures only one retry task exists regardless of how many entries failed.

**Task Type**: `vector_store_index_retry`

**Task Name** (unique): `vector_store_index_retry`

**Task Body**: `{}` (empty - the task is a trigger, not entry-specific)

**Task Behavior**:
1. Query entries where `indexedContent IS NOT NULL AND indexedAt IS NULL`
2. Attempt to index each entry in the vector store
3. Set `indexedAt = now()` for successful entries
4. If any entries still fail, reschedule the task for retry
5. If all entries succeed, task completes (and is deleted)

This approach requires an enhancement to the task queue (see [015-task-queue.md](./015-task-queue.md)):
- Add optional `task_name` column with unique constraint
- Task creation with a name is idempotent (no-op if task with that name exists)

**Why singleton task instead of per-entry tasks?**

| Approach | Tasks Created | Overhead |
|----------|---------------|----------|
| Per-entry tasks | N tasks for N failures | High - N database rows, N task executions |
| Singleton task | 1 task regardless of failures | Low - 1 database row, 1 batch execution |

## API Changes

### REST API

**Before (openapi.yml) - Conversation-level indexing:**
```yaml
IndexTranscriptRequest:
  type: object
  required: [conversationId, transcript, untilEntryId]
  properties:
    conversationId:
      type: string
      format: uuid
    title:
      type: string
      nullable: true
    transcript:
      type: string
      description: Single transcript for entire conversation
    untilEntryId:
      type: string
      format: uuid
      description: Marker for which entries have been indexed
```

**After (openapi.yml) - Per-entry indexing:**
```yaml
/v1/conversations/index:
  post:
    tags: [Search]
    summary: Index conversation entries
    description: |-
      Indexes searchable text for conversation entries. Each item in the request
      array specifies a single entry with the text that should be searchable.

      This endpoint is called by batch indexing services after processing
      conversation entries. The indexed text becomes searchable via
      `/v1/conversations/search`.

      If an entry has already been indexed, its text is replaced with the new value.
      The entry's `indexedAt` timestamp is updated when successfully indexed.

      **Note:** This endpoint may return successfully even if vector store indexing
      fails. In that case, the indexed content is stored and a background retry task
      is created to complete the indexing asynchronously. Entries will become
      searchable once the retry task succeeds.

      Requires indexer or admin role.
    operationId: indexConversations
    requestBody:
      required: true
      content:
        application/json:
          schema:
            type: array
            items:
              $ref: '#/components/schemas/IndexEntryRequest'
    responses:
      '200':
        description: Entries indexed successfully.
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/IndexConversationsResponse'
      '403':
        description: Indexer or admin role required.
        $ref: '#/components/responses/Error'
      '404':
        $ref: '#/components/responses/NotFound'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []

IndexEntryRequest:
  type: object
  required: [conversationId, entryId, indexedContent]
  properties:
    conversationId:
      type: string
      format: uuid
      description: The conversation containing the entry.
    entryId:
      type: string
      format: uuid
      description: The entry ID to index.
    indexedContent:
      type: string
      description: The searchable text for this entry.

IndexConversationsResponse:
  type: object
  properties:
    indexed:
      type: integer
      description: |-
        Number of entries processed. These entries have their indexed content
        stored and will be searchable. If vector store indexing failed for some
        entries, they will become searchable asynchronously via background retry.

/v1/conversations/unindexed:
  get:
    tags: [Search]
    summary: List entries needing indexing
    description: |-
      Returns entries from the history channel that have not yet had their
      index content generated (where `indexedContent` is null). This endpoint
      is used by batch indexing jobs to discover entries that need processing.

      Entries are returned with their full content so that callers can
      process the content before submitting index text. Results are sorted
      by `createdAt` for consistent ordering.

      Uses cursor-based pagination. The `indexed_content` index enables efficient
      queries for unindexed entries.

      Requires indexer or admin role.
    operationId: listUnindexedEntries
    parameters:
      - name: limit
        in: query
        required: false
        description: Maximum number of entries to return.
        schema:
          type: integer
          default: 100
      - name: cursor
        in: query
        required: false
        description: Pagination cursor from previous response.
        schema:
          type: string
    responses:
      '200':
        description: Paginated list of unindexed entries.
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/UnindexedEntriesResponse'
      '403':
        description: Indexer or admin role required.
        $ref: '#/components/responses/Error'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []

UnindexedEntriesResponse:
  type: object
  properties:
    data:
      type: array
      items:
        $ref: '#/components/schemas/UnindexedEntry'
    cursor:
      type: string
      nullable: true
      description: Cursor for fetching next page. Null when no more results.

UnindexedEntry:
  type: object
  properties:
    conversationId:
      type: string
      format: uuid
    entry:
      $ref: '#/components/schemas/Entry'

# Entry schema unchanged - indexedContent and indexedAt are internal database fields only
# They are NOT exposed in REST/gRPC responses

# Add indexedContent to CreateEntryRequest (input only)
CreateEntryRequest:
  # ... existing properties ...
  properties:
    indexedContent:
      type: string
      nullable: true
      description: |-
        Optional text to index for search. Only valid for entries in the history
        channel. If provided, the entry will be indexed for search immediately
        after creation. Returns 400 Bad Request if specified for non-history channels.
```

### gRPC API

**Before:**
```protobuf
// Required agent API key
rpc IndexConversationTranscript(IndexTranscriptRequest) returns (Entry);

message IndexTranscriptRequest {
  bytes conversation_id = 1;
  optional string title = 2;
  string transcript = 3;
  bytes until_entry_id = 4;
}
```

**After:**
```protobuf
// Requires indexer or admin role
rpc IndexConversations(IndexConversationsRequest) returns (IndexConversationsResponse);

message IndexConversationsRequest {
  repeated IndexEntryRequest entries = 1;
}

message IndexEntryRequest {
  bytes conversation_id = 1;
  bytes entry_id = 2;
  string indexed_content = 3;
}

message IndexConversationsResponse {
  int32 indexed = 1;  // Total entries indexed
}

// Requires indexer or admin role
rpc ListUnindexedEntries(ListUnindexedEntriesRequest) returns (ListUnindexedEntriesResponse);

message ListUnindexedEntriesRequest {
  int32 limit = 1;
  optional string cursor = 2;
}

message ListUnindexedEntriesResponse {
  repeated UnindexedEntry entries = 1;
  optional string cursor = 2;  // Cursor for next page, absent when no more results
}

message UnindexedEntry {
  bytes conversation_id = 1;
  Entry entry = 2;
}

// Entry message unchanged - indexed_content and indexed_at are internal fields only
// They are NOT exposed in gRPC responses

// AppendEntryRequest gains indexed_content field
message AppendEntryRequest {
  // ... existing fields ...
  optional string indexed_content = N;  // Text to index for search (history channel only)
}
```

## Scope of Changes

### 1. OpenAPI Contracts

**Files:**
- `memory-service-contracts/src/main/resources/openapi.yml` - New request/response schemas, updated endpoint
- `memory-service-contracts/src/main/resources/openapi-admin.yml` - Remove `/v1/admin/conversations/index`

Changes:
- Add `indexedContent` field to `CreateEntryRequest` schema (input only, not in Entry output)
- Replace `IndexTranscriptRequest` with `IndexEntryRequest`
- Add `IndexConversationsResponse` schema
- Add `/v1/conversations/unindexed` endpoint with `UnindexedEntriesResponse`, `UnindexedEntry` schemas
- Update `SearchResult` schema to add `entryId` field
- Update endpoint authorization description

### 2. gRPC Contracts

**File:** `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`

Changes:
- Add `indexed_content` field to `AppendEntryRequest` message (input only, not in Entry response)
- Rename `IndexConversationTranscript` to `IndexConversations`
- Replace `IndexTranscriptRequest` with new message types (flat format)
- Add `IndexConversationsRequest`, `IndexConversationsResponse` messages
- Add `ListUnindexedEntries` RPC with request/response messages

### 3. Database Schema

**Files:** `memory-service/src/main/resources/db/migration/*.sql`

Changes (internal fields - not exposed in REST/gRPC):
- Add `indexed_content` column to entries table (nullable, text) - stores the text submitted for indexing
- Add `indexed_at` column to entries table (nullable, timestamp) - tracks when entry was indexed in vector store
- Add index on `indexed_content` for efficient unindexed entry queries (`WHERE indexed_content IS NULL`)
- Add composite index on `(indexed_content, created_at)` for paginated unindexed queries
- Add `entry_id` column to vector search table
- Remove `until_entry_id` column
- Add unique constraint on `(conversation_id, entry_id)`
- Migration for existing data

### 4. DTO Classes

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/CreateEntryRequest.java` (modified) - add indexedContent field (input only)
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/EntryDto.java` (unchanged) - indexedContent and indexedAt are internal only
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexEntryRequest.java` (new) - conversationId, entryId, text
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexConversationsResponse.java` (new)
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/UnindexedEntriesResponse.java` (new) - with cursor field
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/UnindexedEntry.java` (new) - conversationId, entry
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexTranscriptRequest.java` (delete)

### 5. Memory Store

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java`
- `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`
- `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java`

Changes:
- Update `appendEntry()` to store `indexedContent` field
- Update `appendEntry()` to attempt synchronous vector store indexing when `indexedContent` is provided
- On vector store failure, create singleton retry task via `TaskRepository.createTask("vector_store_index_retry", "vector_store_index_retry", Map.of())`
- Replace `indexTranscript()` with `indexEntries()`
- Add `listUnindexedEntries()` method with cursor-based pagination (for `indexed_content IS NULL`)
- Add `findEntriesPendingVectorIndexing()` method (for `indexed_content IS NOT NULL AND indexed_at IS NULL`)
- Add `setIndexedAt()` method to update the indexed_at timestamp
- Implement per-entry embedding storage with upsert
- Update `indexedAt` timestamp when entries are indexed
- Query entries where `indexed_content IS NULL` for unindexed list, sorted by `createdAt`

### 6. REST Resource

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/api/ConversationResource.java`
  - Update `appendEntry()` to handle inline indexing with `indexedContent`
  - Add `indexConversations()` endpoint with indexer role
  - Add `listUnindexedEntries()` endpoint with indexer role
- `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java` - Remove admin index endpoint

### 7. gRPC Service

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/grpc/ConversationGrpcService.java`
  - Update `appendEntry()` to handle inline indexing with `indexed_content`
- `memory-service/src/main/java/io/github/chirino/memory/grpc/SearchGrpcService.java`
  - Rename to `indexConversations()`
  - Add indexer role check
  - Use new message types
  - Add `listUnindexedEntries()` with indexer role

### 8. Cucumber Tests

**Files:**
- `memory-service/src/test/resources/features/index-rest.feature` - New request format, indexer role
- `memory-service/src/test/resources/features/index-grpc.feature` - New message format, indexer role
- `memory-service/src/test/resources/features/admin-rest.feature` - Remove admin index tests

## Implementation Plan

### Phase 1: Update OpenAPI Contracts

1. Replace `IndexTranscriptRequest` with new schemas in `openapi.yml`:
   - Request body is flat array of `IndexEntryRequest`
   - `IndexEntryRequest` with `conversationId`, `entryId`, and `indexedContent`
   - `IndexConversationsResponse` with total `indexed` count
2. Add `indexedContent` field to `CreateEntryRequest` schema (input only)
3. Add `/v1/conversations/unindexed` endpoint:
   - `UnindexedEntriesResponse` with `data` array and `cursor` field
   - `UnindexedEntry` with `conversationId` and `entry`
   - `limit` and `cursor` parameters
4. Update `/v1/conversations/index` endpoint:
   - Change operationId to `indexConversations`
   - Update request/response schemas
   - Add 403 response for missing indexer role
5. Update `SearchResult` schema to add `entryId` at top level
6. Remove `/v1/admin/conversations/index` from `openapi-admin.yml`
7. Remove `IndexTranscriptRequest` schema from admin contract

### Phase 2: Update Database Schema

1. Add `indexed_at` column to entries table:
   - Nullable timestamp, null means not yet indexed
   - Create index on `indexed_content` for efficient `WHERE indexed_content IS NULL` queries
   - Create composite index on `(indexed_content, created_at)` for paginated unindexed queries
2. Modify vector search table to store per-entry embeddings:
   - Add `entry_id` column (foreign key to entries)
   - Remove `until_entry_id` column
   - Create unique constraint on `(conversation_id, entry_id)`
3. Migration to handle existing indexed data

### Phase 3: Update REST Implementation

1. Create/update DTOs:
   - `IndexEntryRequest` with conversationId, entryId, and text
   - `IndexConversationsResponse` with indexed count
   - `UnindexedEntriesResponse` with data list and cursor
   - `UnindexedEntry` with conversationId and entry
   - Update `CreateEntryRequest` to add `indexedContent` field (input only)
   - Update `SearchResultDto` to add `entryId` field
2. Update `ConversationResource`:
   - Rename method to `indexConversations()`
   - Add `listUnindexedEntries()` method with cursor pagination
   - Add indexer role check to both methods
   - Implement per-entry indexing logic
   - Set `indexedAt` timestamp when entries are indexed
3. Remove `AdminResource.adminIndexConversationTranscript()`

### Phase 4: Update gRPC Implementation

1. Update proto file:
   - Rename RPC to `IndexConversations`
   - Add new message types with batch format
2. Update `SearchGrpcService`:
   - Implement new `indexConversation()` method
   - Add indexer role check using `AdminRoleResolver`

### Phase 5: Update Memory Store

1. Update `MemoryStore` interface:
   - Change `indexTranscript()` to `indexEntries()`
   - Accept list of entries with conversationId, entryId, and text
   - Add `listUnindexedEntries()` with cursor-based pagination
2. Update `PostgresMemoryStore` implementation:
   - Insert/update embeddings per entry
   - Use upsert for idempotency
   - Set `indexed_at` timestamp on successful indexing
   - Query entries where `indexed_content IS NULL`, sorted by `created_at`
   - Implement cursor-based pagination using `created_at` values
3. Update `MongoMemoryStore` implementation with same changes

### Phase 6: Update Tests

1. Update `entries-rest.feature`:
   - Test entry creation with `indexedContent` input field
   - Verify entry is searchable after creation with `indexedContent`
   - Verify internal `indexed_content` is stored (via unindexed endpoint check)
2. Update `entries-grpc.feature`:
   - Test AppendEntry with `indexed_content` input field
   - Verify entry is searchable after creation
3. Update `index-rest.feature`:
   - New flat request format with conversationId, entryId, text
   - Indexer role authorization
   - Test re-indexing (upsert behavior)
   - Test list unindexed entries endpoint with pagination
   - Verify entries disappear from unindexed list after indexing (internal indexedContent no longer null)
   - Test cursor-based pagination
4. Update `index-grpc.feature`:
   - New flat message format
   - Indexer role authorization
   - Test ListUnindexedEntries RPC with pagination
5. Remove admin index tests from `admin-rest.feature`
6. Update search tests to verify entry-level results

**Note:** gRPC test scenarios should mirror REST test scenarios. For each REST test added above, implement an equivalent gRPC test where applicable.

### Additional Test Scenarios

The following scenarios should be covered in the test suite:

#### Error Cases (`index-rest.feature`, `index-grpc.feature`)

```gherkin
Scenario: Index with non-existent entryId returns 404
  Given I have a valid conversation
  When I index an entry with a non-existent entryId
  Then the response status should be 404

Scenario: Index with non-existent conversationId returns 404
  Given I have a valid entry
  When I index with a non-existent conversationId
  Then the response status should be 404

Scenario: Index with mismatched conversationId and entryId returns 404
  Given I have an entry in conversation A
  When I index that entry with conversation B's ID
  Then the response status should be 404
```

#### Non-History Channel Behavior (`entries-rest.feature`, `entries-grpc.feature`)

```gherkin
Scenario: indexedContent on non-history channel returns 400
  When I create an entry in the "agent" channel with indexedContent "test content"
  Then the response status should be 400
  And the error message should indicate that indexedContent is only supported for history channel

Scenario: Entry without indexedContent succeeds on any channel
  When I create an entry in the "agent" channel without indexedContent
  Then the entry should be created successfully

Scenario: Only history channel entries appear in unindexed list
  Given I have entries in "history", "agent", and "summary" channels without indexedContent
  When I list unindexed entries
  Then only the "history" channel entry should be returned
```

#### SearchResult.entryId Verification (`search-rest.feature`, `search-grpc.feature`)

```gherkin
Scenario: Search result includes entryId without full entry
  Given I have indexed entries
  When I search with includeEntry=false
  Then each result should have entryId at top level
  And each result should have entry field as null

Scenario: Search result includes entryId with full entry
  Given I have indexed entries
  When I search with includeEntry=true
  Then each result should have entryId at top level
  And each result should have entry field populated
  And entryId should match entry.id
```

#### Admin Search (`admin-rest.feature`)

```gherkin
Scenario: Admin search returns entryId in results
  Given I have indexed entries across multiple users
  When admin searches for a term
  Then each result should include entryId at top level
```

## Verification

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# 1. Create entry with inline indexing
curl -X POST /v1/conversations/550e8400-e29b-41d4-a716-446655440000/entries \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "userId": "user_1234",
    "channel": "history",
    "contentType": "message",
    "content": [{"type": "text", "text": "Based on your past chats..."}],
    "indexedContent": "Based on your past chats, here are three possible approaches"
  }'

# Expected response (indexedContent and indexedAt are NOT included - internal fields):
# {
#   "id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
#   "channel": "history",
#   "content": [...],
#   "createdAt": "2025-01-10T14:40:12Z",
#   ...
# }

# 2. List unindexed entries (paginated)
curl -X GET '/v1/conversations/unindexed?limit=10' \
  -H "Authorization: Bearer $INDEXER_TOKEN"

# Expected response (entries where internal indexedContent is null):
# {
#   "data": [
#     {
#       "conversationId": "550e8400-e29b-41d4-a716-446655440000",
#       "entry": {
#         "id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
#         "channel": "history",
#         "content": [...],
#         "createdAt": "2025-01-10T14:40:12Z"
#       }
#     }
#   ],
#   "cursor": "eyJjcmVhdGVkQXQiOiIyMDI1LTAxLTEwVDE0OjQwOjEyWiJ9"
# }
# When cursor is null and data is empty, batch job is complete

# 3. Fetch next page
curl -X GET '/v1/conversations/unindexed?limit=10&cursor=eyJjcmVhdGVkQXQiOi...}' \
  -H "Authorization: Bearer $INDEXER_TOKEN"

# 4. Process entries and index them via batch endpoint (flat array)
curl -X POST /v1/conversations/index \
  -H "Authorization: Bearer $INDEXER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '[
    {
      "conversationId": "550e8400-e29b-41d4-a716-446655440000",
      "entryId": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
      "indexedContent": "User asked about conversation forking and branching strategies"
    },
    {
      "conversationId": "550e8400-e29b-41d4-a716-446655440000",
      "entryId": "7ca8c921-0ebe-22e2-91c5-11d05ge541d9",
      "indexedContent": "Assistant explained fork tree data model and access control"
    },
    {
      "conversationId": "661f9511-f30c-52e5-b827-557766551111",
      "entryId": "8db9d032-1fcf-33f3-a2d6-22e16hf652ea",
      "indexedContent": "Discussion about API design patterns"
    }
  ]'

# Expected response
# {"indexed": 3}

# 5. Verify entries no longer appear in unindexed list (indexedContent is now set)
curl -X GET '/v1/conversations/unindexed' \
  -H "Authorization: Bearer $INDEXER_TOKEN"
# Should return fewer entries after indexing

# 6. Verify 403 without indexer role
curl -X GET /v1/conversations/unindexed \
  -H "Authorization: Bearer $USER_TOKEN"
# Should return 403

curl -X POST /v1/conversations/index \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '[...]'
# Should return 403

# 7. Test search returns specific entry
curl -X POST /v1/conversations/search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": "fork tree data model"}'
# Should return result with entryId pointing to the matching entry
```

## Files Modified (Complete List)

| File | Change Type |
|------|-------------|
| `memory-service-contracts/src/main/resources/openapi.yml` | Modified |
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Modified |
| `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/CreateEntryRequest.java` | Modified (add indexedContent - input only) |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexEntryRequest.java` | New |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexConversationsResponse.java` | New |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/UnindexedEntriesResponse.java` | New |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/UnindexedEntry.java` | New |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexTranscriptRequest.java` | Deleted |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/SearchResultDto.java` | Modified (add entryId) |
| `memory-service/src/main/java/io/github/chirino/memory/api/ConversationResource.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/grpc/ConversationGrpcService.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/grpc/SearchGrpcService.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/grpc/GrpcDtoMapper.java` | Modified |
| `memory-service/src/main/resources/db/migration/*.sql` | New (migration for indexedContent, indexedAt) |
| `memory-service/src/test/resources/features/index-rest.feature` | Modified |
| `memory-service/src/test/resources/features/index-grpc.feature` | Modified |
| `memory-service/src/test/resources/features/admin-rest.feature` | Modified |
| `memory-service/src/test/resources/features/entries-rest.feature` | Modified (inline indexing tests) |

## Backward Compatibility

This enhancement includes breaking changes:

| Change | Impact |
|--------|--------|
| Request body format changed | `transcript` + `untilEntryId` replaced with flat array of `{conversationId, entryId, text}` |
| Title no longer updated via index | Conversation title must be updated through other endpoints |
| `/v1/conversations/index` requires indexer role | Clients using agent API keys must switch to indexer role |
| `/v1/admin/conversations/index` removed | Clients must use `/v1/conversations/index` instead |
| gRPC `IndexConversationTranscript` renamed | Use `IndexConversations` with new flat message format |
| Response format changed | Returns `{"indexed": N}` instead of `Entry` |
| Unindexed endpoint now paginated | Use `cursor` parameter for pagination |
| `CreateEntryRequest.indexedContent` added | New optional input field, additive change (non-breaking) |
| Internal `indexed_content` and `indexed_at` columns | Database-only, not exposed in API |
| `SearchResult.entryId` added | New field, additive change (non-breaking) |

Since this is a pre-release API, backward compatibility is not required.

## Search API Integration

The new per-entry indexing model affects how search results work.

### Current SearchResult Schema

```yaml
SearchResult:
  properties:
    conversationId: uuid
    conversationTitle: string
    score: float
    highlights: string
    entry: Entry  # optional, when includeEntry=true
```

### Issues with Current Design

1. **Missing `entryId` at top level**: When `includeEntry=false`, there's no way to know which entry matched. The UI needs the entry ID for deep-linking even without the full entry content.

2. **Highlights source**: *(Addressed in [Enhancement 031](./031-hybrid-highlights.md))* Currently `highlights` is extracted from the original entry content (`entry.content`). With per-entry indexing, the search matches against the **indexed text** (submitted via the index API), so highlights should come from the indexed text for consistency.

3. **Entry content vs indexed text**: The `entry.content` contains the original content, but the search matched against the indexed text. These may differ (e.g., if the batch job summarizes, redacts, or transforms the content before indexing).

### Proposed Changes to SearchResult

```yaml
SearchResult:
  properties:
    conversationId:
      type: string
      format: uuid
    conversationTitle:
      type: string
    entryId:
      type: string
      format: uuid
      description: ID of the matched entry. Always present for deep-linking.
    score:
      type: number
      format: float
    highlights:
      type: string
      nullable: true
      description: Highlighted text snippet from the INDEXED text (not original content).
    entry:
      $ref: '#/components/schemas/Entry'
      description: |-
        The original entry content. Only included when includeEntry=true.
        Note: This is the original content, not the indexed text.
```

### Key Changes

| Field | Before | After |
|-------|--------|-------|
| `entryId` | Not present (must extract from `entry.id`) | Always present at top level |
| `highlights` | Extracted from original entry content | Extracted from indexed text |
| `entry` | Required for entry ID | Optional, entry ID available separately |

### Deep-Link Behavior

With `entryId` always present, UIs can:
1. Show search results with just metadata (`includeEntry=false`)
2. Link directly to the conversation at the matched entry
3. Fetch entry content only when the user clicks through

### Content Distinction

The indexed text may differ from the original entry content (e.g., the batch job may summarize, redact PII, or transform the content before indexing). UIs should be aware:

- `highlights` - snippet from the **indexed text** (what was searched against)
- `entry.content` - the **original content** (may differ from indexed text)

This distinction matters when the batch job processes content before indexing.

### Admin Search

The admin search endpoint (`/v1/admin/conversations/search`) uses the same `SearchResult` schema, so the `entryId` addition applies there too. No other changes needed for admin search.

## Future Considerations

- **Delete indexed entries**: Remove indexed text when entries are deleted
- **Re-indexing trigger**: API to mark entries for re-indexing (e.g., after PII rules change). Could reset internal `indexed_at` to null.
- **Indexing metrics**: Track indexing lag, throughput, and error rates. Use internal `indexed_at` timestamps for lag calculations.
- **Indexed text field**: Consider adding `indexedText` to SearchResult for transparency about what was actually matched
- **Bulk re-index**: API to reset internal `indexed_at` for a conversation or all entries, forcing re-indexing
