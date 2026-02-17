---
status: partial
superseded-by:
  - 017-hide-conversation-groups.md
  - 046-simpler-forking.md
---

# Conversation Forking Data Model (Design Draft)

> **Status**: Partially implemented. Conversation groups hidden from public API by
> [017](017-hide-conversation-groups.md). Fork creation simplified to auto-create on first entry by
> [046](046-simpler-forking.md).

## Goals
- Support many messages per conversation and allow forking at any message.
- Fork creation must be cheap: no message copying when creating a fork.
- Each fork path gets its own `conversationId` to simplify API usage when appending HISTORY or MEMORY messages.
- The UI can load all messages across forks and filter to show only the active fork path.
- Forking can occur at any user message, and forks can be forked again (multi-level tree).

## Non-Goals (for now)
- Optimizing fork queries for massive datasets beyond basic indexing.
- Hard requirements for a full graph traversal API (can be added later if needed).

## Core Concepts
- **Conversation**: A logical thread with its own `conversationId`.
- **Conversation group**: A container for all forks that share a common origin. Every fork belongs to exactly one group.
- **Fork node**: A child conversation created from a specific message in a parent conversation. Conversations are part of the same fork node if they have the same `forked_at_message_id` and `forked_at_conversation_id`.
- **Fork path**: The chain of messages that represent a user-selected branch.

## Data Model Proposal (Conversation Groups + parent lineage)

### PostgreSQL: `conversation_groups` table
New table to represent a fork family:
- `id` (UUID, PK)
  - Created for every new conversation and shared by all forks in the group.
  - For the initial conversation, `conversation_groups.id == conversations.id` (implementation detail, not a semantic requirement).
- `created_at` (timestamp)

### PostgreSQL: `conversations` table
Add explicit fork metadata columns to the `conversations` table:
- `conversation_group_id` (UUID, NOT NULL, FK to `conversation_groups.id`)
  - Shared by all forks in the same tree.
- `forked_at_message_id` (UUID, nullable)
  - The message id immediately before the forked message in the parent path.
  - This must reference a HISTORY message in the `forked_at_conversation_id` path.
  - `NULL` when the fork happens at the first HISTORY message.
- `forked_at_conversation_id` (UUID, nullable)
  - The conversation id of the messages pointed to by `forked_at_conversation_id`. If `forked_at_conversation_id` is NULL then it should be the initial conversation in the group.
  - Enables multi-level lineage and avoids extra joins for tree traversal.

### PostgreSQL: `messages` table
No changes required to the `messages` table schema. Messages are stored per `conversation_id`.
- Drop `messages.parent_message_id` since it is not used in the fork model.
- Add `messages.conversation_group_id` (UUID, NOT NULL, FK to `conversation_groups.id`) to track the group for each message.
- `messages.conversation_id` stores the fork conversation id for the message.
- For messages on the initial conversation, `messages.conversation_group_id` equals `messages.conversation_id`.

### MongoDB: `conversationGroups` collection
New collection to represent a fork family:
- `_id` (string)
- `createdAt` (timestamp)

### MongoDB: `conversations` collection
Add the same fork metadata fields to the `conversations` documents:
- `conversationGroupId` (string, not null)
- `forkedAtMessageId` (string, nullable)
- `forkedAtConversationId` (string, nullable)

### Indexes
- PostgreSQL:
  - `conversations(conversation_group_id)`
  - `conversations(forked_at_conversation_id)`
  - `conversations(forked_at_message_id)`
  - `messages(conversation_id, created_at)`
  - `messages(conversation_group_id, created_at)`
- MongoDB:
  - `conversations.conversationGroupId`
  - `conversations.forkedAtConversationId`
  - `conversations.forkedAtMessageId`
  - `messages.conversationId` + `messages.createdAt`
  - `messages.conversationGroupId` + `messages.createdAt`

## Fork Creation (Cheap)
When forking at message `M` in conversation `P`:
1. Create new conversation `C` with:
   - `forked_at_conversation_id = P`
   - `conversation_group_id = group(P)`
   - `forked_at_message_id = previous(M)` (or `NULL` if `M` is the first HISTORY message)
2. Do not copy messages.
3. The client/agent appends new messages to `C` using the normal append APIs.

## UI Rendering Model
- The UI loads messages for the **active conversation** only.
- To show fork choices at a user message:
  - Fetch all conversations in the tree:
    - `conversation_group_id == activeGroupId`
  - Filter client-side to show only forks that:
    - share the same `forked_at_message_id` for parent-path grouping, and
    - are not the current conversation, and
    - are not ancestors (unless explicitly requested).

## API Implications
- `GET /v1/conversations/{id}` should return:
  - `conversationGroupId`
  - `forkedAtMessageId`
  - `forkedAtConversationId`
- `GET /v1/conversations/{id}/forks` should return:
  - all conversations whose `conversationGroupId` matches `{id}`'s group
  - include `forkedAtConversationId` for easier client filtering
- Forking endpoint:
  - `POST /v1/conversations/{conversationId}/messages/{messageId}/fork`
  - creates a new conversation `C`, returns `C` (no message is auto-appended)
  - only allowed when the target message is a HISTORY + USER message
  - server sets `forked_at_message_id` and `forked_at_conversation_id` at fork time

## Delete Semantics (Group Deletes)
- Deleting a conversation deletes its conversation group.
- Group deletion cascades to all conversations, messages, memberships, and other group-scoped rows.
- Individual messages are never deleted directly; they are removed only when a conversation is deleted.

## Access Control
- Access control is defined at the conversation group level.
- Forks enforce the permissions of the group.
- Fork conversations never have their own members or ownership policies.

## Summaries / Memory Epochs
- Summaries (agent-only) are tied to a conversation id.
- Forking does not copy summaries; each fork manages its own summary timeline.

## Migration / Backfill Notes
- Create `conversation_groups` and backfill one row per group.
  - For existing roots: set `conversation_groups.id = conversations.id`.
  - For existing forks: set `conversation_groups.id = conversations.root_conversation_id`.
- Backfill `conversations.conversation_group_id` from `root_conversation_id` (or `id` when `root_conversation_id` is null).
- Backfill `messages.conversation_group_id` by joining through `messages.conversation_id -> conversations.conversation_group_id`.
- Move group-scoped tables (memberships, ownership transfers, shares) to reference `conversation_group_id` instead of `conversation_id`.
- Drop `root_conversation_id` once reads/writes are fully switched.

## Open Questions / Constraints to Document
## Constraints / Decisions to Document
- Message ordering is based on the message timestamp (`created_at`).
- Forking is only allowed on HISTORY + USER messages.
- We do not support deleting individual messages.
- Deleting a conversation deletes the group and all related data.
- Fork creation must validate that the target message belongs to the same group.
- We do not need a server-side API to return only forks for a specific message yet.
- Permission changes apply to all forks via group policy enforcement.

## Open Questions
- Should `conversation_group_id` use a partial index to speed up group-only queries?
- Do we want a lightweight endpoint to list all forks for a tree (server-side filtering vs. client-only filtering)? No.
- Should fork creation validate that `forked_at_message_id` exists in `forked_at_conversation_id` and is a HISTORY message? Yes.
- What is the canonical ordering of messages across forks (created_at vs. logical sequence)? Use the message timestamp.
- Do we need consistent behavior for deleted messages that were fork points? Messages are only deleted when the group is deleted.
