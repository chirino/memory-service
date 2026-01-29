# 019 - Rename Summary to Index and Consolidate Conversation Endpoints

## Summary

Rename the `/v1/conversations/{conversationId}/summaries` endpoint to `/v1/conversations/index` with operation name `indexConversationTranscript`, rename the `summary` field to `transcript`, rename the `summary` channel to `transcript`, make `title` optional, remove `summarizedAt`, and move the search endpoint from `/v1/user/search/entries` to `/v1/conversations/search`. These changes better reflect the actual purpose of these operations, simplify the request schema, and consolidate conversation-related endpoints under a common path prefix.

## Motivation

### Summaries Endpoint Misnaming

The current `/v1/conversations/{conversationId}/summaries` endpoint is misleadingly named. While it accepts summary text, its primary purpose is to **index conversation transcripts** for later retrieval via semantic search. The operation:

1. Updates the conversation title
2. Stores an index entry containing the transcript text
3. Tracks which entries have been indexed via `untilEntryId`

This is fundamentally an **indexing operation**, not a summary storage operation. The transcript text is the input that gets indexed, and the endpoint's purpose is to maintain a searchable index of conversation content.

### Search Endpoint Path Inconsistency

The current `/v1/user/search/entries` path:

1. Uses a `/v1/user/` prefix that's inconsistent with other conversation operations
2. Searches across conversations the user has access to, which is conceptually a conversation-scoped operation
3. Would be more discoverable under `/v1/conversations/search`

### Benefits of Consolidation

Moving both endpoints under `/v1/conversations/` provides:

1. **Discoverability**: All conversation-related operations are grouped together
2. **Consistency**: Uniform path structure for conversation operations
3. **Clarity**: Path names accurately describe the operation

## Current State

### OpenAPI (`openapi.yml`)

| Path | Operation | Description |
|------|-----------|-------------|
| `POST /v1/conversations/{conversationId}/summaries` | `createConversationSummary` | Store summarization of previous entries |
| `POST /v1/user/search/entries` | `searchConversations` | Semantic search across conversations |

### Request/Response Schemas

**CreateSummaryRequest**:
```yaml
CreateSummaryRequest:
  type: object
  required:
    - title
    - summary
    - untilEntryId
    - summarizedAt
  properties:
    title:
      type: string
    summary:
      type: string
    untilEntryId:
      type: string
    summarizedAt:
      type: string
      format: date-time
```

Note: The `summary` field will be renamed to `transcript`, `title` becomes optional, and `summarizedAt` is removed in the new schema.

**SearchConversationsRequest**:
```yaml
SearchConversationsRequest:
  type: object
  required:
    - query
  properties:
    query:
      type: string
    topK:
      type: integer
      default: 20
    conversationIds:
      type: array
      items:
        type: string
      nullable: true
    before:
      type: string
      nullable: true
```

### Java Classes

| Category | Classes |
|----------|---------|
| Resources | `ConversationsResource.createConversationSummary()` |
| DTOs | `CreateSummaryRequest` (generated from OpenAPI) |

## Proposed Changes

### 1. Rename Summaries Endpoint to Index

#### Path Change

| Old Path | New Path |
|----------|----------|
| `POST /v1/conversations/{conversationId}/summaries` | `POST /v1/conversations/index` |

#### Operation Name Change

| Old Operation | New Operation |
|---------------|---------------|
| `createConversationSummary` | `indexConversationTranscript` |

#### Request Schema Change

Move `conversationId` from path parameter to request body, rename `summary` to `transcript`, make `title` optional, and remove `summarizedAt` (server can derive timestamps from `untilEntryId`):

```yaml
IndexTranscriptRequest:
  type: object
  required:
    - conversationId
    - transcript
    - untilEntryId
  properties:
    conversationId:
      type: string
      description: The conversation to index.
    title:
      type: string
      nullable: true
      description: Optional conversation title to store/update.
    transcript:
      type: string
      description: Transcript text to index for semantic search.
    untilEntryId:
      type: string
      description: Highest entry id covered by the index (inclusive).
```

#### OpenAPI Spec Update

```yaml
/v1/conversations/index:
  post:
    tags: [Search]
    summary: Index a conversation transcript
    description: |-
      Indexes conversation transcript content for semantic search. Stores the
      provided transcript text as searchable content and updates the conversation
      title. The `untilEntryId` tracks which entries have been indexed.

      This endpoint is typically called by agents after processing recent
      conversation entries. The transcript text becomes searchable via
      the `/v1/conversations/search` endpoint.

      Requires a valid agent API key.
    operationId: indexConversationTranscript
    requestBody:
      required: true
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/IndexTranscriptRequest'
    responses:
      '201':
        description: The index entry was created.
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Entry'
      '404':
        $ref: '#/components/responses/NotFound'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []
```

### 2. Move Search Endpoint

#### Path Change

| Old Path | New Path |
|----------|----------|
| `POST /v1/user/search/entries` | `POST /v1/conversations/search` |

#### Operation Name Change

| Old Operation | New Operation |
|---------------|---------------|
| `searchEntries` | `searchConversations` |

#### OpenAPI Spec Update

```yaml
/v1/conversations/search:
  post:
    tags: [Search]
    summary: Semantic search across conversations
    description: |-
      Performs semantic and/or keyword search across all conversations the user has access to.
      Backed by an internal vector store (pgvector, MongoDB, etc.).
    operationId: searchConversations
    requestBody:
      required: true
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/SearchConversationsRequest'
    responses:
      '200':
        description: Search results.
        content:
          application/json:
            schema:
              type: object
              properties:
                data:
                  type: array
                  items:
                    $ref: '#/components/schemas/SearchResult'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []
```

### 3. Rename Channel

Rename the `summary` channel to `transcript` to reflect that it holds indexed transcript content.

#### Channel Enum Change

| Old | New |
|----|-----|
| `summary` | `transcript` |

The `Channel` enum becomes: `history`, `memory`, `transcript`

#### OpenAPI Schema Update

```yaml
Channel:
  type: string
  description: Logical channel of the entry within the conversation.
  enum:
    - history
    - memory
    - transcript  # was: summary
```

### 4. Schema Changes

| Old | New |
|----|-----|
| `CreateSummaryRequest` | `IndexTranscriptRequest` |
| `CreateSummaryRequest.summary` | `IndexTranscriptRequest.transcript` |
| `CreateSummaryRequest.title` (required) | `IndexTranscriptRequest.title` (optional) |
| `CreateSummaryRequest.summarizedAt` | Removed (server derives from `untilEntryId`) |
| `SearchEntriesRequest` | `SearchConversationsRequest` |
| `Channel.summary` | `Channel.transcript` |

### 5. Java Resource Changes

#### ConversationsResource

Update method signature and path:

```java
// Old
@POST
@Path("/v1/conversations/{conversationId}/summaries")
public Response createConversationSummary(
    @PathParam("conversationId") String conversationId,
    CreateSummaryRequest request) { ... }

// New
@POST
@Path("/v1/conversations/index")
public Response indexConversationTranscript(IndexTranscriptRequest request) {
    String conversationId = request.getConversationId();
    // ... rest of implementation
}
```

Update search endpoint path and method name:

```java
// Old
@POST
@Path("/v1/user/search/entries")
public Response searchEntries(SearchEntriesRequest request) { ... }

// New
@POST
@Path("/v1/conversations/search")
public Response searchConversations(SearchConversationsRequest request) { ... }
```

### 6. Generated Client Updates

The OpenAPI generator will produce updated client code:

- New `indexConversationTranscript()` method replacing `createConversationSummary()`
- New `IndexTranscriptRequest` type replacing `CreateSummaryRequest`
- New `searchConversations()` method replacing `searchEntries()`
- New `SearchConversationsRequest` type replacing `SearchEntriesRequest`

### 7. Test Updates

#### Feature Files

Update `summaries-rest.feature` to `index-rest.feature`:

**Before**:
```gherkin
When POST to "/v1/conversations/${conversationId}/summaries" with:
  """
  {
    "title": "Test Conversation",
    "summary": "User discussed testing approaches",
    "untilEntryId": "${entryId}",
    "summarizedAt": "2025-01-10T14:40:12Z"
  }
  """
```

**After**:
```gherkin
When POST to "/v1/conversations/index" with:
  """
  {
    "conversationId": "${conversationId}",
    "title": "Test Conversation",
    "transcript": "User discussed testing approaches",
    "untilEntryId": "${entryId}"
  }
  """
```

Update search endpoint paths in relevant feature files:

**Before**:
```gherkin
When POST to "/v1/user/search/entries" with:
  """
  {"query": "testing approaches"}
  """
```

**After**:
```gherkin
When POST to "/v1/conversations/search" with:
  """
  {"query": "testing approaches"}
  """
```

### 8. Framework Integration Updates

#### LangChain4j Integration

Update `MemoryServiceChatMemoryStore.java` if it calls the summary endpoint.

#### Spring AI Integration

Update any summarization advisors that call the summary endpoint.

### 9. Site Documentation Updates

#### `site/src/pages/docs/concepts/entries.md`

Update channel references:

| Old | New |
|----|-----|
| `summary` | `transcript` |
| "Summarization entries" | "Transcript index entries" |

**Before**:
```markdown
| Channel | Description |
|---------|-------------|
| `history` | User-visible conversation between users and agents |
| `memory` | Agent memory entries, scoped to the calling client ID |
| `summary` | Summarization entries (not visible in user-facing lists) |
```

**After**:
```markdown
| Channel | Description |
|---------|-------------|
| `history` | User-visible conversation between users and agents |
| `memory` | Agent memory entries, scoped to the calling client ID |
| `transcript` | Transcript index entries (not visible in user-facing lists) |
```

Also update the channel filter documentation:
- `channel` - Filter by channel: `history` (default), `memory`, or `transcript`

#### `site/src/pages/docs/changelog.md`

Remove "Enhanced summarization capabilities" from the Planned Features list.

**Before**:
```markdown
### Planned Features

- Spring Boot starter support
- Additional vector store integrations
- Enhanced summarization capabilities
- Multi-agent conversation support
```

**After**:
```markdown
### Planned Features

- Spring Boot starter support
- Additional vector store integrations
- Multi-agent conversation support
```

## Summary of File Changes

### Contracts (1 file)

| File | Changes |
|------|---------|
| `openapi.yml` | Rename paths, operations, schemas, and `Channel.summary` → `Channel.transcript` |

### Java Resources (1-2 files)

| File | Changes |
|------|---------|
| `ConversationsResource.java` | Update paths and method signatures |

### Java Models (1 file)

| File | Changes |
|------|---------|
| `Channel.java` | Rename `SUMMARY` → `TRANSCRIPT` |

### Tests (2+ files)

| File | Changes |
|------|---------|
| `summaries-rest.feature` → `index-rest.feature` | Update paths and request bodies |
| Other feature files | Update search endpoint paths, channel references |

### Site Documentation (2 files)

| File | Changes |
|------|---------|
| `site/src/pages/docs/concepts/entries.md` | Rename `summary` channel to `transcript` |
| `site/src/pages/docs/changelog.md` | Remove "Enhanced summarization capabilities" |

### Generated Code (auto-updated)

| Component | Changes |
|-----------|---------|
| TypeScript client | New method names, types, and channel enum |
| Java REST clients | New method names, types, and channel enum |

## Migration Considerations

Since this project has not yet been released, there are no backward-compatibility concerns.

## Alternatives Considered

### Keep `/summaries` but rename operation

Pros:
- Less change to paths

Cons:
- Path still misleading about purpose
- `conversationId` in path requires separate endpoints per conversation

### Use `/v1/conversations/{conversationId}/index`

Pros:
- Keeps conversationId in path
- RESTful resource hierarchy

Cons:
- Less flexibility for batch operations in the future
- Inconsistent with other operations that may need cross-conversation scope

### Keep `/v1/user/search/entries`

Pros:
- No change required

Cons:
- Inconsistent path prefix
- Less discoverable
- User-scoped prefix when search is really conversation-scoped

## Decision

Rename the summaries endpoint to `/v1/conversations/index` with operation `indexConversationTranscript`, rename the `summary` field to `transcript`, rename the `summary` channel to `transcript`, make `title` optional, remove `summarizedAt` (server derives timestamps), move `conversationId` to the request body, and relocate search to `/v1/conversations/search`. This provides accurate naming, a simpler request schema, and consistent path structure before the first release.
