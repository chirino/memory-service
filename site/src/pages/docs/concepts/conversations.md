---
layout: ../../../layouts/DocsLayout.astro
title: Conversations
description: Understanding conversations in Memory Service.
---

Conversations are the fundamental unit of organization in Memory Service. A conversation represents a sequence of messages between users, agents, and AI models.

## What is a Conversation?

A conversation in Memory Service is:

- A container for a sequence of **messages**
- Identified by a unique **conversation ID**
- Owned by a **user** (for access control)
- Optionally associated with **metadata**

## Conversation Lifecycle

### Creating a Conversation

```bash
curl -X POST http://localhost:8080/v1/conversations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"title": "Support chat", "metadata": {"topic": "support"}}'
```

Response:

```json
{
  "id": "conv_01HF8XH1XABCD1234EFGH5678",
  "title": "Support chat",
  "ownerUserId": "user_1234",
  "createdAt": "2025-01-10T14:32:05Z",
  "updatedAt": "2025-01-10T14:32:05Z",
  "accessLevel": "owner",
  "conversationGroupId": "conv_01HF8XH1XABCD1234EFGH5678"
}
```

### Retrieving a Conversation

```bash
curl http://localhost:8080/v1/conversations/{conversationId} \
  -H "Authorization: Bearer <token>"
```

### Listing Conversations

```bash
curl "http://localhost:8080/v1/conversations?limit=20" \
  -H "Authorization: Bearer <token>"
```

### Deleting a Conversation

```bash
curl -X DELETE http://localhost:8080/v1/conversations/{conversationId} \
  -H "Authorization: Bearer <token>"
```

## Conversation Properties

| Property | Description |
|----------|-------------|
| `id` | Unique identifier (string) |
| `title` | Optional conversation title |
| `ownerUserId` | User who owns the conversation |
| `createdAt` | Creation timestamp |
| `updatedAt` | Last modification timestamp |
| `lastMessagePreview` | Preview of the last message |
| `accessLevel` | Current user's access level (`owner`, `manager`, `writer`, `reader`) |
| `conversationGroupId` | Group ID shared by forked conversations |
| `forkedAtConversationId` | ID of conversation this was forked from (if forked) |
| `forkedAtMessageId` | Message ID where the fork occurred (if forked) |

## Best Practices

1. **Set metadata** - Tag conversations for easier filtering
2. **Handle pagination** - Use limit/offset for conversations with many messages

## Next Steps

- Learn about [Messages](/docs/concepts/messages/)
- Understand [Conversation Forking](/docs/concepts/forking/)
