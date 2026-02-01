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

## Design Decisions

### Single Index Endpoint with Per-Entry Text

The indexing functionality will be consolidated into a single endpoint that accepts searchable text per entry:

**Endpoint**: `POST /v1/conversations/index`

**Authorization**: Requires `indexer` or `admin` role

**Request Body** (array of conversations):
```json
[
  {
    "conversationId": "550e8400-e29b-41d4-a716-446655440000",
    "title": "Optional conversation title",
    "entries": [
      {
        "id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
        "text": "The searchable text for this entry"
      },
      {
        "id": "7ca8c921-0ebe-22e2-91c5-11d05ge541d9",
        "text": "The searchable text for another entry"
      }
    ]
  },
  {
    "conversationId": "661f9511-f30c-52e5-b827-557766551111",
    "entries": [
      {
        "id": "8db9d032-1fcf-33f3-a2d6-22e16hf652ea",
        "text": "Another conversation's entry text"
      }
    ]
  }
]
```

**Key characteristics**:

| Aspect | Current Design | New Design |
|--------|----------------|------------|
| Authorization | Agent API key or indexer role | Indexer or admin role only |
| Granularity | Single transcript per conversation | Text per entry |
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

Both gRPC endpoints require the `indexer` role, matching the REST behavior.

### List Unindexed Entries Endpoint

A batch indexing job needs to discover which entries require indexing. This endpoint returns entries from the `history` channel that have not yet been indexed.

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
         │  List of entries needing indexing             │
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
         └                       └                       └
```

**Response includes**:
- Entry ID, conversation ID, and content (for processing)
- Conversation title (for context)

**No pagination**: Since new entries can appear at any time, there's no stable ordering for pagination. Instead, the endpoint returns up to `limit` entries. The batch job workflow is:
1. Fetch a batch of unindexed entries
2. Process and index them
3. Repeat until an empty result is returned

The index endpoint uses upsert semantics, so re-indexing an entry is safe if entries are processed multiple times.

**Query parameters**:

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `limit` | integer | 100 | Maximum number of entries to return |
| `conversationId` | uuid | null | Filter to a specific conversation |

**Response format**:
```json
{
  "data": [
    {
      "conversationId": "550e8400-e29b-41d4-a716-446655440000",
      "conversationTitle": "Design discussion",
      "entry": {
        "id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
        "channel": "history",
        "contentType": "message",
        "content": [...],
        "createdAt": "2025-01-10T14:40:12Z"
      }
    }
  ]
}
```

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
      Indexes searchable text for conversation entries across one or more
      conversations. Each conversation in the request array specifies entries
      with the text that should be searchable.

      This endpoint is called by batch indexing services after processing
      conversation entries. The indexed text becomes searchable via
      `/v1/conversations/search`.

      If an entry has already been indexed, its text is replaced with the new value.

      Requires indexer or admin role.
    operationId: indexConversations
    requestBody:
      required: true
      content:
        application/json:
          schema:
            type: array
            items:
              $ref: '#/components/schemas/IndexConversationRequest'
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

IndexConversationRequest:
  type: object
  required: [conversationId, entries]
  properties:
    conversationId:
      type: string
      format: uuid
      description: The conversation containing the entries to index.
    title:
      type: string
      nullable: true
      description: Optional conversation title to store/update.
    entries:
      type: array
      description: Entries to index with their searchable text.
      items:
        $ref: '#/components/schemas/IndexEntryRequest'

IndexEntryRequest:
  type: object
  required: [id, text]
  properties:
    id:
      type: string
      format: uuid
      description: The entry ID to index.
    text:
      type: string
      description: The searchable text for this entry.

IndexConversationsResponse:
  type: object
  properties:
    indexed:
      type: integer
      description: Total number of entries indexed across all conversations.

/v1/conversations/unindexed:
  get:
    tags: [Search]
    summary: List entries needing indexing
    description: |-
      Returns entries from the history channel that have not yet been indexed
      for search. This endpoint is used by batch indexing jobs to discover
      entries that need processing.

      Entries are returned with their full content so that callers can
      process the content before submitting index text.

      **No pagination**: Since new entries can appear at any time, there's no
      stable ordering for pagination. The endpoint returns up to `limit` entries.
      Batch jobs should call this repeatedly, processing and indexing each batch,
      until an empty result is returned.

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
      - name: conversationId
        in: query
        required: false
        description: Filter to entries from a specific conversation.
        schema:
          type: string
          format: uuid
    responses:
      '200':
        description: List of unindexed entries.
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

UnindexedEntry:
  type: object
  properties:
    conversationId:
      type: string
      format: uuid
    conversationTitle:
      type: string
      nullable: true
    entry:
      $ref: '#/components/schemas/Entry'
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
  repeated IndexConversationRequest conversations = 1;
}

message IndexConversationRequest {
  bytes conversation_id = 1;
  optional string title = 2;
  repeated IndexEntryRequest entries = 3;
}

message IndexEntryRequest {
  bytes id = 1;
  string text = 2;
}

message IndexConversationsResponse {
  int32 indexed = 1;  // Total entries indexed across all conversations
}

// Requires indexer or admin role
rpc ListUnindexedEntries(ListUnindexedEntriesRequest) returns (ListUnindexedEntriesResponse);

message ListUnindexedEntriesRequest {
  int32 limit = 1;
  optional bytes conversation_id = 2;
}

message ListUnindexedEntriesResponse {
  repeated UnindexedEntry entries = 1;
}

message UnindexedEntry {
  bytes conversation_id = 1;
  optional string conversation_title = 2;
  Entry entry = 3;
}
```

## Scope of Changes

### 1. OpenAPI Contracts

**Files:**
- `memory-service-contracts/src/main/resources/openapi.yml` - New request/response schemas, updated endpoint
- `memory-service-contracts/src/main/resources/openapi-admin.yml` - Remove `/v1/admin/conversations/index`

Changes:
- Replace `IndexTranscriptRequest` with `IndexConversationRequest`, `IndexEntryRequest`
- Add `IndexConversationsResponse` schema
- Add `/v1/conversations/unindexed` endpoint with `UnindexedEntriesResponse`, `UnindexedEntry` schemas
- Update `SearchResult` schema to add `entryId` field
- Update endpoint authorization description

### 2. gRPC Contracts

**File:** `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`

Changes:
- Rename `IndexConversationTranscript` to `IndexConversations`
- Replace `IndexTranscriptRequest` with new message types (batch format)
- Add `IndexConversationsRequest`, `IndexConversationsResponse` messages
- Add `ListUnindexedEntries` RPC with request/response messages

### 3. Database Schema

**Files:** `memory-service/src/main/resources/db/migration/*.sql`

Changes:
- Add `entry_id` column to vector search table
- Remove `until_entry_id` column
- Add unique constraint on `(conversation_id, entry_id)`
- Migration for existing data

### 4. DTO Classes

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexConversationRequest.java` (new)
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexEntryRequest.java` (new)
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexConversationsResponse.java` (new)
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/UnindexedEntriesResponse.java` (new)
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/UnindexedEntry.java` (new)
- `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexTranscriptRequest.java` (delete)

### 5. Memory Store

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java`
- `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`
- `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java`

Changes:
- Replace `indexTranscript()` with `indexEntries()`
- Add `listUnindexedEntries()` method
- Implement per-entry embedding storage with upsert

### 6. REST Resource

**Files:**
- `memory-service/src/main/java/io/github/chirino/memory/api/ConversationResource.java` - New implementation with indexer role
- `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java` - Remove admin index endpoint

### 7. gRPC Service

**File:** `memory-service/src/main/java/io/github/chirino/memory/grpc/SearchGrpcService.java`

Changes:
- Rename to `indexConversation()`
- Add indexer role check
- Use new message types

### 8. Cucumber Tests

**Files:**
- `memory-service/src/test/resources/features/index-rest.feature` - New request format, indexer role
- `memory-service/src/test/resources/features/index-grpc.feature` - New message format, indexer role
- `memory-service/src/test/resources/features/admin-rest.feature` - Remove admin index tests

## Implementation Plan

### Phase 1: Update OpenAPI Contracts

1. Replace `IndexTranscriptRequest` with new schemas in `openapi.yml`:
   - Request body is array of `IndexConversationRequest`
   - `IndexConversationRequest` with `conversationId`, `title`, and `entries` array
   - `IndexEntryRequest` with `id` and `text`
   - `IndexConversationsResponse` with total `indexed` count
2. Add `/v1/conversations/unindexed` endpoint:
   - `UnindexedEntriesResponse` and `UnindexedEntry` schemas
   - `limit` and optional `conversationId` filter (no pagination cursor)
3. Update `/v1/conversations/index` endpoint:
   - Change operationId to `indexConversation`
   - Update request/response schemas
   - Add 403 response for missing indexer role
4. Update `SearchResult` schema to add `entryId` at top level
5. Remove `/v1/admin/conversations/index` from `openapi-admin.yml`
6. Remove `IndexTranscriptRequest` schema from admin contract

### Phase 2: Update Database Schema

1. Modify vector search table to store per-entry embeddings:
   - Add `entry_id` column (foreign key to entries)
   - Remove `until_entry_id` column
   - Create unique constraint on `(conversation_id, entry_id)`
2. Migration to handle existing indexed data

### Phase 3: Update REST Implementation

1. Create/update DTOs:
   - `IndexConversationRequest` with conversationId, title, entries list
   - `IndexEntryRequest` with id and text
   - `IndexConversationsResponse` with indexed count
   - `UnindexedEntriesResponse` with data list
   - `UnindexedEntry` with conversationId, title, and entry
   - Update `SearchResultDto` to add `entryId` field
2. Update `ConversationResource`:
   - Rename method to `indexConversation()`
   - Add `listUnindexedEntries()` method
   - Add indexer role check to both methods
   - Implement per-entry indexing logic
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
   - Accept list of entry IDs with their text
2. Update `PostgresMemoryStore` implementation:
   - Insert/update embeddings per entry
   - Use upsert for idempotency
3. Update `MongoMemoryStore` implementation

### Phase 6: Update Tests

1. Update `index-rest.feature`:
   - New request format with entries array
   - Indexer role authorization
   - Test re-indexing (upsert behavior)
   - Test list unindexed entries endpoint
   - Verify entries disappear from unindexed list after indexing
2. Update `index-grpc.feature`:
   - New message format
   - Indexer role authorization
   - Test ListUnindexedEntries RPC
3. Remove admin index tests from `admin-rest.feature`
4. Update search tests to verify entry-level results

## Verification

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# 1. List unindexed entries
curl -X GET '/v1/conversations/unindexed?limit=10' \
  -H "Authorization: Bearer $INDEXER_TOKEN"

# Expected response:
# {
#   "data": [
#     {
#       "conversationId": "550e8400-e29b-41d4-a716-446655440000",
#       "conversationTitle": "Design discussion",
#       "entry": {
#         "id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
#         "channel": "history",
#         "content": [...]
#       }
#     }
#   ]
# }
# When empty, batch job is complete

# 2. Process entries and index them (batch of conversations)
curl -X POST /v1/conversations/index \
  -H "Authorization: Bearer $INDEXER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '[
    {
      "conversationId": "550e8400-e29b-41d4-a716-446655440000",
      "title": "Conversation Forking Design",
      "entries": [
        {
          "id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
          "text": "User asked about conversation forking and branching strategies"
        },
        {
          "id": "7ca8c921-0ebe-22e2-91c5-11d05ge541d9",
          "text": "Assistant explained fork tree data model and access control"
        }
      ]
    },
    {
      "conversationId": "661f9511-f30c-52e5-b827-557766551111",
      "entries": [
        {
          "id": "8db9d032-1fcf-33f3-a2d6-22e16hf652ea",
          "text": "Discussion about API design patterns"
        }
      ]
    }
  ]'

# Expected response
# {"indexed": 3}

# 3. Verify entries no longer appear in unindexed list
curl -X GET '/v1/conversations/unindexed?conversationId=550e8400-e29b-41d4-a716-446655440000' \
  -H "Authorization: Bearer $INDEXER_TOKEN"
# Should return empty data array for that conversation

# 4. Verify 403 without indexer role
curl -X GET /v1/conversations/unindexed \
  -H "Authorization: Bearer $USER_TOKEN"
# Should return 403

curl -X POST /v1/conversations/index \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{...}'
# Should return 403

# 5. Test search returns specific entry
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
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/IndexConversationRequest.java` | New |
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
| `memory-service/src/main/java/io/github/chirino/memory/grpc/SearchGrpcService.java` | Modified |
| `memory-service/src/main/java/io/github/chirino/memory/grpc/GrpcDtoMapper.java` | Modified |
| `memory-service/src/main/resources/db/migration/*.sql` | New (migration) |
| `memory-service/src/test/resources/features/index-rest.feature` | Modified |
| `memory-service/src/test/resources/features/index-grpc.feature` | Modified |
| `memory-service/src/test/resources/features/admin-rest.feature` | Modified |

## Backward Compatibility

This enhancement includes breaking changes:

| Change | Impact |
|--------|--------|
| Request body format changed | `transcript` + `untilEntryId` replaced with array of conversations with `entries` |
| `/v1/conversations/index` requires indexer role | Clients using agent API keys must switch to indexer role |
| `/v1/admin/conversations/index` removed | Clients must use `/v1/conversations/index` instead |
| gRPC `IndexConversationTranscript` renamed | Use `IndexConversations` with new batch message format |
| Response format changed | Returns `{"indexed": N}` instead of `Entry` |
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

2. **Highlights source**: Currently `highlights` is extracted from the original entry content (`entry.content`). With per-entry indexing, the search matches against the **indexed text** (submitted via the index API), so highlights should come from the indexed text for consistency.

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
- **Re-indexing trigger**: API to mark entries for re-indexing (e.g., after PII rules change)
- **Indexing metrics**: Track indexing lag, throughput, and error rates
- **Indexed text field**: Consider adding `indexedText` to SearchResult for transparency about what was actually matched
