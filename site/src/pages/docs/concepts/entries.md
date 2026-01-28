---
layout: ../../../layouts/DocsLayout.astro
title: Entries
description: Understanding entries in Memory Service.
---

Entries are the individual units of communication within a conversation. Memory Service stores entries with full context and metadata.

## Entry Channels

Entries are organized into logical channels within a conversation:

| Channel | Description |
|---------|-------------|
| `history` | User-visible conversation between users and agents |
| `memory` | Agent memory entries, scoped to the calling client ID |
| `summary` | Summarization entries (not visible in user-facing lists) |

## Entry Structure

Each entry contains:

```json
{
  "id": "entry_01HF8XJQWXYZ9876ABCD5432",
  "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
  "userId": "user_1234",
  "channel": "history",
  "contentType": "message",
  "epoch": null,
  "content": [
    {
      "type": "text",
      "text": "What's the weather like?"
    }
  ],
  "createdAt": "2025-01-10T14:40:12Z"
}
```

| Property | Description |
|----------|-------------|
| `id` | Unique entry identifier |
| `conversationId` | ID of the parent conversation |
| `userId` | Human user associated with the entry |
| `channel` | Logical channel (`history`, `memory`, `summary`) |
| `contentType` | Type of content (e.g., `message`) |
| `epoch` | Memory epoch number (for `memory` channel entries) |
| `content` | Array of content blocks (opaque, agent-defined) |
| `createdAt` | Creation timestamp |

## Adding Entries

```bash
curl -X POST http://localhost:8080/v1/conversations/{conversationId}/entries \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "channel": "history",
    "contentType": "message",
    "content": [{"type": "text", "text": "Hello!"}]
  }'
```

Response:

```json
{
  "id": "entry_01HF8XJQWXYZ9876ABCD5432",
  "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
  "userId": "user_1234",
  "channel": "history",
  "contentType": "message",
  "content": [{"type": "text", "text": "Hello!"}],
  "createdAt": "2025-01-10T14:40:12Z"
}
```

## Entry Ordering

Entries are returned ordered by creation time (`createdAt`). Use cursor-based pagination with the `after` parameter to retrieve entries in batches.

## Retrieving Entries

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/entries?limit=50&channel=history" \
  -H "Authorization: Bearer <token>"
```

Response:

```json
{
  "data": [
    {
      "id": "entry_01HF8XJQWXYZ9876ABCD5432",
      "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
      "userId": "user_1234",
      "channel": "history",
      "contentType": "message",
      "content": [{"type": "text", "text": "Hello!"}],
      "createdAt": "2025-01-10T14:40:12Z"
    }
  ],
  "nextCursor": "entry_01HF8XJQWXYZ9876ABCD5433"
}
```

Query parameters:
- `limit` - Maximum entries to return (default: 50)
- `after` - Cursor for pagination (entry ID)
- `channel` - Filter by channel: `history` (default), `memory`, or `summary`
- `epoch` - For `memory` channel: `latest`, `all`, or a specific epoch number

## Memory Epochs

Memory epochs provide a versioning mechanism for agent memory that enables auditability and historical inspection of the agent's state at any point in time.

### How Epochs Work

When an agent stores entries in the `memory` channel, each entry is tagged with an epoch number. Most updates to agent memory simply add new entries to the current epoch. However, when an agent performs a **compaction** operation—where it resets or summarizes previous entries to reduce context size—the new entries are stored in the next epoch.

```
Epoch 0: [entry1, entry2, entry3, entry4, entry5]
         ↓ (compaction/summarization)
Epoch 1: [summary_entry, entry6, entry7]
         ↓ (compaction/summarization)
Epoch 2: [new_summary_entry, entry8, entry9, ...]
```

### Retrieving Entries by Epoch

You can query entries from different epochs using the `epoch` parameter:

| Value | Description |
|-------|-------------|
| `latest` | Returns only entries from the most recent epoch (default for agent use) |
| `all` | Returns entries from all epochs |
| `{number}` | Returns entries from a specific epoch (e.g., `0`, `1`, `2`) |

```bash
# Get latest epoch only (what the agent currently uses)
curl "http://localhost:8080/v1/conversations/{id}/entries?channel=memory&epoch=latest"

# Get all entries across all epochs (full audit trail)
curl "http://localhost:8080/v1/conversations/{id}/entries?channel=memory&epoch=all"

# Get entries from a specific epoch
curl "http://localhost:8080/v1/conversations/{id}/entries?channel=memory&epoch=0"
```

### Auditability and Time Travel

The epoch system provides important benefits for observability and compliance:

- **Full Audit Trail**: By querying with `epoch=all`, you can see every entry the agent ever stored, including those that were later compacted or summarized. Nothing is ever deleted.

- **Point-in-Time Inspection**: You can inspect exactly what the agent's memory looked like at any historical epoch. This is valuable for debugging agent behavior or understanding why the agent made certain decisions.

- **Compaction Transparency**: When an agent summarizes or compacts its memory, both the original detailed entries (in earlier epochs) and the summarized version (in the new epoch) are preserved. You can compare them to verify that compaction preserved essential information.

- **Compliance Requirements**: For applications requiring audit trails, epochs ensure that even after memory optimizations, the complete history remains accessible for review.

### Example: Inspecting Agent Memory Evolution

```bash
# See the agent's current working memory
curl "...?channel=memory&epoch=latest"
# Returns: [summary_of_early_conversation, recent_entry1, recent_entry2]

# Investigate what was in the original detailed memory
curl "...?channel=memory&epoch=0"
# Returns: [detailed_entry1, detailed_entry2, ..., detailed_entry50]

# Get the complete picture across all compactions
curl "...?channel=memory&epoch=all"
# Returns: [detailed_entry1, ..., detailed_entry50, summary, recent_entry1, recent_entry2]
```

## Next Steps

- Learn about [Semantic Search](/docs/concepts/semantic-search/)
- Understand [Conversation Forking](/docs/concepts/forking/)
