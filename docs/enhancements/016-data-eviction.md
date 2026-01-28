# Data Eviction (Hard Delete) for Soft-Deleted Resources

## Motivation

Enhancement 013 (Soft Deletes) introduced soft deletion, where resources are marked with a `deletedAt` timestamp instead of being physically removed. This preserves data for audit trails but creates unbounded storage growth.

This enhancement adds an admin endpoint to **permanently hard-delete** resources that have been soft-deleted for longer than a configurable retention period. This addresses:

1. **Storage Management**: Prevent unbounded growth of soft-deleted records.
2. **GDPR Compliance**: Support "right to erasure" by permanently removing data after the retention period.
3. **Performance**: Reduce table sizes by removing stale data that will never be accessed.
4. **Compliance**: Meet organizational data retention policy requirements (e.g., "delete all data older than 7 years").

## Dependencies

- **Enhancement 013 (Soft Deletes)**: Resources must have `deletedAt` timestamps
- **Enhancement 014 (Admin Access)**: Eviction endpoint uses admin authorization and audit logging
- **Enhancement 015 (Task Queue)**: Vector store cleanup uses the background task queue

## Design Decisions

### Endpoint Design

A new admin endpoint triggers the eviction process:

```
POST /v1/admin/evict
```

**Request:**
```json
{
  "retentionPeriod": "P90D",
  "resourceTypes": ["conversation_groups", "conversation_memberships"],
  "justification": "Quarterly data cleanup per policy XYZ"
}
```

**Response (default):** `204 No Content`

**Response (with `Accept: text/event-stream`):** SSE stream with progress updates:
```
data: {"progress": 0}

data: {"progress": 25}

data: {"progress": 50}

data: {"progress": 100}

```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `retentionPeriod` | ISO 8601 Duration | Yes | Resources soft-deleted longer than this are hard-deleted (e.g., `P90D` = 90 days, `PT24H` = 24 hours) |
| `resourceTypes` | array of strings | Yes | Which resource types to evict. Valid values: `conversation_groups`, `conversation_memberships` |
| `justification` | string | No* | Reason for the eviction (required if `memory-service.admin.require-justification=true`) |

*Note: The `justification` field follows the same rules as other admin endpoints per enhancement 014.*

### Resource Types and Cascade Behavior

The eviction endpoint accepts a list of resource types to delete. Due to the existing `ON DELETE CASCADE` constraints, deleting certain resource types automatically removes their children:

| Resource Type | Cascade Deletes | Notes |
|---------------|-----------------|-------|
| `conversation_groups` | `conversations`, `messages`, `conversation_memberships`, `conversation_ownership_transfers` | Top-level delete - removes entire conversation trees |
| `conversation_memberships` | _(none)_ | Standalone - only removes orphaned membership records |

**Why only these two types?**

- **`conversation_groups`**: The root entity. When a conversation is soft-deleted, the entire group (including all conversations in the tree, messages, and memberships) is marked deleted. Hard-deleting the group cascades to all children via `ON DELETE CASCADE`.

- **`conversation_memberships`**: Memberships can be soft-deleted independently (e.g., when a user is removed from a shared conversation). These orphaned membership records should be evicted separately from the parent group.

**Why not `conversations` or `messages` directly?**

- `conversations` always belong to a `conversation_group`. The soft-delete design marks the entire group as deleted, not individual conversations. Evicting at the group level is cleaner and ensures referential integrity.

- `messages` are never soft-deleted; they inherit their deleted status from their parent conversation. They are cascade-deleted when the group is hard-deleted.

### Cutoff Time Calculation

The cutoff time is calculated in the application layer before querying:

```java
OffsetDateTime cutoff = OffsetDateTime.now().minus(retentionPeriod);
```

Then used in queries to find records to evict:

```java
// PostgreSQL
WHERE deleted_at IS NOT NULL AND deleted_at < :cutoff

// MongoDB  
{ deletedAt: { $ne: null, $lt: cutoff } }
```

This means: "Delete resources whose `deleted_at` timestamp is older than the retention period."

Example: If `retentionPeriod=P90D` and today is 2026-01-27, resources deleted before 2025-10-29 are evicted.

### Raw SQL for Hard Deletes

The eviction implementation uses **native SQL queries** instead of JPA/Hibernate operations. This is critical for efficiency:

| Approach | Problem |
|----------|---------|
| `entityManager.remove(entity)` | Loads entity and all cascade-mapped children into memory, triggers lifecycle callbacks, executes N+1 DELETE statements |
| JPQL `DELETE FROM Entity WHERE ...` | Bypasses `ON DELETE CASCADE` - database constraints don't fire from JPQL |
| **Native SQL `DELETE FROM table WHERE ...`** | Single statement, database handles cascade, no entity loading |

The existing soft-delete code uses Panache's `update()` with JPQL strings, which translates to bulk UPDATE statements (efficient). For hard deletes, we need native SQL to leverage `ON DELETE CASCADE` at the database level.

```java
// WRONG - ORM loads entities, N+1 deletes
for (UUID id : groupIds) {
    ConversationGroupEntity entity = entityManager.find(ConversationGroupEntity.class, id);
    entityManager.remove(entity);  // Triggers cascade loading!
}

// WRONG - JPQL bypasses DB cascade
entityManager.createQuery("DELETE FROM ConversationGroupEntity g WHERE g.id IN :ids")
    .setParameter("ids", groupIds)
    .executeUpdate();  // Children NOT deleted!

// CORRECT - Native SQL, DB handles cascade
entityManager.createNativeQuery("DELETE FROM conversation_groups WHERE id = ANY(:ids)")
    .setParameter("ids", groupIds.toArray(new UUID[0]))
    .executeUpdate();  // ON DELETE CASCADE fires!
```

### Batch Processing Strategy

Long-running deletes holding table locks can degrade performance for concurrent user operations. The eviction process uses **application-level batched deletes** to minimize lock contention:

```java
int batchSize = 1000;  // configurable
int totalDeleted = 0;

while (true) {
    List<UUID> batch = findEvictableGroupIds(cutoffTime, batchSize);
    if (batch.isEmpty()) break;
    
    deleteGroupsBatch(batch);  // DELETE FROM conversation_groups WHERE id IN (:batch)
    totalDeleted += batch.size();
    
    // Small delay between batches to reduce load
    Thread.sleep(batchDelayMs);
}
```

**Benefits:**
- Works on all databases (PostgreSQL, MongoDB)
- Fine-grained control over batch size and delay
- Supports progress reporting via SSE
- Portable - no database-specific syntax required

### Concurrency Safety

The eviction endpoint does **not** implement distributed locking. Concurrent calls are allowed and may be useful (e.g., one process evicting `conversation_groups` while another evicts `conversation_memberships`).

To ensure consistency when multiple eviction processes run concurrently on the same resource type, the batch selection query uses `FOR UPDATE SKIP LOCKED`:

```sql
-- PostgreSQL
SELECT id FROM conversation_groups 
WHERE deleted_at IS NOT NULL AND deleted_at < :cutoff 
FOR UPDATE SKIP LOCKED
LIMIT 1000;
```

**How this works:**
- Each batch query locks the rows it selects
- Concurrent queries skip already-locked rows
- No duplicate deletes, no deadlocks
- Work is naturally distributed across concurrent callers

**Trade-offs:**
- Progress reporting may be less accurate when concurrent callers "share" the work
- Total database load increases with concurrent calls
- Operators should implement external locking if they need single-caller semantics

**MongoDB:** Uses `findOneAndDelete` in a loop, which is inherently safe for concurrent access since each call atomically finds and removes one document. For bulk efficiency, batch deletes with `deleteMany` are acceptable since deleting an already-deleted document is a no-op.

### Impact on Normal User Operations

**None.** The eviction locks do not affect normal user queries because:

1. **Disjoint row sets**: User queries filter `WHERE deleted_at IS NULL`. Eviction queries filter `WHERE deleted_at IS NOT NULL`. These are completely separate sets of rows with no overlap.

2. **Row-level locking**: PostgreSQL's `FOR UPDATE` acquires row-level locks, not table locks. Only soft-deleted rows (which users never access) are locked.

3. **Index optimization**: The partial indexes (`WHERE deleted_at IS NULL`) ensure user queries never scan soft-deleted rows, so they don't encounter the locks at all.

The only indirect impacts are:
- **I/O contention**: Heavy eviction competes for disk bandwidth, mitigated by batch delays
- **Autovacuum load**: Mass deletes trigger background cleanup, handled automatically by PostgreSQL

### Response Behavior

The eviction endpoint supports two response modes based on the `Accept` header:

#### Default: 204 No Content

```
POST /v1/admin/evict
Accept: application/json
Content-Type: application/json

{"retentionPeriod": "P90D", "resourceTypes": ["conversation_groups"]}
```

Response: `204 No Content`

This is the simplest mode - the request blocks until eviction completes, then returns with no body. This is ideal for cron jobs or scripts that just need to know "it finished."

#### Optional: SSE Progress Stream

```
POST /v1/admin/evict
Accept: text/event-stream
Content-Type: application/json

{"retentionPeriod": "P90D", "resourceTypes": ["conversation_groups"]}
```

Response: `200 OK` with `Content-Type: text/event-stream`

```
data: {"progress": 0}

data: {"progress": 15}

data: {"progress": 33}

data: {"progress": 67}

data: {"progress": 100}

```

The server streams progress updates as Server-Sent Events (SSE). Each event contains a JSON object with a `progress` field (0-100 representing percentage complete).

**Progress calculation:**

Since we don't count cascade-deleted records (to keep the implementation simple and fast), progress is calculated based on the number of batches:

```java
int totalBatches = estimateTotalBatches(cutoff);  // COUNT(*) / batchSize
int completedBatches = 0;

while (true) {
    List<UUID> batch = findEvictableGroupIds(cutoff, batchSize);
    if (batch.isEmpty()) break;
    
    hardDeleteBatch(batch);
    completedBatches++;
    
    int progress = (int) ((completedBatches * 100.0) / totalBatches);
    sseEmitter.send(new ProgressEvent(progress));
}
sseEmitter.send(new ProgressEvent(100));
sseEmitter.complete();
```

**Why not return detailed counts?**

Counting cascade-deleted records (messages, conversations) would require:
1. Extra queries before each batch delete
2. Disabling `ON DELETE CASCADE` and doing manual cascades
3. Significantly slower eviction

Since the primary goal is efficient background cleanup, we prioritize speed over detailed statistics. If detailed counts are needed, admins can query table sizes before/after eviction.

### Vector Store Cleanup

The memory-service supports mixed storage configurations (e.g., PostgreSQL data store with MongoDB vector store). When a conversation group is hard-deleted, the vector store embeddings must also be cleaned up.

**Approach:** Use the background task queue (enhancement 015) for robust, eventually-consistent cleanup.

When hard-deleting conversation groups, create `vector_store_delete` tasks:

```java
@Override
public void hardDeleteConversationGroups(List<UUID> groupIds) {
    if (groupIds.isEmpty()) return;
    
    // 1. Create tasks for vector store cleanup (before delete, so we have the IDs)
    for (UUID groupId : groupIds) {
        taskRepository.createTask(
            "vector_store_delete",
            Map.of("conversationGroupId", groupId.toString())
        );
    }
    
    // 2. Hard delete from data store (cascade handles children)
    entityManager.createNativeQuery(
        "DELETE FROM conversation_groups WHERE id = ANY(:ids)")
        .setParameter("ids", groupIds.toArray(new UUID[0]))
        .executeUpdate();
}
```

**Benefits:**
- **Fault tolerant**: Vector store can be temporarily unavailable without blocking eviction
- **Eventually consistent**: Embeddings are cleaned up when the vector store recovers
- **No data loss**: Tasks persist across restarts

See [015-task-queue.md](015-task-queue.md) for task queue implementation details.

### Authorization

The eviction endpoint requires the `admin` role (not `auditor`). This follows the pattern established in enhancement 014 where write operations require `admin`.

### Audit Logging

All eviction operations are logged via the `AdminAuditLogger`:

```
INFO  [admin.audit] ADMIN_WRITE user=alice action=evict params={retentionPeriod=P90D,resourceTypes=[conversation_groups,conversation_memberships]} justification="Quarterly cleanup"
```

### Configuration

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.eviction.batch-size` | `1000` | Number of records to delete per batch |
| `memory-service.eviction.batch-delay-ms` | `100` | Delay between batches (ms) to reduce load |

```properties
# application.properties
memory-service.eviction.batch-size=1000
memory-service.eviction.batch-delay-ms=100
```

## API Specification

Add to `openapi-admin.yml`:

```yaml
/v1/admin/evict:
  post:
    tags: [Admin]
    summary: Hard-delete soft-deleted resources past retention period
    description: |-
      Permanently removes resources that have been soft-deleted for longer than
      the specified retention period. This operation is irreversible.
      
      The operation runs in batches to minimize database lock contention.
      
      **Response modes (based on Accept header):**
      - `Accept: application/json` (default): Returns 204 No Content when complete.
      - `Accept: text/event-stream`: Streams progress updates as SSE events.
      
      Requires admin role.
    operationId: adminEvict
    requestBody:
      required: true
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/EvictRequest'
    responses:
      '200':
        description: |-
          SSE progress stream (when Accept: text/event-stream).
          Each event contains {"progress": N} where N is 0-100.
        content:
          text/event-stream:
            schema:
              type: string
              description: Server-Sent Events stream with progress updates.
            example: |
              data: {"progress": 0}
              
              data: {"progress": 50}
              
              data: {"progress": 100}
      '204':
        description: Eviction completed successfully (default response).
      '400':
        description: Invalid request (bad retention period format, unknown resource type).
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ErrorResponse'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []

components:
  schemas:
    EvictRequest:
      type: object
      required:
        - retentionPeriod
        - resourceTypes
      properties:
        retentionPeriod:
          type: string
          description: |-
            ISO 8601 duration. Resources soft-deleted longer than this are hard-deleted.
            Examples: P90D (90 days), P1Y (1 year), PT24H (24 hours).
          example: "P90D"
        resourceTypes:
          type: array
          items:
            type: string
            enum:
              - conversation_groups
              - conversation_memberships
          description: Which resource types to evict.
          example: ["conversation_groups"]
        justification:
          type: string
          description: Reason for the eviction (for audit log).
          example: "Quarterly data cleanup per retention policy"

    EvictProgressEvent:
      type: object
      properties:
        progress:
          type: integer
          minimum: 0
          maximum: 100
          description: Percentage of eviction completed (0-100).
          example: 55
```

## Scope of Changes

### 1. OpenAPI Admin Specification

**File:** `memory-service-contracts/src/main/resources/openapi-admin.yml`

Add the `/v1/admin/evict` endpoint and associated schemas.

### 2. Eviction Service

**New file:** `memory-service/src/main/java/io/github/chirino/memory/service/EvictionService.java`

Core eviction logic with batched deletes, concurrent-safe queries, and optional progress callback:

```java
@ApplicationScoped
public class EvictionService {

    @Inject MemoryStore memoryStore;
    
    @ConfigProperty(name = "memory-service.eviction.batch-size", defaultValue = "1000")
    int batchSize;
    
    @ConfigProperty(name = "memory-service.eviction.batch-delay-ms", defaultValue = "100")
    long batchDelayMs;
    
    /**
     * Evict soft-deleted resources older than the cutoff.
     * @param retentionPeriod resources deleted longer than this are hard-deleted
     * @param resourceTypes which resource types to evict
     * @param progressCallback optional callback for progress updates (0-100)
     */
    public void evict(Duration retentionPeriod, Set<String> resourceTypes, 
                      Consumer<Integer> progressCallback) {
        OffsetDateTime cutoff = OffsetDateTime.now().minus(retentionPeriod);
        
        // Estimate total work for progress calculation
        long totalEstimate = estimateTotalRecords(cutoff, resourceTypes);
        long processed = 0;
        
        if (resourceTypes.contains("conversation_groups")) {
            processed = evictConversationGroups(cutoff, processed, totalEstimate, progressCallback);
        }
        
        if (resourceTypes.contains("conversation_memberships")) {
            processed = evictMemberships(cutoff, processed, totalEstimate, progressCallback);
        }
        
        // Final 100% progress
        if (progressCallback != null) {
            progressCallback.accept(100);
        }
    }
    
    private long evictConversationGroups(OffsetDateTime cutoff, long processed, 
                                          long totalEstimate, Consumer<Integer> progressCallback) {
        while (true) {
            List<UUID> batch = memoryStore.findEvictableGroupIds(cutoff, batchSize);
            if (batch.isEmpty()) break;
            
            // Hard delete (creates vector_store_delete tasks, then deletes from data store)
            memoryStore.hardDeleteConversationGroups(batch);
            
            processed += batch.size();
            reportProgress(processed, totalEstimate, progressCallback);
            
            sleepBetweenBatches();
        }
        return processed;
    }
    
    private long evictMemberships(OffsetDateTime cutoff, long processed, 
                                   long totalEstimate, Consumer<Integer> progressCallback) {
        while (true) {
            int deleted = memoryStore.hardDeleteMembershipsBatch(cutoff, batchSize);
            if (deleted == 0) break;
            
            processed += deleted;
            reportProgress(processed, totalEstimate, progressCallback);
            
            sleepBetweenBatches();
        }
        return processed;
    }
    
    private void reportProgress(long processed, long total, Consumer<Integer> callback) {
        if (callback != null && total > 0) {
            int progress = (int) Math.min(99, (processed * 100) / total);
            callback.accept(progress);
        }
    }
    
    private long estimateTotalRecords(OffsetDateTime cutoff, Set<String> resourceTypes) {
        long total = 0;
        if (resourceTypes.contains("conversation_groups")) {
            total += memoryStore.countEvictableGroups(cutoff);
        }
        if (resourceTypes.contains("conversation_memberships")) {
            total += memoryStore.countEvictableMemberships(cutoff);
        }
        return total;
    }
}
```

### 3. Store Interface Extensions

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java`

Add eviction methods:

```java
// Eviction support
List<UUID> findEvictableGroupIds(OffsetDateTime cutoff, int limit);
long countEvictableGroups(OffsetDateTime cutoff);
void hardDeleteConversationGroups(List<UUID> groupIds);
long countEvictableMemberships(OffsetDateTime cutoff);
int hardDeleteMembershipsBatch(OffsetDateTime cutoff, int limit);
```

### 4. PostgresMemoryStore Implementation

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`

**Important:** Use raw SQL (native queries) instead of JPQL/HQL for hard deletes. This ensures:
1. **No N+1 problems** - ORM won't load entities before deleting
2. **Proper cascade handling** - Database's `ON DELETE CASCADE` executes at the SQL level
3. **Single statement execution** - No ORM overhead or entity lifecycle callbacks

```java
@Inject
EntityManager entityManager;

@Inject
TaskRepository taskRepository;

@Override
public List<UUID> findEvictableGroupIds(OffsetDateTime cutoff, int limit) {
    // Use native SQL with FOR UPDATE SKIP LOCKED for concurrent safety
    @SuppressWarnings("unchecked")
    List<UUID> ids = entityManager.createNativeQuery(
        "SELECT id FROM conversation_groups " +
        "WHERE deleted_at IS NOT NULL AND deleted_at < :cutoff " +
        "ORDER BY deleted_at " +
        "LIMIT :limit " +
        "FOR UPDATE SKIP LOCKED")
        .setParameter("cutoff", cutoff)
        .setParameter("limit", limit)
        .getResultList();
    return ids;
}

@Override
public long countEvictableGroups(OffsetDateTime cutoff) {
    return ((Number) entityManager.createNativeQuery(
        "SELECT COUNT(*) FROM conversation_groups " +
        "WHERE deleted_at IS NOT NULL AND deleted_at < :cutoff")
        .setParameter("cutoff", cutoff)
        .getSingleResult()).longValue();
}

@Override
public void hardDeleteConversationGroups(List<UUID> groupIds) {
    if (groupIds.isEmpty()) return;
    
    // 1. Create tasks for vector store cleanup
    for (UUID groupId : groupIds) {
        taskRepository.createTask(
            "vector_store_delete",
            Map.of("conversationGroupId", groupId.toString())
        );
    }
    
    // 2. Single DELETE statement - ON DELETE CASCADE handles all children
    entityManager.createNativeQuery(
        "DELETE FROM conversation_groups WHERE id = ANY(:ids)")
        .setParameter("ids", groupIds.toArray(new UUID[0]))
        .executeUpdate();
}

@Override
public long countEvictableMemberships(OffsetDateTime cutoff) {
    return ((Number) entityManager.createNativeQuery(
        "SELECT COUNT(*) FROM conversation_memberships " +
        "WHERE deleted_at IS NOT NULL AND deleted_at < :cutoff")
        .setParameter("cutoff", cutoff)
        .getSingleResult()).longValue();
}

@Override
public int hardDeleteMembershipsBatch(OffsetDateTime cutoff, int limit) {
    // Single statement: select and delete in one CTE
    // FOR UPDATE SKIP LOCKED ensures concurrent safety
    return entityManager.createNativeQuery(
        "WITH batch AS (" +
        "  SELECT conversation_group_id, user_id FROM conversation_memberships " +
        "  WHERE deleted_at IS NOT NULL AND deleted_at < :cutoff " +
        "  LIMIT :limit " +
        "  FOR UPDATE SKIP LOCKED" +
        ") " +
        "DELETE FROM conversation_memberships m " +
        "USING batch b " +
        "WHERE m.conversation_group_id = b.conversation_group_id " +
        "  AND m.user_id = b.user_id")
        .setParameter("cutoff", cutoff)
        .setParameter("limit", limit)
        .executeUpdate();
}
```

### 5. MongoMemoryStore Implementation

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java`

```java
@Inject
MongoTaskRepository taskRepository;

@Override
public List<String> findEvictableGroupIds(Instant cutoff, int limit) {
    return conversationGroupCollection
        .find(Filters.and(
            Filters.ne("deletedAt", null),
            Filters.lt("deletedAt", cutoff)))
        .limit(limit)
        .map(doc -> doc.getString("_id"))
        .into(new ArrayList<>());
}

@Override
public void hardDeleteConversationGroups(List<String> groupIds) {
    if (groupIds.isEmpty()) return;
    
    // 1. Create tasks for vector store cleanup
    for (String groupId : groupIds) {
        taskRepository.createTask(
            "vector_store_delete",
            Map.of("conversationGroupId", groupId)
        );
    }
    
    // 2. Delete from data store (explicit cascade - MongoDB has no ON DELETE CASCADE)
    Bson filter = Filters.in("conversationGroupId", groupIds);
    Bson groupFilter = Filters.in("_id", groupIds);
    
    // Delete children first, then parents
    messageCollection.deleteMany(filter);
    conversationCollection.deleteMany(filter);
    membershipCollection.deleteMany(filter);
    ownershipTransferCollection.deleteMany(filter);
    conversationGroupCollection.deleteMany(groupFilter);
}
```

**Key differences from PostgreSQL:**
- No `ON DELETE CASCADE` - application must delete children explicitly
- Delete order matters (children before parents) for referential integrity
- Same task queue pattern for vector store cleanup

### 6. Admin Resource

**File:** `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java`

Add eviction endpoint with content negotiation:

```java
@POST
@Path("/evict")
@Consumes(MediaType.APPLICATION_JSON)
public Response evict(
        @HeaderParam("Accept") @DefaultValue(MediaType.APPLICATION_JSON) String accept,
        EvictRequest request) {
    
    roleResolver.requireAdmin(identity, apiKeyContext);
    auditLogger.logWrite("evict", 
        Map.of("retentionPeriod", request.getRetentionPeriod(),
               "resourceTypes", request.getResourceTypes()),
        request.getJustification(), identity, apiKeyContext);
    
    Duration retention = Duration.parse(request.getRetentionPeriod());
    Set<String> resourceTypes = Set.copyOf(request.getResourceTypes());
    
    boolean wantsSSE = accept.contains("text/event-stream");
    
    if (wantsSSE) {
        // Return SSE stream with progress updates
        return Response.ok(new StreamingOutput() {
            @Override
            public void write(OutputStream output) throws IOException {
                try (PrintWriter writer = new PrintWriter(output, true)) {
                    evictionService.evict(retention, resourceTypes, progress -> {
                        writer.println("data: {\"progress\": " + progress + "}");
                        writer.println();
                        writer.flush();
                    });
                }
            }
        }).type("text/event-stream").build();
    } else {
        // Default: simple 204 No Content
        evictionService.evict(retention, resourceTypes, null);
        return Response.noContent().build();
    }
}
```

### 7. Application Configuration

**File:** `memory-service/src/main/resources/application.properties`

```properties
# Eviction configuration
memory-service.eviction.batch-size=1000
memory-service.eviction.batch-delay-ms=100
```

### 8. Cucumber Tests

**New file:** `memory-service/src/test/resources/features/eviction-rest.feature`

```gherkin
Feature: Data Eviction

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Evict conversation groups past retention period (default response)
    Given I have a conversation with title "Old Conversation"
    And set "oldGroupId" to "${conversationGroupId}"
    And the conversation was soft-deleted 100 days ago
    And I have a conversation with title "Recent Conversation"
    And set "recentGroupId" to "${conversationGroupId}"
    And the conversation was soft-deleted 10 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"],
        "justification": "Test cleanup"
      }
      """
    Then the response status should be 204
    # Verify old conversation is gone
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE id = '${oldGroupId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |
    # Verify recent conversation still exists
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE id = '${recentGroupId}'
      """
    Then the SQL result should match:
      | count |
      | 1     |
    # Verify vector store cleanup task was created
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM tasks WHERE task_type = 'vector_store_delete'
      """
    Then the SQL result should match:
      | count |
      | 1     |

  Scenario: Evict with SSE progress stream
    Given I have a conversation with title "To Evict"
    And the conversation was soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" with Accept "text/event-stream" and body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 200
    And the response content type should be "text/event-stream"
    And the SSE stream should contain progress events
    And the final progress should be 100

  Scenario: Concurrent eviction is safe
    Given I have 100 conversations soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" concurrently 3 times with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then all responses should have status 204
    # Verify all conversations were deleted exactly once
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE deleted_at IS NOT NULL
      """
    Then the SQL result should match:
      | count |
      | 0     |

  Scenario: Non-admin user cannot evict
    Given I am authenticated as auditor user "charlie"
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 403

  Scenario: Invalid retention period format rejected
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "90 days",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 400

  Scenario: Unknown resource type rejected
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["messages"]
      }
      """
    Then the response status should be 400

  Scenario: Cascade deletes child records
    Given I have a conversation with title "Parent Conversation"
    And set "groupId" to "${conversationGroupId}"
    And the conversation has messages
    And the conversation was soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 204
    # Verify messages were cascade deleted
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM messages WHERE conversation_group_id = '${groupId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |
```

## Implementation Order

1. **Implement enhancement 015 (Task Queue)** - Required dependency
2. **OpenAPI spec** - Add `/v1/admin/evict` endpoint to `openapi-admin.yml`
3. **Store interface** - Add eviction methods to `MemoryStore`
4. **PostgresMemoryStore** - Implement eviction methods with `FOR UPDATE SKIP LOCKED`
5. **MongoMemoryStore** - Implement eviction methods (with explicit cascade)
6. **EvictionService** - Core batched eviction logic with progress callback
7. **AdminResource** - Eviction endpoint with SSE support
8. **Cucumber tests** - Eviction scenarios including concurrency
9. **Configuration** - Add eviction properties
10. **Compile and test**

## Verification

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# Run eviction-specific tests
./mvnw test -Dcucumber.filter.tags="@eviction"
```

## Files to Modify (Complete List)

| File | Change Type |
|------|-------------|
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Add evict endpoint |
| `memory-service/src/main/java/io/github/chirino/memory/service/EvictionService.java` | New file |
| `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java` | Add eviction methods |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java` | Implement eviction |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java` | Implement eviction |
| `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java` | Add evict endpoint |
| `memory-service/src/main/java/io/github/chirino/memory/api/dto/EvictRequest.java` | New file |
| `memory-service/src/main/resources/application.properties` | Add eviction config |
| `memory-service/src/test/resources/features/eviction-rest.feature` | New file |
| `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java` | Add eviction steps |

## Assumptions

1. **Soft deletes are already implemented** per enhancement 013. All evictable resources have `deletedAt IS NOT NULL`.

2. **Admin APIs are already implemented** per enhancement 014. The eviction endpoint follows the same authorization and audit patterns.

3. **Task queue is implemented** per enhancement 015. Vector store cleanup tasks are processed asynchronously.

4. **Cascade behavior is reliable.** PostgreSQL's `ON DELETE CASCADE` will correctly remove all child records. MongoDB requires explicit cascades in the application.

5. **Vector store cleanup is eventually consistent.** Tasks are queued for vector store deletion; cleanup happens asynchronously via the task processor. The vector store can be temporarily unavailable without blocking eviction.

6. **The retention period is absolute from `deletedAt`.** There is no "soft" retention that considers last access time.

7. **Eviction is synchronous.** The endpoint blocks until data store eviction completes. Vector store cleanup happens asynchronously via tasks.

8. **Messages are never soft-deleted independently.** They only inherit deleted status from their parent conversation and are cascade-deleted when the group is hard-deleted.

9. **Ownership transfers are cascade-deleted with the group.** They are not independently evictable.

10. **Concurrent eviction calls are safe.** The `FOR UPDATE SKIP LOCKED` pattern ensures no duplicate deletes or deadlocks when multiple eviction processes run simultaneously.

## Open Questions for Discussion

### Q1: What happens to active user sessions referencing evicted conversations?

When a conversation is hard-deleted while a user has it open in the UI:

- Next API call returns 404
- WebSocket subscriptions receive a "deleted" event (if implemented)

**Recommendation:** Return 404 as with any deleted resource. WebSocket events are out of scope.
