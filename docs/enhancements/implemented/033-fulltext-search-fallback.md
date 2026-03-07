---
status: implemented
---

# Full-Text Search Fallback with GIN Indexes

> **Status**: Implemented.

## Motivation

The vector search implementation (032) falls back to keyword search when:
1. Embeddings haven't been stored yet (e.g., inline `indexedContent`)
2. The embedding service is disabled
3. Vector search returns no results

The current keyword search fallback has severe performance issues:

```java
// PostgresMemoryStore.searchEntries() - Current implementation
List<EntryEntity> candidates = entryRepository
    .find("conversation.id in ?1", userConversationIds)
    .list();  // ❌ Loads ALL entries into memory

for (EntryEntity m : candidates) {
    List<Object> content = decryptContent(m.getContent());  // ❌ Decrypts everything
    String text = extractSearchText(content);
    if (text.toLowerCase().contains(query)) {  // ❌ Linear scan
        // match found
    }
}
```

**Problems:**
- **O(E) complexity**: Loads all entries for user's conversations
- **Memory intensive**: Decrypts content for every entry
- **No indexes used**: Linear `String.contains()` in Java
- **No highlighting**: Can't identify where matches occur

## Current State Analysis

### What Happens Today

```
Search Request
     │
     ▼
┌─────────────────────────────────────────────┐
│ PgVectorStore.search()                       │
├─────────────────────────────────────────────┤
│ 1. Embed query → float[]                     │
│ 2. Vector search on entry_embeddings         │
│ 3. If empty → fallback to keyword search     │
└─────────────────────────────────────────────┘
     │
     ▼ (fallback)
┌─────────────────────────────────────────────┐
│ PostgresMemoryStore.searchEntries()          │
├─────────────────────────────────────────────┤
│ 1. Load ALL entries for user's conversations │
│ 2. Decrypt each entry's content              │
│ 3. String.contains() in Java                 │
│ 4. No database indexes used                  │
└─────────────────────────────────────────────┘
```

### Performance Impact

| User's Entries | Memory Used | Time (estimated) |
|----------------|-------------|------------------|
| 1,000 | ~10 MB | ~500ms |
| 10,000 | ~100 MB | ~5s |
| 100,000 | ~1 GB | ~50s |
| 1,000,000 | OOM | N/A |

## Design Decision: PostgreSQL Full-Text Search

Use PostgreSQL's built-in full-text search with `tsvector` and `tsquery` rather than `pg_trgm`:

| Approach | Pros | Cons |
|----------|------|------|
| **tsvector + GIN** | Language-aware stemming, ranking, efficient | No substring matching |
| pg_trgm + GIN | Substring/fuzzy matching | Slower, no stemming |
| Hybrid (both) | Best of both worlds | More complexity, larger indexes |

**Recommendation**: Start with `tsvector` for natural language queries. This complements vector search well since both focus on semantic/linguistic matching rather than exact substring matching.

### Why tsvector?

1. **Stemming**: "running" matches "run", "runs", "runner"
2. **Stop words**: Ignores common words like "the", "a", "is"
3. **Ranking**: `ts_rank()` provides relevance scores
4. **Highlighting**: `ts_headline()` extracts matching snippets
5. **Efficient**: GIN index provides fast lookups

## API Changes: Search Type Parameter

### New `searchType` Parameter

Add a `searchType` parameter to `SearchConversationsRequest` in `openapi.yml:1319`:

```yaml
SearchConversationsRequest:
  type: object
  required:
    - query
  properties:
    query:
      type: string
      description: Natural language query.
    searchType:
      type: string
      enum: [auto, semantic, fulltext]
      default: auto
      description: |
        The search method to use:
        - `auto` (default): Try semantic (vector) search first, fall back to full-text if no results or unavailable
        - `semantic`: Use only vector/embedding-based semantic search
        - `fulltext`: Use only PostgreSQL full-text search with GIN index

        If the requested search type is not available on the server, a 501 (Not Implemented)
        error is returned with details about which search types are available.
    after:
      # ... existing fields ...
```

### Search Type Behavior

| searchType | Behavior |
|------------|----------|
| `auto` | Try semantic first → fall back to fulltext → empty if both fail |
| `semantic` | Vector search only. Returns 501 if embeddings disabled. |
| `fulltext` | GIN-indexed full-text search only. Returns 501 if not available. |

### Error Responses (HTTP 501 Not Implemented)

When the requested search type is unavailable, the server returns 501 with details:

```json
{
  "error": "search_type_unavailable",
  "message": "Semantic search is not available. The embedding service is disabled on this server.",
  "availableTypes": ["fulltext"]
}
```

```json
{
  "error": "search_type_unavailable",
  "message": "Full-text search is not available. The server is not configured with PostgreSQL full-text search support.",
  "availableTypes": ["semantic"]
}
```

### Implementation

```java
@ApplicationScoped
public class PgVectorStore implements VectorStore {

    @ConfigProperty(name = "memory-service.search.semantic.enabled", defaultValue = "true")
    boolean semanticSearchEnabled;

    @ConfigProperty(name = "memory-service.search.fulltext.enabled", defaultValue = "true")
    boolean fullTextSearchEnabled;

    @Inject EmbeddingService embeddingService;

    @Override
    public SearchResultsDto search(String userId, SearchEntriesRequest request) {
        String searchType = request.getSearchType() != null ? request.getSearchType() : "auto";

        return switch (searchType) {
            case "semantic" -> {
                validateSemanticSearchAvailable();
                yield semanticSearch(userId, request);
            }
            case "fulltext" -> {
                validateFullTextSearchAvailable();
                yield fullTextSearch(userId, request);
            }
            case "auto" -> autoSearch(userId, request);
            default -> throw new BadRequestException(
                "Invalid searchType: " + searchType + ". Valid values: auto, semantic, fulltext");
        };
    }

    private void validateSemanticSearchAvailable() {
        if (!semanticSearchEnabled || !embeddingService.isEnabled()) {
            throw new SearchTypeUnavailableException(
                "Semantic search is not available. The embedding service is disabled on this server.",
                List.of("fulltext"));
        }
    }

    private void validateFullTextSearchAvailable() {
        if (!fullTextSearchEnabled) {
            throw new SearchTypeUnavailableException(
                "Full-text search is not available. The server is not configured with PostgreSQL full-text search support.",
                List.of("semantic"));
        }
    }

    private SearchResultsDto autoSearch(String userId, SearchEntriesRequest request) {
        // Try semantic first if available
        if (semanticSearchEnabled && embeddingService.isEnabled()) {
            SearchResultsDto results = semanticSearch(userId, request);
            if (!results.getResults().isEmpty()) {
                return results;
            }
        }

        // Fall back to full-text if available
        if (fullTextSearchEnabled) {
            return fullTextSearch(userId, request);
        }

        // No search methods available
        return emptyResults();
    }
}
```

### Exception Class

```java
public class SearchTypeUnavailableException extends RuntimeException {
    private final List<String> availableTypes;

    public SearchTypeUnavailableException(String message, List<String> availableTypes) {
        super(message);
        this.availableTypes = availableTypes;
    }

    public List<String> getAvailableTypes() {
        return availableTypes;
    }
}
```

### Exception Mapper

```java
@Provider
public class SearchTypeUnavailableExceptionMapper
        implements ExceptionMapper<SearchTypeUnavailableException> {

    @Override
    public Response toResponse(SearchTypeUnavailableException e) {
        // 501 Not Implemented - server doesn't support the requested search type
        return Response.status(501)
            .entity(Map.of(
                "error", "search_type_unavailable",
                "message", e.getMessage(),
                "availableTypes", e.getAvailableTypes()
            ))
            .build();
    }
}
```

### Server Configuration

```properties
# Enable/disable search methods (default: both enabled)
memory-service.search.semantic.enabled=true
memory-service.search.fulltext.enabled=true

# Future: Could be used to completely disable search
# memory-service.search.enabled=true
```

### Use Cases

| Scenario | Recommended searchType |
|----------|----------------------|
| General search (most users) | `auto` (default) |
| Finding conceptually similar content | `semantic` |
| Finding exact terms/phrases | `fulltext` |
| Debugging search issues | Explicit type to isolate |
| Server without pgvector | `fulltext` |
| Server without PostgreSQL | `semantic` |

## Proposed Implementation

### Phase 1: Schema Changes

Add a generated `tsvector` column with GIN index:

```sql
-- Add full-text search column (generated from indexed_content)
ALTER TABLE entries ADD COLUMN IF NOT EXISTS indexed_content_tsv tsvector
    GENERATED ALWAYS AS (to_tsvector('english', COALESCE(indexed_content, ''))) STORED;

-- Create GIN index for fast full-text search
CREATE INDEX IF NOT EXISTS idx_entries_indexed_content_fts
    ON entries USING GIN (indexed_content_tsv);
```

**Notes:**
- `GENERATED ALWAYS AS ... STORED` automatically maintains the tsvector
- No application code changes needed to populate it
- Works with existing `indexed_content` column
- Uses English dictionary for stemming (configurable)

### Phase 2: Repository Method

Add a new method to `PgVectorEmbeddingRepository` or create a dedicated `FullTextSearchRepository`:

```java
@ApplicationScoped
public class FullTextSearchRepository {

    @Inject EntityManager entityManager;

    /**
     * Full-text search on indexed_content with access control.
     *
     * @param userId the user ID for access control
     * @param query the search query
     * @param limit maximum results
     * @param groupByConversation when true, returns best match per conversation
     * @return search results with scores and highlights
     */
    @Transactional
    public List<FullTextSearchResult> search(
            String userId, String query, int limit, boolean groupByConversation) {

        // Sanitize query for tsquery
        String tsQuery = toTsQuery(query);

        String sql;
        if (groupByConversation) {
            sql = """
                WITH accessible_ranked AS (
                    SELECT
                        e.id AS entry_id,
                        e.conversation_id,
                        ts_rank(e.indexed_content_tsv, plainto_tsquery('english', ?1)) AS score,
                        ts_headline('english', e.indexed_content, plainto_tsquery('english', ?1),
                            'StartSel=<mark>, StopSel=</mark>, MaxWords=50, MinWords=20') AS highlight,
                        ROW_NUMBER() OVER (
                            PARTITION BY e.conversation_id
                            ORDER BY ts_rank(e.indexed_content_tsv, plainto_tsquery('english', ?1)) DESC
                        ) AS rank_in_conversation
                    FROM entries e
                    JOIN conversations c ON c.id = e.conversation_id AND c.deleted_at IS NULL
                    JOIN conversation_groups cg ON cg.id = c.conversation_group_id AND cg.deleted_at IS NULL
                    JOIN conversation_memberships cm ON cm.conversation_group_id = cg.id AND cm.user_id = ?2
                    WHERE e.indexed_content_tsv @@ plainto_tsquery('english', ?1)
                )
                SELECT entry_id, conversation_id, score, highlight
                FROM accessible_ranked
                WHERE rank_in_conversation = 1
                ORDER BY score DESC
                LIMIT ?3
                """;
        } else {
            sql = """
                SELECT
                    e.id AS entry_id,
                    e.conversation_id,
                    ts_rank(e.indexed_content_tsv, plainto_tsquery('english', ?1)) AS score,
                    ts_headline('english', e.indexed_content, plainto_tsquery('english', ?1),
                        'StartSel=<mark>, StopSel=</mark>, MaxWords=50, MinWords=20') AS highlight
                FROM entries e
                JOIN conversations c ON c.id = e.conversation_id AND c.deleted_at IS NULL
                JOIN conversation_groups cg ON cg.id = c.conversation_group_id AND cg.deleted_at IS NULL
                JOIN conversation_memberships cm ON cm.conversation_group_id = cg.id AND cm.user_id = ?2
                WHERE e.indexed_content_tsv @@ plainto_tsquery('english', ?1)
                ORDER BY score DESC
                LIMIT ?3
                """;
        }

        @SuppressWarnings("unchecked")
        List<Object[]> rows = entityManager
                .createNativeQuery(sql)
                .setParameter(1, query)
                .setParameter(2, userId)
                .setParameter(3, limit)
                .getResultList();

        return rows.stream()
                .map(row -> new FullTextSearchResult(
                        row[0].toString(),  // entry_id
                        row[1].toString(),  // conversation_id
                        ((Number) row[2]).doubleValue(),  // score
                        (String) row[3]  // highlight
                ))
                .toList();
    }

    public record FullTextSearchResult(
        String entryId,
        String conversationId,
        double score,
        String highlight
    ) {}
}
```

### Phase 3: Update PgVectorStore Fallback

Replace the inefficient keyword search fallback with full-text search:

```java
@ApplicationScoped
public class PgVectorStore implements VectorStore {

    @Inject FullTextSearchRepository fullTextSearchRepository;

    @Override
    public SearchResultsDto search(String userId, SearchEntriesRequest request) {
        // ... existing vector search code ...

        // Fall back to full-text search if vector search returns no results
        if (vectorResults.isEmpty()) {
            LOG.debug("Vector search returned no results, falling back to full-text search");
            return fullTextSearch(userId, request);
        }

        // ... build results ...
    }

    private SearchResultsDto fullTextSearch(String userId, SearchEntriesRequest request) {
        int limit = request.getLimit() != null ? request.getLimit() : 20;
        boolean groupByConversation =
                request.getGroupByConversation() == null || request.getGroupByConversation();

        List<FullTextSearchResult> ftsResults = fullTextSearchRepository.search(
                userId,
                request.getQuery(),
                limit + 1,
                groupByConversation);

        List<SearchResultDto> resultsList = new ArrayList<>();
        for (FullTextSearchResult fts : ftsResults) {
            if (resultsList.size() >= limit) {
                break;
            }
            SearchResultDto dto = buildSearchResultDtoFromFts(fts, request.getIncludeEntry());
            if (dto != null) {
                resultsList.add(dto);
            }
        }

        SearchResultsDto result = new SearchResultsDto();
        result.setResults(resultsList);

        if (ftsResults.size() > limit && !resultsList.isEmpty()) {
            result.setNextCursor(resultsList.get(resultsList.size() - 1).getEntryId());
        }

        return result;
    }
}
```

### Phase 4: Deprecate Old Implementation

Mark `PostgresMemoryStore.searchEntries()` as deprecated:

```java
/**
 * @deprecated Use {@link PgVectorStore#search(String, SearchEntriesRequest)} instead.
 * This method uses inefficient in-memory filtering.
 */
@Deprecated(forRemoval = true)
@Override
public SearchResultsDto searchEntries(String userId, SearchEntriesRequest request) {
    // ... existing implementation ...
}
```

## Database Schema Changes

### New Liquibase Changeset

Add to `db.changelog-master.yaml`:

```yaml
- changeSet:
    id: 3-fulltext-search-schema
    author: memory-service
    preConditions:
      - onFail: MARK_RAN
      - dbms:
          type: postgresql
    changes:
      - sqlFile:
          path: db/fulltext-schema.sql
          relativeToChangelogFile: false
```

Create `db/fulltext-schema.sql`:

```sql
-- Full-text search support for keyword fallback
-- Uses PostgreSQL's built-in tsvector with GIN index

-- Add generated tsvector column for full-text search
-- This is automatically maintained when indexed_content changes
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'entries' AND column_name = 'indexed_content_tsv'
    ) THEN
        ALTER TABLE entries ADD COLUMN indexed_content_tsv tsvector
            GENERATED ALWAYS AS (to_tsvector('english', COALESCE(indexed_content, ''))) STORED;
    END IF;
END $$;

-- GIN index for fast full-text search
CREATE INDEX IF NOT EXISTS idx_entries_indexed_content_fts
    ON entries USING GIN (indexed_content_tsv);
```

## Highlighting Behavior

### Vector Search vs Full-Text Search Highlights

| Search Type | Highlight Source | Behavior |
|-------------|------------------|----------|
| Vector search | First 200 chars of indexed_content | Static prefix, no query context |
| Full-text search | `ts_headline()` | Query-aware, shows matching terms |

### ts_headline Configuration

```sql
ts_headline('english', indexed_content, query,
    'StartSel=<mark>, StopSel=</mark>, MaxWords=50, MinWords=20')
```

| Option | Value | Effect |
|--------|-------|--------|
| StartSel | `<mark>` | HTML tag to highlight match start |
| StopSel | `</mark>` | HTML tag to highlight match end |
| MaxWords | 50 | Maximum words in highlight |
| MinWords | 20 | Minimum words to show around match |

**Example Output:**
```
Query: "API gateway"
Content: "This document describes how to configure the API gateway for production..."
Highlight: "how to configure the <mark>API</mark> <mark>gateway</mark> for production environments"
```

## Performance Comparison

| Metric | Before (In-Memory) | After (GIN Index) |
|--------|-------------------|-------------------|
| Query complexity | O(E) | O(log N) |
| Memory usage | O(E) - all entries | O(L) - limit only |
| Index type | None | GIN (inverted) |
| Decryption needed | Every entry | Matched entries only |
| Highlighting | None | Query-aware snippets |

### Benchmark Targets

| Scenario | Before | After |
|----------|--------|-------|
| 10K entries, top 20 | ~500ms | <20ms |
| 100K entries, top 20 | ~5s | <50ms |
| 1M entries, top 20 | OOM | <100ms |

## Search Quality Comparison

| Query | Vector Search | Full-Text Search |
|-------|---------------|------------------|
| "deployment pipeline" | Matches "CI/CD workflow" | Matches "deployment", "pipeline" |
| "running tests" | Matches "test execution" | Matches "run", "test" (stemmed) |
| "config" | Matches "configuration", "settings" | Matches "config*" |
| Typos ("deploiment") | May match semantically | No match |

**Takeaway**: Full-text search is a good fallback for exact/stemmed matching when vector search isn't available, but doesn't provide semantic similarity.

## Scope of Changes

| File | Change |
|------|--------|
| `openapi.yml` | Add `searchType` parameter to SearchConversationsRequest |
| `SearchEntriesRequest.java` | Add `searchType` field |
| `SearchResource.java` | Pass through `searchType` to internal request |
| `db/fulltext-schema.sql` | New: tsvector column + GIN index |
| `db.changelog-master.yaml` | Add changeset for fulltext schema |
| `FullTextSearchRepository.java` | New: Repository for FTS queries |
| `PgVectorStore.java` | Add search type routing, semantic/fulltext methods |
| `SearchTypeUnavailableException.java` | New: Exception for unavailable search type |
| `SearchTypeUnavailableExceptionMapper.java` | New: JAX-RS exception mapper |
| `application.properties` | Add `memory-service.search.*.enabled` config |
| `PostgresMemoryStore.java` | Deprecate `searchEntries()` |

## Testing

### Unit Tests

```java
@Test
void fullTextSearch_findsStemmedMatches() {
    // Given
    createEntryWithIndexedContent("conv-1", "entry-1", "The user is running tests");

    // When searching with different word form
    var results = fullTextSearchRepository.search(userId, "run test", 10, false);

    // Then stemming matches
    assertThat(results).hasSize(1);
    assertThat(results.get(0).score()).isGreaterThan(0);
}

@Test
void fullTextSearch_providesHighlights() {
    // Given
    createEntryWithIndexedContent("conv-1", "entry-1",
        "Configure the API gateway for production deployment");

    // When
    var results = fullTextSearchRepository.search(userId, "API gateway", 10, false);

    // Then highlight contains marked terms
    assertThat(results.get(0).highlight()).contains("<mark>API</mark>");
    assertThat(results.get(0).highlight()).contains("<mark>gateway</mark>");
}

@Test
void fullTextSearch_respectsAccessControl() {
    // Given entry user doesn't have access to
    createEntryWithIndexedContent("private-conv", "entry-1", "Secret API docs");

    // When searching
    var results = fullTextSearchRepository.search(userWithoutAccess, "API", 10, false);

    // Then no results
    assertThat(results).isEmpty();
}

@Test
void fullTextSearch_groupsByConversation() {
    // Given multiple entries in same conversation
    createEntryWithIndexedContent("conv-1", "entry-1", "API configuration guide");
    createEntryWithIndexedContent("conv-1", "entry-2", "Advanced API setup");

    // When searching with grouping
    var results = fullTextSearchRepository.search(userId, "API", 10, true);

    // Then one result per conversation
    assertThat(results).hasSize(1);
}
```

### Search Type Tests

```java
@Test
void search_withSemanticType_usesVectorSearch() {
    // Given indexed entries with embeddings
    indexEntry("conv-1", "entry-1", "API configuration guide");

    // When searching with semantic type
    var request = new SearchEntriesRequest()
        .query("API setup")
        .searchType("semantic");
    var results = vectorStore.search(userId, request);

    // Then uses vector similarity (score varies)
    assertThat(results.getResults()).isNotEmpty();
    assertThat(results.getResults().get(0).getScore()).isLessThan(1.0);
}

@Test
void search_withFulltextType_usesGinIndex() {
    // Given indexed entries
    createEntryWithIndexedContent("conv-1", "entry-1", "API configuration guide");

    // When searching with fulltext type
    var request = new SearchEntriesRequest()
        .query("API")
        .searchType("fulltext");
    var results = vectorStore.search(userId, request);

    // Then uses FTS (highlight contains marks)
    assertThat(results.getResults()).isNotEmpty();
    assertThat(results.getResults().get(0).getHighlights()).contains("<mark>");
}

@Test
void search_withSemanticType_whenDisabled_returnsError() {
    // Given semantic search is disabled
    // (via config: memory-service.search.semantic.enabled=false)

    // When searching with semantic type
    var request = new SearchEntriesRequest()
        .query("API")
        .searchType("semantic");

    // Then error with available types
    assertThatThrownBy(() -> vectorStore.search(userId, request))
        .isInstanceOf(SearchTypeUnavailableException.class)
        .satisfies(e -> {
            var ex = (SearchTypeUnavailableException) e;
            assertThat(ex.getAvailableTypes()).contains("fulltext");
        });
}

@Test
void search_withAutoType_fallsBackToFulltext() {
    // Given entry with indexed content but no embedding
    createEntryWithIndexedContent("conv-1", "entry-1", "deployment pipeline");

    // When searching with auto type (default)
    var request = new SearchEntriesRequest()
        .query("deployment");
    var results = vectorStore.search(userId, request);

    // Then falls back to fulltext and finds results
    assertThat(results.getResults()).isNotEmpty();
}
```

### Integration Tests (Cucumber)

```gherkin
Feature: Full-text search fallback

  Scenario: Full-text search with stemming
    Given a conversation with entry containing "The application is running smoothly"
    And the entry has indexed content
    When I search for "run application"
    Then I should receive results
    And the highlight should contain "<mark>running</mark>"

  Scenario: Full-text search respects access control
    Given user "alice" has a conversation with indexed content "secret data"
    And user "bob" does not have access to that conversation
    When "bob" searches for "secret"
    Then "bob" should receive no results

  Scenario: Full-text search groups by conversation
    Given a conversation with 3 entries all containing "deployment"
    When I search for "deployment" with groupByConversation=true
    Then I should receive 1 result

  Scenario: Explicit search type - semantic
    Given a conversation with indexed and embedded entry "API gateway configuration"
    When I search for "API setup" with searchType="semantic"
    Then I should receive results with semantic similarity scores

  Scenario: Explicit search type - fulltext
    Given a conversation with indexed entry "API gateway configuration"
    When I search for "API" with searchType="fulltext"
    Then I should receive results with highlighted matches

  Scenario: Search type unavailable error
    Given semantic search is disabled on the server
    When I search for "test" with searchType="semantic"
    Then I should receive a 501 error
    And the error should indicate "semantic" is unavailable
    And the error should list "fulltext" as available
```

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Index build time on large tables | Run migration during maintenance window |
| Storage overhead | ~10-20% of indexed_content size for tsvector |
| Language mismatch | Make dictionary configurable (`english` default) |
| Empty indexed_content | `COALESCE` handles NULL gracefully |

## Configuration

```properties
# Search method availability (default: both enabled)
# Set to false to disable a search method and return errors when requested
memory-service.search.semantic.enabled=true
memory-service.search.fulltext.enabled=true

# Full-text search configuration
# Dictionary for stemming and stop words (default: english)
memory-service.fulltext.dictionary=english

# Highlight configuration
memory-service.fulltext.highlight.max-words=50
memory-service.fulltext.highlight.min-words=20
memory-service.fulltext.highlight.start-sel=<mark>
memory-service.fulltext.highlight.stop-sel=</mark>
```

### Configuration Scenarios

| Scenario | semantic.enabled | fulltext.enabled | Behavior |
|----------|------------------|------------------|----------|
| Full functionality | true | true | Both available, auto falls back |
| Vector-only | true | false | Only semantic, fulltext returns 501 |
| PostgreSQL FTS only | false | true | Only fulltext, semantic returns 501 |
| Search disabled | false | false | All explicit types return 501, auto returns empty |

## Future Considerations

- **Multi-language support**: Detect language per entry, use appropriate dictionary
- **Phrase matching**: Support `"exact phrase"` queries
- **Boolean operators**: Support `AND`, `OR`, `NOT` in queries
- **Prefix matching**: Support `config*` wildcard queries (requires pg_trgm)
- **Hybrid ranking**: Combine vector similarity with FTS rank for better results
