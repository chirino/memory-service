---
status: proposed
---

# Enhancement 059: Entries and Entry Embeddings Table Partitioning

> **Status**: Proposed.

## Summary

Implement hash partitioning by `conversation_group_id` for both the `entries` and `entry_embeddings` tables in PostgreSQL to improve query performance and enable more efficient data lifecycle management at scale.

## Motivation

The `entries` table is the largest and most frequently queried table in the memory service. Every conversation sync, history retrieval, search operation, and fork traversal touches this table. The `entry_embeddings` table grows 1:1 with indexed entries and has identical access patterns, making it a natural co-candidate.

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

CREATE TABLE entry_embeddings (
    entry_id              UUID PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
    conversation_id       UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    embedding             vector NOT NULL,
    model                 VARCHAR(128) NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Current Indexes

**entries (7 indexes)**:
```sql
idx_entries_conversation_channel_client_epoch_created_at  -- Primary memory operations
idx_entries_conversation_created_at                        -- History channel queries
idx_entries_group_created_at                               -- Fork-aware retrieval
idx_entries_unindexed                                      -- Batch indexing
idx_entries_pending_vector_indexing                         -- Vector indexing queue
idx_entries_indexed_content_fts                             -- GIN full-text search
```

**entry_embeddings (3 indexes)**:
```sql
idx_entry_embeddings_group    -- Access control via conversation_group_id
idx_entry_embeddings_model    -- Selective re-indexing by model
idx_entry_embeddings_hnsw     -- ANN vector search (HNSW)
```

### Growth Characteristics

- Every conversation sync appends at least one entry (since Enhancement 026 consolidated N messages → 1 entry).
- History channel entries accumulate over a conversation's lifetime (one per user/AI turn).
- Memory channel entries grow with each sync cycle (though old epochs become unreachable).
- The `content` column stores encrypted BYTEA, which can be large for conversations with many messages or attachments.
- `entry_embeddings` grows 1:1 with indexed entries; each row contains a high-dimensional vector.
- Soft-deleted conversations retain their entries until eviction runs.

### Performance Concerns

1. **Index bloat**: As tables grow, all indexes grow proportionally. Index maintenance (inserts, vacuuming) becomes more expensive.
2. **Full-text search**: The GIN index on `indexed_content_tsv` degrades with table size since GIN indexes don't support partial scans efficiently.
3. **ANN search**: HNSW and IVFFlat vector indexes perform better with smaller candidate sets. Partitioning reduces the search space per partition.
4. **Eviction**: Batch deletion of soft-deleted entries requires scanning and vacuuming large table segments.
5. **Sequential scans**: Queries that can't use indexes (e.g., complex fork tree traversals) become slower.

## Design

### Partitioning Strategy: Hash by `conversation_group_id`

Hash partitioning by `conversation_group_id` best aligns with the dominant query patterns for both tables:

1. **Memory sync** (`listMemoryEntriesAtLatestEpoch`): Filters by `conversation_id` → single partition.
2. **History listing** (`listByChannel`): Filters by `conversation_id` → single partition.
3. **Fork-aware retrieval** (`listByConversationGroup`): Filters by `conversation_group_id` → single partition.
4. **Vector search**: Filters by `conversation_group_id` for access control → single partition.
5. **Full-text search**: Must scan all partitions, but benefits from smaller per-partition GIN indexes.
6. **Eviction**: Must scan all partitions, but eviction is a background task.

Using the same partition key and modulus for both tables means a given conversation group's entries and embeddings always land in the same numbered partition, keeping the schema conceptually consistent.

### Schema Changes

#### 1. Primary Keys Must Include the Partition Key

PostgreSQL requires the partition key to be part of the primary key:

```sql
-- entries: BEFORE
CREATE TABLE entries (
    id UUID PRIMARY KEY,
    ...
);

-- entries: AFTER
CREATE TABLE entries (
    id UUID NOT NULL,
    conversation_group_id UUID NOT NULL,
    ...
    PRIMARY KEY (id, conversation_group_id)
) PARTITION BY HASH (conversation_group_id);

-- entry_embeddings: BEFORE
CREATE TABLE entry_embeddings (
    entry_id UUID PRIMARY KEY,
    conversation_group_id UUID NOT NULL,
    ...
);

-- entry_embeddings: AFTER
CREATE TABLE entry_embeddings (
    entry_id UUID NOT NULL,
    conversation_group_id UUID NOT NULL,
    ...
    PRIMARY KEY (entry_id, conversation_group_id)
) PARTITION BY HASH (conversation_group_id);
```

This changes the uniqueness constraint from `(id)` to `(id, conversation_group_id)`. Since UUIDs are globally unique by design, this is safe.

#### 2. Foreign Keys

PostgreSQL does not support foreign keys referencing partitioned tables (as of PG 17). The FKs from `attachments` (to `entries`) and from `entry_embeddings` (to `entries`) must be dropped and replaced with application-level enforcement:

```sql
-- attachments: BEFORE
CREATE TABLE attachments (
    entry_id UUID REFERENCES entries(id) ON DELETE CASCADE,
    ...
);

-- attachments: AFTER — no FK, cascade handled by eviction task
CREATE TABLE attachments (
    entry_id UUID,
    ...
);

-- entry_embeddings: BEFORE
CREATE TABLE entry_embeddings (
    entry_id UUID PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
    ...
);

-- entry_embeddings: AFTER — no FK to entries
CREATE TABLE entry_embeddings (
    entry_id UUID NOT NULL,
    ...
);
```

The cascade delete behavior is handled by the eviction task queue (already the case for soft-deleted entries).

#### 3. Partition Creation

Create 16 hash partitions for each table (a reasonable starting point):

```sql
DO $$
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format(
            'CREATE TABLE entries_p%s PARTITION OF entries FOR VALUES WITH (MODULUS 16, REMAINDER %s)',
            i, i
        );
        EXECUTE format(
            'CREATE TABLE entry_embeddings_p%s PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER %s)',
            i, i
        );
    END LOOP;
END $$;
```

#### 4. Index Changes

Indexes defined on the parent table are automatically created on each partition by PostgreSQL. The existing index definitions work as-is:

```sql
-- Defined on entries, automatically applied to entries_p0..entries_p15
CREATE INDEX idx_entries_conversation_channel_... ON entries (...);

-- Defined on entry_embeddings, automatically applied to entry_embeddings_p0..p15
CREATE INDEX idx_entry_embeddings_hnsw ON entry_embeddings USING hnsw (...);
```

### JPA/Hibernate Changes

Since the `id`/`entry_id` UUIDs are globally unique, keep them as the logical `@Id` in the entity classes. Handle the composite PK at the DDL level only — Hibernate doesn't need to know about the partition key in the PK.

### Migration Strategy

Since the project is pre-release, migrations can simply recreate the tables:

```sql
-- entries
ALTER TABLE entries RENAME TO entries_old;
CREATE TABLE entries (...) PARTITION BY HASH (conversation_group_id);
DO $$ ... $$;  -- create partitions
INSERT INTO entries SELECT * FROM entries_old;
DROP TABLE entries_old CASCADE;

-- entry_embeddings (depends on entries existing first)
ALTER TABLE entry_embeddings RENAME TO entry_embeddings_old;
CREATE TABLE entry_embeddings (...) PARTITION BY HASH (conversation_group_id);
DO $$ ... $$;  -- create partitions
INSERT INTO entry_embeddings SELECT * FROM entry_embeddings_old;
DROP TABLE entry_embeddings_old CASCADE;

-- Recreate indexes
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

-- Vector search (verify single partition scanned)
EXPLAIN ANALYZE
SELECT * FROM entry_embeddings
WHERE conversation_group_id = $1
ORDER BY embedding <=> $2
LIMIT 10;
```

### Functional Tests

All existing Cucumber tests must pass without modification — partitioning is transparent to the application.

```bash
./mvnw test -pl memory-service > test.log 2>&1
```

### Partition Verification

```sql
-- Verify partition pruning on entries
EXPLAIN SELECT * FROM entries WHERE conversation_group_id = 'some-uuid';
-- Should show: "Append" with only one partition scanned

-- Verify partition pruning on entry_embeddings
EXPLAIN SELECT * FROM entry_embeddings WHERE conversation_group_id = 'some-uuid';
-- Should show: "Append" with only one partition scanned

-- Check data distribution
SELECT tableoid::regclass, count(*) FROM entries GROUP BY tableoid ORDER BY tableoid::regclass;
SELECT tableoid::regclass, count(*) FROM entry_embeddings GROUP BY tableoid ORDER BY tableoid::regclass;
```

## Files to Modify

| File | Change |
|------|--------|
| `memory-service/src/main/resources/db/schema.sql` | Add `PARTITION BY HASH`, composite PKs, partition creation for `entries` |
| `memory-service/src/main/resources/db/pgvector-schema.sql` | Add `PARTITION BY HASH`, composite PK, partition creation for `entry_embeddings`; drop FK to `entries` |
| `memory-service/src/main/resources/db/changelog/` | **New**: Liquibase migration changeset |
| `memory-service/.../persistence/entity/EntryEntity.java` | Verify `@Id` mapping works with composite DDL PK |
| `memory-service/.../persistence/entity/AttachmentEntity.java` | Remove FK constraint annotation to `entries` |
| `memory-service/.../persistence/entity/EntryEmbeddingsEntity.java` | Remove FK constraint annotation to `entries`; verify `@Id` mapping |

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
# \d+ entry_embeddings
# SELECT tableoid::regclass, count(*) FROM entries GROUP BY tableoid;
# SELECT tableoid::regclass, count(*) FROM entry_embeddings GROUP BY tableoid;
```

## Design Decisions

1. **Hash over range**: The dominant query pattern is by `conversation_id`/`conversation_group_id`, not by time. Hash partitioning gives partition pruning on the hot path.
2. **Same key and modulus for both tables**: Using `conversation_group_id` with modulus 16 on both tables keeps the design consistent and means related data lands in the same partition number.
3. **16 partitions**: A moderate starting point. Can be increased by re-partitioning (data migration required). Too few partitions give little benefit; too many add planning overhead.
4. **Application-level cascade**: Losing FK constraints is the main trade-off. Since the service already uses soft deletes and background eviction tasks, the cascade is effectively application-managed already.
5. **MongoDB not affected**: MongoDB has its own sharding model and doesn't use SQL partitioning. This enhancement is PostgreSQL-only.
6. **No `pg_partman`**: For initial simplicity, use static hash partitions. `pg_partman` is useful for time-range partitions with automatic creation/retention, which we're not using.

## Future Considerations

- **Hybrid partitioning**: If time-based eviction becomes important, consider sub-partitioning each hash partition by `created_at` range. PostgreSQL supports multi-level partitioning.
- **Partition count tuning**: Monitor `pg_stat_user_tables` per partition to verify even distribution. Rebalance if needed.
