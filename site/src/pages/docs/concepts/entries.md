---
layout: ../../../layouts/DocsLayout.astro
title: Entries
description: Understanding entries in Memory Service.
---

Entries are the individual units of communication within a conversation. Memory Service stores entries with full context and metadata.

## Entry Channels

Entries are organized into logical channels within a conversation:

| Channel   | Description                                                            |
| --------- | ---------------------------------------------------------------------- |
| `history` | User-visible conversation between users and agents                     |
| `context` | Agent context entries, scoped to the authenticated client ID and epoch |
| `journal` | Opaque agent execution records, scoped to the authenticated client ID  |

## Entry Structure

Each entry contains:

```json
{
  "id": "entry_01HF8XJQWXYZ9876ABCD5432",
  "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
  "userId": "user_1234",
  "channel": "history",
  "contentType": "history",
  "epoch": null,
  "seq": 42,
  "content": [
    {
      "role": "USER",
      "text": "What's the weather like?"
    }
  ],
  "createdAt": "2025-01-10T14:40:12Z"
}
```

| Property         | Description                                                           |
| ---------------- | --------------------------------------------------------------------- |
| `id`             | Unique entry identifier                                               |
| `conversationId` | ID of the parent conversation                                         |
| `userId`         | Human user associated with the entry                                  |
| `channel`        | Logical channel (`history`, `context`, or `journal`)                  |
| `contentType`    | Type of content (e.g., `history`, `history/lc4j`, or `agent/step`)    |
| `epoch`          | Context epoch number (for `context` channel entries)                  |
| `seq`            | Optional sequence number, unique within the conversation when present |
| `content`        | Array of content blocks (opaque, agent-defined)                       |
| `createdAt`      | Creation timestamp                                                    |

## Adding Entries

```bash
curl -X POST http://localhost:8080/v1/conversations/{conversationId}/entries \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "channel": "history",
    "contentType": "history",
    "content": [{"role": "USER", "text": "Hello!"}]
  }'
```

Response:

```json
{
  "id": "entry_01HF8XJQWXYZ9876ABCD5432",
  "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
  "userId": "user_1234",
  "channel": "history",
  "contentType": "history",
  "content": [{ "role": "USER", "text": "Hello!" }],
  "createdAt": "2025-01-10T14:40:12Z"
}
```

## Entry Ordering

Entries are returned ordered by creation time (`createdAt`). When multiple entries have the same timestamp, entries without `seq` sort first, followed by sequenced entries in ascending `seq`, then `id`. Use `afterCursor` for forward pagination, `tail=true` to open at the newest page, and `beforeCursor` to load older pages. All returned pages remain chronological.

Clients that need deterministic replay across channels can set `seq` when appending entries. `seq` values are unsigned 32-bit integers and must be unique within a conversation. When listing entries with `fromSeq`, Memory Service returns entries with `seq >= fromSeq`, excludes entries without `seq`, and orders the response by `seq` ascending.

## Retrieving Entries

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/entries?limit=50&channel=history" \
  -H "Authorization: Bearer <token>"
```

For a chat UI that opens at the bottom of history, request the tail and then
follow `beforeCursor` while the user scrolls upward:

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/entries?channel=history&tail=true&limit=50" \
  -H "Authorization: Bearer <token>"
```

`afterCursor`, `beforeCursor`, and `tail=true` are mutually exclusive. Entry
filters—including channel, fork ancestry, epoch, `upToEntryId`, and
`fromSeq`—are applied before pagination.

Response:

```json
{
  "data": [
    {
      "id": "entry_01HF8XJQWXYZ9876ABCD5432",
      "conversationId": "conv_01HF8XH1XABCD1234EFGH5678",
      "userId": "user_1234",
      "channel": "history",
      "contentType": "history",
      "content": [{ "role": "USER", "text": "Hello!" }],
      "createdAt": "2025-01-10T14:40:12Z"
    }
  ],
  "nextCursor": "entry_01HF8XJQWXYZ9876ABCD5433"
}
```

Query parameters:

- `limit` - Maximum entries to return (default: 50)
- `after` - Cursor for pagination (entry ID)
- `fromSeq` - Return sequenced entries with `seq >= fromSeq`
- `channel` - Filter by channel: `history` (default for end-user reads), `context`, or `journal`
- `epoch` - For `context` channel: `latest`, `all`, or a specific epoch number

Explicit `channel=context` and `channel=journal` reads require an authenticated client identity. User-only requests without a client identity default to `history`.

## Journal Entries

Use `journal` for structured execution records that should not become user-visible chat history: tool calls, model calls, planner steps, or other replay/debug state. Journal entries are client-scoped like context entries, but they are not epoch-scoped.

```bash
curl -X POST http://localhost:8080/v1/conversations/{conversationId}/entries \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <agent-token>" \
  -d '{
    "channel": "journal",
    "contentType": "agent/step",
    "seq": 43,
    "content": [{"stepType": "tool_call", "tool": "search"}]
  }'
```

`indexedContent` is accepted only on `history` entries. Journal and context entries are not indexed for search.

## Context Epochs

Context epochs provide a versioning mechanism for agent context that enables auditability and historical inspection of the agent's state at any point in time.

### How Epochs Work

When an agent stores entries in the `context` channel, each entry is tagged with an epoch number. Most updates to agent context simply add new entries to the current epoch. However, when an agent performs a **compaction** operation, where it resets or summarizes previous entries to reduce context size, the new entries are stored in the next epoch.

```
Epoch 0: [entry1, entry2, entry3, entry4, entry5]
         ↓ (compaction/summarization)
Epoch 1: [summary_entry, entry6, entry7]
         ↓ (compaction/summarization)
Epoch 2: [new_summary_entry, entry8, entry9, ...]
```

### Retrieving Entries by Epoch

You can query entries from different epochs using the `epoch` parameter:

| Value      | Description                                                             |
| ---------- | ----------------------------------------------------------------------- |
| `latest`   | Returns only entries from the most recent epoch (default for agent use) |
| `all`      | Returns entries from all epochs                                         |
| `{number}` | Returns entries from a specific epoch (e.g., `0`, `1`, `2`)             |

```bash
# Get latest epoch only (what the agent currently uses)
curl "http://localhost:8080/v1/conversations/{id}/entries?channel=context&epoch=latest"

# Get all entries across all epochs (full audit trail)
curl "http://localhost:8080/v1/conversations/{id}/entries?channel=context&epoch=all"

# Get entries from a specific epoch
curl "http://localhost:8080/v1/conversations/{id}/entries?channel=context&epoch=0"
```

### Auditability and Time Travel

The epoch system provides important benefits for observability and compliance:

- **Full Audit Trail**: By querying with `epoch=all`, you can see every entry the agent ever stored, including those that were later compacted or summarized. Nothing is ever deleted.

- **Point-in-Time Inspection**: You can inspect exactly what the agent's context looked like at any historical epoch. This is valuable for debugging agent behavior or understanding why the agent made certain decisions.

- **Compaction Transparency**: When an agent summarizes or compacts its context, both the original detailed entries (in earlier epochs) and the summarized version (in the new epoch) are preserved. You can compare them to verify that compaction preserved essential information.

- **Compliance Requirements**: For applications requiring audit trails, epochs ensure that even after memory optimizations, the complete history remains accessible for review.

### Example: Inspecting Agent Context Evolution

```bash
# See the agent's current working context
curl "...?channel=context&epoch=latest"
# Returns: [summary_of_early_conversation, recent_entry1, recent_entry2]

# Investigate what was in the original detailed context
curl "...?channel=context&epoch=0"
# Returns: [detailed_entry1, detailed_entry2, ..., detailed_entry50]

# Get the complete picture across all compactions
curl "...?channel=context&epoch=all"
# Returns: [detailed_entry1, ..., detailed_entry50, summary, recent_entry1, recent_entry2]
```

## Next Steps

- Learn about [Indexing & Search](/docs/concepts/indexing-and-search/)
- Understand [Conversation Forking](/docs/concepts/forking/)
