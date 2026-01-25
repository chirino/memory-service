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
curl -X POST http://localhost:8080/v1/user/search/messages \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "query": "How do I configure authentication?",
    "topK": 10
  }'
```

### Response

```json
{
  "data": [
    {
      "message": {
        "id": "msg_01HF8XJQWXYZ9876ABCD5430",
        "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
        "channel": "history",
        "content": [{"type": "text", "text": "To configure authentication..."}],
        "createdAt": "2025-01-10T14:32:05Z"
      },
      "score": 0.93,
      "highlights": "configure authentication"
    }
  ]
}
```

## Search Options

| Option | Description | Default |
|--------|-------------|---------|
| `query` | Natural language search text (required) | - |
| `topK` | Maximum results to return | 20 |
| `conversationIds` | Filter to specific conversations | all |
| `before` | Only messages before this message ID | - |

## Filtering Results

### By Conversation

```bash
curl -X POST http://localhost:8080/v1/user/search/messages \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "query": "authentication",
    "conversationIds": ["conv_01HF8XH1XABCD1234EFGH5678", "conv_01HF8XH1XABCD1234EFGH5679"]
  }'
```

### With Temporal Filter

```bash
curl -X POST http://localhost:8080/v1/user/search/messages \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "query": "deployment",
    "before": "msg_01HF8XJQWXYZ9876ABCD5431"
  }'
```

## Use Cases

### 1. Knowledge Retrieval

Find relevant past conversations to inform current agent responses using RAG (Retrieval Augmented Generation).

### 2. Duplicate Detection

Check if a similar question was already asked to avoid redundant conversations.

### 3. Analytics

Find patterns across conversations by searching for common themes or issues.

## Performance Tips

1. **Use filters** - Narrow search scope with conversation IDs
2. **Adjust topK** - Request only as many results as needed
3. **Use highlights** - The `highlights` field shows matching text snippets

## Next Steps

- Configure [Vector Stores](/docs/deployment/databases/)
- View the [API Contracts](/docs/api-contracts/)
