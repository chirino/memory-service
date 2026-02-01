# Search Optimizations: Vector Search Implementation

## Motivation

The current `/v1/conversations/search` endpoint has several fundamental issues:

1. **Placeholder Implementation**: The `VectorStore.search()` method delegates to keyword search instead of using the embeddings that are already being stored.

2. **O(E) Scaling**: The keyword fallback loads ALL entries into memory, causing performance degradation as users accumulate conversations.

3. **No Conversation Grouping**: Multiple entries from the same conversation appear as separate results.

4. **Access Control Scalability**: Using `WHERE conversation_id IN (...)` with many accessible conversation IDs won't scale well.

This enhancement implements proper vector search using the existing embedding infrastructure, adds conversation-level result grouping, and solves access control scalability.

## Current State Analysis

### What's Already in Place

```
Indexing Pipeline (working):
┌─────────────────────────────────────────────────────────────────┐
│ indexEntries() receives IndexEntryRequest                       │
├─────────────────────────────────────────────────────────────────┤
│ 1. Stores indexed_content → entries.indexed_content             │
│ 2. Calls EmbeddingService.embed(text) → float[]                 │
│ 3. Calls VectorStore.upsertTranscriptEmbedding() → entry_embeddings │
└─────────────────────────────────────────────────────────────────┘
```

### What's Missing

```java
// PgVectorStore.java - Current placeholder implementation
@Override
public SearchResultsDto search(String userId, SearchEntriesRequest request) {
    return postgresMemoryStore.searchEntries(userId, request);  // ❌ Keyword search fallback
}
```

The search method should:
1. Embed the query using `EmbeddingService.embed(query)`
2. Perform vector similarity search on `entry_embeddings`
3. Filter by user-accessible conversations via JOIN (not IN clause)
4. Group by conversation, return highest-scoring entry per conversation
5. Return results ranked by similarity score

## Design Decisions

### Embedding Model: Configurable Provider

Support multiple embedding providers via configuration, defaulting to in-process for zero external dependencies.

#### Available Providers

| Provider | Dimensions | Use Case | Trade-off |
|----------|------------|----------|-----------|
| `in-process` (default) | 384 | No external dependencies | Lower quality than cloud models |
| `openai` | 1536 | High quality semantic search | API costs, latency, requires API key |
| `ollama` | varies | Self-hosted, no API costs | Requires Ollama running |
| `none` | - | Disable embeddings | No vector search available |

#### Configuration

```properties
# Embedding provider: in-process (default), openai, ollama, none
memory-service.embedding.provider=in-process

# OpenAI settings (when provider=openai)
memory-service.embedding.openai.api-key=${OPENAI_API_KEY:}
memory-service.embedding.openai.model=text-embedding-3-small

# Ollama settings (when provider=ollama)
memory-service.embedding.ollama.base-url=http://localhost:11434
memory-service.embedding.ollama.model=nomic-embed-text
```

#### Dimension Handling

Different providers produce different embedding dimensions. The schema must match:

| Provider | Model | Dimensions |
|----------|-------|------------|
| in-process | all-MiniLM-L6-v2 | 384 |
| openai | text-embedding-3-small | 1536 |
| openai | text-embedding-3-large | 3072 |
| ollama | nomic-embed-text | 768 |

**Important:** Changing providers requires re-indexing all entries since dimensions differ. The `entry_embeddings` table dimension must match the configured provider.

#### Dependencies

```xml
<!-- In-process embedding (default) -->
<dependency>
  <groupId>dev.langchain4j</groupId>
  <artifactId>langchain4j-embeddings-all-minilm-l6-v2-q</artifactId>
  <version>1.0.0-beta3</version>
</dependency>

<!-- OpenAI (optional, add if using openai provider) -->
<dependency>
  <groupId>io.quarkiverse.langchain4j</groupId>
  <artifactId>quarkus-langchain4j-openai</artifactId>
  <version>${quarkus-langchain4j.version}</version>
</dependency>

<!-- Ollama (optional, add if using ollama provider) -->
<dependency>
  <groupId>io.quarkiverse.langchain4j</groupId>
  <artifactId>quarkus-langchain4j-ollama</artifactId>
  <version>${quarkus-langchain4j.version}</version>
</dependency>
```

#### Implementation

```java
@ApplicationScoped
public class EmbeddingServiceProducer {

    @ConfigProperty(name = "memory-service.embedding.provider", defaultValue = "in-process")
    String provider;

    @Inject
    Instance<AllMiniLmL6V2QuantizedEmbeddingModel> inProcessModel;

    @Inject
    @IfBuildProperty(name = "memory-service.embedding.provider", stringValue = "openai")
    Instance<OpenAiEmbeddingModel> openAiModel;

    @Inject
    @IfBuildProperty(name = "memory-service.embedding.provider", stringValue = "ollama")
    Instance<OllamaEmbeddingModel> ollamaModel;

    @Produces
    @ApplicationScoped
    public EmbeddingService embeddingService() {
        return switch (provider) {
            case "in-process" -> new LangChain4jEmbeddingService(inProcessModel.get());
            case "openai" -> new LangChain4jEmbeddingService(openAiModel.get());
            case "ollama" -> new LangChain4jEmbeddingService(ollamaModel.get());
            case "none" -> new NoopEmbeddingService();
            default -> throw new IllegalArgumentException("Unknown embedding provider: " + provider);
        };
    }
}

// Unified wrapper for any LangChain4j EmbeddingModel
public class LangChain4jEmbeddingService implements EmbeddingService {

    private final dev.langchain4j.model.embedding.EmbeddingModel model;

    public LangChain4jEmbeddingService(dev.langchain4j.model.embedding.EmbeddingModel model) {
        this.model = model;
    }

    @Override
    public boolean isEnabled() {
        return model != null;
    }

    @Override
    public float[] embed(String text) {
        if (!isEnabled() || text == null || text.isBlank()) {
            return new float[0];
        }
        return model.embed(text).content().vector();
    }

    @Override
    public int dimensions() {
        return model.dimension();
    }
}
```

### Access Control: Scalable Membership-Based Filtering

**Problem**: `WHERE conversation_id IN (:accessibleIds)` doesn't scale when users have access to many conversations. With 1000+ conversation IDs, the IN clause becomes unwieldy and inefficient.

**Solution**: The access control strategy depends on whether the vector store is in the same database as the main datastore.

#### Same-Database Configuration (No Replication)

When the vector store uses the same database as the main datastore, use JOIN for access control:

| Configuration | Access Control Strategy |
|---------------|------------------------|
| PostgreSQL + pgvector | JOIN `conversation_memberships` |
| MongoDB + Atlas Vector Search | `$lookup` memberships collection |

```sql
-- PostgreSQL + pgvector: JOIN existing membership table
JOIN conversation_memberships cm
  ON cm.conversation_group_id = ee.conversation_group_id
  AND cm.user_id = :userId  -- ✅ Scales with index, no replication
```

#### External Vector Store (Limited IN Clause)

When using an external vector store (Pinecone, Weaviate, Qdrant, etc.), use a metadata filter with the user's N most recently updated conversation groups:

| Configuration | Access Control Strategy |
|---------------|------------------------|
| PostgreSQL + external vector store | `conversation_group_id IN (top N recent groups)` |
| MongoDB + external vector store | `conversation_group_id IN (top N recent groups)` |

**Approach:**
1. Query main datastore for user's N most recently updated conversation groups
2. Pass group IDs as metadata filter to vector store
3. Vector search returns results only from those groups

```java
// Get user's N most recently updated conversation groups
List<UUID> recentGroupIds = membershipRepository.findRecentGroupsForUser(
    userId,
    maxGroups  // e.g., 100-500
);

// Vector search with metadata filter
results = vectorStore.search(
    embedding=queryEmbedding,
    filter={"conversation_group_id": {"$in": recentGroupIds}},
    limit=20
);
```

**Trade-offs:**

| Aspect | Behavior |
|--------|----------|
| Recent conversations | Full search coverage |
| Older conversations | Not included in search results |
| Scalability | Bounded by N, handles users with many conversations |
| Consistency | No replication complexity |

**Configuration:**
```properties
# Maximum conversation groups to include in external vector store search
memory-service.vector.external.max-groups=200
```

**Why this works:**
- Users typically search for content in recent/active conversations
- External vector stores handle hundreds of values in IN clauses efficiently
- No sync complexity - reads from authoritative membership data
- Graceful degradation for power users with many conversations

#### Embedding Schema

Both strategies use the same embedding schema with `conversation_group_id`:

```sql
CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id              UUID PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
    conversation_id       UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    embedding             vector(384) NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for access control JOIN (same-database) or filtering (external)
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_group
    ON entry_embeddings (conversation_group_id);

-- HNSW index for vector similarity search
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_hnsw
    ON entry_embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
```

### Conversation Grouping with Access Control

Combined query that handles both access control and conversation grouping:

```sql
WITH accessible_ranked AS (
    SELECT
        ee.entry_id,
        ee.conversation_id,
        c.title AS conversation_title,
        1 - (ee.embedding <=> CAST(:embedding AS vector)) AS score,
        ROW_NUMBER() OVER (
            PARTITION BY ee.conversation_id
            ORDER BY ee.embedding <=> CAST(:embedding AS vector)
        ) AS rank_in_conversation
    FROM entry_embeddings ee
    JOIN conversation_memberships cm
        ON cm.conversation_group_id = ee.conversation_group_id
        AND cm.user_id = :userId
    JOIN conversations c
        ON c.id = ee.conversation_id
        AND c.deleted_at IS NULL
    JOIN conversation_groups cg
        ON cg.id = ee.conversation_group_id
        AND cg.deleted_at IS NULL
)
SELECT entry_id, conversation_id, conversation_title, score
FROM accessible_ranked
WHERE rank_in_conversation = 1
ORDER BY score DESC
LIMIT :limit
```

**Benefits:**
- Access control via indexed JOIN (O(1) per membership lookup)
- Groups by conversation using window function
- Returns only highest-scoring entry per conversation
- Single query, no IN clause with unbounded IDs

## Proposed Implementation

### Phase 1: Embedding Infrastructure

1. Add `langchain4j-embeddings-all-minilm-l6-v2-q` dependency (default in-process)
2. Create `EmbeddingServiceProducer` with provider selection logic
3. Create `LangChain4jEmbeddingService` wrapper for unified interface
4. Update `application.properties` with `memory-service.embedding.provider=in-process`
5. Add optional dependencies for OpenAI/Ollama providers

### Phase 2: Schema Updates

1. Uncomment `entry_embeddings` table in schema.sql
2. Change dimension from 768 to 384
3. Add `conversation_group_id` column
4. Add `idx_entry_embeddings_group` index
5. Update HNSW index configuration
6. Update `PgVectorEmbeddingRepository.upsertEmbedding()` to store `conversation_group_id`

### Phase 3: Vector Search Implementation

1. Add `searchSimilarGrouped()` method to `PgVectorEmbeddingRepository`
2. Update `PgVectorStore.search()` to:
   - Embed query using `EmbeddingService`
   - Call repository with JOIN-based access control
   - Return grouped results with scores
3. Remove keyword search fallback from `PostgresMemoryStore.searchEntries()`

### Phase 4: API Updates

1. Add `groupByConversation` request parameter (default: true)
2. Update OpenAPI spec
3. Regenerate clients

### Phase 5: MongoDB Parity

1. Implement MongoDB Atlas Vector Search equivalent
2. Add conversation grouping for MongoDB

## Database Schema Changes

```sql
------------------------------------------------------------
-- Semantic search (pgvector-backed)
------------------------------------------------------------

-- Enable pgvector extension (requires superuser or extension already installed)
-- CREATE EXTENSION IF NOT EXISTS vector;

-- Embeddings are associated with individual entries.
-- Uses all-MiniLM-L6-v2 model which produces 384-dimensional vectors.
CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id              UUID PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
    conversation_id       UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    embedding             vector(384) NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for filtering by conversation group (for access control via JOIN)
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_group
    ON entry_embeddings (conversation_group_id);

-- HNSW index for fast approximate nearest neighbor search
-- HNSW is preferred over IVFFlat for better query performance
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_hnsw
    ON entry_embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
```

## API Changes

### Request Changes

```yaml
SearchConversationsRequest:
  properties:
    groupByConversation:
      type: boolean
      default: true
      description: |
        When true (default), groups results by conversation and returns only
        the highest-scoring entry per conversation.
```

### Response Changes

The `score` field now contains meaningful similarity scores:

| Score | Meaning |
|-------|---------|
| 1.0 | Identical (exact match) |
| 0.8+ | Highly similar |
| 0.5-0.8 | Moderately similar |
| < 0.5 | Weak similarity |

## Performance Comparison

| Metric | Before (Keyword) | After (Vector) |
|--------|------------------|----------------|
| Query complexity | O(E) - scan all entries | O(log N) - HNSW index |
| Access control | O(C) - IN clause with C IDs | O(1) - indexed JOIN |
| Memory usage | O(E) - load all | O(L) - limit only |
| Database queries | 3 + in-memory filter | 1 |
| Relevance | Exact keyword match | Semantic similarity |

### Benchmark Targets

| Scenario | Before | Target |
|----------|--------|--------|
| 10K entries, find top 20 | ~1s | <50ms |
| 100K entries, find top 20 | ~10s | <100ms |
| 1M entries, find top 20 | OOM | <200ms |
| User with 1000 conversations | Slow (large IN) | Fast (indexed JOIN) |

## Scope of Changes

| File | Change |
|------|--------|
| `pom.xml` | Add `langchain4j-embeddings-all-minilm-l6-v2-q` + optional OpenAI/Ollama deps |
| `EmbeddingServiceProducer.java` | New: CDI producer with provider selection |
| `LangChain4jEmbeddingService.java` | New: Unified wrapper for LangChain4j models |
| `NoopEmbeddingService.java` | New: Disabled embedding provider |
| `application.properties` | Add `memory-service.embedding.provider=in-process` |
| `schema.sql` | Uncomment entry_embeddings, add conversation_group_id, 384 dims |
| `PgVectorEmbeddingRepository.java` | Add `searchSimilarGrouped()`, update `upsertEmbedding()` |
| `PgVectorStore.java` | Implement proper vector search with JOIN-based access control |
| `openapi.yml` | Add `groupByConversation` parameter |
| `MongoVectorStore.java` | Implement MongoDB vector search |

## Testing

### Unit Tests

```java
@Test
void vectorSearch_returnsSimilarEntries() {
    // Given entries with embeddings
    indexEntry("conv-1", "entry-1", "How do I configure the API gateway?");
    indexEntry("conv-2", "entry-2", "API gateway configuration guide");
    indexEntry("conv-3", "entry-3", "Unrelated content about cooking");

    // When searching
    var results = vectorStore.search(userId,
        new SearchEntriesRequest().query("API gateway setup"));

    // Then similar entries rank higher
    assertThat(results.getResults()).hasSize(2);
    assertThat(results.getResults().get(0).getScore()).isGreaterThan(0.7);
}

@Test
void vectorSearch_groupsByConversation() {
    // Given multiple entries in same conversation
    indexEntry("conv-1", "entry-1", "API gateway intro");
    indexEntry("conv-1", "entry-2", "API gateway advanced config");
    indexEntry("conv-2", "entry-3", "API gateway troubleshooting");

    // When searching with grouping
    var results = vectorStore.search(userId,
        new SearchEntriesRequest()
            .query("API gateway")
            .groupByConversation(true));

    // Then one result per conversation
    assertThat(results.getResults()).hasSize(2);
    var conversationIds = results.getResults().stream()
        .map(SearchResultDto::getConversationId)
        .collect(toSet());
    assertThat(conversationIds).containsExactlyInAnyOrder("conv-1", "conv-2");
}

@Test
void vectorSearch_respectsAccessControl() {
    // Given entries in conversations user doesn't have access to
    indexEntry("private-conv", "entry-1", "Secret API docs");

    // When searching
    var results = vectorStore.search(userWithoutAccess,
        new SearchEntriesRequest().query("API"));

    // Then no results
    assertThat(results.getResults()).isEmpty();
}

@Test
void vectorSearch_scalesWithManyConversations() {
    // Given user has access to 1000 conversations
    for (int i = 0; i < 1000; i++) {
        createConversationWithAccess("conv-" + i, userId);
        indexEntry("conv-" + i, "entry-" + i, "Content " + i);
    }

    // When searching
    long start = System.currentTimeMillis();
    var results = vectorStore.search(userId,
        new SearchEntriesRequest().query("Content"));
    long elapsed = System.currentTimeMillis() - start;

    // Then query completes quickly (JOIN, not IN clause)
    assertThat(elapsed).isLessThan(200);
}
```

### Integration Tests

```gherkin
Feature: Vector search

  Scenario: Semantic search finds related content
    Given a conversation with entry "The deployment pipeline uses GitHub Actions"
    And the entry is indexed
    When I search for "CI/CD workflow"
    Then I should receive results with score > 0.5

  Scenario: Results grouped by conversation
    Given conversation "conv-1" with entries:
      | content                    |
      | Setting up authentication  |
      | OAuth2 configuration       |
    And conversation "conv-2" with entries:
      | content                    |
      | Database migrations        |
    When I search for "auth setup" with groupByConversation=true
    Then I should receive 1 result from "conv-1"
    And the result should be the highest-scoring entry

  Scenario: Access control scales with many conversations
    Given user "alice" has access to 500 conversations
    And each conversation has indexed entries
    When "alice" searches for "meeting notes"
    Then the search completes in under 200ms
```

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| pgvector not installed | Check extension availability at startup, log clear error |
| Embedding dimension mismatch | Validate dimension matches 384 at startup |
| Large result sets | Enforce limit, use pagination |
| Cold HNSW index | Index is persisted, minimal warmup needed |
| ONNX model loading time | Load model once at startup (singleton producer) |

## Dependencies

- PostgreSQL 15+ with pgvector 0.5+
- `langchain4j-embeddings-all-minilm-l6-v2-q` (in-process ONNX)
- Entries indexed via `indexEntries()` endpoint

## Configuration

```properties
# Embedding provider: in-process (default), openai, ollama, none
memory-service.embedding.provider=in-process

# OpenAI settings (when provider=openai)
# memory-service.embedding.openai.api-key=${OPENAI_API_KEY:}
# memory-service.embedding.openai.model=text-embedding-3-small

# Ollama settings (when provider=ollama)
# memory-service.embedding.ollama.base-url=http://localhost:11434
# memory-service.embedding.ollama.model=nomic-embed-text

# External vector store settings
# memory-service.vector.external.max-groups=200
```

## Future Considerations

- **Hybrid search**: Combine vector similarity with keyword boost for exact matches
- **Reranking**: Use cross-encoder for top-k reranking
- **Faceted search**: Filter by date range, conversation metadata
- **Query expansion**: Expand query with synonyms before embedding
- **Additional providers**: Cohere, Azure OpenAI, Amazon Bedrock
