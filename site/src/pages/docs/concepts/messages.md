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
  "memoryEpoch": null,
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
| `memoryEpoch` | Memory epoch number (for `memory` channel messages) |
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

## Next Steps

- Learn about [Semantic Search](/docs/concepts/semantic-search/)
- Understand [Conversation Forking](/docs/concepts/forking/)
