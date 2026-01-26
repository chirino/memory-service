---
layout: ../../../layouts/DocsLayout.astro
title: Conversation Forking
description: Create branches in conversation history with forking.
---

Conversation forking allows you to create a new conversation branch from any point in an existing conversation, preserving the original while exploring alternative paths.

## What is Forking?

Forking creates a copy of a conversation up to, but not including, the specified user message.  Forking allows you to:

- **Explore alternatives** - Try different responses or approaches
- **Debug issues** - Isolate problematic conversation states
- **A/B test** - Compare different agent behaviors
- **Recover from errors** - Branch from before an error occurred

## How Forking Works

The forked conversation:

- Gets a new new conversation ID
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

## Conversation Groups

A **conversation group** is a logical grouping that links an original conversation with all of its forks. Every conversation belongs to exactly one group, identified by its `conversationGroupId`.

### How Groups Work

When you create a conversation, it is automatically assigned to its own group (where the group ID equals the conversation ID). 

```
conversationGroupId: f81d4fae-7dec-11d0-a765-00a0c91e6bf6
│
└── Fork 1 
    └── conversationId: f81d4fae-7dec-11d0-a765-00a0c91e6bf6
```

When you fork a conversation, the new fork inherits the same `conversationGroupId` as the original, linking them together.

```
conversationGroupId: f81d4fae-7dec-11d0-a765-00a0c91e6bf6
│
├── Fork 1 
│   └── conversationId: f81d4fae-7dec-11d0-a765-00a0c91e6bf6
│
└── Fork 2 
    └── conversationId: 64ac96aa-c0c7-47a9-8035-dd61ef7164f2
```

All the conversations that have a common ancestor share the same group ID, making it easy to find related conversations.

### Why Groups Matter

Conversation groups enable:

- **Finding related conversations** - Query all forks of an original conversation
- **Understanding lineage** - Track the family tree of forked conversations
- **UI navigation** - Build interfaces that show conversation branches and allow switching between them
- **Analytics** - Analyze how users explore different conversation paths

### Querying a Group

To retrieve all conversations in a group, use the `/forks` endpoint on any conversation in the group:

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/forks" \
  -H "Authorization: Bearer <token>"
```

This returns all conversations that share the same `conversationGroupId`, regardless of which conversation in the group you query.

## Use Cases

### 1. User Correction

Allow users to "go back" and try a different question. When a user wants to rephrase their last message, fork at that message to create a new branch.

### 2. Agent Development

Fork a conversation to test different prompts or agent behaviors without affecting the original conversation.

### 3. Parallel Exploration

Create multiple forks from the same point to explore different conversation paths simultaneously.

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
