---
layout: ../../../layouts/DocsLayout.astro
title: Messages
description: Understanding messages in Memory Service.
---

Messages are the individual units of communication within a conversation. Memory Service stores messages with full context and metadata.

## Message Types

Memory Service supports different message types:

| Type | Description |
|------|-------------|
| `USER` | Messages from the end user |
| `AI` | Responses from the AI model |
| `SYSTEM` | System prompts and instructions |
| `TOOL_EXECUTION` | Results from tool/function calls |

## Message Structure

Each message contains:

```json
{
  "id": "msg-123",
  "conversationId": "conv-456",
  "type": "USER",
  "content": "What's the weather like?",
  "timestamp": "2024-01-15T10:30:00Z",
  "metadata": {
    "client": "web",
    "language": "en"
  }
}
```

## Adding Messages

### Using LangChain4j Integration

```java
ChatMemory memory = memoryProvider.get(conversationId);

// Add a user message
memory.add(UserMessage.from("Hello, how can I help?"));

// Add an AI response
memory.add(AiMessage.from("I'm here to assist you!"));
```

### Using the REST API

```bash
curl -X POST http://localhost:8080/api/v1/conversations/conv-123/messages \
  -H "Content-Type: application/json" \
  -d '{
    "type": "USER",
    "content": "Hello!",
    "metadata": {"source": "api"}
  }'
```

## Message Ordering

Messages are stored with:

- **Timestamp** - When the message was created
- **Sequence number** - Order within the conversation

This ensures consistent replay even if timestamps are identical.

## Embeddings

When semantic search is enabled, messages are automatically:

1. Processed by the configured embedding model
2. Stored in the vector store
3. Indexed for similarity search

```properties
# Configure embedding model
memory-service.embedding.model=text-embedding-ada-002
memory-service.embedding.api-key=${OPENAI_API_KEY}
```

## Retrieving Messages

### All Messages

```java
List<ChatMessage> allMessages = memory.messages();
```

### With Pagination

```bash
curl "http://localhost:8080/api/v1/conversations/conv-123/messages?limit=20&offset=0"
```

### By Time Range

```bash
curl "http://localhost:8080/api/v1/conversations/conv-123/messages?after=2024-01-01T00:00:00Z"
```

## Next Steps

- Learn about [Semantic Search](/docs/concepts/semantic-search/)
- Understand [Conversation Forking](/docs/concepts/forking/)
