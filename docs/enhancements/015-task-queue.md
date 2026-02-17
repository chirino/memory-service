---
status: implemented
---

# Background Task Queue

> **Status**: Implemented.

## Motivation

The memory-service needs to perform asynchronous operations that may fail temporarily and require retry logic. Examples include:

1. **Vector store cleanup** after conversation eviction (enhancement 015)
2. **Future use cases**: async notifications, external service calls, batch processing

A robust task queue provides:
- **Fault tolerance**: Operations can fail and retry without blocking the main request
- **Eventually consistent cleanup**: External services (like vector stores) can be temporarily unavailable
- **Observability**: Failed tasks accumulate with error messages for debugging
- **Replica safety**: Multiple service replicas can process tasks concurrently

## Design Decisions

### Task Table Schema (PostgreSQL)

```sql
CREATE TABLE tasks (
    id              UUID PRIMARY KEY,
    task_name       TEXT UNIQUE,  -- Optional unique name for singleton tasks
    task_type       TEXT NOT NULL,
    task_body       JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    retry_count     INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_tasks_ready ON tasks (task_type, retry_at);
CREATE UNIQUE INDEX idx_tasks_name ON tasks (task_name) WHERE task_name IS NOT NULL;
```

| Column | Type | Description |
|--------|------|-------------|
| `id` | UUID | Primary key |
| `task_name` | TEXT | Optional unique name for singleton/idempotent tasks |
| `task_type` | TEXT | Type identifier (e.g., `vector_store_delete`) |
| `task_body` | JSONB | Task-specific parameters |
| `created_at` | TIMESTAMPTZ | When the task was created |
| `retry_at` | TIMESTAMPTZ | When the task is eligible for processing |
| `last_error` | TEXT | Error message from last failed attempt |
| `retry_count` | INT | Number of failed attempts |

The `task_name` column enables **singleton tasks** - tasks where only one instance should exist. When creating a task with a name, if a task with that name already exists, the creation is a no-op (idempotent).

The index on `(task_type, retry_at)` optimizes finding ready tasks. Note: PostgreSQL doesn't allow `NOW()` in partial index predicates (functions must be IMMUTABLE), so the query filters by `retry_at <= NOW()` at query time rather than in the index predicate.

### Task Types

| Task Type | Task Name | Task Body | Description |
|-----------|-----------|-----------|-------------|
| `vector_store_delete` | (none) | `{"conversationGroupId": "uuid"}` | Delete embeddings for a conversation group |
| `vector_store_index_retry` | `vector_store_index_retry` | `{}` | Retry indexing entries where vector store failed |

**Singleton Tasks**: Tasks with a `task_name` are singleton tasks. The `vector_store_index_retry` task is a singleton - only one instance exists regardless of how many indexing failures occur. When the task runs, it queries for all entries needing retry (`indexedContent IS NOT NULL AND indexedAt IS NULL`) and processes them in batch.

New task types can be added by:
1. Defining the task type string, optional task name, and body schema
2. Adding a handler in the `TaskProcessor`

### Task Lifecycle

```
┌─────────┐     ┌───────────┐     ┌─────────┐
│ Created │────▶│ Processing│────▶│ Deleted │  (success)
└─────────┘     └───────────┘     └─────────┘
                     │
                     ▼
               ┌───────────┐
               │  Retry    │  (failure - retry_at set to future)
               │ Scheduled │
               └───────────┘
                     │
                     ▼
               (back to Processing when retry_at reached)
```

1. **Created**: Task inserted with `retry_at = NOW()`
2. **Processing**: Task picked up by a processor replica
3. **Success**: Task deleted from table
4. **Failure**: `retry_at` set to future time, `last_error` and `retry_count` updated

### Concurrent Replica Safety (PostgreSQL)

Multiple memory-service replicas can safely run the task processor concurrently using `FOR UPDATE SKIP LOCKED`:

```sql
SELECT * FROM tasks 
WHERE retry_at <= NOW() 
ORDER BY retry_at 
LIMIT 100 
FOR UPDATE SKIP LOCKED;
```

**How this works:**
- Each batch query locks the rows it selects
- Concurrent queries skip already-locked rows
- No duplicate processing, no deadlocks
- Work is naturally distributed across replicas

```
Replica A: SELECT ... FOR UPDATE SKIP LOCKED → gets tasks 1, 2, 3
Replica B: SELECT ... FOR UPDATE SKIP LOCKED → gets tasks 4, 5, 6 (skips 1, 2, 3)
Replica C: SELECT ... FOR UPDATE SKIP LOCKED → gets tasks 7, 8, 9 (skips 1-6)
```

The lock is held for the duration of the transaction. If a replica crashes mid-processing, PostgreSQL automatically releases the lock and the task becomes available for another replica.

### MongoDB Task Collection Schema

```javascript
// tasks collection
{
  "_id": "uuid-string",
  "taskName": null,  // Optional unique name for singleton tasks
  "taskType": "vector_store_delete",
  "taskBody": { "conversationGroupId": "uuid-string" },
  "createdAt": ISODate("2026-01-27T10:00:00Z"),
  "retryAt": ISODate("2026-01-27T10:00:00Z"),
  "processingAt": null,  // Set when a replica claims the task
  "lastError": null,
  "retryCount": 0
}

// Index for finding ready tasks
db.tasks.createIndex({ "retryAt": 1, "processingAt": 1 })
// Unique index for singleton tasks (sparse to allow null)
db.tasks.createIndex({ "taskName": 1 }, { unique: true, sparse: true })
```

The `processingAt` field is additional for MongoDB (not needed in PostgreSQL) to handle concurrent safety.
The `taskName` field enables singleton tasks with idempotent creation.

### Concurrent Replica Safety (MongoDB)

MongoDB doesn't have `FOR UPDATE SKIP LOCKED`, so we use `findOneAndUpdate` to atomically claim tasks:

```javascript
db.tasks.findOneAndUpdate(
  {
    retryAt: { $lte: now },
    $or: [
      { processingAt: null },
      { processingAt: { $lt: staleClaimCutoff } }  // 5 minute timeout
    ]
  },
  { $set: { processingAt: now } },
  { sort: { retryAt: 1 }, returnDocument: "after" }
)
```

**How this works:**
1. **Atomic claim**: `findOneAndUpdate` atomically finds a ready task and sets `processingAt = now`
2. **No duplicate processing**: Only one replica can claim each task
3. **Stale claim recovery**: If a replica crashes, its claimed tasks become available after 5 minutes

```
Replica A: findOneAndUpdate → claims task 1, sets processingAt
Replica B: findOneAndUpdate → claims task 2 (task 1 excluded by processingAt filter)
Replica C: findOneAndUpdate → claims task 3
```

The `processingAt` field serves as a distributed lock with automatic expiry.

### Configuration

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.tasks.retry-delay` | `PT10M` | Delay before retrying failed tasks (ISO 8601 duration) |
| `memory-service.tasks.processor-interval` | `1m` | How often to check for pending tasks |
| `memory-service.tasks.batch-size` | `100` | Max tasks to process per run |
| `memory-service.tasks.stale-claim-timeout` | `PT5M` | MongoDB only: how long before a claimed task is considered abandoned |

## Scope of Changes

### 1. Task Table Schema (PostgreSQL)

**File:** `memory-service/src/main/resources/db/schema.sql`

Add to the initial schema (DB will be reset):

```sql
------------------------------------------------------------
-- Background task queue
------------------------------------------------------------

CREATE TABLE IF NOT EXISTS tasks (
    id              UUID PRIMARY KEY,
    task_type       TEXT NOT NULL,
    task_body       JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    retry_count     INT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_tasks_ready 
    ON tasks (task_type, retry_at);
```

### 2. Task Entity (PostgreSQL)

**New file:** `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/TaskEntity.java`

```java
package io.github.chirino.memory.persistence.entity;

import jakarta.persistence.*;
import org.hibernate.annotations.JdbcTypeCode;
import org.hibernate.type.SqlTypes;

import java.time.OffsetDateTime;
import java.util.Map;
import java.util.UUID;

@Entity
@Table(name = "tasks")
public class TaskEntity {

    @Id
    @Column(name = "id", nullable = false, updatable = false)
    private UUID id;

    @Column(name = "task_name", unique = true)
    private String taskName;

    @Column(name = "task_type", nullable = false)
    private String taskType;

    @JdbcTypeCode(SqlTypes.JSON)
    @Column(name = "task_body", nullable = false, columnDefinition = "jsonb")
    private Map<String, Object> taskBody;

    @Column(name = "created_at", nullable = false)
    private OffsetDateTime createdAt;

    @Column(name = "retry_at", nullable = false)
    private OffsetDateTime retryAt;

    @Column(name = "last_error")
    private String lastError;

    @Column(name = "retry_count", nullable = false)
    private int retryCount = 0;

    @PrePersist
    public void prePersist() {
        if (id == null) {
            id = UUID.randomUUID();
        }
        if (createdAt == null) {
            createdAt = OffsetDateTime.now();
        }
        if (retryAt == null) {
            retryAt = OffsetDateTime.now();
        }
    }

    // Getters and setters
    public UUID getId() { return id; }
    public void setId(UUID id) { this.id = id; }

    public String getTaskName() { return taskName; }
    public void setTaskName(String taskName) { this.taskName = taskName; }

    public String getTaskType() { return taskType; }
    public void setTaskType(String taskType) { this.taskType = taskType; }

    public Map<String, Object> getTaskBody() { return taskBody; }
    public void setTaskBody(Map<String, Object> taskBody) { this.taskBody = taskBody; }

    public OffsetDateTime getCreatedAt() { return createdAt; }
    public void setCreatedAt(OffsetDateTime createdAt) { this.createdAt = createdAt; }

    public OffsetDateTime getRetryAt() { return retryAt; }
    public void setRetryAt(OffsetDateTime retryAt) { this.retryAt = retryAt; }

    public String getLastError() { return lastError; }
    public void setLastError(String lastError) { this.lastError = lastError; }

    public int getRetryCount() { return retryCount; }
    public void setRetryCount(int retryCount) { this.retryCount = retryCount; }
}
```

### 4. Task Repository (PostgreSQL)

**New file:** `memory-service/src/main/java/io/github/chirino/memory/persistence/repo/TaskRepository.java`

```java
package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.TaskEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import java.time.OffsetDateTime;
import java.util.List;
import java.util.Map;
import java.util.UUID;

@ApplicationScoped
public class TaskRepository implements PanacheRepositoryBase<TaskEntity, UUID> {

    @Inject
    EntityManager entityManager;

    /**
     * Find and lock ready tasks using FOR UPDATE SKIP LOCKED.
     * Safe for concurrent execution across multiple replicas.
     */
    @SuppressWarnings("unchecked")
    public List<TaskEntity> findReadyTasks(int limit) {
        return entityManager.createNativeQuery(
            "SELECT * FROM tasks " +
            "WHERE retry_at <= NOW() " +
            "ORDER BY retry_at " +
            "LIMIT :limit " +
            "FOR UPDATE SKIP LOCKED",
            TaskEntity.class)
            .setParameter("limit", limit)
            .getResultList();
    }

    /**
     * Create a new task for background processing.
     */
    public void createTask(String taskType, Map<String, Object> body) {
        createTask(null, taskType, body);
    }

    /**
     * Create a named task (singleton/idempotent).
     * If a task with the given name already exists, this is a no-op.
     */
    public void createTask(String taskName, String taskType, Map<String, Object> body) {
        if (taskName != null && findByName(taskName) != null) {
            return; // Task already exists, idempotent no-op
        }
        TaskEntity task = new TaskEntity();
        task.setId(UUID.randomUUID());
        task.setTaskName(taskName);
        task.setTaskType(taskType);
        task.setTaskBody(body);
        task.setCreatedAt(OffsetDateTime.now());
        task.setRetryAt(OffsetDateTime.now());
        persist(task);
    }

    /**
     * Find a task by its unique name.
     */
    public TaskEntity findByName(String taskName) {
        return find("taskName", taskName).firstResult();
    }

    /**
     * Mark a task as failed and schedule retry.
     */
    public void markFailed(TaskEntity task, String error, java.time.Duration retryDelay) {
        task.setLastError(error);
        task.setRetryCount(task.getRetryCount() + 1);
        task.setRetryAt(OffsetDateTime.now().plus(retryDelay));
        persist(task);
    }
}
```

### 5. MongoDB Task Repository

**New file:** `memory-service/src/main/java/io/github/chirino/memory/mongo/repo/MongoTaskRepository.java`

```java
package io.github.chirino.memory.mongo.repo;

import com.mongodb.client.MongoClient;
import com.mongodb.client.MongoCollection;
import com.mongodb.client.model.*;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.bson.Document;
import org.bson.conversions.Bson;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.time.Duration;
import java.time.Instant;
import java.util.*;

@ApplicationScoped
public class MongoTaskRepository {

    @Inject
    MongoClient mongoClient;

    @ConfigProperty(name = "memory-service.tasks.stale-claim-timeout", defaultValue = "PT5M")
    Duration staleClaimTimeout;

    private MongoCollection<Document> getCollection() {
        return mongoClient.getDatabase("memory").getCollection("tasks");
    }

    /**
     * Atomically find and claim a ready task using findOneAndUpdate.
     * Safe for concurrent execution across multiple replicas.
     *
     * Each call claims ONE task by setting processingAt to now.
     * Returns null if no tasks are ready.
     */
    public Document claimNextTask() {
        Instant now = Instant.now();
        Instant staleClaimCutoff = now.minus(staleClaimTimeout);

        return getCollection().findOneAndUpdate(
            Filters.and(
                Filters.lte("retryAt", now),
                Filters.or(
                    Filters.eq("processingAt", null),
                    Filters.lt("processingAt", staleClaimCutoff)
                )
            ),
            Updates.set("processingAt", now),
            new FindOneAndUpdateOptions()
                .sort(Sorts.ascending("retryAt"))
                .returnDocument(ReturnDocument.AFTER)
        );
    }

    /**
     * Find multiple ready tasks by claiming them one at a time.
     */
    public List<Document> findReadyTasks(int limit) {
        List<Document> tasks = new ArrayList<>();
        for (int i = 0; i < limit; i++) {
            Document task = claimNextTask();
            if (task == null) break;
            tasks.add(task);
        }
        return tasks;
    }

    /**
     * Create a new task for background processing.
     */
    public void createTask(String taskType, Map<String, Object> body) {
        createTask(null, taskType, body);
    }

    /**
     * Create a named task (singleton/idempotent).
     * If a task with the given name already exists, this is a no-op.
     */
    public void createTask(String taskName, String taskType, Map<String, Object> body) {
        if (taskName != null && findByName(taskName) != null) {
            return; // Task already exists, idempotent no-op
        }
        Document task = new Document()
            .append("_id", UUID.randomUUID().toString())
            .append("taskName", taskName)
            .append("taskType", taskType)
            .append("taskBody", new Document(body))
            .append("createdAt", Instant.now())
            .append("retryAt", Instant.now())
            .append("processingAt", null)
            .append("lastError", null)
            .append("retryCount", 0);
        getCollection().insertOne(task);
    }

    /**
     * Find a task by its unique name.
     */
    public Document findByName(String taskName) {
        return getCollection().find(Filters.eq("taskName", taskName)).first();
    }

    /**
     * Delete a completed task.
     */
    public void deleteTask(String taskId) {
        getCollection().deleteOne(Filters.eq("_id", taskId));
    }

    /**
     * Mark a task as failed and schedule retry.
     */
    public void markFailed(String taskId, String error, Duration retryDelay) {
        getCollection().updateOne(
            Filters.eq("_id", taskId),
            Updates.combine(
                Updates.set("lastError", error),
                Updates.inc("retryCount", 1),
                Updates.set("retryAt", Instant.now().plus(retryDelay)),
                Updates.set("processingAt", null)  // Release claim
            )
        );
    }
}
```

### 6. Task Processor

**New file:** `memory-service/src/main/java/io/github/chirino/memory/service/TaskProcessor.java`

```java
package io.github.chirino.memory.service;

import io.github.chirino.memory.persistence.entity.TaskEntity;
import io.github.chirino.memory.persistence.repo.TaskRepository;
import io.github.chirino.memory.vector.VectorStore;
import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import java.time.Duration;
import java.util.List;

@ApplicationScoped
public class TaskProcessor {

    private static final Logger LOG = Logger.getLogger(TaskProcessor.class);

    @Inject
    TaskRepository taskRepository;

    @Inject
    VectorStore vectorStore;

    @ConfigProperty(name = "memory-service.tasks.retry-delay", defaultValue = "PT10M")
    Duration retryDelay;

    @ConfigProperty(name = "memory-service.tasks.batch-size", defaultValue = "100")
    int batchSize;

    @Scheduled(every = "${memory-service.tasks.processor-interval:1m}")
    @Transactional
    void processPendingTasks() {
        List<TaskEntity> tasks = taskRepository.findReadyTasks(batchSize);

        for (TaskEntity task : tasks) {
            try {
                executeTask(task);
                taskRepository.delete(task);
                LOG.debugf("Task %s completed successfully", task.getId());
            } catch (Exception e) {
                LOG.warnf(e, "Task %s failed, scheduling retry", task.getId());
                taskRepository.markFailed(task, e.getMessage(), retryDelay);
            }
        }

        if (!tasks.isEmpty()) {
            LOG.infof("Processed %d tasks", tasks.size());
        }
    }

    @Inject
    MemoryStore memoryStore;

    private void executeTask(TaskEntity task) {
        switch (task.getTaskType()) {
            case "vector_store_delete" -> {
                String groupId = (String) task.getTaskBody().get("conversationGroupId");
                vectorStore.deleteByConversationGroupId(groupId);
            }
            case "vector_store_index_retry" -> {
                // Query entries where indexedContent IS NOT NULL AND indexedAt IS NULL
                // These are entries that have index content but failed vector store indexing
                List<Entry> pendingEntries = memoryStore.findEntriesPendingVectorIndexing(batchSize);
                boolean allSucceeded = true;
                for (Entry entry : pendingEntries) {
                    try {
                        vectorStore.index(entry.getConversationId(), entry.getId(), entry.getIndexedContent());
                        memoryStore.setIndexedAt(entry.getId(), OffsetDateTime.now());
                    } catch (Exception e) {
                        LOG.warnf(e, "Failed to index entry %s in vector store", entry.getId());
                        allSucceeded = false;
                    }
                }
                if (!allSucceeded || pendingEntries.size() == batchSize) {
                    // More entries to process or some failed - reschedule task
                    throw new RuntimeException("Retry needed - some entries pending or failed");
                }
            }
            default -> throw new IllegalArgumentException("Unknown task type: " + task.getTaskType());
        }
    }
}
```

### 7. Application Configuration

**File:** `memory-service/src/main/resources/application.properties`

```properties
# Task processor configuration
memory-service.tasks.retry-delay=PT10M
memory-service.tasks.processor-interval=1m
memory-service.tasks.batch-size=100
memory-service.tasks.stale-claim-timeout=PT5M
```

### 8. Cucumber Tests

**New file:** `memory-service/src/test/resources/features/task-queue.feature`

```gherkin
Feature: Background Task Queue

  Scenario: Task is created and processed successfully
    Given I create a task with type "vector_store_delete" and body:
      """
      {"conversationGroupId": "test-group-123"}
      """
    When the task processor runs
    Then the task should be deleted
    And the vector store should have received a delete call for "test-group-123"

  Scenario: Failed task is scheduled for retry
    Given I create a task with type "vector_store_delete" and body:
      """
      {"conversationGroupId": "failing-group"}
      """
    And the vector store will fail for "failing-group"
    When the task processor runs
    Then the task should still exist
    And the task retry_at should be in the future
    And the task last_error should contain the failure message
    And the task retry_count should be 1

  Scenario: Task is retried after retry delay
    Given I have a failed task with retry_at in the past
    When the task processor runs
    Then the task should be processed again

  Scenario: Multiple replicas process tasks concurrently without duplicates
    Given I have 100 pending tasks
    When 3 task processors run concurrently
    Then each task should be processed exactly once
```

## Implementation Order

1. **Task table schema** - Add `tasks` table to `schema.sql`
2. **Task entity** - `TaskEntity` JPA entity
3. **PostgreSQL repository** - `TaskRepository` with `FOR UPDATE SKIP LOCKED`
4. **MongoDB repository** - `MongoTaskRepository` with `findOneAndUpdate`
5. **Task processor** - Scheduled job with handler dispatch
6. **Configuration** - Add task processor properties
7. **Cucumber tests** - Task queue scenarios
8. **Compile and test**

## Verification

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# Run task-queue-specific tests
./mvnw test -Dcucumber.filter.tags="@task-queue"
```

## Files to Modify (Complete List)

| File | Change Type |
|------|-------------|
| `memory-service/src/main/resources/db/schema.sql` | Add tasks table |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/TaskEntity.java` | New file |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/repo/TaskRepository.java` | New file |
| `memory-service/src/main/java/io/github/chirino/memory/mongo/repo/MongoTaskRepository.java` | New file |
| `memory-service/src/main/java/io/github/chirino/memory/service/TaskProcessor.java` | New file |
| `memory-service/src/main/resources/application.properties` | Add task config |
| `memory-service/src/test/resources/features/task-queue.feature` | New file |

## Assumptions

1. **PostgreSQL and MongoDB are the supported data stores.** The task queue implementation provides separate implementations for each.

2. **Tasks are idempotent or safe to retry.** If a task partially completes before failure, re-execution should be safe.

3. **Task processing order is not guaranteed.** Tasks are processed approximately in `retry_at` order, but concurrent replicas may process out of order.

4. **Failed tasks retry indefinitely.** There is no max retry limit. A future enhancement could add dead-letter handling.

5. **Task body is JSON-serializable.** Complex objects should be serialized to JSON-compatible maps.

## Future Considerations

- **Dead-letter queue**: Move tasks to a separate table after N failures
- **Task priorities**: Add a priority field for urgent tasks
- **Task expiration**: Auto-delete tasks older than a threshold
- **Admin API**: Endpoints to list, retry, or cancel pending tasks
- **Metrics**: Expose task queue depth and processing rate
