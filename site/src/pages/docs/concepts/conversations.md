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

Conversations are created automatically when you first add a message:

```java
ChatMemory memory = memoryProvider.get("my-conversation-id");
memory.add(UserMessage.from("Hello!"));
// Conversation is now persisted
```

Or explicitly via the API:

```bash
curl -X POST http://localhost:8080/api/v1/conversations \
  -H "Content-Type: application/json" \
  -d '{"id": "my-conversation-id", "metadata": {"topic": "support"}}'
```

### Retrieving a Conversation

```java
// Get all messages in a conversation
List<ChatMessage> messages = memory.messages();
```

### Deleting a Conversation

```bash
curl -X DELETE http://localhost:8080/api/v1/conversations/my-conversation-id
```

## Conversation Properties

| Property | Description |
|----------|-------------|
| `id` | Unique identifier (string) |
| `ownerId` | User who owns the conversation |
| `createdAt` | Creation timestamp |
| `updatedAt` | Last modification timestamp |
| `metadata` | Custom key-value pairs |
| `parentId` | ID of parent conversation (if forked) |
| `forkPoint` | Message index where fork occurred |

## Best Practices

1. **Use meaningful IDs** - Include context like user ID or session ID
2. **Set metadata** - Tag conversations for easier filtering
3. **Consider retention** - Implement cleanup policies for old conversations
4. **Handle pagination** - Use limit/offset for conversations with many messages

## Next Steps

- Learn about [Messages](/docs/concepts/messages/)
- Understand [Conversation Forking](/docs/concepts/forking/)
