---
layout: ../../../layouts/DocsLayout.astro
title: Conversation Forking
description: Create branches in conversation history with forking.
---

Conversation forking allows you to create a new conversation branch from any point in an existing conversation, preserving the original while exploring alternative paths.

## What is Forking?

Forking creates a copy of a conversation up to a specific message, allowing you to:

- **Explore alternatives** - Try different responses or approaches
- **Debug issues** - Isolate problematic conversation states
- **A/B test** - Compare different agent behaviors
- **Recover from errors** - Branch from before an error occurred

## How Forking Works

```
Original: msg1 → msg2 → msg3 → msg4 → msg5
                  ↓
Fork at msg2:  msg1 → msg2 → (new messages...)
```

The forked conversation:
- Contains all messages up to the fork point
- Has a new unique ID
- References the parent conversation
- Can be modified independently

## Creating a Fork

### Using the REST API

Fork at a specific message by calling the fork endpoint with the message ID:

```bash
curl -X POST "http://localhost:8080/v1/conversations/{conversationId}/messages/{messageId}/fork" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"title": "Alternative approach"}'
```

The fork point must be an existing user-authored message. The forked conversation will contain history up to (but not including) that message.

## Fork Properties

When you fork a conversation, the new conversation has:

| Property | Description |
|----------|-------------|
| `forkedAtConversationId` | ID of the conversation where the fork occurred |
| `forkedAtMessageId` | Message ID at which the fork diverged |
| `conversationGroupId` | Shared group ID linking related conversations |
| `ownerUserId` | Same owner as the original conversation |

## Use Cases

### 1. User Correction

Allow users to "go back" and try a different question. When a user wants to rephrase their last message, fork at that message to create a new branch.

### 2. Agent Development

Fork a conversation to test different prompts or agent behaviors without affecting the original conversation.

### 3. Parallel Exploration

Create multiple forks from the same point to explore different conversation paths simultaneously.

## Best Practices

1. **Name forks meaningfully** - Include the purpose or variant in the ID
2. **Track lineage** - Use metadata to record why forks were created
3. **Clean up experiments** - Delete test forks when no longer needed
4. **Consider storage** - Forks duplicate messages, plan storage accordingly

## Listing Forks

To see all forks related to a conversation:

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/forks" \
  -H "Authorization: Bearer <token>"
```

This returns all forked conversations that share the same `conversationGroupId`.

## Limitations

- Fork point must be an existing user-authored message
- The forked message itself is not included in the new conversation

## Next Steps

- Learn about [Semantic Search](/docs/concepts/semantic-search/)
- Explore the [API Contracts](/docs/api-contracts/)
