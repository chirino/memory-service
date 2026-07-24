---
layout: ../../../layouts/DocsLayout.astro
title: Conversation Forking
description: Create branches in conversation history with forking.
---

Conversation forking allows you to create a new conversation branch from an allowed entry boundary in an existing conversation, preserving the original while exploring alternative paths.

## What is Forking?

Forking creates a branch of a conversation up to, but not including, the specified fork-point entry. User-facing chat flows usually fork at a `history` entry. Trusted runtime clients can also fork at a client-visible `journal` entry for replay and debugging. Forking allows you to:

- **Explore alternatives** - Try different responses or approaches
- **Debug issues** - Isolate problematic conversation states
- **A/B test** - Compare different agent behaviors
- **Recover from errors** - Branch from before an error occurred

## How Forking Works

The forked conversation:

- Gets a new conversation ID
- References the parent conversation
- Can be modified independently

## Creating a Fork

A fork is created implicitly when you append the first entry to a new conversation with fork metadata. Include `forkedAtConversationId` and `forkedAtEntryId` in the entry request body — if the target conversation doesn't exist yet, the service auto-creates it as a fork.

`forkedAtEntryId` is the first parent entry to exclude from the new branch. If you omit it, the fork is a blank slate that inherits no parent entries.

Valid fork points are:

- `history` entries, usually the user message a chat UI is replacing.
- `journal` entries, when the authenticated client can read that journal entry.

`context` entries are not valid fork points because they are derived, epoch-scoped state rather than replayable event boundaries.

### Using the REST API

```bash
# Append an entry to a new conversation ID with fork metadata
curl -X POST "http://localhost:8080/v1/conversations/{newConversationId}/entries" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "forkedAtConversationId": "{originalConversationId}",
    "forkedAtEntryId": "{entryId}",
    "channel": "history",
    "contentType": "history",
    "content": [{"role": "USER", "text": "A different question"}]
  }'
```

The forked conversation inherits entries from the original up to (but not including) the specified entry.

## Fork Properties

When you fork a conversation, the new conversation has:

| Property                 | Description                                    |
| ------------------------ | ---------------------------------------------- |
| `forkedAtConversationId` | ID of the conversation where the fork occurred |
| `forkedAtEntryId`        | Entry ID at which the fork diverged            |
| `ownerUserId`            | Same owner as the original conversation        |

## Fork Trees

When you create a conversation, the service internally groups it with any future forks. When you fork a conversation, the new fork is linked to the original. All conversations that share a common ancestor belong to the same fork tree.

Use the `/forks` endpoint on any conversation to obtain all accessible conversation IDs in the group and the fork controls relevant to that conversation's visible history and journal.

Deleting any conversation in a fork tree deletes the entire tree (root and all forks), along with associated entries and memberships.

### Querying Related Conversations

To retrieve fork navigation, use the `/forks` endpoint on the conversation being displayed:

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/forks" \
  -H "Authorization: Bearer <token>"
```

The `conversationIds` array contains the accessible group membership. Each `forkPoints` item identifies a history or journal entry visible in the requested conversation and the alternative conversation/entry options at that position. Journal points are client-scoped: user navigation returns them only to the authenticated client that can read the journal entry, while admin navigation includes every journal point.

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

This returns the complete group ID list and the fork points relevant to the requested conversation. Each continuation uses its first navigation-visible history or journal entry as the active display location. The response is intentionally not paginated so clients can index fork controls before paging entries in either direction.

## Limitations

- The forked entry itself is not included in the new conversation

## Next Steps

- Learn about [Indexing & Search](/docs/concepts/indexing-and-search/)
- Explore the [API Contracts](/docs/api-contracts/)
