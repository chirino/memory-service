---
layout: ../../../layouts/DocsLayout.astro
title: Semantic Search
description: Search conversations using vector similarity.
---

Memory Service provides semantic search capabilities, allowing you to find relevant messages across all conversations based on meaning rather than exact keyword matches.

## How It Works

1. **Embedding** - Messages are converted to vector embeddings using an AI model
2. **Indexing** - Vectors are stored in a vector database
3. **Querying** - Search queries are embedded and compared against stored vectors
4. **Ranking** - Results are returned sorted by similarity score

## Configuration

### Enable Semantic Search

```properties
# Vector store configuration
memory-service.vector-store.type=pgvector  # or mongodb

# Embedding configuration
memory-service.embedding.model=text-embedding-ada-002
memory-service.embedding.api-key=${OPENAI_API_KEY}
memory-service.embedding.dimension=1536
```

### Supported Vector Stores

| Store | Best For |
|-------|----------|
| **pgvector** | PostgreSQL users, simpler setup |
| **MongoDB Atlas** | MongoDB users, integrated solution |

## Performing Searches

### REST API

```bash
curl -X POST http://localhost:8080/api/v1/search \
  -H "Content-Type: application/json" \
  -d '{
    "query": "How do I configure authentication?",
    "limit": 10,
    "minScore": 0.7
  }'
```

### Response

```json
{
  "results": [
    {
      "messageId": "msg-123",
      "conversationId": "conv-456",
      "content": "To configure authentication, you need to...",
      "score": 0.92,
      "metadata": {}
    }
  ]
}
```

### Java API

```java
@Inject
SearchService searchService;

public List<SearchResult> findRelevant(String query) {
    return searchService.search(SearchRequest.builder()
        .query(query)
        .limit(10)
        .minScore(0.7)
        .build());
}
```

## Search Options

| Option | Description | Default |
|--------|-------------|---------|
| `query` | Search text (required) | - |
| `limit` | Maximum results | 10 |
| `minScore` | Minimum similarity (0-1) | 0.5 |
| `conversationIds` | Filter to specific conversations | all |
| `messageTypes` | Filter by message type | all |
| `after` | Only messages after timestamp | - |
| `before` | Only messages before timestamp | - |

## Filtering Results

### By Conversation

```bash
curl -X POST http://localhost:8080/api/v1/search \
  -d '{
    "query": "authentication",
    "conversationIds": ["conv-1", "conv-2"]
  }'
```

### By Message Type

```bash
curl -X POST http://localhost:8080/api/v1/search \
  -d '{
    "query": "error handling",
    "messageTypes": ["AI"]
  }'
```

### By Time Range

```bash
curl -X POST http://localhost:8080/api/v1/search \
  -d '{
    "query": "deployment",
    "after": "2024-01-01T00:00:00Z"
  }'
```

## Use Cases

### 1. Knowledge Retrieval

Find relevant past conversations to inform current responses:

```java
// Get context for the current question
List<SearchResult> context = searchService.search(
    SearchRequest.builder()
        .query(userQuestion)
        .limit(5)
        .build()
);

// Include in system prompt
String enhancedPrompt = buildPromptWithContext(context);
```

### 2. Duplicate Detection

Check if a question was already asked:

```java
List<SearchResult> similar = searchService.search(
    SearchRequest.builder()
        .query(newQuestion)
        .minScore(0.95)
        .limit(1)
        .build()
);

if (!similar.isEmpty()) {
    // Return cached response or reference existing conversation
}
```

### 3. Analytics

Find patterns in conversations:

```java
// Find all conversations about errors
List<SearchResult> errorConvs = searchService.search(
    SearchRequest.builder()
        .query("error exception failed")
        .messageTypes(List.of("USER"))
        .build()
);
```

## Performance Tips

1. **Index selectively** - Not all messages need embedding (e.g., skip system messages)
2. **Use filters** - Narrow search scope with conversation IDs or time ranges
3. **Tune minScore** - Higher scores = more relevant but fewer results
4. **Batch embeddings** - Embed multiple messages in one API call

## Next Steps

- Configure [Vector Stores](/docs/deployment/databases/)
- Learn about the [REST API](/docs/integrations/rest-api/)
