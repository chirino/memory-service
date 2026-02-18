---
status: proposed
---

# Enhancement 062: Pluggable Embedding Providers

> **Status**: Proposed.

## Summary

Add support for configurable embedding providers, starting with OpenAI embeddings alongside the existing local all-MiniLM-L6-v2 ONNX model. The embedding provider is selected via `memory-service.embedding.type` configuration, following the established selector pattern used throughout the codebase.

## Motivation

The memory-service currently hardcodes a single embedding model (`AllMiniLmL6V2QuantizedEmbeddingModel` — 384-dimension in-process ONNX model). While this works well for local development and small deployments, production users need:

1. **Higher-quality embeddings**: OpenAI's `text-embedding-3-small` and `text-embedding-3-large` produce significantly better semantic search results than the quantized MiniLM model.
2. **Flexible deployment**: Some environments prefer offloading embedding computation to an API rather than running ONNX inference in-process.
3. **Model choice**: Different use cases benefit from different embedding models — smaller/faster for high-volume indexing, larger/better for precision search.

### Current State

- `DefaultEmbeddingService` directly injects `AllMiniLmL6V2QuantizedEmbeddingModel` (hardcoded).
- `EmbeddingModelProducer` is a CDI producer that creates the ONNX model singleton.
- The pgvector schema hardcodes `vector(384)` in the `entry_embeddings` table.
- The `EmbeddingService` interface has only `isEnabled()` and `embed(String text)` — no dimension awareness.
- Both `AllMiniLmL6V2QuantizedEmbeddingModel` and `OpenAiEmbeddingModel` implement LangChain4j's `EmbeddingModel` interface.

## Design

### Rename `VectorStore` → `SearchStore`

The current `VectorStore` interface is misleading — it handles search (semantic + full-text + auto), embedding storage, and cleanup. The MongoDB implementation doesn't even do vector search (it only supports full-text). The name should reflect what the interface actually represents: a **search backend**.

| Current | Proposed |
|---------|----------|
| `VectorStore` | `SearchStore` |
| `PgVectorStore` | `PgSearchStore` |
| `MongoVectorStore` | `MongoSearchStore` |
| `VectorStoreSelector` | `SearchStoreSelector` |
| `VectorStoreSelectorTest` | `SearchStoreSelectorTest` |
| `memory-service.vector.type` | `memory-service.search.store.type` |

This rename also affects `compose.yaml` (`MEMORY_SERVICE_VECTOR_TYPE` → `MEMORY_SERVICE_SEARCH_STORE_TYPE`), configuration docs, and all injection sites.

### Configuration Property Cleanup

Rename and simplify search-related config properties to use a consistent namespace:

| Current | Proposed | Values |
|---------|----------|--------|
| `memory-service.vector.type` | `memory-service.search.store.type` | `postgres`, `mongo`, `none` |
| `memory-service.embedding.enabled` | `memory-service.embedding.type` | `local`, `openai`, `none` |
| `memory-service.search.semantic.enabled` | *(no change)* | `true`, `false` |
| `memory-service.search.fulltext.enabled` | *(no change)* | `true`, `false` |

The old `pgvector` and `mongodb` aliases for the search store type are dropped — since the store handles both semantic and full-text search (not just vectors), the datastore name (`postgres`, `mongo`) is more accurate.

### Embedding Provider Configuration

New config property `memory-service.embedding.type` with values:

| Value | Description |
|-------|-------------|
| `local` (default) | In-process all-MiniLM-L6-v2 ONNX model (384 dimensions) |
| `openai` | OpenAI Embeddings API (requires API key) |
| `none` | Disabled — semantic search falls back to full-text |

OpenAI-specific settings (when `type=openai`):

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.embedding.openai.api-key` | (required) | OpenAI API key |
| `memory-service.embedding.openai.model-name` | `text-embedding-3-small` | OpenAI model name |
| `memory-service.embedding.openai.base-url` | `https://api.openai.com/v1` | API base URL (for Azure OpenAI or proxies) |
| `memory-service.embedding.openai.dimensions` | (model default) | Optional dimension override (supported by text-embedding-3-*) |

Environment variable equivalents:
```bash
MEMORY_SERVICE_EMBEDDING_TYPE=openai
MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY=sk-...
MEMORY_SERVICE_EMBEDDING_OPENAI_MODEL_NAME=text-embedding-3-small
MEMORY_SERVICE_EMBEDDING_OPENAI_BASE_URL=https://api.openai.com/v1
MEMORY_SERVICE_EMBEDDING_OPENAI_DIMENSIONS=1536
```

### Interface Change

Extend `EmbeddingService` with `dimensions()` and `modelId()`:

```java
public interface EmbeddingService {
    boolean isEnabled();
    float[] embed(String text);
    int dimensions();
    String modelId();
}
```

The `modelId()` returns a stable identifier for the provider+model combination (e.g., `local/all-MiniLM-L6-v2`, `openai/text-embedding-3-small`). This is stored in the `entry_embeddings.model` column and used to filter search results to the current model.

### Implementation Classes

Replace `DefaultEmbeddingService` + `EmbeddingModelProducer` with:

1. **`LocalEmbeddingService`** — wraps `AllMiniLmL6V2QuantizedEmbeddingModel`, `dimensions()=384`, `modelId()="local/all-MiniLM-L6-v2"`
2. **`OpenAiEmbeddingService`** — wraps LangChain4j `OpenAiEmbeddingModel`, configurable dimensions, `modelId()="openai/{model-name}"`
3. **`DisabledEmbeddingService`** — `isEnabled()=false`, `dimensions()=0`, `modelId()="none"`

### Selector / CDI Producer

`EmbeddingServiceProducer` uses `@Produces @Singleton` to produce the correct `EmbeddingService` based on config. This approach (vs. a getter-based selector) means all existing `@Inject EmbeddingService` injection points — `PgVectorStore`, `PostgresMemoryStore`, `MongoMemoryStore` — require **zero code changes**.

```java
@ApplicationScoped
public class EmbeddingServiceProducer {
    @ConfigProperty(name = "memory-service.embedding.type", defaultValue = "local")
    String embeddingType;
    // ... OpenAI config properties ...

    @Produces @Singleton
    public EmbeddingService embeddingService() {
        return switch (embeddingType.trim().toLowerCase()) {
            case "local" -> new LocalEmbeddingService();
            case "openai" -> new OpenAiEmbeddingService(apiKey, modelName, baseUrl, dimensions);
            case "none" -> new DisabledEmbeddingService();
            default -> throw new IllegalStateException("Unsupported: " + embeddingType);
        };
    }
}
```

### Schema Migration

Update `pgvector-schema.sql` to support pluggable providers and future migration between them. Two changes:

1. **Remove hardcoded dimension** — `vector(384)` → `vector` (unparameterized). pgvector accepts any dimension; the HNSW index works as long as all vectors in the index share the same dimension.

2. **Add `model` column** — Records which embedding provider/model produced each vector. This is essential for future migration: when switching providers, the system can identify stale embeddings and selectively re-index them.

**Before:**
```sql
CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id              UUID PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
    conversation_id       UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    embedding             vector(384) NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

**After:**
```sql
CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id              UUID PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
    conversation_id       UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    embedding             vector NOT NULL,
    model                 VARCHAR(128) NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_entry_embeddings_model
    ON entry_embeddings (model);
```

The `model` column stores an identifier like `local/all-MiniLM-L6-v2` or `openai/text-embedding-3-small`. Each `EmbeddingService` implementation provides this via a new `modelId()` method on the interface:

```java
public interface EmbeddingService {
    boolean isEnabled();
    float[] embed(String text);
    int dimensions();
    String modelId();
}
```

The upsert query and search queries are updated to include the `model` column. Search queries filter by the current model so that stale embeddings from a previous provider are excluded from results.

### Dependency

Add the core LangChain4j OpenAI library (not the Quarkus extension, to avoid auto-configuration side effects):

```xml
<dependency>
    <groupId>dev.langchain4j</groupId>
    <artifactId>langchain4j-open-ai</artifactId>
    <version>1.0.0-beta3</version>
</dependency>
```

Same version as the existing `langchain4j-embeddings-all-minilm-l6-v2-q` dependency.

### Compose Configuration

```yaml
# To use OpenAI embeddings instead of the default local model:
MEMORY_SERVICE_EMBEDDING_TYPE: openai
MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY: ${OPENAI_API_KEY}
MEMORY_SERVICE_EMBEDDING_OPENAI_MODEL_NAME: text-embedding-3-small
```

## Testing

### Unit Tests

```java
// EmbeddingServiceProducerTest
@Test void selects_local_by_default()    // → LocalEmbeddingService, 384 dims
@Test void selects_openai_with_config()  // → OpenAiEmbeddingService (mock API)
@Test void selects_disabled_for_none()   // → DisabledEmbeddingService
@Test void openai_requires_api_key()     // → IllegalStateException
@Test void rejects_unknown_type()        // → IllegalStateException
```

### Existing Tests

All existing Cucumber tests should pass unchanged since the default embedding type is `local`, which matches current behavior. The test `application.properties` should set `memory-service.embedding.type=local`.

## Tasks

### Rename VectorStore → SearchStore
- [ ] Rename `VectorStore` → `SearchStore` interface
- [ ] Rename `PgVectorStore` → `PgSearchStore`
- [ ] Rename `MongoVectorStore` → `MongoSearchStore`
- [ ] Rename `VectorStoreSelector` → `SearchStoreSelector`
- [ ] Rename `VectorStoreSelectorTest` → `SearchStoreSelectorTest`
- [ ] Update config property `memory-service.vector.type` → `memory-service.search.store.type`
- [ ] Update all injection sites, compose.yaml, and docs

### Pluggable Embedding Providers
- [ ] Add `dimensions()` and `modelId()` to `EmbeddingService` interface
- [ ] Create `LocalEmbeddingService` (extracts logic from `DefaultEmbeddingService`)
- [ ] Create `OpenAiEmbeddingService` (wraps LangChain4j `OpenAiEmbeddingModel`)
- [ ] Create `DisabledEmbeddingService`
- [ ] Create `EmbeddingServiceProducer` with `@Produces @Singleton`
- [ ] Delete `DefaultEmbeddingService` and `EmbeddingModelProducer`
- [ ] Add `langchain4j-open-ai` dependency to `memory-service/pom.xml`
- [ ] Update `pgvector-schema.sql` — remove hardcoded `vector(384)`, add `model` column + index
- [ ] Update `PgVectorEmbeddingRepository` — include `model` in upsert and filter searches by model
- [ ] Update `application.properties` — replace `embedding.enabled` with `embedding.type`
- [ ] Update test `application.properties`
- [ ] Update `compose.yaml` — add commented OpenAI config + pgvector image
- [ ] Update `configuration.mdx` docs
- [ ] Write unit tests for `EmbeddingServiceProducer`

## Files to Modify

### Rename VectorStore → SearchStore

| File | Change |
|------|--------|
| `memory-service/src/main/java/.../vector/VectorStore.java` | **Rename** → `SearchStore.java` |
| `memory-service/src/main/java/.../vector/PgVectorStore.java` | **Rename** → `PgSearchStore.java` |
| `memory-service/src/main/java/.../vector/MongoVectorStore.java` | **Rename** → `MongoSearchStore.java` |
| `memory-service/src/main/java/.../config/VectorStoreSelector.java` | **Rename** → `SearchStoreSelector.java`, update config key |
| `memory-service/src/test/java/.../config/VectorStoreSelectorTest.java` | **Rename** → `SearchStoreSelectorTest.java` |
| `memory-service/src/main/java/.../store/impl/PostgresMemoryStore.java` | Update `VectorStore` references |
| `memory-service/src/main/java/.../store/impl/MongoMemoryStore.java` | Update `VectorStore` references |
| `memory-service/src/main/java/.../api/SearchResource.java` | Update `VectorStore` references |
| `memory-service/src/main/java/.../api/AdminResource.java` | Update `VectorStore` references |
| `memory-service/src/main/java/.../grpc/SearchGrpcService.java` | Update `VectorStore` references |
| `memory-service/src/main/java/.../service/TaskProcessor.java` | Update `VectorStore` references |
| `memory-service/src/test/java/.../cucumber/StepDefinitions.java` | Update `VectorStore` references |
| `memory-service/src/main/resources/application.properties` | `memory-service.vector.type` → `memory-service.search.store.type` |

### Pluggable Embedding Providers

| File | Change |
|------|--------|
| `memory-service/src/main/java/.../vector/EmbeddingService.java` | Add `dimensions()` and `modelId()` methods |
| `memory-service/src/main/java/.../vector/LocalEmbeddingService.java` | **New** — local ONNX embedding impl |
| `memory-service/src/main/java/.../vector/OpenAiEmbeddingService.java` | **New** — OpenAI embedding impl |
| `memory-service/src/main/java/.../vector/DisabledEmbeddingService.java` | **New** — disabled/noop impl |
| `memory-service/src/main/java/.../vector/EmbeddingServiceProducer.java` | **New** — CDI producer with type selection |
| `memory-service/src/main/java/.../vector/DefaultEmbeddingService.java` | **Delete** — replaced by LocalEmbeddingService |
| `memory-service/src/main/java/.../vector/EmbeddingModelProducer.java` | **Delete** — absorbed into producer |
| `memory-service/pom.xml` | Add `langchain4j-open-ai` dependency |
| `memory-service/src/main/java/.../vector/PgVectorEmbeddingRepository.java` | Add `model` param to upsert, filter searches by model |
| `memory-service/src/main/resources/db/pgvector-schema.sql` | `vector(384)` → `vector`, add `model` column + index |
| `memory-service/src/main/resources/application.properties` | Replace `embedding.enabled` with `embedding.type` + OpenAI props |
| `memory-service/src/test/resources/application.properties` | Set `embedding.type=local` |
| `compose.yaml` | Add pgvector image + commented OpenAI embedding env vars |
| `site/src/pages/docs/configuration.mdx` | Update embedding + search store config docs |

## Verification

```bash
# Compile
./mvnw compile

# Run tests
./mvnw test -pl memory-service > test.log 2>&1
# Search for failures using Grep tool on test.log
```

## Future: Multi-Provider Migration

The schema changes in this enhancement (unparameterized `vector` column + `model` column) are designed to support migrating between embedding providers in a future enhancement. This section outlines how that migration would work — **implementation is out of scope** for this enhancement, but the schema must not block it.

### How migration would work

1. **Change `memory-service.embedding.type`** to the new provider (e.g., `local` → `openai`). Restart the service.

2. **New embeddings use the new model.** The `model` column records `openai/text-embedding-3-small` for newly indexed entries. Search queries filter by the current model, so old embeddings are excluded from semantic results (full-text search is unaffected).

3. **Background re-indexing.** A re-index task (triggered via admin API or scheduled) iterates over entries where `entry_embeddings.model != currentModelId` (or where no embedding exists). It re-embeds each entry with the new provider and upserts the result.

4. **Cleanup.** Once re-indexing is complete, old embeddings (with the previous model) can be deleted. The HNSW index is rebuilt to reflect the new dimension.

### What the schema enables

- **`model` column** — identifies which provider produced each embedding, enabling selective re-indexing and model-filtered search.
- **Unparameterized `vector`** — allows vectors of any dimension to coexist during migration (the HNSW index is rebuilt after migration completes).
- **`entry_embeddings.model` index** — efficient queries for "entries needing re-indexing" (`WHERE model != ?`).

### What would need to be built (future enhancement)

- Admin API endpoint to trigger re-indexing (e.g., `POST /v1/admin/reindex-embeddings`)
- Background task that re-embeds entries in batches (leveraging existing task queue infrastructure)
- Progress tracking (count of entries re-indexed vs. total)
- HNSW index rebuild after migration completes
- Search query changes to filter `WHERE model = ?` on the current model

## Non-Goals

- **Other providers** (Anthropic, Cohere, HuggingFace Inference API) — future work, but the architecture supports adding them easily.
- **Automatic re-indexing** on provider switch — out of scope, but the schema supports it (see Future section above).
- **Multiple simultaneous providers** — one active provider per deployment; the `model` column tracks provenance but doesn't enable concurrent querying across models.

## Design Decisions

1. **Core `langchain4j-open-ai` instead of `quarkus-langchain4j-openai`**: The Quarkus extension auto-discovers and produces chat model beans, which would conflict with the example apps and add unwanted build-time configuration. The core library gives us just the `OpenAiEmbeddingModel` builder.

2. **`@Produces @Singleton` instead of getter-based selector**: Unlike `VectorStoreSelector.getVectorStore()`, the `EmbeddingService` is injected directly by type in three places. A CDI producer makes the switch transparent — zero changes to consumers.

3. **Unparameterized `vector` column + `model` column**: Removing `vector(384)` allows any embedding dimension. The `model` column tracks which provider produced each embedding, enabling filtered search and future migration. Runtime consistency within a single model is enforced by the application — pgvector will reject inserts with mismatched dimensions against the HNSW index if one exists.
