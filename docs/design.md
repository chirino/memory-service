# Memory Service Design

This document describes the API and data model of the Memory Service. The goal is to provide a storage and retrieval layer for all entries exchanged between AI agents, users, and other actors, with strong ownership semantics, sharing, semantic search, and support for replay and forking of conversations.

## Related Design Documents

For detailed design specifications on specific features, see:

- **[Architecture](architecture.md)**: System context, component overview, store abstractions, request flows, and module structure.
- **[Entry Data Model](entry-data-model.md)**: Detailed documentation of how entries are stored and retrieved, including channels, memory epochs, conversation forking, and multi-agent support.
- **[Database Design](db-design.md)**: PostgreSQL schema UML diagram, table descriptions, and key design patterns (access control, soft deletes, encryption, full-text search).

## High-level Responsibilities

- Persist all entries exchanged between agents, users, and LLMs.
- Allow users to list and inspect their conversations.
- Allow agents to fetch conversation context optimized for LLM calls.
- Support forking a conversation at any entry.
- Enforce per-conversation access control (owner, manager, writer, reader).
- Support semantic search across all conversations visible to a user.
- Allow agents to store memory entries that are not visible to end users.

---

## API Design (OpenAPI Overview)

The HTTP API is defined in `openapi.yml` (OpenAPI 3.1) for the main API and `openapi-admin.yml` for admin operations. A gRPC API is also available via `memory_service.proto`.

### Common Features

- Base path: `/v1/...`
- Authentication: `BearerAuth` (JWT bearer token) via the `Authorization` header.
- Resources:
  - Conversations
  - Entries (with channels: HISTORY, MEMORY)
  - Conversation memberships (sharing)
  - Ownership transfers
  - Semantic search and indexing

### System / Health

- `GET /v1/health`
  - Simple JSON health status (`{"status": "ok"}`) used for readiness and liveness checks.

---

### Conversations API

#### List Conversations

- `GET /v1/conversations`
  - Lists conversations the authenticated user can access (owner, manager, writer, reader).
  - Parameters:
    - `mode`: Controls which conversations are returned from each fork tree:
      - `all`: include all conversations (roots and forks).
      - `roots`: only include root conversations (not forks).
      - `latest-fork` (default): only the most recently updated conversation per fork tree.
    - `after`: cursor for pagination (UUID).
    - `limit`: maximum number of conversations to return.
    - `query`: optional text query for title/metadata search.
  - Returns `ConversationSummary` objects with:
    - `id`, `title`, `ownerUserId`
    - `createdAt`, `updatedAt`
    - `lastMessagePreview`
    - `accessLevel` (current user's effective level)

#### Create/Get/Delete Conversations

- `POST /v1/conversations`
  - Creates a new conversation owned by the current user.
  - Request: `CreateConversationRequest` (optional `title`, `metadata`).
  - Response: `Conversation` (includes fork lineage fields: `forkedAtEntryId`, `forkedAtConversationId`).

- `GET /v1/conversations/{conversationId}`
  - Returns a `Conversation` if the user has access.

- `DELETE /v1/conversations/{conversationId}`
  - Deletes a conversation and all conversations in the same fork tree (the root and all its forks), along with their entries and memberships.

---

### Entries API

#### List Entries

- `GET /v1/conversations/{conversationId}/entries`
  - Lists entries in a conversation, ordered by creation time.
  - Parameters:
    - `channel`: Filter by channel (`history` default, `memory`).
    - `epoch`: For memory channel - `latest` (default), `all`, or a specific epoch number.
    - `after`: cursor for pagination (UUID).
    - `limit`: maximum entries to return.
    - `forks`: Controls fork inclusion:
      - `none` (default): follows fork ancestry path, returning entries from target and ancestors up to fork points.
      - `all`: returns entries from all forks in the conversation group.
  - Returns `Entry` objects with: `id`, `conversationId`, `userId`, `channel`, `epoch`, `contentType`, `content`, `createdAt`.
  - See [Entry Data Model](entry-data-model.md) for details on channels and fork-aware entry retrieval.

#### Append Entry

- `POST /v1/conversations/{conversationId}/entries`
  - Appends a new entry to the conversation.
  - Request: `CreateEntryRequest`
    - `userId`: optional user ID.
    - `channel`: `history` or `memory`.
    - `contentType`: schema identifier (e.g., `history` for chat, `LC4J`, `SpringAI` for memory).
    - `content`: array of content blocks.
    - `indexedContent`: optional text to index for search (history channel only).
  - Response: created `Entry`.

#### Sync Agent Memory

- `POST /v1/conversations/{conversationId}/entries/sync`
  - Synchronizes agent memory entries for a conversation.
  - Request: single `CreateEntryRequest` where `content` array contains all messages in the agent's memory.
  - The service compares against existing entries in the latest memory epoch:
    - If content matches exactly: no-op.
    - If content extends existing (prefix match): append delta to current epoch.
    - If content diverges: create new epoch with delta.
  - Memory entries are scoped per `(conversationId, clientId)` where `clientId` is derived from the API key.
  - Response: `SyncEntryResponse` with `epoch`, `noOp`, `epochIncremented`, and optional `entry`.
  - See [Entry Data Model](entry-data-model.md) for details on memory epochs and multi-agent support.

---

### Forking API

#### Fork a Conversation

- `POST /v1/conversations/{conversationId}/entries/{entryId}/fork`
  - Forks an existing conversation at a specific entry.
  - Request: `ForkFromEntryRequest`
    - `title`: optional title for the new forked conversation.
  - Behavior:
    - "Fork at entry X" means the fork includes all parent entries up to but NOT including X.
    - A new conversation is created with:
      - `forkedAtConversationId`: the original conversation id.
      - `forkedAtEntryId`: the entry id immediately before the fork point (or `null` if forking at the first entry).
    - No entries are copied; fork creation is O(1) writes.
  - Response: new `Conversation` with fork metadata.
  - Access control is enforced at the fork tree level; all forks share the same permissions.

#### List Forks

- `GET /v1/conversations/{conversationId}/forks`
  - Returns all forks in the same fork tree as the given conversation.
  - Response: list of `ConversationForkSummary` with:
    - `conversationId`, `forkedAtEntryId`, `forkedAtConversationId`, `title`, `createdAt`.

See [Conversation Forking Design](conversation-forking-design.md) and [Entry Data Model](entry-data-model.md) for detailed data model and fork-aware entry retrieval.

---

### Sharing & Memberships API

#### List Memberships

- `GET /v1/conversations/{conversationId}/memberships`
  - Returns all `ConversationMembership` entries for the conversation (applies to entire fork tree).
  - Response: list with `conversationId`, `userId`, `accessLevel`, `createdAt`.

#### Share Conversation

- `POST /v1/conversations/{conversationId}/memberships`
  - Grants another user access to a conversation (and all forks in the same fork tree).
  - Request: `ShareConversationRequest` (`userId`, `accessLevel`).
  - Response: created `ConversationMembership`.

#### Update/Delete Membership

- `PATCH /v1/conversations/{conversationId}/memberships/{userId}`
  - Updates the `accessLevel` for an existing member.

- `DELETE /v1/conversations/{conversationId}/memberships/{userId}`
  - Revokes conversation access for a user.

---

### Ownership Transfer API

Ownership transfers use a two-step flow with explicit acceptance.

#### List Pending Transfers

- `GET /v1/ownership-transfers`
  - Returns pending transfers where the user is sender or recipient.
  - Parameter: `role` filter (`sender`, `recipient`, `all`).
  - Response: list of `OwnershipTransfer`.

#### Create Transfer Request

- `POST /v1/ownership-transfers`
  - Initiates ownership transfer to another user.
  - Request: `CreateOwnershipTransferRequest` (`conversationId`, `newOwnerUserId`).
  - Constraints:
    - Only the current owner can initiate.
    - Recipient must be an existing member.
    - Only one pending transfer per conversation at a time.
  - Response: created `OwnershipTransfer`.

#### Get/Delete Transfer

- `GET /v1/ownership-transfers/{transferId}`
  - Returns transfer details.

- `DELETE /v1/ownership-transfers/{transferId}`
  - Cancels (sender) or rejects (recipient) the transfer.
  - The transfer record is hard deleted.

#### Accept Transfer

- `POST /v1/ownership-transfers/{transferId}/accept`
  - Recipient accepts the transfer.
  - Result: recipient becomes owner, previous owner becomes manager.

---

### Search API

#### Semantic Search

- `POST /v1/conversations/search`
  - Performs semantic and/or keyword search across conversations the user has access to.
  - Request: `SearchConversationsRequest`
    - `query`: natural language query.
    - `searchType`: `auto` (default), `semantic`, or `fulltext`.
    - `after`: cursor for pagination.
    - `limit`: maximum results (default 20).
    - `includeEntry`: whether to include full entry in results (default true).
    - `groupByConversation`: when true (default), returns only highest-scoring entry per conversation.
  - Response: list of `SearchResult` with:
    - `conversationId`, `conversationTitle`, `entryId`, `score`, `highlights`, `entry`.
  - Backed by pluggable vector store (pgvector, Qdrant).

#### Index Entries for Search

- `POST /v1/conversations/index`
  - Batch indexes entries for semantic search. Requires indexer or admin role.
  - Request: array of `IndexEntryRequest` objects, each with:
    - `conversationId`, `entryId`, `indexedContent`.
  - Response: `IndexConversationsResponse` with count of entries indexed.

#### List Unindexed Entries

- `GET /v1/conversations/unindexed`
  - Returns history channel entries that need indexing (where `indexedContent` is null).
  - Used by batch indexing jobs to discover entries for processing.
  - Requires indexer or admin role.
  - Response: paginated list of `UnindexedEntry` objects.

---

### Response Resumer (Streaming Replay)

For streaming agent responses, the service can record tokens and allow replay after reconnects.

- `DELETE /v1/conversations/{conversationId}/response`
  - Cancels an in-progress response stream for the conversation.
  - Requires WRITER access.

The response-resumer locator store is pluggable via `memory-service.response-resumer` with supported backends `redis`, `infinispan`, or `none` to disable. See `README.md` for configuration.

---

### Admin API

The admin API (`openapi-admin.yml`) provides system-wide access to conversations and entries. Requires admin or auditor role.

Key endpoints:

- `GET /v1/admin/conversations` - List all conversations with filters (userId, deleted status, date ranges).
- `GET /v1/admin/conversations/{id}` - Get any conversation including soft-deleted.
- `DELETE /v1/admin/conversations/{id}` - Soft-delete a conversation.
- `POST /v1/admin/conversations/{id}/restore` - Restore a soft-deleted conversation.
- `GET /v1/admin/conversations/{id}/entries` - Get entries from any conversation.
- `GET /v1/admin/conversations/{id}/memberships` - Get memberships for any conversation.
- `GET /v1/admin/conversations/{id}/forks` - List forks for any conversation.
- `POST /v1/admin/conversations/search` - System-wide semantic search with userId filter.
- `POST /v1/admin/evict` - Hard-delete resources past retention period.

---

## Data Model

The Memory Service supports multiple data stores (PostgreSQL, MongoDB) with schemas defined in `schema.sql` and equivalent MongoDB collections. The data model uses conversation groups internally to enable efficient forking, with access control and ownership scoped to groups rather than individual conversations.

Key concepts:

- **Entries**: The fundamental unit of stored data in a conversation. Entries represent chat messages, agent memory snapshots, tool results, or any structured data. Each entry has a `channel` (HISTORY or MEMORY), `contentType`, and `content`.
- **Channels**: Entries are organized into logical channels - HISTORY for visible conversation and MEMORY for agent-internal state.
- **Memory Epochs**: Agent memory entries use epochs for versioning. When an agent's context changes significantly, it creates a new epoch that supersedes previous ones.
- **Fork Trees**: All forks of a conversation share the same internal group, enabling shared access control. The `conversationGroupId` is not exposed in the API.
- **Vector Embeddings**: Optional vector store integration for semantic search (pgvector, Qdrant).

For detailed data model specifications, see:
- [Entry Data Model](entry-data-model.md) for comprehensive documentation of entries, channels, epochs, and fork-aware retrieval.
- [Conversation Forking Design](conversation-forking-design.md) for fork data model and access patterns.
- `schema.sql` for PostgreSQL DDL.

---

## Access Control Model

- Conversations are owned by a single user at a time.
- Access is enforced at the fork tree level - all forks share the same memberships.
- Access to a conversation for a user is defined by `conversation_memberships.access_level`:
  - `owner`: full control, including deletion and ownership transfer.
  - `manager`: can read/write and manage memberships (depending on policy).
  - `writer`: can read and append entries.
  - `reader`: can read entries but not modify.
- All user-facing endpoints check that the authenticated user has an appropriate membership.
- Agent-facing endpoints are authenticated via API key and can operate across users but must still respect conversation access constraints.

---

## Forking and Replay Semantics

Forking a conversation:

- When `POST /v1/conversations/{conversationId}/entries/{entryId}/fork` is called:
  - A new conversation is created in the same internal fork tree as the original.
  - Fork metadata (`forked_at_conversation_id`, `forked_at_entry_id`) is set on the new conversation.
  - No entries are copied; fork creation is O(1) writes.
- Replay (view semantics):
  - Each fork has its own `conversation_id` and stores its own entries.
  - When fetching entries for a forked conversation, the service returns entries from the entire parent chain up to the fork points, plus the fork's own entries.
  - The UI loads entries for the active conversation, which transparently includes inherited parent entries.
  - To show fork choices, the UI uses the `/forks` endpoint to discover related conversations.

See [Conversation Forking Design](conversation-forking-design.md) and [Entry Data Model](entry-data-model.md) for detailed data model, implementation, and fork-aware entry retrieval.

---

## gRPC API

The gRPC API (`memory_service.proto`) provides equivalent functionality to the REST API:

- **SystemService**: `GetHealth`
- **ConversationsService**: `ListConversations`, `CreateConversation`, `GetConversation`, `DeleteConversation`, `ForkConversation`, `ListForks`
- **ConversationMembershipsService**: `ListMemberships`, `ShareConversation`, `UpdateMembership`, `DeleteMembership`
- **OwnershipTransfersService**: `ListOwnershipTransfers`, `GetOwnershipTransfer`, `CreateOwnershipTransfer`, `AcceptOwnershipTransfer`, `DeleteOwnershipTransfer`
- **EntriesService**: `ListEntries`, `AppendEntry`, `SyncEntries`
- **SearchService**: `SearchConversations`, `IndexConversations`, `ListUnindexedEntries`
- **ResponseRecorderService**: `Record`, `Replay`, `Cancel`, `IsEnabled`, `CheckRecordings`

UUID fields are represented as 16-byte big-endian binary values in protobuf messages.

---

## Summary of Files

- `openapi.yml`
  - Full HTTP API specification (OpenAPI 3.1) for the main user and agent APIs.

- `openapi-admin.yml`
  - Admin API specification for system-wide access to conversations and entries.

- `memory_service.proto`
  - gRPC service definitions with equivalent functionality to the REST APIs.

- `schema.sql`
  - PostgreSQL DDL for the complete data model including conversation groups, conversations, memberships, entries, optional embeddings, and ownership transfers.

- `design.md`
  - This document, providing the high-level API design and explaining how it meets the requirements for ownership, sharing, replay, forking, and semantic search.

- `entry-data-model.md`
  - Detailed documentation of how entries are stored and retrieved, including channels, memory epochs, conversation forking, and multi-agent support.

