---
status: implemented
---

# Enhancement 062: Pluggable Embedding Providers & Vector Search Backends

> **Status**: Implemented — All phases complete. Includes interface split to `VectorSearchStore` + `FullTextSearchStore` and caller-level search orchestration.

## Summary

Make both **embedding generation** and **vector search storage** pluggable. Phase 1 renamed `VectorStore` → `SearchStore`. Phase 2 added configurable embedding providers (local ONNX, OpenAI, disabled). Phase 3 introduced a LangChain4j `EmbeddingStore`-based vector backend (Qdrant). A subsequent refactor split the search abstraction into `VectorSearchStore` and `FullTextSearchStore` so semantic/vector and full-text responsibilities are separated.

## Implementation Update (2026-02-20)

- `SearchStore` has been renamed to `VectorSearchStore` to make responsibilities explicit.
- Search execution now uses two optional store types:
  - `VectorSearchStore` for semantic/vector search and embedding persistence.
  - `FullTextSearchStore` for datastore-native full-text search.
- `SearchExecutionService` orchestrates `searchType=auto` by trying semantic first (if available) and then full-text.
- `memory-service.vector.store.type` now selects only vector backends: `none`, `pgvector`, `qdrant`.
  Full-text availability is selected independently from `memory-service.datastore.type` (`postgres` or `mongo`).

### Interface Refactoring Details

- New vector contract: `VectorSearchStore`
  - `isEnabled()`
  - `isSemanticSearchAvailable()`
  - `search(...)` and `adminSearch(...)` for semantic queries
  - `upsertTranscriptEmbedding(...)` and `deleteByConversationGroupId(...)` for vector index lifecycle
- New full-text contract: `FullTextSearchStore`
  - `isFullTextSearchAvailable()`
  - `search(...)` and `adminSearch(...)` for full-text queries
- Implementations:
  - `PgSearchStore` implements both `VectorSearchStore` and `FullTextSearchStore`
  - `LangChain4jSearchStore` implements `VectorSearchStore`
  - `MongoSearchStore` implements `FullTextSearchStore`
- Selector split:
  - `SearchStoreSelector` now resolves only vector stores (`pgvector`, `qdrant`, `none`)
  - `FullTextSearchStoreSelector` resolves datastore-native full-text store (`postgres` or `mongo`)
- `SearchExecutionService` now orchestrates `searchType` at the caller layer:
  - `semantic`: requires `VectorSearchStore` semantic availability
  - `fulltext`: requires `FullTextSearchStore` availability
  - `auto`: tries semantic first, then full-text
- Vectorization paths (`EntryVectorizationObserver`, `PostgresMemoryStore`, `MongoMemoryStore`) now gate vector work on `isSemanticSearchAvailable()` and treat vector store as optional.

## Motivation

The memory-service currently has two `SearchStore` implementations: `PgSearchStore` (pgvector + PostgreSQL full-text) and `MongoSearchStore` (MongoDB full-text only). Both are tightly coupled to their respective databases — the vector index lives inside the same database as the application data.

Production deployments benefit from **dedicated vector databases** for several reasons:

1. **Scalability**: Dedicated vector DBs (Qdrant, Milvus, Weaviate) are purpose-built for high-dimensional similarity search with HNSW/IVF indexes, payload filtering, and horizontal scaling — capabilities that pgvector provides but PostgreSQL wasn't designed to optimize.
2. **Operational independence**: Scaling vector search independently of the transactional datastore avoids resource contention (CPU, memory, I/O) between OLTP and ANN workloads.
3. **Multi-tenancy at scale**: Qdrant's payload-based filtering and shard-key routing support large-scale multi-tenant deployments without requiring one collection per tenant.
4. **Ecosystem**: LangChain4j provides a uniform `EmbeddingStore<TextSegment>` interface with implementations for 15+ vector databases. Using this abstraction lets operators choose the best vector DB for their environment without code changes.

### Current State

- `VectorSearchStore` for semantic/vector search and vector index lifecycle.
- `FullTextSearchStore` for datastore-native full-text search.
- `SearchStoreSelector` routes vector stores via `memory-service.vector.store.type` (`pgvector`, `qdrant`, `none`).
- `FullTextSearchStoreSelector` routes full-text via `memory-service.datastore.type` (`postgres`, `mongo`/`mongodb`).
- `EmbeddingService` interface with `LocalEmbeddingService`, `OpenAiEmbeddingService`, `DisabledEmbeddingService`.
- `EmbeddingServiceProducer` selects provider via `memory-service.embedding.type` (`local`, `openai`, `none`).
- pgvector schema uses unparameterized `vector` column + `model` column for multi-model support.
- Event-driven vectorization via `EntryVectorizationObserver` (AFTER_SUCCESS, timeout-bounded).

---

## Phase 1: Rename VectorStore → SearchStore [Implemented]

Renamed `VectorStore` → `SearchStore`, `PgVectorStore` → `PgSearchStore`, `MongoVectorStore` → `MongoSearchStore`, `VectorStoreSelector` → `SearchStoreSelector`. Config property `memory-service.vector.type` → `memory-service.vector.store.type`.

## Phase 2: Pluggable Embedding Providers [Implemented]

Added `dimensions()` and `modelId()` to `EmbeddingService`. Created `LocalEmbeddingService` (384-dim ONNX), `OpenAiEmbeddingService` (configurable), `DisabledEmbeddingService`. `EmbeddingServiceProducer` selects via `memory-service.embedding.type`. Updated pgvector schema to unparameterized `vector` + `model` column. Added `langchain4j-open-ai` dependency. Created event-driven vectorization pipeline (`EntryVectorizationEvent`/`Observer`).

---

## Phase 3: LangChain4j Vector Search Abstraction [Implemented]

### Architecture Overview

Introduce a single, generic `LangChain4jSearchStore` that adapts **any** LangChain4j `EmbeddingStore<TextSegment>` to the `VectorSearchStore` interface. A CDI producer creates the concrete `EmbeddingStore` based on configuration. Adding a new vector database backend requires only a new Maven dependency and a new case in the producer — no new vector search implementation.

```
VectorSearchStore
├── PgSearchStore            [pgvector semantic + vector lifecycle]
└── LangChain4jSearchStore   [generic adapter]
    └── wraps EmbeddingStore<TextSegment>
        ├── QdrantEmbeddingStore    [from langchain4j-qdrant]
        ├── ChromaEmbeddingStore    [future]
        ├── MilvusEmbeddingStore    [future]
        └── ...

FullTextSearchStore
├── PgSearchStore            [PostgreSQL full-text]
└── MongoSearchStore         [MongoDB text index full-text]
```

### LangChain4jSearchStore

A single `@ApplicationScoped` class that implements `VectorSearchStore` by delegating vector operations to a LangChain4j `EmbeddingStore<TextSegment>`.

```java
@ApplicationScoped
public class LangChain4jSearchStore implements VectorSearchStore {

    @Inject EmbeddingStore<TextSegment> embeddingStore;
    @Inject EmbeddingService embeddingService;
    @Inject EntityManager entityManager;              // for membership queries
    @Inject EntryRepository entryRepository;
    @Inject ConversationRepository conversationRepository;
    @Inject DataEncryptionService dataEncryptionService;
    @Inject ObjectMapper objectMapper;

    // Optional full-text search (available when datastore is Postgres)
    @Inject Instance<FullTextSearchRepository> fullTextSearchRepository;

    @ConfigProperty(name = "memory-service.search.semantic.enabled", defaultValue = "true")
    boolean semanticSearchEnabled;

    @ConfigProperty(name = "memory-service.search.fulltext.enabled", defaultValue = "true")
    boolean fullTextSearchEnabled;
}
```

#### Semantic Search

Uses LangChain4j's `EmbeddingStore.search()` with metadata filters for access control and model filtering:

```java
private SearchResultsDto semanticSearch(String userId, SearchEntriesRequest request) {
    float[] queryVector = embeddingService.embed(request.getQuery());
    Embedding queryEmbedding = Embedding.from(queryVector);

    // Resolve user's allowed conversation groups from Postgres
    List<String> allowedGroupIds = getAllowedGroupIds(userId);
    if (allowedGroupIds.isEmpty()) {
        return emptyResults();
    }

    // Build metadata filter: group access + model
    Filter filter = new And(
        new IsIn("conversation_group_id", allowedGroupIds),
        new IsEqualTo("model", embeddingService.modelId())
    );

    int limit = request.getLimit() != null ? request.getLimit() : 20;
    boolean groupByConversation = request.getGroupByConversation() == null
        || request.getGroupByConversation();

    // Overfetch when grouping (post-process deduplication)
    int fetchLimit = groupByConversation ? limit * 3 : limit + 1;

    EmbeddingSearchRequest searchRequest = EmbeddingSearchRequest.builder()
        .queryEmbedding(queryEmbedding)
        .filter(filter)
        .maxResults(fetchLimit)
        .build();

    EmbeddingSearchResult<TextSegment> results = embeddingStore.search(searchRequest);

    // Post-process: group by conversation if needed, build DTOs
    return buildResults(results.matches(), limit, groupByConversation, includeEntry);
}
```

#### Access Control

The current `PgSearchStore` enforces access control via SQL JOINs with `conversation_memberships` inside the vector query. With an external vector store, access control becomes a **two-step process**:

1. **Pre-query**: Fetch the user's allowed `conversation_group_id`s from Postgres.
2. **Filter**: Pass allowed group IDs as a metadata filter to the vector store.

```java
private List<String> getAllowedGroupIds(String userId) {
    @SuppressWarnings("unchecked")
    List<Object> rows = entityManager
        .createNativeQuery(
            "SELECT DISTINCT conversation_group_id::text FROM conversation_memberships WHERE user_id = ?1")
        .setParameter(1, userId)
        .getResultList();
    return rows.stream().map(Object::toString).toList();
}
```

This trades a single SQL JOIN for a two-step process but keeps all access control decisions in Postgres — the vector store never sees unauthorized data in search results.

#### GroupByConversation

`PgSearchStore` uses SQL `ROW_NUMBER() OVER (PARTITION BY conversation_id)` to return the best match per conversation. LangChain4j's `EmbeddingStore` doesn't support this natively.

Strategy: **overfetch + post-process**.

1. Request `limit * 3` results from the vector store.
2. Iterate results, keeping only the highest-scoring entry per `conversation_id`.
3. Return the top `limit` grouped results.

```java
private List<EmbeddingMatch<TextSegment>> groupByConversation(
        List<EmbeddingMatch<TextSegment>> matches, int limit) {
    Map<String, EmbeddingMatch<TextSegment>> bestPerConversation = new LinkedHashMap<>();
    for (EmbeddingMatch<TextSegment> match : matches) {
        String convId = match.embedded().metadata().getString("conversation_id");
        bestPerConversation.putIfAbsent(convId, match); // first = highest score
    }
    return bestPerConversation.values().stream().limit(limit).toList();
}
```

#### Admin Search

Admin search has no membership restrictions but supports optional `userId` filter and `includeDeleted` flag. These fields (`owner_user_id`, `deleted_at`) are conversation-level attributes not stored in the vector store metadata.

Strategy:
- **No userId filter**: Search with `model` filter only, no membership restriction.
- **With userId filter**: Pre-query the user's conversation group IDs from Postgres and filter in the vector store.
- **includeDeleted**: Always filter `deleted_at IS NULL` in Postgres when building result DTOs. Overfetch from the vector store to account for filtered-out deleted results.

#### Full-Text Search

`LangChain4jSearchStore` conditionally supports full-text search by delegating to `FullTextSearchRepository` when the primary datastore is PostgreSQL. This is the same repository that `PgSearchStore` uses.

- If `FullTextSearchRepository` is resolvable (Postgres datastore) → full-text search available.
- If not resolvable (e.g., MongoDB datastore + Qdrant vector) → full-text search throws `SearchTypeUnavailableException`.
- `auto` mode: tries semantic first, falls back to full-text if available and semantic returned no results.

#### Shared Search Result Building

Both `PgSearchStore` and `LangChain4jSearchStore` need identical logic for building `SearchResultDto` from entry/conversation entities: fetching entities, decrypting content, extracting highlights. This shared logic should be extracted into a `SearchResultDtoBuilder` utility class to avoid duplication.

### EmbeddingStoreProducer

A CDI producer that creates the appropriate LangChain4j `EmbeddingStore<TextSegment>` based on the vector store type configuration.

```java
@ApplicationScoped
public class EmbeddingStoreProducer {

    @ConfigProperty(name = "memory-service.vector.store.type", defaultValue = "none")
    String storeType;

    // Qdrant config
    @ConfigProperty(name = "memory-service.vector.qdrant.host", defaultValue = "localhost")
    String qdrantHost;

    @ConfigProperty(name = "memory-service.vector.qdrant.port", defaultValue = "6334")
    int qdrantPort;

    @ConfigProperty(name = "memory-service.vector.qdrant.collection-name",
                    defaultValue = "memory_segments")
    String qdrantCollectionName;

    @ConfigProperty(name = "memory-service.vector.qdrant.api-key")
    Optional<String> qdrantApiKey;

    @ConfigProperty(name = "memory-service.vector.qdrant.use-tls", defaultValue = "false")
    boolean qdrantUseTls;

    @Inject EmbeddingService embeddingService;

    @Produces @Singleton
    public EmbeddingStore<TextSegment> embeddingStore() {
        return switch (storeType.trim().toLowerCase()) {
            case "qdrant" -> buildQdrantStore();
            default -> throw new IllegalStateException(
                "No LangChain4j EmbeddingStore configured for store type: " + storeType
                    + ". This producer is only called when the SearchStoreSelector"
                    + " routes to LangChain4jSearchStore.");
        };
    }

    private EmbeddingStore<TextSegment> buildQdrantStore() {
        var builder = QdrantEmbeddingStore.builder()
            .host(qdrantHost)
            .port(qdrantPort)
            .collectionName(qdrantCollectionName)
            .useTls(qdrantUseTls);

        qdrantApiKey.ifPresent(builder::apiKey);

        return builder.build();
    }
}
```

> **Note**: The `EmbeddingStore` bean is only resolved when `SearchStoreSelector` routes to `LangChain4jSearchStore` (via `Instance<LangChain4jSearchStore>`). When `storeType` is `pgvector` or `none`, the producer is never called.

### Selector Update

Vector store routing:

```java
@ApplicationScoped
public class SearchStoreSelector {

    @Inject Instance<PgSearchStore> pgSearchStore;
    @Inject Instance<LangChain4jSearchStore> langChain4jSearchStore;

    public VectorSearchStore getSearchStore() {
        return switch (type) {
            case "pgvector" -> pgSearchStore.get();
            case "qdrant" -> langChain4jSearchStore.get();
            case "none" -> null;
            default -> null;
        };
    }
}
```

Full-text routing:

```java
@ApplicationScoped
public class FullTextSearchStoreSelector {

    @Inject Instance<PgSearchStore> pgSearchStore;
    @Inject Instance<MongoSearchStore> mongoSearchStore;

    public FullTextSearchStore getFullTextSearchStore() {
        return switch (datastoreType) {
            case "postgres" -> pgSearchStore.get();
            case "mongo", "mongodb" -> mongoSearchStore.get();
            default -> null;
        };
    }
}
```

Future LangChain4j backends (`chroma`, `milvus`, `weaviate`) still route through `LangChain4jSearchStore`; the producer selects the concrete `EmbeddingStore`.

### Metadata Schema

Each point stored in the LangChain4j `EmbeddingStore` carries:

| Metadata Key | Type | Purpose |
|---|---|---|
| `conversation_id` | String (UUID) | Links to Postgres conversation; used for groupByConversation |
| `conversation_group_id` | String (UUID) | Tenant partition key; used for access control filtering |
| `model` | String | Embedding model identifier; filters to current model |

The point `id` is the `entry_id` (UUID as string). LangChain4j's `EmbeddingStore.add(String id, Embedding, TextSegment)` supports explicit IDs, enabling upsert semantics.

**Upsert:**
```java
TextSegment segment = TextSegment.from("", Metadata.from(Map.of(
    "conversation_id", conversationId,
    "conversation_group_id", conversationGroupId,
    "model", embeddingService.modelId()
)));
embeddingStore.add(entryId, Embedding.from(embedding), segment);
```

**Delete by conversation group:**
```java
embeddingStore.removeAll(
    new IsEqualTo("conversation_group_id", conversationGroupId)
);
```

### Qdrant Collection Management

Qdrant does **not** auto-create collections. The `QdrantEmbeddingStore` from `langchain4j-qdrant`
does not provision collection schema in constructor/search/upsert paths.

The service provisions the collection at startup when
`memory-service.vector.migrate-at-start=true` (default). The collection schema:

- **Collection name**: `memory_segments` (configurable)
- **Vector params**: `size` = embedding dimensions, `distance` = Cosine
- **Payload indexes** (for filter performance):
  - `conversation_group_id` — keyword index (high-selectivity, used in every search + deletes)
  - `model` — keyword index (used in every search)
  - `conversation_id` — keyword index (used for groupByConversation deduplication)

### Configuration

New properties for Qdrant (when `memory-service.vector.store.type=qdrant`):

| Property | Default | Description |
|---|---|---|
| `memory-service.vector.qdrant.host` | `localhost` | Qdrant server hostname |
| `memory-service.vector.qdrant.port` | `6334` | Qdrant gRPC port |
| `memory-service.vector.qdrant.collection-name` | `memory_segments` | Qdrant collection name |
| `memory-service.vector.qdrant.api-key` | *(none)* | API key for authentication |
| `memory-service.vector.qdrant.use-tls` | `false` | Enable TLS for gRPC connection |
| `memory-service.vector.migrate-at-start` | `true` | Auto-provision vector store schema at startup |
| `memory-service.vector.qdrant.startup-timeout` | `PT30S` | Timeout for Qdrant migration API calls |

Environment variable equivalents:
```bash
MEMORY_SERVICE_VECTOR_STORE_TYPE=qdrant
MEMORY_SERVICE_VECTOR_QDRANT_HOST=localhost
MEMORY_SERVICE_VECTOR_QDRANT_PORT=6334
MEMORY_SERVICE_VECTOR_QDRANT_COLLECTION_NAME=memory_segments
MEMORY_SERVICE_VECTOR_QDRANT_API_KEY=change-me
MEMORY_SERVICE_VECTOR_QDRANT_USE_TLS=false
```

### Docker Compose

Add Qdrant service to `compose.yaml`:

```yaml
qdrant:
  image: qdrant/qdrant:v1.16.3
  ports:
    - "6333:6333"   # HTTP/REST
    - "6334:6334"   # gRPC
  volumes:
    - qdrant_storage:/qdrant/storage
  environment:
    QDRANT__SERVICE__API_KEY: "${QDRANT_API_KEY:-change-me}"
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://localhost:6333/readyz"]
    interval: 10s
    timeout: 3s
    retries: 10
```

Memory service environment (when using Qdrant):
```yaml
MEMORY_SERVICE_VECTOR_STORE_TYPE: qdrant
MEMORY_SERVICE_VECTOR_QDRANT_HOST: qdrant
MEMORY_SERVICE_VECTOR_QDRANT_PORT: 6334
MEMORY_SERVICE_VECTOR_QDRANT_API_KEY: "${QDRANT_API_KEY:-change-me}"
MEMORY_SERVICE_EMBEDDING_TYPE: "${MEMORY_SERVICE_EMBEDDING_TYPE:-local}"
```

### Dependencies

Add the LangChain4j Qdrant module (same version as existing LangChain4j dependencies):

```xml
<dependency>
    <groupId>dev.langchain4j</groupId>
    <artifactId>langchain4j-qdrant</artifactId>
    <version>1.0.0-beta3</version>
</dependency>
```

This transitively brings in `langchain4j-core` (which provides `EmbeddingStore`, `Filter`, `TextSegment`, etc.) and `io.qdrant:client` (official Qdrant Java SDK via gRPC).

> **Why not `quarkus-langchain4j-qdrant`?** The Quarkus extension auto-produces an `EmbeddingStore` bean, which would conflict when adding additional LangChain4j backends (Chroma, Milvus). Using the core library gives us full control over bean production via `EmbeddingStoreProducer`, maintaining the pluggable architecture.

---

## Testing

### Phase 2 Unit Tests [Implemented]

```java
// EmbeddingServiceProducerTest
@Test void selects_local_by_default()
@Test void selects_openai_with_config()
@Test void selects_disabled_for_none()
@Test void openai_requires_api_key()
@Test void rejects_unknown_type()
```

### Phase 3 Tests

#### Integration Tests (Qdrant via Testcontainers)

```java
@QuarkusTest
@TestProfile(QdrantTestProfile.class)
class LangChain4jSearchStoreTest {

    @Test void semantic_search_returns_relevant_results()
    @Test void semantic_search_filters_by_membership()
    @Test void semantic_search_excludes_wrong_model()
    @Test void semantic_search_groups_by_conversation()
    @Test void upsert_stores_embedding_with_metadata()
    @Test void upsert_overwrites_existing_entry()
    @Test void delete_by_conversation_group_removes_all_points()
    @Test void auto_search_falls_back_to_fulltext()
    @Test void admin_search_no_membership_restriction()
    @Test void admin_search_filters_by_user_id()
}
```

`QdrantTestProfile` sets:
```properties
memory-service.vector.store.type=qdrant
memory-service.vector.qdrant.host=localhost
memory-service.vector.qdrant.port=${qdrant.mapped.port}
memory-service.embedding.type=local
```

The Qdrant container is started via `@QuarkusTestResource` using Testcontainers:

```java
public class QdrantTestResource implements QuarkusTestResourceLifecycleManager {
    private GenericContainer<?> qdrant;

    @Override
    public Map<String, String> start() {
        qdrant = new GenericContainer<>("qdrant/qdrant:v1.16.3")
            .withExposedPorts(6333, 6334);
        qdrant.start();
        return Map.of(
            "memory-service.vector.qdrant.host", qdrant.getHost(),
            "memory-service.vector.qdrant.port", String.valueOf(qdrant.getMappedPort(6334))
        );
    }

    @Override
    public void stop() { if (qdrant != null) qdrant.stop(); }
}
```

#### Cucumber Scenarios

Extend existing search scenarios to verify Qdrant backend. Key scenarios:

```gherkin
Feature: Semantic search via Qdrant vector store

  Background:
    Given a user "alice" with a conversation containing entries:
      | role      | content                              |
      | user      | Tell me about quantum computing      |
      | assistant | Quantum computing uses qubits...      |
      | user      | How does machine learning work?      |
      | assistant | Machine learning is a subset of AI... |
    And embeddings have been indexed

  Scenario: Semantic search returns relevant entries
    When "alice" searches for "quantum physics" with type "semantic"
    Then the search returns results containing entry about "quantum computing"
    And each result has a score between 0 and 1

  Scenario: Search respects membership access control
    Given a user "bob" with no access to alice's conversations
    When "bob" searches for "quantum computing" with type "semantic"
    Then the search returns no results

  Scenario: Search filters by embedding model
    Given entries were indexed with model "local/all-MiniLM-L6-v2"
    And the embedding service now reports model "openai/text-embedding-3-small"
    When "alice" searches for "quantum computing" with type "semantic"
    Then the search returns no results

  Scenario: Delete removes all embeddings for a conversation group
    When the conversation group is deleted
    Then searching for "quantum computing" returns no results
```

#### Unit Tests

```java
class EmbeddingStoreProducerTest {
    @Test void produces_qdrant_store_when_type_is_qdrant()
    @Test void throws_for_unsupported_type()
}

class SearchStoreSelectorTest {
    @Test void routes_qdrant_to_langchain4j_search_store()  // NEW
}

class SearchResultDtoBuilderTest {
    @Test void builds_dto_from_vector_result()
    @Test void builds_dto_from_fulltext_result()
    @Test void decrypts_conversation_title()
    @Test void handles_missing_entry_gracefully()
}
```

---

## Tasks

### Phase 1: Rename VectorStore → SearchStore [Implemented]
- [x] Rename `VectorStore` → `SearchStore` interface
- [x] Rename `PgVectorStore` → `PgSearchStore`
- [x] Rename `MongoVectorStore` → `MongoSearchStore`
- [x] Rename `VectorStoreSelector` → `SearchStoreSelector`
- [x] Rename `VectorStoreSelectorTest` → `SearchStoreSelectorTest`
- [x] Update config property `memory-service.vector.type` → `memory-service.vector.store.type`
- [x] Update all injection sites, compose.yaml, and docs

### Phase 2: Pluggable Embedding Providers [Implemented]
- [x] Add `dimensions()` and `modelId()` to `EmbeddingService` interface
- [x] Create `LocalEmbeddingService`, `OpenAiEmbeddingService`, `DisabledEmbeddingService`
- [x] Create `EmbeddingServiceProducer` with `@Produces @Singleton`
- [x] Delete `DefaultEmbeddingService` and `EmbeddingModelProducer`
- [x] Add `langchain4j-open-ai` dependency
- [x] Update pgvector schema — unparameterized `vector`, add `model` column + index
- [x] Update `PgVectorEmbeddingRepository` — model in upsert, model filter in search
- [x] Update application.properties — `embedding.type` replaces `embedding.enabled`
- [x] Create event-driven vectorization pipeline (`EntryVectorizationEvent`/`Observer`)
- [x] Write unit tests for `EmbeddingServiceProducer`

### Phase 3: LangChain4j Vector Search Abstraction [Implemented]
- [x] Add `langchain4j-qdrant` dependency to `memory-service/pom.xml`
- [x] Extract `SearchResultDtoBuilder` from `PgSearchStore` (shared result building logic)
- [x] Refactor `PgSearchStore` to use `SearchResultDtoBuilder`
- [x] Create `LangChain4jSearchStore` implementing `SearchStore`
- [x] Create `EmbeddingStoreProducer` (CDI producer for `EmbeddingStore<TextSegment>`)
- [x] Update `SearchStoreSelector` — add `qdrant` routing to `LangChain4jSearchStore`
- [x] Add Qdrant configuration properties to `application.properties`
- [x] Add Qdrant service to `compose.yaml`
- [x] Write unit tests for `EmbeddingStoreProducer` and `SearchStoreSelector`
- [x] Update `configuration.mdx` docs with Qdrant configuration
- [x] Verify existing pgvector Cucumber tests still pass

### Phase 4: Search Interface Split [Implemented]
- [x] Rename `SearchStore` interface to `VectorSearchStore`
- [x] Add `FullTextSearchStore` interface for datastore-native full-text search
- [x] Split selector responsibilities:
  - [x] `SearchStoreSelector` for vector store selection (`pgvector`, `qdrant`, `none`)
  - [x] `FullTextSearchStoreSelector` for full-text selection by datastore (`postgres`, `mongo`)
- [x] Refactor `SearchExecutionService` to orchestrate semantic/full-text/auto across optional stores
- [x] Update `PgSearchStore`, `LangChain4jSearchStore`, and `MongoSearchStore` to implement the new contracts
- [x] Update vectorization/indexing paths to gate on `isSemanticSearchAvailable()`
- [x] Add/refresh unit tests for selectors and `SearchExecutionService`

## Files to Modify

### Phase 3: LangChain4j Vector Search Abstraction

| File | Change |
|---|---|
| `memory-service/pom.xml` | Add `langchain4j-qdrant` dependency |
| `.../vector/SearchResultDtoBuilder.java` | **New** — extracted shared DTO building logic |
| `.../vector/PgSearchStore.java` | Refactor to use `SearchResultDtoBuilder` |
| `.../vector/LangChain4jSearchStore.java` | **New** — generic LangChain4j `EmbeddingStore` adapter |
| `.../vector/EmbeddingStoreProducer.java` | **New** — CDI producer for `EmbeddingStore<TextSegment>` |
| `.../config/SearchStoreSelector.java` | Add `qdrant` case routing to `LangChain4jSearchStore` |
| `memory-service/src/main/resources/application.properties` | Add `memory-service.vector.qdrant.*` properties |
| `memory-service/src/test/resources/application.properties` | Add Qdrant test profile properties |
| `compose.yaml` | Add `qdrant` service + memory-service Qdrant env vars |
| `.devcontainer/devcontainer.json` | Add Qdrant service for dev environment |
| `.../test/QdrantTestResource.java` | **New** — Testcontainers lifecycle manager |
| `.../test/LangChain4jSearchStoreTest.java` | **New** — integration tests |
| `.../test/EmbeddingStoreProducerTest.java` | **New** — unit tests |
| `.../test/SearchResultDtoBuilderTest.java` | **New** — unit tests |
| `site/src/pages/docs/configuration.mdx` | Add Qdrant vector store configuration docs |

### Phase 4: Search Interface Split

| File | Change |
|---|---|
| `.../vector/VectorSearchStore.java` | **New** vector contract (semantic + vector lifecycle) |
| `.../vector/FullTextSearchStore.java` | **New** full-text contract |
| `.../config/SearchStoreSelector.java` | Refactor to vector-only selection |
| `.../config/FullTextSearchStoreSelector.java` | **New** full-text selector by datastore |
| `.../vector/SearchExecutionService.java` | Orchestrate `auto` across optional vector/full-text stores |
| `.../vector/PgSearchStore.java` | Implement both `VectorSearchStore` and `FullTextSearchStore` |
| `.../vector/LangChain4jSearchStore.java` | Implement `VectorSearchStore` |
| `.../vector/MongoSearchStore.java` | Implement `FullTextSearchStore` |
| `.../vector/EntryVectorizationObserver.java` | Gate vector work with semantic availability |
| `.../store/impl/PostgresMemoryStore.java` | Gate vector indexing with semantic availability |
| `.../store/impl/MongoMemoryStore.java` | Gate vector indexing with semantic availability |
| `.../service/TaskProcessor.java` | Handle optional vector store in cleanup task |
| `.../test/config/FullTextSearchStoreSelectorTest.java` | **New** selector unit tests |
| `.../test/vector/SearchExecutionServiceTest.java` | Updated orchestration unit tests |

## Verification

```bash
# Compile
./mvnw compile

# Run tests (includes Qdrant integration tests via Testcontainers)
./mvnw test -pl memory-service > test.log 2>&1
# Search for failures using Grep tool on test.log
```

## Design Decisions

1. **Core `langchain4j-qdrant` instead of `quarkus-langchain4j-qdrant`**: The Quarkus extension auto-produces an `EmbeddingStore` bean. When we add a second LangChain4j backend (Chroma, Milvus), two extensions would produce competing beans. Using the core library with our own `EmbeddingStoreProducer` keeps bean production under our control and supports the pluggable architecture.

2. **Single `LangChain4jSearchStore` for all backends**: Rather than writing `QdrantSearchStore`, `ChromaSearchStore`, etc., one generic class adapts any `EmbeddingStore<TextSegment>`. The `EmbeddingStoreProducer` is the only code that knows about specific backends.

3. **Two-step access control (pre-query groups, filter in vector store)**: The current pgvector approach uses a SQL JOIN inside the vector query. With an external vector store, we first query Postgres for allowed `conversation_group_id`s, then pass them as a metadata filter. This keeps access control decisions in Postgres and avoids storing user membership data in the vector store.

4. **Overfetch + post-process for groupByConversation**: LangChain4j's `EmbeddingStore` doesn't support SQL-style window functions. We overfetch (3x limit) and deduplicate by conversation in Java. This is acceptable because the overfetch factor is bounded and the result set is small.

5. **Conditional full-text search**: `LangChain4jSearchStore` delegates full-text search to `FullTextSearchRepository` (PostgreSQL) when available. This means Postgres + Qdrant deployments get both semantic and full-text search. Non-Postgres deployments get semantic only.

6. **`@Produces @Singleton` for `EmbeddingStore`**: Consistent with the `EmbeddingServiceProducer` pattern. The produced bean is only resolved when `SearchStoreSelector` routes to `LangChain4jSearchStore` (via `Instance<>` lazy resolution).

7. **Metadata-only storage in vector store**: We store `conversation_id`, `conversation_group_id`, and `model` as metadata — not the full entry text. Entry content is fetched from Postgres when building result DTOs. This avoids data duplication and keeps the vector store lightweight.

## Non-Goals

- **Quarkus Dev Services for Qdrant** — We use Testcontainers directly instead of the Quarkus extension to maintain control over bean production.
- **Vector store migration tooling** (pgvector → Qdrant) — Users can re-index by changing `vector.store.type` and triggering a re-index. Automated migration is out of scope.
- **Multiple simultaneous vector stores** — One active vector store per deployment.
- **Qdrant clustering / distributed mode** — Single-node Qdrant is sufficient for the initial implementation. Clustering is an operational concern, not an application concern.

## Open Questions

1. **Collection initialization**: Should the `EmbeddingStoreProducer` (or `LangChain4jSearchStore`) explicitly create the Qdrant collection + payload indexes on startup? Or rely on langchain4j-qdrant's implicit collection creation? Explicit creation gives control over distance metric and payload indexes but adds startup logic.

2. **Payload indexes**: LangChain4j's `QdrantEmbeddingStore` may not create payload indexes automatically. We may need a startup hook (or admin endpoint) to create indexes on `conversation_group_id`, `model`, and `conversation_id` for filter performance.

3. **Score normalization**: Qdrant cosine similarity returns scores in `[0, 1]`. Verify that LangChain4j's `QdrantEmbeddingStore` passes through scores without transformation, to match the `1 - cosine_distance` scoring in pgvector.
