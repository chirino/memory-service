---
layout: ../../../layouts/DocsLayout.astro
title: Conversation Forking
description: Create branches in conversation history with forking.
---

Conversation forking allows you to create a new conversation branch from any point in an existing conversation, preserving the original while exploring alternative paths.

## What is Forking?

Forking creates a copy of a conversation up to, but not including, the specified user entry.  Forking allows you to:

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

Fork at a specific entry by calling the fork endpoint with the entry ID:

```bash
curl -X POST "http://localhost:8080/v1/conversations/{conversationId}/entries/{entryId}/fork" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"title": "Alternative approach"}'
```

The fork point must be an existing user-authored entry. The forked conversation will contain history up to (but not including) that entry.

## Fork Properties

When you fork a conversation, the new conversation has:

| Property | Description |
|----------|-------------|
| `forkedAtConversationId` | ID of the conversation where the fork occurred |
| `forkedAtEntryId` | Entry ID at which the fork diverged |
| `ownerUserId` | Same owner as the original conversation |

## Fork Trees

When you create a conversation, the service internally groups it with any future forks. When you fork a conversation, the new fork is linked to the original. All conversations that share a common ancestor belong to the same fork tree.

You don't need to know about this grouping directly. Use the `/forks` endpoint on any conversation to discover all related conversations in the tree.

Deleting any conversation in a fork tree deletes the entire tree (root and all forks), along with associated entries and memberships.

### Querying Related Conversations

To retrieve all conversations in a fork tree, use the `/forks` endpoint on any conversation in the tree:

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/forks" \
  -H "Authorization: Bearer <token>"
```

This returns all conversations in the same fork tree.

## Use Cases

### 1. User Correction

Allow users to "go back" and try a different question. When a user wants to rephrase their last entry, fork at that entry to create a new branch.

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

This returns all conversations in the same fork tree.

## Limitations

- Fork point must be an existing user-authored entry
- The forked entry itself is not included in the new conversation

## Next Steps

- Learn about [Semantic Search](/docs/concepts/semantic-search/)
- Explore the [API Contracts](/docs/api-contracts/)
