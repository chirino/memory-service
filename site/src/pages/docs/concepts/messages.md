---
layout: ../../../layouts/DocsLayout.astro
title: Messages
description: Understanding messages in Memory Service.
---

Messages are the individual units of communication within a conversation. Memory Service stores messages with full context and metadata.

## Message Channels

Messages are organized into logical channels within a conversation:

| Channel | Description |
|---------|-------------|
| `history` | User-visible conversation between users and agents |
| `memory` | Agent memory messages, scoped to the calling client ID |
| `summary` | Summarization messages (not visible in user-facing lists) |

## Message Structure

Each message contains:

```json
{
  "id": "msg_01HF8XJQWXYZ9876ABCD5432",
  "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
  "userId": "user_1234",
  "channel": "history",
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
| `id` | Unique message identifier |
| `conversationId` | ID of the parent conversation |
| `userId` | Human user associated with the message |
| `channel` | Logical channel (`history`, `memory`, `summary`) |
| `epoch` | Memory epoch number (for `memory` channel messages) |
| `content` | Array of content blocks (opaque, agent-defined) |
| `createdAt` | Creation timestamp |

## Adding Messages

```bash
curl -X POST http://localhost:8080/v1/conversations/{conversationId}/messages \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "channel": "history",
    "content": [{"type": "text", "text": "Hello!"}]
  }'
```

Response:

```json
{
  "id": "msg_01HF8XJQWXYZ9876ABCD5432",
  "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
  "userId": "user_1234",
  "channel": "history",
  "content": [{"type": "text", "text": "Hello!"}],
  "createdAt": "2025-01-10T14:40:12Z"
}
```

## Message Ordering

Messages are returned ordered by creation time (`createdAt`). Use cursor-based pagination with the `after` parameter to retrieve messages in batches.

## Retrieving Messages

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/messages?limit=50&channel=history" \
  -H "Authorization: Bearer <token>"
```

Response:

```json
{
  "data": [
    {
      "id": "msg_01HF8XJQWXYZ9876ABCD5432",
      "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
      "userId": "user_1234",
      "channel": "history",
      "content": [{"type": "text", "text": "Hello!"}],
      "createdAt": "2025-01-10T14:40:12Z"
    }
  ],
  "nextCursor": "msg_01HF8XJQWXYZ9876ABCD5433"
}
```

Query parameters:
- `limit` - Maximum messages to return (default: 50)
- `after` - Cursor for pagination (message ID)
- `channel` - Filter by channel: `history` (default), `memory`, or `summary`
- `epoch` - For `memory` channel: `latest`, `all`, or a specific epoch number

## Memory Epochs

Memory epochs provide a versioning mechanism for agent memory that enables auditability and historical inspection of the agent's state at any point in time.

### How Epochs Work

When an agent stores messages in the `memory` channel, each message is tagged with an epoch number. Most updates to agent memory simply add new messages to the current epoch. However, when an agent performs a **compaction** operation—where it resets or summarizes previous messages to reduce context size—the new messages are stored in the next epoch.

```
Epoch 0: [msg1, msg2, msg3, msg4, msg5]
         ↓ (compaction/summarization)
Epoch 1: [summary_msg, msg6, msg7]
         ↓ (compaction/summarization)  
Epoch 2: [new_summary_msg, msg8, msg9, ...]
```

### Retrieving Messages by Epoch

You can query messages from different epochs using the `epoch` parameter:

| Value | Description |
|-------|-------------|
| `latest` | Returns only messages from the most recent epoch (default for agent use) |
| `all` | Returns messages from all epochs |
| `{number}` | Returns messages from a specific epoch (e.g., `0`, `1`, `2`) |

```bash
# Get latest epoch only (what the agent currently uses)
curl "http://localhost:8080/v1/conversations/{id}/messages?channel=memory&epoch=latest"

# Get all messages across all epochs (full audit trail)
curl "http://localhost:8080/v1/conversations/{id}/messages?channel=memory&epoch=all"

# Get messages from a specific epoch
curl "http://localhost:8080/v1/conversations/{id}/messages?channel=memory&epoch=0"
```

### Auditability and Time Travel

The epoch system provides important benefits for observability and compliance:

- **Full Audit Trail**: By querying with `epoch=all`, you can see every message the agent ever stored, including those that were later compacted or summarized. Nothing is ever deleted.

- **Point-in-Time Inspection**: You can inspect exactly what the agent's memory looked like at any historical epoch. This is valuable for debugging agent behavior or understanding why the agent made certain decisions.

- **Compaction Transparency**: When an agent summarizes or compacts its memory, both the original detailed messages (in earlier epochs) and the summarized version (in the new epoch) are preserved. You can compare them to verify that compaction preserved essential information.

- **Compliance Requirements**: For applications requiring audit trails, epochs ensure that even after memory optimizations, the complete history remains accessible for review.

### Example: Inspecting Agent Memory Evolution

```bash
# See the agent's current working memory
curl "...?channel=memory&epoch=latest"
# Returns: [summary_of_early_conversation, recent_msg1, recent_msg2]

# Investigate what was in the original detailed memory
curl "...?channel=memory&epoch=0"
# Returns: [detailed_msg1, detailed_msg2, ..., detailed_msg50]

# Get the complete picture across all compactions
curl "...?channel=memory&epoch=all"
# Returns: [detailed_msg1, ..., detailed_msg50, summary, recent_msg1, recent_msg2]
```

## Next Steps

- Learn about [Semantic Search](/docs/concepts/semantic-search/)
- Understand [Conversation Forking](/docs/concepts/forking/)
