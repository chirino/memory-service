# Memory Service Design

This document describes the API and data model of the Memory Service. The goal is to provide a storage and retrieval layer for all messages exchanged between AI agents, users, and other actors, with strong ownership semantics, sharing, semantic search, and support for replay and forking of conversations.

## Related Design Documents

For detailed design specifications on specific features, see:

- **[Conversation Forking Design](conversation-forking-design.md)**: Detailed data model and implementation approach for conversation forking using conversation groups.
- **[Summarization Design](summarization-design.md)**: Detailed design for conversation summarization, including title updates, coverage metadata, and vector embeddings pipeline.

## High-level Responsibilities

- Persist all messages exchanged between agents, users, and LLMs.
- Allow users to list and inspect their conversations.
- Allow agents to fetch conversation context optimized for LLM calls.
- Support forking a conversation at any message.
- Enforce per-conversation access control (owner, manager, writer, reader).
- Support semantic search across all conversations visible to a user.
- Allow agents to store summaries of older messages that are not visible to end users.

---

## API Design (OpenAPI Overview)

The HTTP API is defined in `openapi.yml` (OpenAPI 3.1). It follows a pattern similar to the LlamaStack conversation APIs, with a clear separation between user-facing and agent-facing operations via URL prefixes and tags.

### Common Features

- Base path: `/v1/...`
- Authentication: `BearerAuth` (JWT bearer token) via the `Authorization` header.
- Resources:
  - Conversations
  - Messages
  - Conversation memberships (sharing)
  - Semantic search
  - Agent context and summaries

### System / Health

- `GET /v1/health`
  - Simple JSON health status (`{"status": "ok"}`) used for readiness and liveness checks.

### User-facing API

The user-facing API is intended for chat frontends and user-oriented features.

#### Conversations

- `GET /v1/user/conversations`
  - Lists conversations the authenticated user can access (owner, manager, writer, reader).
  - Supports cursor (`after`) and `limit`, plus a basic `query` parameter for title/metadata search.
  - Returns `ConversationSummary` objects with:
    - `id`, `title`
    - `ownerUserId`
    - `createdAt`, `updatedAt`
    - `lastMessagePreview`
    - `accessLevel` (current user’s effective level)

- `POST /v1/user/conversations`
  - Creates a new conversation owned by the current user.
  - Request: `CreateConversationRequest` (optional `title`, `metadata`).
  - Response: `Conversation` (includes fork lineage fields).

- `GET /v1/user/conversations/{conversationId}`
  - Returns a `Conversation` if the user has access.

- `DELETE /v1/user/conversations/{conversationId}`
  - Deletes a conversation (policy: typically owner or manager only).

#### Messages (User View)

- `GET /v1/user/conversations/{conversationId}/messages`
  - Lists messages visible to the end user.
  - Summary messages and other agent-only content are filtered out (`visibility != agent`).
  - Supports cursor (`after`) and `limit`.
  - Returns `Message` objects.

- `POST /v1/user/conversations/{conversationId}/messages`
  - Appends a user-authored message to a conversation.
  - Request: `CreateUserMessageRequest` (plain text content + optional metadata).
  - Response: created `Message` with `role=user`, `visibility=user`.

#### Forking Conversations

- `POST /v1/user/conversations/{conversationId}/messages/{messageId}/fork`
  - Forks an existing conversation at a specific *user message* (HISTORY channel).
  - Request: `ForkFromMessageRequest`
    - `title?`: optional title for the new forked conversation.
    - `newMessage`: a `CreateUserMessageRequest` containing the replacement user message content.
  - Behavior:
    - The fork point (`messageId`) must identify an existing HISTORY + USER message.
    - A new conversation is created with:
      - `conversationGroupId`: same as the original conversation (all forks share the same group).
      - `forkedAtConversationId`: the original conversation id.
      - `forkedAtMessageId`: the message id immediately before the fork point (or `NULL` if forking at the first message).
    - No messages are copied; fork creation is O(1) writes.
    - The new conversation can immediately receive new messages via normal append APIs.
  - Response: new `Conversation` with fork metadata.
  - Access control is enforced at the conversation group level; all forks share the same permissions.
  - See [Conversation Forking Design](conversation-forking-design.md) for detailed data model and implementation.

#### Sharing & Access Control

- `GET /v1/user/conversations/{conversationId}/memberships`
  - Returns all `ConversationMembership` entries for the conversation:
    - `conversationId`, `userId`, `accessLevel`, `createdAt`.

- `POST /v1/user/conversations/{conversationId}/memberships`
  - Grants another user access to a conversation.
  - Request: `ShareConversationRequest` (`userId`, `accessLevel`).
  - Response: created `ConversationMembership`.

- `PATCH /v1/user/conversations/{conversationId}/memberships/{userId}`
  - Updates the `accessLevel` for an existing member.

- `DELETE /v1/user/conversations/{conversationId}/memberships/{userId}`
  - Revokes conversation access for a user.

#### Ownership Transfer

- `POST /v1/user/conversations/{conversationId}/transfer-ownership`
  - Initiates ownership transfer to `newOwnerUserId`.
  - Response is `202 Accepted`. Actual acceptance flow is handled elsewhere (e.g., via a separate endpoint, notification, or external workflow), with backing data in `conversation_ownership_transfers`.

### Agent-facing API

The agent-facing API is tuned to what an AI agent needs for building prompts and storing results; it can access agent-only data such as summaries and internal messages.

#### Conversation Context for LLMs

- `GET /v1/agent/conversations/{conversationId}/context`
  - Returns `ConversationContext` for the agent:
    - `conversationId`
    - `messages`: list of `Message` objects (may include summary messages with `visibility=agent`).
    - `nextAfterMessageId`: cursor for fetching the next slice.
  - Parameters:
    - `afterMessageId` (optional): start after this message id.
    - `limit`: max number of messages to return.
  - Server logic can:
    - Replace long spans of old messages with summary messages.
    - Ensure ordering by `sequence`.

#### Writing Messages (Agent / LLM / Tools)

- `POST /v1/agent/conversations/{conversationId}/messages`
  - Appends messages to a conversation in bulk.
  - Request body:
    - `messages`: list of `CreateAgentMessageRequest`:
      - `role` (`user`, `assistant`, `system`, `tool`, `agent`)
      - `visibility` (`user`, `agent`, `system`)
      - `content`
      - optional `metadata` and `parentMessageId`
  - Response: created `Message` objects.

#### Summaries

- `POST /v1/agent/conversations/{conversationId}/summaries`
  - Stores an internal summarization of earlier messages.
  - Requires agent API key authentication (no user identity).
  - Request: `CreateSummaryRequest`
    - `title`: conversation title (updates the conversation title).
    - `summary`: summary text (e.g., compressed history, PII-stripped).
    - `untilMessageId`: highest message covered by this summary.
    - `summarizedAt`: timestamp of the last message included in the summary.
  - The summary is persisted as a `Message` with:
    - `channel=SUMMARY` (not visible in user-facing views).
    - Coverage metadata (`untilMessageId`, `summarizedAt`) stored in the SUMMARY message content.
  - If vector store is enabled, embeddings are generated and stored, and `vectorized_at` is updated on the conversation.
  - See [Summarization Design](summarization-design.md) for detailed implementation.

### Response Resumer (Streaming Replay)

For streaming agent responses, the service can record tokens and allow replay
after reconnects. The response-resumer locator store is pluggable via
`memory-service.response-resumer` with supported backends `redis`, `infinispan`,
or `none` to disable resumption. See the response resumer docs in `README.md`
for configuration examples.

### Semantic Search

- `POST /v1/user/search/messages`
  - Performs semantic and/or keyword search across all messages the user has access to.
  - Request: `SearchMessagesRequest`
    - `query`: natural language query.
    - `topK`: number of results to return.
    - Optional `conversationIds` filter.
    - Optional `before` message id bound.
  - Response:
    - List of `SearchResult`:
      - `message`: a `Message` object.
      - `score`: numeric relevance score.
      - optional `highlights` string.
  - Backed by a pluggable vector store (pgvector, MongoDB, etc.).

### Viewing Forks in UIs

To support UIs that want to display previous fork points and allow users to switch to other branches:

- `GET /v1/user/conversations/{conversationId}/forks`
  - Returns a list of `ConversationForkSummary` entries for all forks that belong to the same logical conversation:
    - `conversationId`: the logical conversation id.
    - `forkId`: identifier of the fork.
    - `forkedFromMessageId`: message id at which this fork diverged from its parent fork (may be `null` for the root fork).
    - `title`: optional fork-specific title or label.
    - `createdAt`: when the fork was created (used to determine the oldest fork).
    - `isCurrentFork`: whether this fork corresponds to the branch currently being viewed.
  - A UI typically calls this endpoint once per conversation to fetch all fork points and then:
    - Marks which messages in the timeline are fork points (based on `forkedFromMessageId`).
    - Allows users to switch to another branch starting at that message.

The combination of fork metadata (exposed as `forkId` and `forkedFromMessageId` in the API) and the conversation-level `/forks` endpoint makes it possible to reconstruct the fork tree and provide rich branch navigation without needing a separate “fork id” column on the messages themselves.

---

## Data Model

The Memory Service supports multiple data stores (PostgreSQL, MongoDB) with schemas defined in `schema.sql` and equivalent MongoDB collections. The data model uses conversation groups to enable efficient forking, with access control and ownership scoped to groups rather than individual conversations.

Key concepts:

- **Conversation Groups**: All forks of a conversation share the same `conversation_group_id`, enabling group-level access control and efficient fork queries.
- **Messages**: Stored per conversation with `channel` types (HISTORY, MEMORY, SUMMARY) to control visibility.
- **Summaries**: Stored as SUMMARY channel messages with coverage metadata embedded in the message content.
- **Vector Embeddings**: Optional vector store integration for semantic search (pgvector, MongoDB, etc.).

For detailed data model specifications, see:
- [Conversation Forking Design](conversation-forking-design.md) for fork data model and access patterns.
- [Summarization Design](summarization-design.md) for summary storage and vector embeddings.
- `schema.sql` for PostgreSQL DDL.

---

## Access Control Model

- Conversations are owned by a single user at a time.
- Access to a conversation for a user is fully defined by `conversation_memberships.access_level`:
  - `owner`: full control, including deletion and ownership transfer.
  - `manager`: can read/write and manage memberships (depending on policy).
  - `writer`: can read and append messages.
  - `reader`: can read messages but not modify.
- All user-facing endpoints check:
  - That the authenticated user has an appropriate membership for the `conversationId`.
- Agent-facing endpoints are authenticated as the agent service and can operate across users but must still respect conversation access constraints if requested on behalf of a user.

---

## Forking and Replay Semantics

Forking a conversation:

- When `POST /v1/user/conversations/{conversationId}/messages/{messageId}/fork` is called:
  - A new conversation is created with the same `conversation_group_id` as the original.
  - Fork metadata (`forked_at_conversation_id`, `forked_at_message_id`) is set on the new conversation.
  - No messages are copied; fork creation is O(1) writes.
- Replay (view semantics):
  - Each fork has its own `conversation_id` and stores its own messages.
  - Messages are ordered by `created_at` within each conversation.
  - The UI loads messages for the active conversation only.
  - To show fork choices, the UI fetches all conversations in the same `conversation_group_id` and filters client-side.

See [Conversation Forking Design](conversation-forking-design.md) for detailed data model, implementation, and access patterns.

---

## Summary of Files

- `openapi.yml`
  - Full HTTP API specification (OpenAPI 3.1).
  - Defines user-facing, agent-facing, and search endpoints, plus schemas and security.

- `schema.sql`
  - PostgreSQL DDL for the complete data model including conversation groups, conversations, memberships, messages, summaries, optional embeddings, and ownership transfers.

- `design.md`
  - This document, providing the high-level API design and explaining how it meets the requirements for ownership, sharing, replay, forking, semantic search, and agent-visible summaries.
- `conversation-forking-design.md`
  - Detailed design for conversation forking using conversation groups.
- `summarization-design.md`
  - Detailed design for conversation summarization, including title updates, coverage metadata, and vector embeddings pipeline.
