---
status: proposed
---

# Enhancement 059: Entries Table Partitioning

> **Status**: Proposed.

## Summary

Investigate and implement table partitioning for the `entries` table in PostgreSQL to improve query performance and enable more efficient data lifecycle management at scale.

## Motivation

The `entries` table is the largest and most frequently queried table in the memory service. Every conversation sync, history retrieval, search operation, and fork traversal touches this table.

### Current Schema

```sql
CREATE TABLE entries (
    id                UUID PRIMARY KEY,
    conversation_id   UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    user_id           TEXT,
    client_id         TEXT,
    channel           TEXT NOT NULL,       -- 'history' or 'memory'
    epoch             BIGINT,
    content_type      TEXT NOT NULL,
    content           BYTEA NOT NULL,      -- encrypted, potentially large
    indexed_content   TEXT,
    indexed_at        TIMESTAMPTZ,
    indexed_content_tsv tsvector GENERATED ALWAYS AS (to_tsvector('english', COALESCE(indexed_content, ''))) STORED,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Current Indexes (7)

```sql
idx_entries_conversation_channel_client_epoch_created_at  -- Primary memory operations
idx_entries_conversation_created_at                        -- History channel queries
idx_entries_group_created_at                               -- Fork-aware retrieval
idx_entries_unindexed                                      -- Batch indexing
idx_entries_pending_vector_indexing                         -- Vector indexing queue
idx_entries_indexed_content_fts                             -- GIN full-text search
```

### Growth Characteristics

- Every conversation sync appends at least one entry (since Enhancement 026 consolidated N messages → 1 entry).
- History channel entries accumulate over a conversation's lifetime (one per user/AI turn).
- Memory channel entries grow with each sync cycle (though old epochs become unreachable).
- The `content` column stores encrypted BYTEA, which can be large for conversations with many messages or attachments.
- Soft-deleted conversations retain their entries until eviction runs.

### Performance Concerns

1. **Index bloat**: As the table grows, all 7 indexes grow proportionally. Index maintenance (inserts, vacuuming) becomes more expensive.
2. **Full-text search**: The GIN index on `indexed_content_tsv` degrades with table size since GIN indexes don't support partial scans efficiently.
3. **Eviction**: Batch deletion of soft-deleted entries requires scanning and vacuuming large table segments.
4. **Sequential scans**: Queries that can't use indexes (e.g., complex fork tree traversals) become slower.

## Design

### Partitioning Strategies Evaluated

#### Option A: Partition by `conversation_group_id` (Hash)

```sql
CREATE TABLE entries (
    ...
) PARTITION BY HASH (conversation_group_id);

CREATE TABLE entries_p0 PARTITION OF entries FOR VALUES WITH (MODULUS 16, REMAINDER 0);
CREATE TABLE entries_p1 PARTITION OF entries FOR VALUES WITH (MODULUS 16, REMAINDER 1);
-- ... entries_p2 through entries_p15
```

**Pros**:
- Aligns with the most common query pattern: all entry queries filter by `conversation_id` or `conversation_group_id`.
- Queries for a single conversation only scan one partition.
- Even data distribution (hash-based).
- Fork-aware queries (`conversation_group_id`) also benefit since all forks share the same group.

**Cons**:
- Cannot easily drop partitions (hash partitions are permanent).
- Cross-conversation queries (search, admin) must scan all partitions.
- Partition count must be chosen upfront (can be altered but requires data migration).

#### Option B: Partition by `created_at` (Range)

```sql
CREATE TABLE entries (
    ...
) PARTITION BY RANGE (created_at);

CREATE TABLE entries_2025_q1 PARTITION OF entries
    FOR VALUES FROM ('2025-01-01') TO ('2025-04-01');
CREATE TABLE entries_2025_q2 PARTITION OF entries
    FOR VALUES FROM ('2025-04-01') TO ('2025-07-01');
```

**Pros**:
- Enables efficient time-based eviction: drop entire partitions instead of row-by-row deletion.
- Recent data (hot partition) stays in cache; old data (cold partitions) can be on slower storage.
- Automatic partition creation can be scripted with `pg_partman` or a scheduled task.

**Cons**:
- Most queries filter by `conversation_id`, not `created_at` — so partition pruning doesn't help the hot path.
- A long-running conversation has entries spread across many partitions.
- Requires adding `created_at` to the primary key (PostgreSQL requirement for partition keys).

#### Option C: Partition by `conversation_group_id` (List, Dynamic)

```sql
CREATE TABLE entries (
    ...
) PARTITION BY LIST (conversation_group_id);

-- Created dynamically per conversation group
CREATE TABLE entries_group_<uuid> PARTITION OF entries
    FOR VALUES IN ('<uuid>');
```

**Pros**:
- Perfect partition pruning for all conversation-scoped queries.
- Dropping a conversation group = dropping one partition (instant eviction).

**Cons**:
- Unbounded partition count (one per conversation group). PostgreSQL performance degrades beyond ~1000 partitions.
- Requires dynamic DDL on conversation creation.
- Not practical at scale.

### Recommended Approach: Option A (Hash by `conversation_group_id`)

Hash partitioning by `conversation_group_id` best aligns with the query patterns:

1. **Memory sync** (`listMemoryEntriesAtLatestEpoch`): Filters by `conversation_id` → single partition.
2. **History listing** (`listByChannel`): Filters by `conversation_id` → single partition.
3. **Fork-aware retrieval** (`listByConversationGroup`): Filters by `conversation_group_id` → single partition.
4. **Search**: Must scan all partitions, but search is already the slowest operation and benefits from vector/GIN indexes within each partition.
5. **Eviction**: Must scan all partitions, but eviction is a background task.

### Schema Changes

#### 1. Primary Key Must Include Partition Key

PostgreSQL requires the partition key to be part of the primary key:

```sql
-- BEFORE
CREATE TABLE entries (
    id UUID PRIMARY KEY,
    ...
);

-- AFTER
CREATE TABLE entries (
    id UUID NOT NULL,
    conversation_group_id UUID NOT NULL,
    ...
    PRIMARY KEY (id, conversation_group_id)
) PARTITION BY HASH (conversation_group_id);
```

This changes the uniqueness constraint from `(id)` to `(id, conversation_group_id)`. Since UUIDs are globally unique by design, this is safe.

#### 2. Foreign Keys

PostgreSQL does not support foreign keys referencing partitioned tables (as of PG 17). The `entry_id` FK from `attachments` and `entry_embeddings` must become an application-level constraint or be dropped:

```sql
-- BEFORE
CREATE TABLE attachments (
    entry_id UUID REFERENCES entries(id) ON DELETE CASCADE,
    ...
);

-- AFTER: No FK, application-level enforcement + cascade via tasks
CREATE TABLE attachments (
    entry_id UUID,  -- No FK constraint
    ...
);
```

The cascade delete behavior must be handled by the application (already partially the case since soft deletes go through the task queue for eviction).

#### 3. Partition Creation

Create 16 hash partitions (a reasonable starting point):

```sql
-- Migration script
DO $$
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format(
            'CREATE TABLE entries_p%s PARTITION OF entries FOR VALUES WITH (MODULUS 16, REMAINDER %s)',
            i, i
        );
    END LOOP;
END $$;
```

#### 4. Index Changes

Indexes are created per-partition automatically in PostgreSQL. The existing index definitions work as-is since PostgreSQL creates them on each partition:

```sql
-- These are automatically created on each partition
CREATE INDEX idx_entries_conversation_channel_... ON entries (...);
```

### JPA/Hibernate Changes

The `EntryEntity` class needs the composite primary key:

```java
// BEFORE
@Id
@Column(name = "id")
private UUID id;

// AFTER: Composite PK
@Id
@Column(name = "id")
private UUID id;

// conversation_group_id is already mapped, just ensure it's part of @IdClass or @EmbeddedId
```

Alternatively, since the `id` UUID is globally unique, keep it as the logical `@Id` and handle the composite PK at the DDL level only (Hibernate doesn't need to know about the partition key in the PK).

### Migration Strategy

Since the project is pre-release, the migration can simply recreate the table:

```sql
-- 1. Rename existing table
ALTER TABLE entries RENAME TO entries_old;

-- 2. Create partitioned table
CREATE TABLE entries (...) PARTITION BY HASH (conversation_group_id);

-- 3. Create partitions
DO $$ ... $$;

-- 4. Copy data
INSERT INTO entries SELECT * FROM entries_old;

-- 5. Drop old table
DROP TABLE entries_old CASCADE;

-- 6. Recreate indexes and constraints
...
```

## Testing

### Performance Benchmarks

Before and after measurements for key queries:

```sql
-- Memory sync (hot path)
EXPLAIN ANALYZE
SELECT * FROM entries
WHERE conversation_id = $1
  AND channel = 'memory'
  AND client_id = $2
  AND epoch = (SELECT MAX(epoch) FROM entries WHERE conversation_id = $1 AND channel = 'memory' AND client_id = $2);

-- History listing
EXPLAIN ANALYZE
SELECT * FROM entries
WHERE conversation_id = $1 AND channel = 'history'
ORDER BY created_at ASC
LIMIT 50;

-- Full-text search
EXPLAIN ANALYZE
SELECT * FROM entries
WHERE indexed_content_tsv @@ plainto_tsquery('english', $1)
ORDER BY ts_rank(indexed_content_tsv, plainto_tsquery('english', $1)) DESC
LIMIT 20;
```

### Functional Tests

All existing Cucumber tests must pass without modification — partitioning is transparent to the application.

```bash
# Run full test suite
./mvnw test -pl memory-service > test.log 2>&1
```

### Partition Verification

```sql
-- Verify partition pruning
EXPLAIN SELECT * FROM entries WHERE conversation_group_id = 'some-uuid';
-- Should show: "Append" with only one partition scanned
```

## Files to Modify

| File | Change |
|------|--------|
| `memory-service/src/main/resources/db/schema.sql` | Add `PARTITION BY HASH`, composite PK, partition creation |
| `memory-service/src/main/resources/db/changelog/` | **New**: Liquibase migration changeset |
| `memory-service/.../persistence/entity/EntryEntity.java` | Adjust PK mapping if needed |
| `memory-service/.../persistence/entity/AttachmentEntity.java` | Remove FK constraint annotation to entries |
| `memory-service/.../persistence/entity/EntryEmbeddingsEntity.java` | Remove FK constraint annotation to entries |
| `memory-service/src/main/resources/db/pgvector-schema.sql` | Update `entry_embeddings` FK |

## Verification

```bash
# Compile
./mvnw compile

# Run all tests
./mvnw test -pl memory-service > test.log 2>&1

# Verify partitioning in dev mode
./mvnw quarkus:dev -pl memory-service
# Then in psql:
# \d+ entries
# SELECT tableoid::regclass, count(*) FROM entries GROUP BY tableoid;
```

## Design Decisions

1. **Hash over range**: The dominant query pattern is by `conversation_id`/`conversation_group_id`, not by time. Hash partitioning gives partition pruning on the hot path. Time-range partitioning helps eviction but hurts the hot path.
2. **16 partitions**: A moderate starting point. Can be increased by re-partitioning (data migration required). Too few partitions give little benefit; too many add planning overhead.
3. **Application-level cascade**: Losing FK constraints is the main trade-off. Since the service already uses soft deletes and background eviction tasks, the cascade is effectively application-managed already.
4. **MongoDB not affected**: MongoDB has its own sharding model and doesn't use SQL partitioning. This enhancement is PostgreSQL-only.
5. **No `pg_partman`**: For initial simplicity, use static hash partitions. `pg_partman` is useful for time-range partitions with automatic creation/retention, which we're not using.

## Future Considerations

- **Hybrid partitioning**: If time-based eviction becomes important, consider sub-partitioning each hash partition by `created_at` range. PostgreSQL supports multi-level partitioning.
- **Partition count tuning**: Monitor `pg_stat_user_tables` per partition to verify even distribution. Rebalance if needed.
- **`entry_embeddings` table**: May benefit from the same partitioning strategy since it has the same `conversation_group_id` column and similar query patterns.
