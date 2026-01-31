# Enhancement 027: Epoch Evictions

## Motivation

Enhancement 016 introduced eviction for soft-deleted resources (conversation groups and memberships). However, there's another category of data that grows unbounded: **old memory epochs**.

When agents compact or summarize their memory, they create new epochs. The old epochs are retained for auditability and debugging (as described in the `memory epochs` documentation), but they are never accessed during normal agent operation. Over time, these historical epochs can consume significant storage.

This enhancement extends the admin eviction endpoint to support evicting entries from old epochs that are no longer the "current" epoch for their conversation and have not been updated for longer than a configurable retention period.

## Dependencies

- **Enhancement 016 (Data Eviction)**: This enhancement extends the existing eviction endpoint and follows the same patterns.

## Key Concepts

### Epoch Eligibility for Eviction

An epoch is eligible for eviction when **both** conditions are met:

1. **Not the latest epoch**: There exists another epoch with a higher number for the same conversation and client. The latest epoch is always preserved as it represents the agent's current working memory.

2. **Past retention period**: The epoch's "logical updatedAt" is older than the specified retention period.

### Logical updatedAt of an Epoch

Since epochs don't have their own timestamp, we define the **logical updatedAt** of an epoch as:

```
max(created_at) of all entries with that (conversation_id, client_id, epoch)
```

This represents the last time any entry was added to that epoch. Once an agent moves to a new epoch, no new entries are added to old epochs, so this timestamp becomes the "frozen at" time.

### Per-Client Epoch Isolation

As noted in Enhancement 026, epochs are scoped per-client:

```
Each agent (clientId) has its own epoch sequence per conversation
```

This means eviction must consider epochs independently for each `(conversation_id, client_id)` pair. Client A's epoch 0 is independent of Client B's epoch 0 in the same conversation.

## Design

### Extended EvictRequest

Add a new resource type `memory_epochs` to the existing eviction endpoint:

```json
{
  "retentionPeriod": "P90D",
  "resourceTypes": ["memory_epochs"],
  "justification": "Quarterly cleanup of old agent memory epochs"
}
```

| Resource Type | Description |
|---------------|-------------|
| `conversation_groups` | Existing - soft-deleted conversation groups |
| `conversation_memberships` | Existing - soft-deleted memberships |
| `memory_epochs` | **New** - entries from non-latest epochs past retention |

### Eviction Algorithm

For `memory_epochs` eviction:

1. **Find evictable epochs**: Query for epochs that are:
   - Not the latest epoch for their `(conversation_id, client_id)` pair
   - Have `max(created_at)` older than the cutoff time

2. **Batch delete entries**: Delete entries matching evictable epochs in batches

3. **Vector store cleanup**: Queue tasks to remove embeddings for deleted entries

### SQL Query: Find Evictable Epochs

```sql
-- Find (conversation_id, client_id, epoch) tuples eligible for eviction
WITH epoch_stats AS (
    SELECT
        conversation_id,
        client_id,
        epoch,
        MAX(created_at) as last_updated,
        MAX(epoch) OVER (PARTITION BY conversation_id, client_id) as latest_epoch
    FROM entries
    WHERE channel = 'MEMORY'
      AND epoch IS NOT NULL
    GROUP BY conversation_id, client_id, epoch
)
SELECT conversation_id, client_id, epoch
FROM epoch_stats
WHERE epoch < latest_epoch           -- Not the latest epoch
  AND last_updated < :cutoff         -- Past retention period
LIMIT :batchSize
FOR UPDATE SKIP LOCKED
```

### SQL Query: Batch Delete Entries

```sql
-- Delete entries for a batch of evictable epochs
DELETE FROM entries
WHERE id IN (
    SELECT id FROM entries
    WHERE (conversation_id, client_id, epoch) IN (
        VALUES (:conv1, :client1, :epoch1),
               (:conv2, :client2, :epoch2),
               ...
    )
    AND channel = 'MEMORY'
    LIMIT :deleteBatchSize
    FOR UPDATE SKIP LOCKED
)
```

### Index Considerations

The existing index supports the eviction queries:

```sql
CREATE INDEX idx_entries_conversation_channel_client_epoch_created_at
    ON entries (conversation_id, channel, client_id, epoch, created_at);
```

However, for efficient eviction scans, consider adding a covering index:

```sql
-- Optional: Partial index for memory channel epoch analysis
CREATE INDEX idx_entries_memory_epoch_stats
    ON entries (conversation_id, client_id, epoch, created_at)
    WHERE channel = 'MEMORY' AND epoch IS NOT NULL;
```

This index is optional since the existing index already covers the columns, but a partial index may improve query planning for large tables.

## API Specification

Update the `EvictRequest` schema in `openapi-admin.yml`:

```yaml
EvictRequest:
  type: object
  required:
    - retentionPeriod
    - resourceTypes
  properties:
    retentionPeriod:
      type: string
      description: |-
        ISO 8601 duration. Resources older than this are evicted.
        Examples: P90D (90 days), P1Y (1 year), PT24H (24 hours).
      example: "P90D"
    resourceTypes:
      type: array
      items:
        type: string
        enum:
          - conversation_groups
          - conversation_memberships
          - memory_epochs  # NEW
      description: Which resource types to evict.
      example: ["memory_epochs"]
    justification:
      type: string
      description: Reason for the eviction (for audit log).
      example: "Quarterly cleanup of old memory epochs"
```

## Implementation

### EvictionService Changes

```java
public void evict(Duration retentionPeriod, Set<String> resourceTypes,
                  Consumer<Integer> progressCallback) {
    OffsetDateTime cutoff = OffsetDateTime.now().minus(retentionPeriod);

    // ... existing conversation_groups and conversation_memberships handling ...

    if (resourceTypes.contains("memory_epochs")) {
        processed = evictMemoryEpochs(store, cutoff, processed, totalEstimate, progressCallback);
    }

    // Final 100% progress
    if (progressCallback != null) {
        progressCallback.accept(100);
    }
}

private long evictMemoryEpochs(
        MemoryStore store,
        OffsetDateTime cutoff,
        long processed,
        long totalEstimate,
        Consumer<Integer> progressCallback) {
    while (true) {
        // Find a batch of evictable epochs
        List<EpochKey> batch = store.findEvictableEpochs(cutoff, batchSize);
        if (batch.isEmpty()) {
            break;
        }

        // Delete entries for these epochs (and queue vector store cleanup)
        int deleted = store.deleteEntriesForEpochs(batch);

        processed += deleted;
        reportProgress(processed, totalEstimate, progressCallback);

        sleepBetweenBatches();
    }
    return processed;
}
```

### EpochKey Record

```java
/**
 * Identifies a unique epoch for a specific agent in a conversation.
 */
public record EpochKey(UUID conversationId, String clientId, long epoch) {}
```

### MemoryStore Interface Extensions

```java
/**
 * Find epochs eligible for eviction (not latest, past retention).
 * @param cutoff epochs with max(created_at) before this are eligible
 * @param limit maximum epochs to return
 * @return list of (conversationId, clientId, epoch) tuples
 */
List<EpochKey> findEvictableEpochs(OffsetDateTime cutoff, int limit);

/**
 * Count entries in evictable epochs for progress estimation.
 */
long countEvictableEpochEntries(OffsetDateTime cutoff);

/**
 * Delete entries for the specified epochs.
 * Also queues vector store cleanup tasks for affected entries.
 * @return number of entries deleted
 */
int deleteEntriesForEpochs(List<EpochKey> epochs);
```

### PostgresMemoryStore Implementation

```java
@Override
public List<EpochKey> findEvictableEpochs(OffsetDateTime cutoff, int limit) {
    @SuppressWarnings("unchecked")
    List<Object[]> results = entityManager.createNativeQuery("""
        WITH epoch_stats AS (
            SELECT
                conversation_id,
                client_id,
                epoch,
                MAX(created_at) as last_updated,
                MAX(epoch) OVER (PARTITION BY conversation_id, client_id) as latest_epoch
            FROM entries
            WHERE channel = 'MEMORY'
              AND epoch IS NOT NULL
            GROUP BY conversation_id, client_id, epoch
        )
        SELECT conversation_id, client_id, epoch
        FROM epoch_stats
        WHERE epoch < latest_epoch
          AND last_updated < :cutoff
        LIMIT :limit
        FOR UPDATE SKIP LOCKED
        """)
        .setParameter("cutoff", cutoff)
        .setParameter("limit", limit)
        .getResultList();

    return results.stream()
        .map(row -> new EpochKey(
            (UUID) row[0],
            (String) row[1],
            ((Number) row[2]).longValue()))
        .toList();
}

@Override
public long countEvictableEpochEntries(OffsetDateTime cutoff) {
    return ((Number) entityManager.createNativeQuery("""
        WITH evictable_epochs AS (
            SELECT
                conversation_id,
                client_id,
                epoch,
                MAX(created_at) as last_updated,
                MAX(epoch) OVER (PARTITION BY conversation_id, client_id) as latest_epoch
            FROM entries
            WHERE channel = 'MEMORY'
              AND epoch IS NOT NULL
            GROUP BY conversation_id, client_id, epoch
        )
        SELECT COUNT(*) FROM entries e
        JOIN evictable_epochs ev
          ON e.conversation_id = ev.conversation_id
         AND e.client_id = ev.client_id
         AND e.epoch = ev.epoch
        WHERE ev.epoch < ev.latest_epoch
          AND ev.last_updated < :cutoff
        """)
        .setParameter("cutoff", cutoff)
        .getSingleResult()).longValue();
}

@Override
public int deleteEntriesForEpochs(List<EpochKey> epochs) {
    if (epochs.isEmpty()) return 0;

    // 1. Get entry IDs for vector store cleanup
    List<UUID> entryIds = findEntryIdsForEpochs(epochs);

    // 2. Queue vector store cleanup tasks
    for (UUID entryId : entryIds) {
        taskRepository.createTask(
            "vector_store_delete_entry",
            Map.of("entryId", entryId.toString())
        );
    }

    // 3. Delete entries
    // Build VALUES clause dynamically
    StringBuilder values = new StringBuilder();
    for (int i = 0; i < epochs.size(); i++) {
        if (i > 0) values.append(", ");
        values.append("(:conv").append(i)
              .append(", :client").append(i)
              .append(", :epoch").append(i).append(")");
    }

    Query query = entityManager.createNativeQuery(String.format("""
        DELETE FROM entries
        WHERE (conversation_id, client_id, epoch) IN (VALUES %s)
          AND channel = 'MEMORY'
        """, values.toString()));

    for (int i = 0; i < epochs.size(); i++) {
        EpochKey key = epochs.get(i);
        query.setParameter("conv" + i, key.conversationId());
        query.setParameter("client" + i, key.clientId());
        query.setParameter("epoch" + i, key.epoch());
    }

    return query.executeUpdate();
}

private List<UUID> findEntryIdsForEpochs(List<EpochKey> epochs) {
    // Similar query structure, SELECT id instead of DELETE
    // ...
}
```

### MongoMemoryStore Implementation

```java
@Override
public List<EpochKey> findEvictableEpochs(Instant cutoff, int limit) {
    // MongoDB aggregation pipeline to find evictable epochs
    List<Document> pipeline = Arrays.asList(
        // Match memory channel entries with epoch
        new Document("$match", new Document()
            .append("channel", "MEMORY")
            .append("epoch", new Document("$ne", null))),

        // Group by (conversationId, clientId, epoch) to get stats
        new Document("$group", new Document()
            .append("_id", new Document()
                .append("conversationId", "$conversationId")
                .append("clientId", "$clientId")
                .append("epoch", "$epoch"))
            .append("lastUpdated", new Document("$max", "$createdAt"))),

        // Add latest epoch per (conversationId, clientId)
        new Document("$group", new Document()
            .append("_id", new Document()
                .append("conversationId", "$_id.conversationId")
                .append("clientId", "$_id.clientId"))
            .append("epochs", new Document("$push", new Document()
                .append("epoch", "$_id.epoch")
                .append("lastUpdated", "$lastUpdated")))
            .append("latestEpoch", new Document("$max", "$_id.epoch"))),

        // Unwind and filter non-latest epochs past cutoff
        new Document("$unwind", "$epochs"),
        new Document("$match", new Document("$expr", new Document("$and", Arrays.asList(
            new Document("$lt", Arrays.asList("$epochs.epoch", "$latestEpoch")),
            new Document("$lt", Arrays.asList("$epochs.lastUpdated", cutoff))
        )))),

        new Document("$limit", limit)
    );

    return entriesCollection.aggregate(pipeline)
        .map(doc -> new EpochKey(
            UUID.fromString(doc.getEmbedded(List.of("_id", "conversationId"), String.class)),
            doc.getEmbedded(List.of("_id", "clientId"), String.class),
            doc.getEmbedded(List.of("epochs", "epoch"), Long.class)))
        .into(new ArrayList<>());
}

@Override
public int deleteEntriesForEpochs(List<EpochKey> epochs) {
    if (epochs.isEmpty()) return 0;

    // Build OR filter for all epoch keys
    List<Bson> epochFilters = epochs.stream()
        .map(key -> Filters.and(
            Filters.eq("conversationId", key.conversationId().toString()),
            Filters.eq("clientId", key.clientId()),
            Filters.eq("epoch", key.epoch()),
            Filters.eq("channel", "MEMORY")))
        .toList();

    // Queue vector store cleanup tasks
    List<UUID> entryIds = entriesCollection
        .find(Filters.or(epochFilters))
        .map(doc -> UUID.fromString(doc.getString("_id")))
        .into(new ArrayList<>());

    for (UUID entryId : entryIds) {
        taskRepository.createTask(
            "vector_store_delete_entry",
            Map.of("entryId", entryId.toString())
        );
    }

    // Delete entries
    DeleteResult result = entriesCollection.deleteMany(Filters.or(epochFilters));
    return (int) result.getDeletedCount();
}
```

## Example Scenarios

### Scenario 1: Single Agent, Multiple Epochs

```
Conversation: conv-123
Client: agent-A
Epochs:
  - Epoch 0: entries from 2025-01-01 to 2025-01-15 (last_updated: 2025-01-15)
  - Epoch 1: entries from 2025-01-15 to 2025-02-01 (last_updated: 2025-02-01)
  - Epoch 2: entries from 2025-02-01 to now (LATEST)

Eviction with retentionPeriod=P30D on 2025-03-01:
- Epoch 0: eligible (not latest, last_updated 45+ days ago)
- Epoch 1: NOT eligible (last_updated only 28 days ago)
- Epoch 2: NOT eligible (is latest)

Result: Only epoch 0 entries are deleted.
```

### Scenario 2: Multiple Agents, Same Conversation

```
Conversation: conv-456
Client agent-A:
  - Epoch 0: last_updated 2025-01-01
  - Epoch 1: last_updated 2025-01-15 (LATEST)

Client agent-B:
  - Epoch 0: last_updated 2025-02-15 (LATEST)

Eviction with retentionPeriod=P30D on 2025-03-01:
- agent-A epoch 0: eligible (not latest, 59 days old)
- agent-A epoch 1: NOT eligible (is latest)
- agent-B epoch 0: NOT eligible (is latest for agent-B)

Result: Only agent-A's epoch 0 entries are deleted.
```

### Scenario 3: Agent with Single Epoch

```
Conversation: conv-789
Client: agent-C
Epochs:
  - Epoch 0: entries from 2024-01-01 to 2024-06-01 (LATEST, only epoch)

Eviction with retentionPeriod=P30D:
- Epoch 0: NOT eligible (is latest, even though very old)

Result: No entries deleted. The latest epoch is always preserved.
```

## Test Cases

### Cucumber Feature

```gherkin
Feature: Memory Epoch Eviction

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Evict old epochs while preserving latest
    Given a conversation "conv-1" with client "agent-A"
    And the conversation has memory entries:
      | epoch | created_at    | content        |
      | 0     | 100 days ago  | old-entry-1    |
      | 0     | 100 days ago  | old-entry-2    |
      | 1     | 50 days ago   | mid-entry-1    |
      | 2     | 10 days ago   | current-entry  |
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P60D",
        "resourceTypes": ["memory_epochs"]
      }
      """
    Then the response status should be 204
    And epoch 0 entries should be deleted
    And epoch 1 entries should still exist
    And epoch 2 entries should still exist

  Scenario: Preserve latest epoch even if old
    Given a conversation "conv-2" with client "agent-B"
    And the conversation has memory entries:
      | epoch | created_at    | content        |
      | 0     | 365 days ago  | ancient-entry  |
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P30D",
        "resourceTypes": ["memory_epochs"]
      }
      """
    Then the response status should be 204
    And epoch 0 entries should still exist

  Scenario: Independent eviction per client
    Given a conversation "conv-3" with entries:
      | client   | epoch | created_at    |
      | agent-A  | 0     | 100 days ago  |
      | agent-A  | 1     | 10 days ago   |
      | agent-B  | 0     | 100 days ago  |
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P30D",
        "resourceTypes": ["memory_epochs"]
      }
      """
    Then agent-A epoch 0 should be deleted
    And agent-A epoch 1 should still exist
    And agent-B epoch 0 should still exist

  Scenario: Evict with SSE progress
    Given 100 conversations with old epochs
    When I call POST "/v1/admin/evict" with Accept "text/event-stream" and body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["memory_epochs"]
      }
      """
    Then the response should stream progress events
    And the final progress should be 100

  Scenario: Vector store cleanup tasks are created
    Given a conversation with vectorized old epoch entries
    When I evict old epochs
    Then vector_store_delete_entry tasks should be created
```

## Scope of Changes

| File | Change Type |
|------|-------------|
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Add `memory_epochs` to resourceTypes enum |
| `memory-service/src/main/java/io/github/chirino/memory/service/EvictionService.java` | Add epoch eviction logic |
| `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java` | Add epoch eviction methods |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java` | Implement epoch eviction |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java` | Implement epoch eviction |
| `memory-service/src/main/java/io/github/chirino/memory/store/EpochKey.java` | New record class |
| `memory-service/src/test/resources/features/eviction-rest.feature` | Add epoch eviction scenarios |

## Implementation Order

1. **OpenAPI spec** - Add `memory_epochs` to resourceTypes enum
2. **EpochKey record** - Create the key class
3. **MemoryStore interface** - Add epoch eviction methods
4. **PostgresMemoryStore** - Implement with CTE queries
5. **MongoMemoryStore** - Implement with aggregation pipeline
6. **EvictionService** - Add epoch eviction handling
7. **Cucumber tests** - Add epoch eviction scenarios
8. **Compile and test**

## Verification

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# Run eviction-specific tests
./mvnw test -Dcucumber.filter.tags="@eviction"
```

## Assumptions

1. **Entries with null epoch are not affected.** Only entries in the `memory` channel with non-null epoch values are candidates for eviction.

2. **Latest epoch is always preserved.** Even if the latest epoch is very old, it represents the agent's current state and must not be deleted.

3. **Epochs are per-client.** Each `(conversation_id, client_id)` pair has its own independent epoch sequence.

4. **Vector store cleanup is async.** Entry embeddings are cleaned up via the task queue, not synchronously during eviction.

5. **Soft-delete status of parent conversation is ignored.** Epoch eviction applies to all conversations, whether soft-deleted or not. If the conversation is soft-deleted, the entire group will eventually be evicted via `conversation_groups` eviction anyway.

6. **The retention period applies to the epoch's last update time.** We use `max(created_at)` of entries in the epoch, not the creation time of the first entry.

## Open Questions

### Q1: Should we add a minimum epoch age before eviction?

For example, require epochs to be at least 24 hours old before considering them for eviction, even if they're not the latest. This would prevent accidental eviction of epochs that were just superseded.

**Recommendation:** Not needed. The retention period already provides this safety margin. If an admin specifies `P1D`, epochs less than 1 day old are automatically protected.

### Q2: Should eviction consider conversation soft-delete status?

Option A: Evict epochs only from non-deleted conversations (soft-deleted conversations will be fully evicted when the group is evicted).

Option B: Evict epochs from all conversations regardless of deletion status.

**Recommendation:** Option B. Evicting old epochs from soft-deleted conversations reduces storage immediately rather than waiting for the group retention period. No harm is done since the data is already marked for deletion.

### Q3: Should we support evicting specific conversations or clients?

Add optional filters to target specific conversations or client IDs for epoch eviction:

```json
{
  "retentionPeriod": "P90D",
  "resourceTypes": ["memory_epochs"],
  "conversationIds": ["uuid-1", "uuid-2"],
  "clientIds": ["agent-A"]
}
```

**Recommendation:** Out of scope for this enhancement. Can be added later if needed. For now, eviction applies globally.
