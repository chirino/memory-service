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

# Phase 2: Combine Epoch Lookup with Entry Fetch

## Motivation

Every GET and SYNC operation on the memory channel requires two sequential queries:

1. `SELECT max(epoch)` - Find the latest epoch for this agent
2. `SELECT * FROM entries WHERE epoch = ?` - Fetch entries from that epoch

This two-query pattern adds latency to every memory operation. By combining these into a single query, we eliminate one database round-trip per operation.

## Optimization Options Considered

### Option 1: Combine into Single Query (Selected)

Use a subquery to get max epoch and entries in one query:

```sql
SELECT e.*
FROM entries e
JOIN conversations c ON e.conversation_id = c.id
JOIN conversation_groups g ON c.conversation_group_id = g.id
WHERE e.conversation_id = :conversationId
  AND e.channel = 'MEMORY'
  AND e.client_id = :clientId
  AND e.epoch = (
    SELECT max(epoch) FROM entries
    WHERE conversation_id = :conversationId
      AND channel = 'MEMORY'
      AND client_id = :clientId
  )
  AND c.deleted_at IS NULL
  AND g.deleted_at IS NULL
ORDER BY e.created_at, e.id
LIMIT :limit
```

**Pros**:
- Eliminates one round-trip per operation
- No schema changes required
- No additional infrastructure
- Simple to implement

**Cons**:
- Subquery still computes `max()`, just within same query execution

### Option 2: Lightweight Lookup Table

Create a dedicated table to track latest epoch per agent:

```sql
CREATE TABLE memory_epochs (
    conversation_id UUID,
    client_id TEXT,
    latest_epoch BIGINT NOT NULL,
    PRIMARY KEY (conversation_id, client_id)
);
```

**Pros**:
- Single-row lookup O(1) vs aggregate O(log n)
- Very small table, likely stays in memory
- Updated atomically when epochs change

**Cons**:
- Schema change required
- Must maintain consistency on every sync
- Migration needed for existing data

### Option 3: Redis Cache

Store `memory:{conversationId}:{clientId}:latest_epoch` in Redis.

**Pros**:
- Sub-millisecond lookup
- No database load for epoch lookup

**Cons**:
- Cache invalidation complexity
- Additional infrastructure dependency
- Consistency challenges on cache miss

## Selected Approach: Option 1

Option 1 provides immediate benefit with minimal implementation risk. The combined query eliminates the round-trip latency while the database optimizer can efficiently execute the subquery using the existing index.

## SQL Changes

### New Query: List Memory Entries at Latest Epoch

**Repository Method**: `EntryRepository.listMemoryEntriesAtLatestEpoch()`

```sql
SELECT e.*
FROM entries e
JOIN conversations c ON e.conversation_id = c.id
JOIN conversation_groups g ON c.conversation_group_id = g.id
WHERE e.conversation_id = :conversationId
  AND e.channel = 'MEMORY'
  AND e.client_id = :clientId
  AND e.epoch = (
    SELECT max(epoch) FROM entries
    WHERE conversation_id = :conversationId
      AND channel = 'MEMORY'
      AND client_id = :clientId
  )
  AND c.deleted_at IS NULL
  AND g.deleted_at IS NULL
ORDER BY e.created_at, e.id
LIMIT :limit
```

**Pagination Variant** (when `afterEntryId` is provided):
```sql
-- First: lookup cursor entry to get its created_at
SELECT * FROM entries WHERE id = :afterEntryId

-- Then: add filter to main query
AND e.created_at > :cursorCreatedAt
```

**Index Used**: `idx_entries_conversation_channel_client_epoch_created_at` (existing)

### Query Count Reduction

| Operation | Before | After | Savings |
|-----------|--------|-------|---------|
| Get Messages (first page) | 2 queries | 1 query | 50% |
| Get Messages (pagination) | 3 queries | 2 queries | 33% |
| Sync (read phase) | 2 queries | 1 query | 50% |

## Implementation Checklist

- [x] Add `EntryRepository.listMemoryEntriesAtLatestEpoch()` method
  - [x] Basic query with subquery for max epoch
  - [x] Pagination variant with cursor support
  - [x] Handle NULL epoch case (no entries exist)
- [x] Update `PostgresMemoryStore.fetchMemoryEntries()` to use new method
- [x] Update `PostgresMemoryStore.syncAgentEntry()` read phase
- [x] Update `MongoMemoryStore` with equivalent optimization
- [ ] Add integration tests for combined query behavior
- [ ] Benchmark before/after query performance

---

# Phase 3: Pluggable Cache for Memory Entries

## Motivation

While Phase 2's combined query eliminates a database round-trip for the epoch lookup, every GET and SYNC operation still requires a database query to fetch the actual entries. For high-frequency agent interactions, this adds latency even with proper indexing.

By caching the **full list of memory entries** (in their encrypted form), we can:

1. **Eliminate database round-trips entirely** - Cache hit returns entries directly
2. **Reduce database load** - Read-heavy workloads served from cache
3. **Enable read scaling** - Distributed cache naturally handles high read throughput
4. **Maintain security** - Cached data remains encrypted (same as database storage)

## Security: Encrypted Data in Cache

**IMPORTANT**: The cache stores entries in their **encrypted form**, exactly as stored in the database:

```
Database: entries.content = BYTEA (encrypted blob)
Cache:    same encrypted bytes
```

The encryption/decryption happens at the application layer (in the memory store), not at the storage layer. This means:

- Cache contents are unreadable without the encryption key
- No additional security risk from caching
- Same security posture as database storage

## Existing Cache Infrastructure

The memory-service already has a pluggable cache strategy supporting **Redis** and **Infinispan**, currently used for tracking in-progress stream responses. We will follow the same pattern:

### Current Pattern: ResponseResumerLocatorStore

```
ResponseResumerLocatorStore (interface)
    ├── RedisResponseResumerLocatorStore
    ├── InfinispanResponseResumerLocatorStore
    └── NoopResponseResumerLocatorStore

ResponseResumerLocatorStoreSelector (selects based on config)
```

**Key files**:
- Interface: [ResponseResumerLocatorStore.java](memory-service/src/main/java/io/github/chirino/memory/resumer/ResponseResumerLocatorStore.java)
- Redis impl: [RedisResponseResumerLocatorStore.java](memory-service/src/main/java/io/github/chirino/memory/resumer/RedisResponseResumerLocatorStore.java)
- Infinispan impl: [InfinispanResponseResumerLocatorStore.java](memory-service/src/main/java/io/github/chirino/memory/resumer/InfinispanResponseResumerLocatorStore.java)
- Selector: [ResponseResumerLocatorStoreSelector.java](memory-service/src/main/java/io/github/chirino/memory/config/ResponseResumerLocatorStoreSelector.java)

**Configuration**: Uses existing `memory-service.cache.type` property:
```properties
memory-service.cache.type=none|redis|infinispan
```

## Cache Design

### Key Structure

```
memory:entries:{conversationId}:{clientId} → CachedMemoryEntries
```

**Example**:
```
memory:entries:550e8400-e29b-41d4-a716-446655440000:agent-1 → {epoch: 42, entries: [...]}
```

### Value Format: CachedMemoryEntries

```java
/**
 * Cached memory entries for a conversation/client pair.
 * Contains the current epoch and all entries in their encrypted form.
 */
public record CachedMemoryEntries(
    long epoch,
    List<CachedEntry> entries
) {
    /**
     * Individual cached entry. Stores encrypted content as-is from database.
     */
    public record CachedEntry(
        UUID id,
        String contentType,
        byte[] encryptedContent,  // Same bytes as entries.content in DB
        Instant createdAt
    ) {}
}
```

**Serialization**: JSON with Base64-encoded encrypted content bytes.

### Cache Size Considerations

Memory channel entries are bounded by:
- LLM context window limits (typically <100 messages per agent)
- Single entry per sync (Phase 1 consolidation)

Estimated cache size per conversation/client:
- ~50-100 messages × ~1-2 KB per message = **50-200 KB typical**
- Bounded by agent context window, not unbounded growth

### TTL Strategy

**Configurable TTL with refresh on access**:

```properties
# Default: 10 minutes
memory-service.cache.epoch.ttl=PT10M
```

- **TTL refreshed on every access**: Both `get()` and `set()` operations reset the TTL
- **Automatic cleanup**: Inactive conversations are evicted from cache after TTL expires
- **Memory bounded**: Prevents unbounded growth from abandoned conversations
- **Graceful degradation**: Cache miss simply triggers a database fetch and re-caches

**Why refresh on get?** Agent workflows typically involve frequent get/sync cycles. Refreshing TTL on reads keeps actively-used conversations cached while allowing inactive ones to expire naturally.

**Implementation by backend**:
- **Redis**: Uses `GETEX` with `EX` option to atomically get and refresh TTL
- **Infinispan**: Uses `maxIdle` parameter which natively refreshes on access (no extra operations needed)

## Architecture: Interface and Implementations

### MemoryEntriesCache Interface

```java
package io.github.chirino.memory.cache;

/**
 * Cache for storing memory entries per conversation/client pair.
 * Entries are stored in their encrypted form for security.
 * Implementations must handle unavailability gracefully.
 */
public interface MemoryEntriesCache {

    /**
     * Returns true if the cache backend is available and configured.
     */
    boolean available();

    /**
     * Get cached memory entries for a conversation/client pair.
     * Refreshes the TTL on cache hit.
     * @return Optional.empty() if not cached or cache unavailable
     */
    Optional<CachedMemoryEntries> get(UUID conversationId, String clientId);

    /**
     * Store memory entries for a conversation/client pair.
     * The entries should contain encrypted content (not decrypted).
     * Sets/refreshes the TTL based on memory-service.cache.epoch.ttl config.
     */
    void set(UUID conversationId, String clientId, CachedMemoryEntries entries);

    /**
     * Remove cached entries (e.g., on conversation delete or eviction).
     */
    void remove(UUID conversationId, String clientId);
}
```

### Redis Implementation

```java
package io.github.chirino.memory.cache;

@ApplicationScoped
public class RedisMemoryEntriesCache implements MemoryEntriesCache {

    private static final Logger log = Logger.getLogger(RedisMemoryEntriesCache.class);
    private static final String KEY_PREFIX = "memory:entries:";
    private static final Duration REDIS_TIMEOUT = Duration.ofSeconds(5);

    @Inject
    ReactiveRedisDataSource redis;

    @Inject
    ObjectMapper objectMapper;

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @ConfigProperty(name = "memory-service.cache.epoch.ttl", defaultValue = "PT10M")
    Duration ttl;

    @Override
    public boolean available() {
        if (!"redis".equalsIgnoreCase(cacheType)) {
            return false;
        }
        try {
            redis.execute("PING").await().atMost(REDIS_TIMEOUT);
            return true;
        } catch (Exception e) {
            log.warn("Redis not available for memory entries cache", e);
            return false;
        }
    }

    @Override
    public Optional<CachedMemoryEntries> get(UUID conversationId, String clientId) {
        if (!available()) return Optional.empty();

        try {
            String key = buildKey(conversationId, clientId);
            String json = redis.value(String.class).getex(key, new GetExArgs().ex(ttl))
                .await().atMost(REDIS_TIMEOUT);

            if (json == null) return Optional.empty();

            return Optional.of(objectMapper.readValue(json, CachedMemoryEntries.class));
        } catch (Exception e) {
            log.warn("Failed to get entries from Redis cache", e);
            return Optional.empty();
        }
    }

    @Override
    public void set(UUID conversationId, String clientId, CachedMemoryEntries entries) {
        if (!available()) return;

        try {
            String key = buildKey(conversationId, clientId);
            String json = objectMapper.writeValueAsString(entries);

            redis.value(String.class).setex(key, ttl.toSeconds(), json)
                .subscribe().with(
                    success -> {},
                    failure -> log.warn("Failed to set entries in Redis cache", failure)
                );
        } catch (Exception e) {
            log.warn("Failed to set entries in Redis cache", e);
        }
    }

    @Override
    public void remove(UUID conversationId, String clientId) {
        if (!available()) return;

        try {
            String key = buildKey(conversationId, clientId);
            redis.key().del(key)
                .subscribe().with(
                    success -> {},
                    failure -> log.warn("Failed to remove entries from Redis cache", failure)
                );
        } catch (Exception e) {
            log.warn("Failed to remove entries from Redis cache", e);
        }
    }

    private String buildKey(UUID conversationId, String clientId) {
        return KEY_PREFIX + conversationId + ":" + clientId;
    }
}
```

### Infinispan Implementation

```java
package io.github.chirino.memory.cache;

@ApplicationScoped
public class InfinispanMemoryEntriesCache implements MemoryEntriesCache {

    private static final Logger log = Logger.getLogger(InfinispanMemoryEntriesCache.class);
    private static final String CACHE_NAME = "memory-entries";

    @Inject
    RemoteCacheManager cacheManager;

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @ConfigProperty(name = "memory-service.cache.infinispan.startup-timeout", defaultValue = "PT30S")
    Duration startupTimeout;

    @ConfigProperty(name = "memory-service.cache.epoch.ttl", defaultValue = "PT10M")
    Duration ttl;

    private volatile RemoteCache<String, CachedMemoryEntries> cache;
    private volatile long startupDeadline;

    @PostConstruct
    void init() {
        startupDeadline = System.nanoTime() + startupTimeout.toNanos();
    }

    @Override
    public boolean available() {
        if (!"infinispan".equalsIgnoreCase(cacheType)) {
            return false;
        }
        return getCache() != null;
    }

    @Override
    public Optional<CachedMemoryEntries> get(UUID conversationId, String clientId) {
        RemoteCache<String, CachedMemoryEntries> c = getCache();
        if (c == null) return Optional.empty();

        return withRetry("get", () -> {
            String key = buildKey(conversationId, clientId);
            // maxIdle automatically refreshes TTL on access - no manual re-put needed
            return Optional.ofNullable(c.get(key));
        });
    }

    @Override
    public void set(UUID conversationId, String clientId, CachedMemoryEntries entries) {
        RemoteCache<String, CachedMemoryEntries> c = getCache();
        if (c == null) return;

        withRetry("set", () -> {
            String key = buildKey(conversationId, clientId);
            // Use maxIdle for sliding TTL that refreshes on access
            // lifespan=-1 means no hard expiration, only idle-based expiration
            c.put(key, entries, -1, TimeUnit.MILLISECONDS, ttl.toMillis(), TimeUnit.MILLISECONDS);
            return null;
        });
    }

    @Override
    public void remove(UUID conversationId, String clientId) {
        RemoteCache<String, CachedMemoryEntries> c = getCache();
        if (c == null) return;

        withRetry("remove", () -> {
            String key = buildKey(conversationId, clientId);
            c.remove(key);
            return null;
        });
    }

    private RemoteCache<String, CachedMemoryEntries> getCache() {
        if (cache != null) return cache;

        try {
            cache = cacheManager.getCache(CACHE_NAME);
            return cache;
        } catch (Exception e) {
            log.warn("Infinispan cache not available", e);
            return null;
        }
    }

    private <T> T withRetry(String operation, Supplier<T> action) {
        while (System.nanoTime() < startupDeadline) {
            try {
                return action.get();
            } catch (RuntimeException e) {
                if (!isRetryable(e)) {
                    log.warn("Non-retryable error in " + operation, e);
                    return null;
                }
                try {
                    Thread.sleep(200);
                } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    return null;
                }
            }
        }
        return null;
    }

    private boolean isRetryable(Exception e) {
        return e instanceof TransportException || e instanceof HotRodClientException;
    }

    private String buildKey(UUID conversationId, String clientId) {
        return conversationId + ":" + clientId;
    }
}
```

### Noop Implementation

```java
package io.github.chirino.memory.cache;

@ApplicationScoped
public class NoopMemoryEntriesCache implements MemoryEntriesCache {

    @Override
    public boolean available() {
        return false;
    }

    @Override
    public Optional<CachedMemoryEntries> get(UUID conversationId, String clientId) {
        return Optional.empty();
    }

    @Override
    public void set(UUID conversationId, String clientId, CachedMemoryEntries entries) {
        // No-op
    }

    @Override
    public void remove(UUID conversationId, String clientId) {
        // No-op
    }
}
```

### Selector

```java
package io.github.chirino.memory.config;

@ApplicationScoped
public class MemoryEntriesCacheSelector {

    @Inject
    RedisMemoryEntriesCache redisCache;

    @Inject
    InfinispanMemoryEntriesCache infinispanCache;

    @Inject
    NoopMemoryEntriesCache noopCache;

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    public MemoryEntriesCache select() {
        String type = cacheType == null ? "none" : cacheType.trim().toLowerCase();

        return switch (type) {
            case "redis" -> redisCache.available() ? redisCache : noopCache;
            case "infinispan" -> infinispanCache.available() ? infinispanCache : noopCache;
            default -> noopCache;
        };
    }
}
```

## Configuration

Uses existing cache infrastructure with one new property for TTL:

```properties
# Already configured in application.properties
memory-service.cache.type=none|redis|infinispan

# NEW: TTL for cached memory entries (default: 10 minutes)
# TTL is refreshed on both get and set operations
memory-service.cache.epoch.ttl=PT10M

# Redis connection (already configured)
quarkus.redis.hosts=redis://localhost:6379

# Infinispan connection (already configured)
quarkus.infinispan-client.server-list=localhost:11222
```

**Dev/Test profiles** already enable Redis:
```properties
%dev.memory-service.cache.type=redis
%test.memory-service.cache.type=redis
```

## Cache Operations

### Read with Fallback (GET entries)

```java
Optional<CachedMemoryEntries> cached = entriesCache.get(conversationId, clientId);

if (cached.isPresent()) {
    // Cache hit - convert cached entries to Entry objects
    return cached.get().entries().stream()
        .map(this::toEntry)
        .toList();
}

// Cache miss - fetch from database and populate cache
List<Entry> entries = entryRepository.listMemoryEntriesAtLatestEpoch(
    conversationId, clientId, limit);

if (!entries.isEmpty()) {
    entriesCache.set(conversationId, clientId, toCachedEntries(entries));
}

return entries;
```

### Cache Update on Sync

When a sync operation modifies entries, the cache is **updated** with the new complete list (not invalidated). This ensures the next read is a cache hit:

```java
// After sync persists entries, update cache with complete latest epoch list
private void updateCacheWithLatestEntries(UUID conversationId, String clientId) {
    List<Entry> allEntries = entryRepository.listMemoryEntriesAtLatestEpoch(
        conversationId, clientId);
    if (!allEntries.isEmpty()) {
        entriesCache.set(conversationId, clientId, toCachedEntries(allEntries));
    }
}

// Called at end of syncAgentEntry() when entries are modified:
// - Case 1: New epoch (diverged) - cache updated with new epoch's entries
// - Case 2: Append to current epoch - cache updated with all entries including delta
// - Case 3: No-op (exact match) - no cache change needed
```

### Cache Update Scenarios

| Event | Action |
|-------|--------|
| Sync creates new epoch | Update cache with new epoch's complete entry list |
| Sync appends to current epoch | Update cache with all entries including new delta |
| No-op sync (exact match) | No cache change |
| Conversation deleted | Remove cache key |
| Eviction removes entries | Remove cache key (force re-query) |

## PostgresMemoryStore Changes

### Conversion Helpers

```java
private CachedMemoryEntries toCachedEntries(List<Entry> entries) {
    if (entries.isEmpty()) {
        return null;
    }
    long epoch = entries.get(0).getEpoch();
    List<CachedEntry> cachedEntries = entries.stream()
        .map(this::toCachedEntry)
        .toList();
    return new CachedMemoryEntries(epoch, cachedEntries);
}

private CachedEntry toCachedEntry(Entry entry) {
    return new CachedEntry(
        entry.getId(),
        entry.getContentType(),
        entry.getContent(),  // Already encrypted bytes
        entry.getCreatedAt()
    );
}

private Entry toEntry(CachedEntry cached, UUID conversationId,
                      UUID groupId, String clientId, long epoch) {
    Entry entry = new Entry();
    entry.setId(cached.id());
    entry.setConversationId(conversationId);
    entry.setConversationGroupId(groupId);
    entry.setClientId(clientId);
    entry.setChannel(Channel.MEMORY);
    entry.setEpoch(epoch);
    entry.setContentType(cached.contentType());
    entry.setContent(cached.encryptedContent());  // Still encrypted
    entry.setCreatedAt(cached.createdAt());
    return entry;
}
```

### fetchMemoryEntries() Update

The cache stores the **complete list** of entries at the latest epoch. Pagination (limit and afterEntryId cursor) is applied in-memory from the cached data, eliminating database queries for all paginated requests after the first cache population.

```java
@Inject
MemoryEntriesCacheSelector entriesCacheSelector;

private MemoryEntriesCache entriesCache;

@PostConstruct
void init() {
    entriesCache = entriesCacheSelector.select();
}

public List<Entry> fetchMemoryEntries(UUID conversationId, String clientId,
                                       String afterEntryId, int limit) {
    // Try cache first - cache stores the complete list, pagination is applied in-memory
    Optional<CachedMemoryEntries> cached = entriesCache.get(conversationId, clientId);
    if (cached.isPresent()) {
        return paginateCachedEntries(cached.get(), conversationId, clientId, afterEntryId, limit);
    }

    // Cache miss - fetch ALL entries from database to populate cache
    List<Entry> allEntries = entryRepository.listMemoryEntriesAtLatestEpoch(
        conversationId, clientId);

    // Populate cache with complete list
    if (!allEntries.isEmpty()) {
        entriesCache.set(conversationId, clientId, toCachedEntries(allEntries));
    }

    // Apply pagination in-memory
    return paginateEntries(allEntries, afterEntryId, limit);
}

private List<Entry> paginateCachedEntries(CachedMemoryEntries cached,
        UUID conversationId, String clientId, String afterEntryId, int limit) {
    List<CachedEntry> entries = cached.entries();

    // Find starting index based on afterEntryId cursor
    int startIndex = 0;
    if (afterEntryId != null) {
        UUID afterId = UUID.fromString(afterEntryId);
        for (int i = 0; i < entries.size(); i++) {
            if (entries.get(i).id().equals(afterId)) {
                startIndex = i + 1;  // Start after the cursor entry
                break;
            }
        }
    }

    // Apply pagination and convert to Entry
    return entries.stream()
            .skip(startIndex)
            .limit(limit)
            .map(e -> toEntry(e, conversationId, getGroupId(conversationId), clientId, cached.epoch()))
            .toList();
}
```

### syncAgentEntry() Update

```java
public Entry syncAgentEntry(UUID conversationId, String clientId,
                            CreateEntryRequest request) {
    // ... existing comparison logic to determine isDiverged, hasNewContent ...

    if (isDiverged) {
        long newEpoch = currentEpoch + 1;
        Entry entry = createEntry(conversationId, clientId, newEpoch, request);
        entryRepository.persist(entry);

        // Update cache with new epoch's complete list
        updateCacheWithLatestEntries(conversationId, clientId);

        return entry;
    }

    if (hasNewContent) {
        Entry deltaEntry = createEntry(conversationId, clientId, currentEpoch, deltaRequest);
        entryRepository.persist(deltaEntry);

        // Update cache with all entries including new delta
        updateCacheWithLatestEntries(conversationId, clientId);

        return deltaEntry;
    }

    // No-op - no cache change
    return null;
}

private void updateCacheWithLatestEntries(UUID conversationId, String clientId) {
    List<Entry> allEntries = entryRepository.listMemoryEntriesAtLatestEpoch(
        conversationId, clientId);
    if (!allEntries.isEmpty()) {
        entriesCache.set(conversationId, clientId, toCachedEntries(allEntries));
    }
}
```

### Conversation Deletion

```java
public void deleteConversation(UUID conversationId, String clientId) {
    // ... existing deletion logic ...

    // Invalidate cache
    entriesCache.remove(conversationId, clientId);
}
```

## Query Count Impact

### Before (Phase 2)

```
GET /entries?channel=memory (no cache):
  Query 1: SELECT * FROM entries WHERE epoch = (SELECT max(epoch)...) LIMIT 50

Sync (no cache):
  Query 1: SELECT * FROM entries WHERE epoch = (SELECT max(epoch)...)
  Insert 1: INSERT INTO entries ...
  Update 1: UPDATE conversations ...
```

### After (Phase 3 with cache hit)

```
GET /entries?channel=memory (cache hit):
  Cache GET: memory:entries:{id}:{client}
  (no database query - pagination applied in-memory!)

GET /entries?channel=memory&afterEntryId=... (cache hit, paginated):
  Cache GET: memory:entries:{id}:{client}
  (no database query - cursor pagination applied in-memory!)

Sync (cache hit):
  Cache GET: memory:entries:{id}:{client}
  (comparison done with cached data)
  Insert 1: INSERT INTO entries ...
  Update 1: UPDATE conversations ...
  Query 1: SELECT * FROM entries WHERE epoch=... (fetch complete list)
  Cache SET: memory:entries:{id}:{client} (update with complete list)
```

**Note**: After sync, the cache is updated with the complete latest epoch list. This ensures subsequent reads are cache hits, avoiding a database query on the next GET.

### Performance Comparison

| Operation | Phase 2 | Phase 3 (hit) | Improvement |
|-----------|---------|---------------|-------------|
| GET latency (first page) | ~5-10ms | ~1-2ms | 80-90% |
| GET latency (paginated) | ~5-10ms | ~1-2ms | 80-90% |
| Sync read phase | ~5-10ms | ~1-2ms | 80-90% |
| Database queries (GET) | 1 per request | 0 (cache hit) | 100% reduction |
| Post-sync read | 1 query | 0 (cache hit) | 100% reduction |

*Note: Actual numbers depend on cache latency and payload size. Paginated requests now use the cache (pagination applied in-memory) instead of bypassing it.*

## Error Handling

### Cache Unavailable

All implementations handle unavailability gracefully - fall back to database:

```java
public Optional<CachedMemoryEntries> get(UUID conversationId, String clientId) {
    if (!available()) return Optional.empty();  // Graceful degradation

    try {
        // ... cache lookup ...
    } catch (Exception e) {
        log.warn("Failed to get entries from cache", e);
        return Optional.empty();  // Fall back to database
    }
}
```

### Stale Cache Detection

If cached epoch doesn't match database (rare edge case):

```java
// During sync, verify epoch matches
Optional<CachedMemoryEntries> cached = entriesCache.get(conversationId, clientId);
if (cached.isPresent()) {
    Long dbEpoch = entryRepository.findLatestMemoryEpoch(conversationId, clientId);
    if (dbEpoch != null && !dbEpoch.equals(cached.get().epoch())) {
        // Stale cache - invalidate and re-fetch
        entriesCache.remove(conversationId, clientId);
        // Continue with database query...
    }
}
```

## Testing Strategy

### Unit Tests

- `RedisMemoryEntriesCacheTest` - Redis operations with mocked client
- `InfinispanMemoryEntriesCacheTest` - Infinispan operations with mocked cache
- `MemoryEntriesCacheSelectorTest` - Selector logic for each cache type
- Test serialization/deserialization of `CachedMemoryEntries`
- Verify encrypted content bytes are preserved exactly

### Integration Tests

- Cucumber scenarios for cache behavior (uses DevServices Redis)
- Test cache population on first access
- Test cache update on sync (append and new epoch)
- Test cache invalidation on delete/eviction
- Test fallback when cache unavailable
- **Security test**: Verify cached content is encrypted

### Chaos Testing

- Cache failure during operation (verify graceful degradation)
- Stale cache detection and recovery
- Network partition between app and cache
- Large payload handling (verify no truncation)

## Metrics to Monitor

```java
@Inject
MeterRegistry registry;

private Counter cacheHits;
private Counter cacheMisses;
private Counter cacheErrors;
private DistributionSummary cachePayloadSize;

@PostConstruct
void initMetrics() {
    cacheHits = registry.counter("memory.entries.cache.hits");
    cacheMisses = registry.counter("memory.entries.cache.misses");
    cacheErrors = registry.counter("memory.entries.cache.errors");
    cachePayloadSize = registry.summary("memory.entries.cache.payload.bytes");
}
```

- `memory_entries_cache_hits_total` - Cache hits
- `memory_entries_cache_misses_total` - Cache misses
- `memory_entries_cache_errors_total` - Cache errors (by type)
- `memory_entries_cache_payload_bytes` - Payload size distribution

## Implementation Checklist

- [x] Create `CachedMemoryEntries` record
  - [x] `epoch` field
  - [x] `entries` list with `CachedEntry` records
  - [x] Jackson annotations for JSON serialization
  - [x] Base64 encoding for `encryptedContent` bytes
- [x] Create `MemoryEntriesCache` interface
  - [x] `available()`, `get()`, `set()`, `remove()` methods
- [x] Create `RedisMemoryEntriesCache` implementation
  - [x] JSON serialization with ObjectMapper
  - [x] Async operations with fire-and-forget writes
  - [x] Timeout handling (5 second default)
  - [x] Error handling with graceful degradation
  - [x] TTL support with `memory-service.cache.epoch.ttl` config
  - [x] TTL refresh on get (using GETEX with EX option)
- [x] Create `InfinispanMemoryEntriesCache` implementation
  - [x] JSON serialization for CachedMemoryEntries
  - [x] Retry logic for transient failures
  - [x] Startup timeout support
  - [x] Cache name: `memory-entries`
  - [x] TTL support with `memory-service.cache.epoch.ttl` config
  - [x] Use `maxIdle` for sliding TTL (auto-refreshes on access)
- [x] Create `NoopMemoryEntriesCache` implementation
- [x] Create `MemoryEntriesCacheSelector`
  - [x] Use existing `memory-service.cache.type` config
  - [x] Fall back to noop if cache unavailable
- [x] Update `PostgresMemoryStore`
  - [x] Add conversion helpers (`toCachedEntries`, `toEntry`, `paginateCachedEntries`)
  - [x] Update `fetchMemoryEntries()` with cache lookup
  - [x] Update `syncAgentEntry()` with cache update (not invalidation)
  - [x] Cache stores full list, pagination applied in-memory
- [x] Update `MongoMemoryStore` with equivalent changes
- [x] Sync operations update cache with latest entries (not invalidate)
- [x] Add Micrometer metrics for cache operations
  - [x] `memory.entries.cache.hits` counter
  - [x] `memory.entries.cache.misses` counter
  - [x] `memory.entries.cache.errors` counter
- [x] Add Infinispan cache configuration for `memory-entries`
- [ ] Write unit tests for each implementation
- [x] Write integration tests for cache behavior
  - [x] `memory-cache-rest.feature` - cache hit/miss and update after sync
- [ ] Add security test verifying encrypted content in cache

---

## Future Optimization Opportunities

*(To be analyzed in follow-up phases)*

- Add content hash for quick no-op detection without full comparison
- Evaluate if soft-delete joins can be eliminated for memory-only queries
- Consider cache warming strategies for predictable access patterns
