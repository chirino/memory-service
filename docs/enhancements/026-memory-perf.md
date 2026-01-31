# Enhancement 026: Memory Channel SQL Access Pattern Analysis

## Overview

This document analyzes the primary SQL access patterns used when agents interact with the memory channel of conversations. The agent-facing API is designed for LLM context retrieval, particularly the `get` and `sync` operations that form the core agent workflow.

## Agent Workflow

Agents primarily perform two operations:

1. **Get Messages** - Retrieve current memory entries for LLM context
2. **Sync Messages** - Update memory entries when the agent's context changes

These map to:
- `GET /v1/conversations/{id}/entries?channel=memory&epoch=latest`
- `POST /v1/conversations/{id}/entries/sync`

## API Entry Points

### Client Implementation

The LangChain4j integration ([MemoryServiceChatMemoryStore.java](quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/langchain4j/MemoryServiceChatMemoryStore.java)) shows the typical agent pattern:

```java
// Get messages (line 64-70)
conversationsApi().listConversationEntries(
    UUID,           // conversationId
    null,           // afterEntryId (cursor)
    50,             // limit
    Channel.MEMORY, // channel
    null            // epoch (defaults to "latest")
);

// Sync messages (line 146-147)
conversationsApi().syncConversationMemory(UUID, SyncEntriesRequest);
```

### Endpoint Implementations

**ConversationsResource.java** handles these requests:
- `listEntries()` (lines 169-234) - GET entries endpoint
- `syncMemoryEntries()` (lines 291-331) - POST sync endpoint

---

## SQL Access Pattern: Get Messages (MEMORY Channel)

### Call Flow

```
API Request
    ↓
ConversationsResource.listEntries()
    ↓
PostgresMemoryStore.getEntries() (line 631-664)
    ↓
PostgresMemoryStore.fetchMemoryEntries() (line 666-694)
    ↓
EntryRepository queries
```

### Query 1: Find Latest Epoch

**Purpose**: Determine the most recent epoch for this agent's memory entries.

**Repository Method**: [EntryRepository.findLatestMemoryEpoch()](memory-service/src/main/java/io/github/chirino/memory/persistence/repo/EntryRepository.java#L79-L95)

```sql
SELECT max(m.epoch)
FROM entries m
JOIN conversations c ON m.conversation_id = c.id
JOIN conversation_groups g ON c.conversation_group_id = g.id
WHERE m.conversation_id = :conversationId
  AND m.channel = 'MEMORY'
  AND m.client_id = :clientId
  AND c.deleted_at IS NULL
  AND g.deleted_at IS NULL
```

**Index Used**: `idx_entries_conversation_channel_client_epoch_created_at`
```sql
CREATE INDEX idx_entries_conversation_channel_client_epoch_created_at
    ON entries (conversation_id, channel, client_id, epoch, created_at);
```

**Notes**:
- Returns `NULL` if no memory entries exist for this conversation/client
- Aggregate `max()` can use the index for efficient retrieval

### Query 2: List Memory Entries by Epoch

**Purpose**: Retrieve all entries from the latest epoch, ordered by creation time.

**Repository Method**: [EntryRepository.listMemoryEntriesByEpoch()](memory-service/src/main/java/io/github/chirino/memory/persistence/repo/EntryRepository.java#L102-L136)

```sql
SELECT m.*
FROM entries m
JOIN conversations c ON m.conversation_id = c.id
JOIN conversation_groups g ON c.conversation_group_id = g.id
WHERE m.conversation_id = :conversationId
  AND m.channel = 'MEMORY'
  AND m.client_id = :clientId
  AND m.epoch = :latestEpoch
  AND c.deleted_at IS NULL
  AND g.deleted_at IS NULL
ORDER BY m.created_at, m.id
LIMIT :limit
```

**Index Used**: `idx_entries_conversation_channel_client_epoch_created_at`

**Pagination Variant** (when `afterEntryId` is provided):
```sql
-- First: lookup cursor entry to get its created_at
SELECT * FROM entries WHERE id = :afterEntryId

-- Then: add filter
AND m.created_at > :cursorCreatedAt
```

### Total Queries for Get Messages

| Scenario | Queries |
|----------|---------|
| First page, no entries exist | 1 (max epoch returns NULL, fallback to listByChannel) |
| First page, entries exist | 2 (max epoch + list by epoch) |
| Subsequent page | 3 (max epoch + cursor lookup + list by epoch) |

---

## SQL Access Pattern: Sync Messages

### Call Flow

```
API Request
    ↓
ConversationsResource.syncMemoryEntries()
    ↓
PostgresMemoryStore.syncAgentEntries() (line 769-831)
    ↓
EntryRepository.findLatestMemoryEpoch()
EntryRepository.listMemoryEntriesByEpoch()
    ↓
(comparison logic)
    ↓
PostgresMemoryStore.appendAgentEntries() (if changes detected)
```

### Query 1: Find Latest Epoch

Same as Get Messages Query 1 above.

### Query 2: Fetch All Entries from Latest Epoch

**Purpose**: Load existing entries to compare against incoming sync request.

**Repository Method**: [EntryRepository.listMemoryEntriesByEpoch()](memory-service/src/main/java/io/github/chirino/memory/persistence/repo/EntryRepository.java#L97-L100) (no-limit variant)

```sql
SELECT m.*
FROM entries m
JOIN conversations c ON m.conversation_id = c.id
JOIN conversation_groups g ON c.conversation_group_id = g.id
WHERE m.conversation_id = :conversationId
  AND m.channel = 'MEMORY'
  AND m.client_id = :clientId
  AND m.epoch = :latestEpoch
  AND c.deleted_at IS NULL
  AND g.deleted_at IS NULL
ORDER BY m.created_at, m.id
```

**Note**: Uses `Integer.MAX_VALUE` as limit to fetch ALL entries for comparison.

### Sync Decision Logic

After fetching existing entries, the sync compares content ([PostgresMemoryStore.java:786-830](memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java#L786-L830)):

| Condition | Result | Queries |
|-----------|--------|---------|
| `existing == incoming` | No-op, return immediately | 2 (find epoch + list entries) |
| `incoming` is prefix extension of `existing` | Append delta to current epoch | 2 + N inserts |
| Otherwise | Create new epoch with all entries | 2 + M inserts |

### Query 3: Insert Entry (Append)

**Repository Method**: Direct entity persist via Panache

```sql
INSERT INTO entries (
    id, conversation_id, conversation_group_id, user_id, client_id,
    channel, epoch, content_type, content, created_at
) VALUES (
    :uuid, :conversationId, :groupId, :userId, :clientId,
    'MEMORY', :epoch, :contentType, :encryptedContent, :now
)
```

**Additional**: Update conversation timestamp
```sql
UPDATE conversations
SET updated_at = :latestHistoryTimestamp
WHERE id = :conversationId
```

### Total Queries for Sync

| Scenario | Queries |
|----------|---------|
| No-op (exact match) | 2 |
| Append to current epoch (N new entries) | 2 + N inserts + 1 update |
| New epoch (M entries) | 2 + M inserts + 1 update |

---

## Database Schema

### Entries Table

```sql
CREATE TABLE entries (
    id                UUID PRIMARY KEY,
    conversation_id   UUID NOT NULL REFERENCES conversations (id),
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id),
    user_id           TEXT,
    client_id         TEXT,          -- Agent identifier (from API key)
    channel           TEXT NOT NULL, -- 'HISTORY', 'MEMORY', or 'TRANSCRIPT'
    epoch             BIGINT,        -- Memory epoch (null for non-memory)
    content_type      TEXT NOT NULL,
    content           BYTEA NOT NULL,-- Encrypted JSON content
    created_at        TIMESTAMPTZ NOT NULL
);
```

### Relevant Indexes

```sql
-- Primary index for memory channel queries
CREATE INDEX idx_entries_conversation_channel_client_epoch_created_at
    ON entries (conversation_id, channel, client_id, epoch, created_at);

-- Used for history channel and general queries
CREATE INDEX idx_entries_conversation_created_at
    ON entries (conversation_id, created_at);

-- Used for group-level queries (forking)
CREATE INDEX idx_entries_group_created_at
    ON entries (conversation_group_id, created_at);
```

---

## Access Pattern Summary

### Memory Get Operation
```
┌─────────────────────────────────────────────────────────────────┐
│ GET /v1/conversations/{id}/entries?channel=memory&epoch=latest  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Query 1: SELECT max(epoch) FROM entries                         │
│          WHERE conversation_id=? AND channel='MEMORY'           │
│          AND client_id=?                                        │
│ Index: idx_entries_conversation_channel_client_epoch_created_at │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Query 2: SELECT * FROM entries                                  │
│          WHERE conversation_id=? AND channel='MEMORY'           │
│          AND client_id=? AND epoch=?                            │
│          ORDER BY created_at, id LIMIT 50                       │
│ Index: idx_entries_conversation_channel_client_epoch_created_at │
└─────────────────────────────────────────────────────────────────┘
```

### Memory Sync Operation
```
┌─────────────────────────────────────────────────────────────────┐
│ POST /v1/conversations/{id}/entries/sync                        │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Query 1: SELECT max(epoch) FROM entries                         │
│          WHERE conversation_id=? AND channel='MEMORY'           │
│          AND client_id=?                                        │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Query 2: SELECT * FROM entries                                  │
│          WHERE conversation_id=? AND channel='MEMORY'           │
│          AND client_id=? AND epoch=?                            │
│          ORDER BY created_at, id (NO LIMIT - fetches all)       │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Compare existing vs incoming messages                           │
├─────────────────────────────────────────────────────────────────┤
│ Case A: Exact match → No-op (0 writes)                          │
│ Case B: Prefix extension → INSERT delta entries (N writes)      │
│ Case C: Diverged → INSERT all entries with new epoch (M writes) │
└─────────────────────────────────────────────────────────────────┘
```

---

## Key Observations

### Current Behavior

1. **Two-query pattern**: Get and Sync both require finding max epoch before listing entries
2. **Full fetch on sync**: Sync loads ALL existing entries to compare, not just count/hash
3. **Content-level comparison**: Sync compares decrypted message content, not just entry IDs
4. **Epoch isolation**: Each agent (clientId) has its own epoch sequence per conversation

### Characteristics

| Aspect | Observation |
|--------|-------------|
| Read amplification | Sync reads all entries even when only checking for no-op |
| Index coverage | Primary index covers conversation+channel+client+epoch+created_at |
| Join overhead | Every query joins conversations + conversation_groups for soft-delete check |
| Content decryption | All entries are decrypted in memory for comparison |

---

---

# Phase 1: Consolidate Sync to Single Entry

## Motivation

The current sync API accepts a `SyncEntriesRequest` containing a list of `CreateEntryRequest` objects, where each entry represents one message. This leads to:

- **N inserts per sync**: When memory diverges, we insert M separate rows
- **N inserts on append**: When extending, we insert N delta rows
- **Row-per-message overhead**: Each message is a separate database row with its own UUID, timestamps, etc.

## Proposed Change

Change the sync API to accept a **single `CreateEntryRequest`** instead of `SyncEntriesRequest`. The entry's `content` array holds **all messages** in the agent's memory.

### Current Model

```
SyncEntriesRequest {
  entries: [
    CreateEntryRequest { contentType: "LC4J", content: [msg1] },
    CreateEntryRequest { contentType: "LC4J", content: [msg2] },
    CreateEntryRequest { contentType: "LC4J", content: [msg3] },
  ]
}
```

Each entry becomes a separate database row:
```
entries table:
| id    | epoch | content      |
|-------|-------|--------------|
| uuid1 | 1     | [msg1]       |
| uuid2 | 1     | [msg2]       |
| uuid3 | 1     | [msg3]       |
```

### Proposed Model

```
CreateEntryRequest {
  contentType: "LC4J",
  channel: "memory",
  content: [msg1, msg2, msg3]  // All messages in one array
}
```

Single database row per sync delta:
```
entries table:
| id    | epoch | content              |
|-------|-------|----------------------|
| uuid1 | 1     | [msg1, msg2, msg3]   |
```

## API Changes

### REST API

**Current** (`POST /v1/conversations/{id}/entries/sync`):
```yaml
SyncEntriesRequest:
  type: object
  required:
    - entries
  properties:
    entries:
      type: array
      items:
        $ref: '#/components/schemas/CreateEntryRequest'
```

**Proposed**:
```yaml
# Reuse existing CreateEntryRequest directly
# Request body is CreateEntryRequest, not SyncEntriesRequest
```

### gRPC API

**Current**:
```protobuf
message SyncEntriesRequest {
  bytes conversation_id = 1;
  repeated CreateEntryRequest entries = 2;
}

rpc SyncEntries(SyncEntriesRequest) returns (SyncEntriesResponse);
```

**Proposed**:
```protobuf
message SyncEntryRequest {
  bytes conversation_id = 1;
  CreateEntryRequest entry = 2;  // Single entry, not repeated
}

rpc SyncEntry(SyncEntryRequest) returns (SyncEntryResponse);
```

## Sync Algorithm Changes

### Comparison Logic

The sync handler compares the incoming `content` array against the **flattened content** of all previous entries in the current epoch.

```
Existing entries (epoch=1):
  Entry1.content = [msg1, msg2]
  Entry2.content = [msg3]

Flattened existing = [msg1, msg2, msg3]

Incoming request:
  content = [msg1, msg2, msg3, msg4, msg5]

Result: Prefix extension detected
  Delta to insert = [msg4, msg5] (as single new entry)
```

### Decision Matrix

| Condition | Action | Inserts |
|-----------|--------|---------|
| `flattened_existing == incoming.content` | No-op | 0 |
| `incoming.content` starts with `flattened_existing` | Append delta entry | 1 |
| Any previous entry has different `contentType` | Diverged → new epoch | 1 |
| Content mismatch | Diverged → new epoch | 1 |

### Divergence Detection

A sync is considered **diverged** if:

1. **Content mismatch**: The incoming content doesn't start with the flattened existing content
2. **ContentType mismatch**: Any previous entry in the epoch has a different `contentType` than the incoming request

```java
// Pseudo-code for divergence check
boolean isDiverged(List<Entry> existingEntries, CreateEntryRequest incoming) {
    // Check contentType consistency
    for (Entry e : existingEntries) {
        if (!e.getContentType().equals(incoming.getContentType())) {
            return true;  // Diverged due to contentType change
        }
    }

    // Flatten existing content
    List<Object> flattenedExisting = existingEntries.stream()
        .flatMap(e -> e.getContent().stream())
        .toList();

    // Check if incoming is prefix extension
    return !isPrefix(flattenedExisting, incoming.getContent());
}
```

## SQL Impact

### Before (Current)

```
Sync with 10 new messages:
  Query 1: SELECT max(epoch) ...
  Query 2: SELECT * FROM entries WHERE epoch=? ...
  Insert 1: INSERT INTO entries ... (msg1)
  Insert 2: INSERT INTO entries ... (msg2)
  ...
  Insert 10: INSERT INTO entries ... (msg10)
  Update: UPDATE conversations SET updated_at=...

Total: 2 queries + 10 inserts + 1 update = 13 operations
```

### After (Proposed)

```
Sync with 10 new messages:
  Query 1: SELECT max(epoch) ...
  Query 2: SELECT * FROM entries WHERE epoch=? ...
  Insert 1: INSERT INTO entries ... ([msg1..msg10] as delta)
  Update: UPDATE conversations SET updated_at=...

Total: 2 queries + 1 insert + 1 update = 4 operations
```

### Reduction Summary

| Scenario | Current | Proposed | Reduction |
|----------|---------|----------|-----------|
| Append N messages | 2 + N + 1 | 2 + 1 + 1 | N-1 inserts saved |
| New epoch (M messages) | 2 + M + 1 | 2 + 1 + 1 | M-1 inserts saved |
| No-op | 2 | 2 | No change |

## Get Messages Impact

The `GET /entries?channel=memory` endpoint must reconstruct the message list from potentially multiple entries:

```java
// Current: Each entry = one message
List<Message> messages = entries.stream()
    .map(e -> decodeMessage(e.getContent().get(0)))
    .toList();

// Proposed: Flatten content arrays from all entries
List<Message> messages = entries.stream()
    .flatMap(e -> e.getContent().stream())
    .map(this::decodeMessage)
    .toList();
```

This is a minor change with no SQL impact.

## Client Library Updates

### LangChain4j Integration

**Current** ([MemoryServiceChatMemoryStore.java](quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/langchain4j/MemoryServiceChatMemoryStore.java)):
```java
// Line 136-147: Creates one CreateEntryRequest per message
List<CreateEntryRequest> entries = messages.stream()
    .map(this::toCreateEntryRequest)
    .toList();
SyncEntriesRequest request = new SyncEntriesRequest();
request.setEntries(entries);
conversationsApi().syncConversationMemory(conversationId, request);
```

**Proposed**:
```java
// Single entry with all messages in content array
CreateEntryRequest entry = new CreateEntryRequest();
entry.setChannel(Channel.MEMORY);
entry.setContentType("LC4J");
entry.setContent(messages.stream()
    .map(this::encodeMessage)
    .toList());
conversationsApi().syncConversationMemory(conversationId, entry);
```

## Migration Considerations

### Backward Compatibility

The new sync endpoint can coexist with the old one during migration:

- `POST /v1/conversations/{id}/entries/sync` (old) - accepts `SyncEntriesRequest`
- `POST /v1/conversations/{id}/memory/sync` (new) - accepts `CreateEntryRequest`

Or deprecate the old endpoint and require client updates.

### Existing Data

Existing entries with single-message content arrays remain valid. The get/sync logic handles both:
- Old entries: `content = [single_message]`
- New entries: `content = [msg1, msg2, ..., msgN]`

The flattening logic works uniformly across both formats.

## Implementation Checklist

- [x] Update OpenAPI spec (`openapi.yml`)
  - [x] Modify existing sync endpoint to accept `CreateEntryRequest`
  - [x] Update response schema (`SyncEntryResponse` with single `entry`)
- [x] Update Proto definition (`memory_service.proto`)
  - [x] Change `SyncEntriesRequest.entries` (repeated) to `entry` (singular)
  - [x] Change `SyncEntriesResponse.entries` (repeated) to `entry` (optional)
- [x] Regenerate API clients
- [x] Update `PostgresMemoryStore.syncAgentEntry()`
  - [x] Accept single `CreateEntryRequest`
  - [x] Implement flattened content comparison
  - [x] Handle contentType divergence check
- [x] Update `MongoMemoryStore.syncAgentEntry()`
- [x] Update `MemorySyncHelper`
  - [x] Add `flattenContent()` utility
  - [x] Add `contentEquals()` and `isContentPrefix()` methods
  - [x] Add `extractDelta()` and `withEpochAndContent()` methods
- [x] Update `ConversationsResource.syncMemoryEntries()`
- [x] Update `EntriesGrpcService.syncEntries()`
- [x] Update LangChain4j client (`MemoryServiceChatMemoryStore`)
- [x] Update Spring AI client (`MemoryServiceChatMemoryRepository`)
- [x] Add/update tests (cucumber feature files and step definitions)

---

## Future Optimization Opportunities

*(To be analyzed in follow-up phases)*

- Combine max(epoch) + list into single query
- Add content hash for quick no-op detection without full fetch
- Consider materialized view for latest epoch per conversation/client
- Evaluate if soft-delete joins can be eliminated for memory-only queries
