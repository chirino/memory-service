---
layout: ../../../layouts/DocsLayout.astro
title: Indexing & Search
description: Understanding how search indexing, full-text search, and semantic search work in the Memory Service
---

The Memory Service provides a search system that lets users find relevant messages across all their conversations. Search supports both full-text keyword matching and semantic similarity.

## Overview

Search in the Memory Service is built around two ideas:

- **Indexed content** — A separate, searchable field on each entry
- **Search types** — Full-text search for keyword matching, semantic search for meaning-based retrieval, or automatic selection

Message content is stored encrypted at rest, so it can't be searched directly. The `indexedContent` field on each entry provides a searchable version of the message text. Client libraries populate this field when recording entries, giving your application a chance to redact sensitive information before it enters the search index in cleartext.

## How Indexing Works

When entries are created with an `indexedContent` field, the Memory Service indexes that text for search. Entries without `indexedContent` are stored normally but won't appear in search results.

For example, if a user sends a message containing sensitive data, your application can redact it in `indexedContent` while preserving the full text in `content`:

```bash
curl -X POST http://localhost:8080/v1/conversations/{conversationId}/entries \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "channel": "history",
    "contentType": "history",
    "content": [
      {"role": "USER", "text": "My card number is 4111-1111-1111-1111 and SSN is 123-45-6789"}
    ],
    "indexedContent": "My card number is [REDACTED] and SSN is [REDACTED]"
  }'
```

The full message is stored encrypted in `content`, while the redacted version in `indexedContent` is what appears in search results and highlights.

The `indexedContent` field is typically set by client-side code when creating entries. See the [Spring Boot](/docs/spring/indexing-and-search/) and [Quarkus](/docs/quarkus/indexing-and-search/) guides for how to automate this in your application.

## Search Types

The Memory Service supports three search modes:

| Search Type | How It Works | Best For |
|-------------|-------------|----------|
| **`fulltext`** | Keyword matching against indexed content | Exact terms, names, error messages |
| **`semantic`** | Vector similarity using embeddings | Conceptual queries, finding related discussions |
| **`auto`** | Automatically selects the best search type | General use (default) |

### Full-Text Search

Full-text search finds entries containing specific keywords or phrases. It uses standard text search indexing and returns results ranked by relevance. Match highlights show which parts of the text matched, using `==highlight==` markers.

### Semantic Search

Semantic search finds entries that are conceptually similar to the query, even if they don't share exact keywords. It works by:

1. Converting indexed content into vector embeddings using an AI model
2. Storing vectors in a vector database (pgvector or Qdrant)
3. Converting the search query into a vector
4. Finding entries with the most similar vectors

Semantic search requires a vector store to be configured. When no vector store is available, `auto` mode falls back to full-text search.

## Performing Searches

### Search Request

```bash
curl -X POST http://localhost:8080/v1/conversations/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "query": "How do I configure authentication?",
    "searchType": "auto",
    "limit": 20,
    "groupByConversation": true,
    "includeEntry": true
  }'
```

### Search Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `query` | string | *(required)* | The search query text |
| `searchType` | string | `"auto"` | `"auto"`, `"semantic"`, or `"fulltext"` |
| `limit` | integer | `20` | Maximum number of results to return |
| `groupByConversation` | boolean | `true` | Group results by conversation |
| `includeEntry` | boolean | `true` | Include the full entry in each result |

### Search Response

```json
{
  "data": [
    {
      "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
      "conversationTitle": "Setting up authentication",
      "entryId": "d15ab065-ab94-4856-8def-a040d6d2d9db",
      "score": 0.95,
      "highlights": ["How do I ==configure authentication==?"],
      "entry": {
        "id": "d15ab065-ab94-4856-8def-a040d6d2d9db",
        "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
        "userId": "bob",
        "channel": "history",
        "contentType": "history",
        "content": [{"role": "USER", "text": "How do I configure authentication?"}],
        "createdAt": "2025-01-10T14:32:05Z"
      }
    }
  ],
  "nextCursor": null
}
```

### Result Fields

| Field | Description |
|-------|-------------|
| `conversationId` | ID of the conversation containing the match |
| `conversationTitle` | Title of the conversation (for display) |
| `entryId` | ID of the matching entry (for deep-linking) |
| `score` | Relevance score |
| `highlights` | Matched text with `==highlight==` markers |
| `entry` | Full entry content (when `includeEntry` is true) |

## Use Cases

### Knowledge Retrieval (RAG)

Find relevant past conversations to inform current agent responses using Retrieval Augmented Generation. An agent can search across a user's conversation history to provide more contextual answers.

### Conversation Discovery

Let users search across all their conversations to find past discussions on a topic. Results link back to specific conversations and entries for easy navigation.

### Duplicate Detection

Before starting a new conversation, check if a similar question was already discussed to avoid redundant work.

## Security Considerations

### Access Control

Search results are scoped to the authenticated user. A user can only find entries in conversations they have access to (at least reader level). The search API enforces the same access control as direct conversation access.

### Content Redaction

The `indexedContent` field is populated by your application's client-side code, giving you full control over what enters the search index in cleartext. Use this to strip PII, credentials, or other sensitive data before indexing. See the framework-specific guides for implementation details.

## Implementation Guides

For framework-specific implementations and code examples:

- [Spring Boot Implementation](/docs/spring/indexing-and-search/) - Complete guide with Spring code examples
- [Quarkus Implementation](/docs/quarkus/indexing-and-search/) - Complete guide with Quarkus code examples

Both guides include:
- Configuring indexed content with redaction support
- Search endpoint setup
- Working curl examples with authentication

## API Operations

The Memory Service provides this search API:

- `POST /v1/conversations/search` - Search across conversations

## Related Concepts

- [Entries](/docs/concepts/entries/) - Understanding entries and the content that gets indexed
- [Conversations](/docs/concepts/conversations/) - How conversations organize entries
- [Sharing & Access Control](/docs/concepts/sharing/) - How access control affects search results
