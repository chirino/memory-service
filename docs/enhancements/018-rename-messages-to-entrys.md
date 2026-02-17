---
status: implemented
---

# Rename Messages to Entries and Add contentType Field

> **Status**: Implemented.

## Summary

Rename the "Messages" resource throughout the API contracts and data models to "Entries" to better reflect the generic nature of stored data. Additionally, add a required `contentType` string field to describe the data type used in the `content` field.

## Motivation

The current "Message" terminology implies chat-style communication, but the storage layer is more generic. A conversation can store various types of content blocks beyond traditional messages:

1. **Flexibility**: "Entry" is a more neutral term that accommodates different content types (chat messages, tool calls, function results, structured data, embeddings metadata, etc.)
2. **Extensibility**: The new `contentType` field enables clients to store and filter different kinds of data within the same conversation
3. **Self-describing data**: With `contentType`, consumers can correctly interpret the `content` array without prior knowledge of what the agent stores
4. **Future-proofing**: As the service evolves to support more use cases, "Entry" scales better semantically than "Message"

## Current State

### OpenAPI (`openapi.yml`)

| Schema | Description |
|--------|-------------|
| `Message` | Core message entity with id, conversationId, userId, channel, epoch, content, createdAt |
| `CreateMessageRequest` | Request body for creating messages |
| `SyncMessagesRequest` | Request body for memory sync |
| `SyncMessagesResponse` | Response for memory sync |
| `SearchResult` | Contains a `message` field with score and highlights |
| `PagedMessages` | Paginated list response (implicit via paths) |

| Path | Description |
|------|-------------|
| `GET /v1/conversations/{conversationId}/messages` | List messages |
| `POST /v1/conversations/{conversationId}/messages` | Append message |
| `POST /v1/conversations/{conversationId}/messages/{messageId}/fork` | Fork at message |
| `POST /v1/conversations/{conversationId}/memory/messages/sync` | Sync memory messages |
| `POST /v1/user/search/messages` | Search messages |

### OpenAPI Admin (`openapi-admin.yml`)

| Path | Description |
|------|-------------|
| `GET /v1/admin/conversations/{id}/messages` | Admin list messages |
| `DELETE /v1/admin/messages/{id}` | Admin delete message |

### Protobuf (`memory_service.proto`)

| Message Type | Description |
|--------------|-------------|
| `Message` | Core message type (lines 160-168) |
| `CreateMessageRequest` | (lines 124-129) |
| `ListMessagesRequest` | (lines 148-153) |
| `ListMessagesResponse` | (lines 155-158) |
| `AppendMessageRequest` | (lines 143-146) |
| `SyncMessagesRequest` | (lines 131-134) |
| `SyncMessagesResponse` | (lines 136-141) |
| `MessageChannel` enum | (lines 34-39) |

| Service | RPC Methods |
|---------|-------------|
| `MessagesService` | `ListMessages`, `AppendMessage`, `SyncMessages` |

### Java Classes

| Category | Classes |
|----------|---------|
| DTOs | `MessageDto`, `PagedMessages`, `SearchResultDto`, `CreateUserMessageRequest`, `SearchMessagesRequest` |
| Persistence (JPA) | `MessageEntity` (table: `messages`) |
| Persistence (MongoDB) | `MongoMessage` (collection: `messages`) |
| Repositories | `MessageRepository`, `MongoMessageRepository` |
| Resources | `ConversationsResource.listMessages()`, `ConversationsResource.appendMessage()`, `SearchResource.searchMessages()` |
| gRPC | `MessagesGrpcService`, `GrpcDtoMapper.toProto(MessageDto)` |
| Models | `MessageChannel`, `MessageRole`, `MessageVisibility` |

### Database Schema

```sql
CREATE TABLE IF NOT EXISTS messages (
    id                UUID PRIMARY KEY,
    conversation_id   UUID NOT NULL,
    conversation_group_id UUID NOT NULL,
    user_id           TEXT,
    client_id         TEXT,
    channel           TEXT NOT NULL,
    epoch             BIGINT,
    content           BYTEA NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Associated table: `message_embeddings` for vector search.

## Proposed Changes

### 1. Add `contentType` Field

The new `contentType` field will be:
- **Required** on create requests
- **Required** in response bodies
- A **string** that describes the schema/format of the `content` array

The service stores `contentType` as-is and does not validate or interpret it. This allows agents to define their own content schemas.

#### contentType Conventions

The following conventions apply to the framework integrations:

| Channel | Framework | contentType | Notes |
|---------|-----------|-------------|-------|
| `history` | All | `message` | User-visible chat messages use a common encoding across frameworks |
| `memory` | LangChain4j | `LC4J` | Agent memory entries specific to LangChain4j |
| `memory` | Spring AI | `SpringAI` | Agent memory entries specific to Spring AI |
| `summary` | All | `message` | Summarization entries use the common message format |

**Versioning**: If a framework releases a new version with a different encoding, append a version suffix (e.g., `LC4J.v2`, `SpringAI.v2`). The initial versions are unversioned for simplicity.

**Custom agents**: Agents not using these frameworks should define their own `contentType` strings following a similar pattern (e.g., `MyAgent`, `CustomBot.v1`).

### 2. OpenAPI Spec Changes (`openapi.yml`)

#### Schema Renames

| Old Name | New Name |
|----------|----------|
| `Message` | `Entry` |
| `CreateMessageRequest` | `CreateEntryRequest` |
| `SyncMessagesRequest` | `SyncEntriesRequest` |
| `SyncMessagesResponse` | `SyncEntriesResponse` |
| `MessageChannel` | `Channel` |

#### Entry Schema (formerly Message)

```yaml
Entry:
  type: object
  required:
    - id
    - conversationId
    - channel
    - contentType
    - content
    - createdAt
  properties:
    id:
      type: string
    conversationId:
      type: string
    userId:
      type: string
      nullable: true
    channel:
      $ref: '#/components/schemas/Channel'
    epoch:
      type: integer
      format: int64
      nullable: true
    contentType:
      type: string
      description: |-
        Describes the schema/format of the content array.
        Examples: "anthropic.messages.v1", "openai.chat.v1", "tool.result.v1"
    content:
      type: array
      items: {}
    createdAt:
      type: string
      format: date-time
```

#### CreateEntryRequest Schema

```yaml
CreateEntryRequest:
  type: object
  required:
    - contentType
    - content
  properties:
    userId:
      type: string
      nullable: true
    channel:
      $ref: '#/components/schemas/Channel'
    epoch:
      type: integer
      format: int64
      nullable: true
    contentType:
      type: string
      description: Schema identifier for the content array.
    content:
      type: array
      items: {}
```

#### Path Renames

| Old Path | New Path |
|----------|----------|
| `GET /v1/conversations/{conversationId}/messages` | `GET /v1/conversations/{conversationId}/entries` |
| `POST /v1/conversations/{conversationId}/messages` | `POST /v1/conversations/{conversationId}/entries` |
| `POST /v1/conversations/{conversationId}/messages/{messageId}/fork` | `POST /v1/conversations/{conversationId}/entries/{entryId}/fork` |
| `POST /v1/conversations/{conversationId}/memory/messages/sync` | `POST /v1/conversations/{conversationId}/entries/sync` |
| `POST /v1/user/search/messages` | `POST /v1/user/search/entries` |

#### SearchResult Schema Update

```yaml
SearchResult:
  type: object
  properties:
    entry:  # was: message
      $ref: '#/components/schemas/Entry'
    score:
      type: number
    highlights:
      type: array
      items:
        type: string
```

### 3. OpenAPI Admin Spec Changes (`openapi-admin.yml`)

| Old Path | New Path |
|----------|----------|
| `GET /v1/admin/conversations/{id}/messages` | `GET /v1/admin/conversations/{id}/entries` |
| `DELETE /v1/admin/messages/{id}` | `DELETE /v1/admin/entries/{id}` |

### 4. Protobuf Changes (`memory_service.proto`)

#### Message Type Renames

| Old Name | New Name |
|----------|----------|
| `Message` | `Entry` |
| `MessageChannel` | `Channel` |
| `CreateMessageRequest` | `CreateEntryRequest` |
| `ListMessagesRequest` | `ListEntriesRequest` |
| `ListMessagesResponse` | `ListEntriesResponse` |
| `AppendMessageRequest` | `AppendEntryRequest` |
| `SyncMessagesRequest` | `SyncEntriesRequest` |
| `SyncMessagesResponse` | `SyncEntriesResponse` |

#### Entry Message (formerly Message)

```protobuf
message Entry {
  string id = 1;
  string conversation_id = 2;
  string user_id = 3;
  Channel channel = 4;
  optional int64 epoch = 5;
  string content_type = 6;  // NEW FIELD
  bytes content = 7;
  google.protobuf.Timestamp created_at = 8;
}
```

#### Service Rename

```protobuf
service EntriesService {  // was: MessagesService
  rpc ListEntries(ListEntriesRequest) returns (ListEntriesResponse);
  rpc AppendEntry(AppendEntryRequest) returns (Entry);
  rpc SyncEntries(SyncEntriesRequest) returns (SyncEntriesResponse);
}
```

#### Field Number Hygiene

For any removed or renamed fields in existing messages, mark old field numbers as `reserved` to prevent accidental reuse.

### 5. Java DTO Changes

| Old Class | New Class |
|-----------|-----------|
| `MessageDto` | `EntryDto` |
| `PagedMessages` | `PagedEntries` |
| `CreateUserMessageRequest` | `CreateUserEntryRequest` |
| `SearchMessagesRequest` | `SearchEntriesRequest` |

#### EntryDto (formerly MessageDto)

Add new field:
```java
private String contentType;

public String getContentType() { return contentType; }
public void setContentType(String contentType) { this.contentType = contentType; }
```

### 6. Java Persistence Layer

#### JPA Entity

Rename `MessageEntity` to `EntryEntity`:
- Table name: `entries` (was `messages`)
- Add `contentType` column (VARCHAR, NOT NULL)

```java
@Entity
@Table(name = "entries")
public class EntryEntity {
    // ... existing fields ...

    @Column(name = "content_type", nullable = false)
    private String contentType;
}
```

#### MongoDB Model

Rename `MongoMessage` to `MongoEntry`:
- Collection name: `entries` (was `messages`)
- Add `contentType` field

```java
@MongoEntity(collection = "entries")
public class MongoEntry {
    // ... existing fields ...

    private String contentType;
}
```

### 7. Repository Layer

| Old Class | New Class |
|-----------|-----------|
| `MessageRepository` | `EntryRepository` |
| `MongoMessageRepository` | `MongoEntryRepository` |

Update all method names:
- `listMessages()` → `listEntries()`
- `findLatestMemoryEpoch()` → `findLatestMemoryEpoch()` (no change)
- `listMemoryMessagesByEpoch()` → `listMemoryEntriesByEpoch()`

### 8. REST Resource Layer

Update `ConversationsResource`:
- `listMessages()` → `listEntries()`
- `appendMessage()` → `appendEntry()`
- `syncConversationMemory()` - update internal references
- `forkConversationAtMessage()` → `forkConversationAtEntry()`

Update `SearchResource`:
- `searchMessages()` → `searchEntries()`

Update `AdminResource`:
- Admin message endpoints → Admin entry endpoints

### 9. gRPC Service Layer

| Old Class | New Class |
|-----------|-----------|
| `MessagesGrpcService` | `EntriesGrpcService` |

Update `GrpcDtoMapper`:
- `toProto(MessageDto)` → `toProto(EntryDto)`

### 10. Store Layer

Update `MemoryStore` interface:
- `getMessages()` → `getEntries()`
- `appendUserMessage()` → `appendUserEntry()`
- `appendAgentMessages()` → `appendAgentEntries()`
- `syncAgentMessages()` → `syncAgentEntries()`

Update implementations:
- `PostgresMemoryStore`
- `MongoMemoryStore`

### 11. Vector Search Layer

Update `VectorStore`, `PgVectorStore`, `MongoVectorStore`:
- Method names referencing "message" → "entry"
- Table/collection: `message_embeddings` → `entry_embeddings`

### 12. Database Schema Changes

Since we are resetting the database schema, modify the existing schema files directly instead of adding a migration.

#### Update `schema.sql`

Rename the `messages` table to `entries` and add the `content_type` column:

```sql
CREATE TABLE IF NOT EXISTS entries (
    id                    UUID PRIMARY KEY,
    conversation_id       UUID NOT NULL,
    conversation_group_id UUID NOT NULL,
    user_id               TEXT,
    client_id             TEXT,
    channel               TEXT NOT NULL,
    epoch                 BIGINT,
    content_type          TEXT NOT NULL,
    content               BYTEA NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_entries_conversation_created_at
    ON entries (conversation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_entries_group_created_at
    ON entries (conversation_group_id, created_at);
CREATE INDEX IF NOT EXISTS idx_entries_conversation_channel_client_epoch_created_at
    ON entries (conversation_id, channel, client_id, epoch, created_at);
```

Rename `message_embeddings` to `entry_embeddings`:

```sql
CREATE TABLE IF NOT EXISTS entry_embeddings (
    -- ... existing columns ...
);
```

#### Update `db.changelog-master.yaml`

Modify existing changesets that reference `messages` table to use `entries` instead. Update column definitions to include `content_type`.

#### MongoDB

Update collection name from `messages` to `entries` in `MongoEntry` entity annotation. Add `contentType` field to the model.

### 13. Model Classes

| Old Class | Action |
|-----------|--------|
| `MessageChannel` | Rename to `Channel` |
| `MessageRole` | Delete (unused) |
| `MessageVisibility` | Delete (unused) |

### 14. Frontend Impact (`frontends/chat-frontend`)

The generated TypeScript client types will change:
- `Message` → `Entry`
- `CreateMessageRequest` → `CreateEntryRequest`
- API endpoints update automatically from OpenAPI spec

Components referencing message types will need updates:
- Type imports
- API call paths
- Variable names (optional, for clarity)

After running `npm run generate` (or equivalent), the TypeScript types will regenerate from the OpenAPI spec.

### 15. Framework Integration Updates

The LangChain4j and Spring AI integrations must be updated to:
1. Use renamed API types (`Entry`, `CreateEntryRequest`, etc.)
2. Set the `contentType` field appropriately when creating entries

#### LangChain4j Integration (Quarkus Extension)

**File**: `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/langchain4j/MemoryServiceChatMemoryStore.java`

Changes:
- Update imports: `Message` → `Entry`, `CreateMessageRequest` → `CreateEntryRequest`, `MessageChannel` → `Channel`
- Update `listConversationMessages()` → `listConversationEntries()`
- Update `syncConversationMemory()` → `syncConversationEntries()`
- Add `contentType` to `toCreateMessageRequest()` method (rename to `toCreateEntryRequest()`)

```java
private CreateEntryRequest toCreateEntryRequest(ChatMessage chatMessage) {
    CreateEntryRequest request = new CreateEntryRequest();
    // ... existing code ...
    request.setChannel(CreateEntryRequest.ChannelEnum.MEMORY);
    request.setContentType("LC4J");  // LangChain4j memory encoding
    // ... rest of method ...
    return request;
}
```

#### Spring AI Integration (Spring Boot Autoconfigure)

**File**: `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/memory/MemoryServiceChatMemoryRepository.java`

Changes:
- Update imports: `Message` → `Entry` (API model), `CreateMessageRequest` → `CreateEntryRequest`, `MessageChannel` → `Channel`
- Update `listConversationMessages()` → `listConversationEntries()`
- Update `syncConversationMemory()` → `syncConversationEntries()`
- Add `contentType` to `toCreateMessageRequest()` method (rename to `toCreateEntryRequest()`)

```java
private CreateEntryRequest toCreateEntryRequest(Message message) {
    CreateEntryRequest request = new CreateEntryRequest();
    request.setChannel(Channel.MEMORY);
    request.setContentType("SpringAI");  // Spring AI memory encoding
    // ... rest of method ...
    return request;
}
```

#### History Channel Entries

For `history` channel entries created by either framework (user-visible chat messages), use `contentType: "message"` since both frameworks use a common chat message format for the history channel.

#### Generated Client Updates

Both frameworks use generated REST clients from the OpenAPI spec. After regenerating clients:
- `quarkus/memory-service-extension/` - Quarkus REST client models
- `spring/memory-service-rest-spring/` - Spring WebClient models

The generated types will automatically reflect the Entry renames.

### 16. Site Documentation Updates

#### `site/src/pages/docs/concepts/messages.md`

Rename file to `entries.md` and update content:

1. **Title**: "Messages" → "Entries"
2. **Description**: Update to reflect generic "entry" terminology
3. **JSON examples**: Add `contentType` field to all examples
4. **Property tables**: Add `contentType` row
5. **Endpoint paths**: Update all `/messages` → `/entries`
6. **Conceptual text**: Replace "message" with "entry" throughout

Example updated structure:

```json
{
  "id": "entry_01HF8XJQWXYZ9876ABCD5432",
  "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
  "userId": "user_1234",
  "channel": "history",
  "epoch": null,
  "contentType": "message",
  "content": [
    {
      "type": "text",
      "text": "What's the weather like?"
    }
  ],
  "createdAt": "2025-01-10T14:40:12Z"
}
```

Updated property table:

| Property | Description |
|----------|-------------|
| `id` | Unique entry identifier |
| `conversationId` | ID of the parent conversation |
| `userId` | Human user associated with the entry |
| `channel` | Logical channel (`history`, `memory`, `summary`) |
| `epoch` | Memory epoch number (for `memory` channel entries) |
| `contentType` | Schema identifier for the content array |
| `content` | Array of content blocks (opaque, agent-defined) |
| `createdAt` | Creation timestamp |

#### `site/src/pages/docs/concepts/conversations.md`

- Update any references to "messages" → "entries"
- Update example JSON if present

#### `site/src/pages/docs/concepts/forking.md`

- Update references to forking "at a message" → "at an entry"
- Update API paths in examples

#### `site/src/pages/docs/api-contracts.md`

- No structural changes needed (links to raw specs)

#### `site/src/pages/docs/getting-started.md`

- Review for any message references (currently minimal)

#### Other Documentation Files

Search and update any remaining references to "message" or "messages" across:
- `site/src/pages/docs/deployment/*.md`
- `site/src/pages/docs/apis/**/*.mdx`
- Example code snippets

### 17. Test Updates

#### BDD Feature Files

Rename and update feature files:
- `messages-rest.feature` → `entries-rest.feature`
- `messages-grpc.feature` → `entries-grpc.feature`

Update all scenarios:
1. Replace `/messages` with `/entries` in all paths
2. Replace `messageId` with `entryId` in path parameters
3. Replace `Message` with `Entry` in assertions
4. Add `contentType` field to all create requests and response assertions

Example before:
```gherkin
When POST to "/v1/conversations/${conversationId}/messages" with:
  """
  {
    "channel": "history",
    "content": [{"type": "text", "text": "Hello"}]
  }
  """
Then status is 201
And body matches:
  """
  {
    "id": "${response.body.id}",
    "conversationId": "${conversationId}",
    "channel": "history",
    "content": [{"type": "text", "text": "Hello"}]
  }
  """
```

Example after:
```gherkin
When POST to "/v1/conversations/${conversationId}/entries" with:
  """
  {
    "channel": "history",
    "contentType": "test.v1",
    "content": [{"type": "text", "text": "Hello"}]
  }
  """
Then status is 201
And body matches:
  """
  {
    "id": "${response.body.id}",
    "conversationId": "${conversationId}",
    "channel": "history",
    "contentType": "test.v1",
    "content": [{"type": "text", "text": "Hello"}]
  }
  """
```

#### Other Feature Files Requiring Updates

| Feature File | Changes |
|--------------|---------|
| `conversations-rest.feature` | Update message references |
| `conversations-grpc.feature` | Update message references |
| `forking-rest.feature` | `/messages/{messageId}/fork` → `/entries/{entryId}/fork` |
| `forking-grpc.feature` | Update gRPC message types |
| `summaries-rest.feature` | Update any message references |
| `summaries-grpc.feature` | Update any message references |
| `multi-agent-memory-rest.feature` | Update sync endpoint paths |
| `admin-rest.feature` | Update admin message endpoints |
| `sharing-rest.feature` | Review for message references |
| `sharing-grpc.feature` | Review for message references |
| `eviction-rest.feature` | Update table name references in SQL assertions (`messages` → `entries`) |

#### Step Definitions (`StepDefinitions.java`)

- Update all `Message` type references to `Entry`
- Update all `MessageRepository` to `EntryRepository`
- Update all `MongoMessageRepository` to `MongoEntryRepository`
- Update protobuf type references
- Ensure `contentType` is included in test data builders

#### New Test Scenarios

Add scenarios to validate `contentType` behavior:

1. **"Create entry without contentType returns 400"**: Verify that omitting `contentType` in a create request returns a validation error.

2. **"Create entry with contentType succeeds"**: Verify that including `contentType` works correctly.

3. **"Entry response includes contentType"**: Verify that `contentType` is returned in list and get responses.

4. **"Search returns entries with contentType"**: Verify that search results include `contentType`.

5. **"Sync entries with contentType"**: Verify that memory sync operations handle `contentType` correctly.

### 18. Summary of File Changes

#### Contracts (3 files)

| File | Changes |
|------|---------|
| `openapi.yml` | Rename schemas, paths, add contentType |
| `openapi-admin.yml` | Rename paths |
| `memory_service.proto` | Rename messages, services, add content_type |

#### Java DTOs (5+ files)

| File | Changes |
|------|---------|
| `MessageDto.java` → `EntryDto.java` | Rename, add contentType |
| `PagedMessages.java` → `PagedEntries.java` | Rename |
| `CreateUserMessageRequest.java` → `CreateUserEntryRequest.java` | Rename, add contentType |
| `SearchMessagesRequest.java` → `SearchEntriesRequest.java` | Rename |
| `SearchResultDto.java` | Update `message` field to `entry` |

#### Java Persistence (4 files)

| File | Changes |
|------|---------|
| `MessageEntity.java` → `EntryEntity.java` | Rename, add contentType, update table name |
| `MongoMessage.java` → `MongoEntry.java` | Rename, add contentType, update collection |
| `MessageRepository.java` → `EntryRepository.java` | Rename class and methods |
| `MongoMessageRepository.java` → `MongoEntryRepository.java` | Rename class and methods |

#### Java Services (6+ files)

| File | Changes |
|------|---------|
| `MemoryStore.java` | Update method signatures |
| `PostgresMemoryStore.java` | Update method names and references |
| `MongoMemoryStore.java` | Update method names and references |
| `ConversationsResource.java` | Update method names and paths |
| `SearchResource.java` | Update method names and paths |
| `AdminResource.java` | Update method names and paths |

#### Java gRPC (2 files)

| File | Changes |
|------|---------|
| `MessagesGrpcService.java` → `EntriesGrpcService.java` | Rename and update |
| `GrpcDtoMapper.java` | Update mapper methods |

#### Database (2 files)

| File | Changes |
|------|---------|
| `schema.sql` | Rename `messages` → `entries`, add `content_type` column |
| `db.changelog-master.yaml` | Modify existing changesets (no new migration) |

#### Models (3 files)

| File | Changes |
|------|---------|
| `MessageChannel.java` → `Channel.java` | Rename |
| `MessageRole.java` | Delete (unused) |
| `MessageVisibility.java` | Delete (unused) |

#### Tests (15+ files)

| File | Changes |
|------|---------|
| `messages-rest.feature` → `entries-rest.feature` | Full rewrite |
| `messages-grpc.feature` → `entries-grpc.feature` | Full rewrite |
| `StepDefinitions.java` | Update types and repositories |
| Other feature files | Path and type updates |

#### Site Documentation (5+ files)

| File | Changes |
|------|---------|
| `concepts/messages.md` → `concepts/entries.md` | Full rewrite |
| `concepts/conversations.md` | Update references |
| `concepts/forking.md` | Update references |
| Other docs | Search and replace |

#### Frontend (auto-generated + manual)

| File | Changes |
|------|---------|
| Generated TypeScript types | Automatic from OpenAPI |
| Components using Message types | Update type references |

#### Framework Integrations (3 files)

| File | Changes |
|------|---------|
| `quarkus/.../langchain4j/MemoryServiceChatMemoryStore.java` | Update types, add `contentType: "LC4J"` for memory entries |
| `spring/.../memory/MemoryServiceChatMemoryRepository.java` | Update types, add `contentType: "SpringAI"` for memory entries |
| `spring/.../history/ConversationHistoryStreamAdvisor.java` | Update types, add `contentType: "message"` for history entries |

## Migration Considerations

Since this project has not yet been released, there are no backward-compatibility concerns. This is a contract change before the first release.

For future reference, if this were a post-release change:
- Old endpoints would need deprecation period
- Database migration would need to handle existing data
- `contentType` default value would need consideration for existing rows

## Alternatives Considered

### Keep "Message" and just add contentType

Pros:
- Less churn
- "Message" is familiar to chat/LLM developers

Cons:
- "Message" implies chat semantics that don't apply to all stored data
- Limits mental model for future use cases

### Use "Record" instead of "Entry"

Pros:
- Also generic

Cons:
- "Record" has database connotations
- Could be confused with Java records
- "Entry" is more neutral and common in API design

### Make contentType optional

Pros:
- Backward compatible
- Less friction for simple use cases

Cons:
- Loses the self-describing data benefit
- Clients can't reliably interpret content without contentType
- "unknown" default defeats the purpose

## Decision

Rename "Messages" to "Entries" throughout all contracts and implementations. Add `contentType` as a required string field. This provides a cleaner, more extensible API before the first release.
