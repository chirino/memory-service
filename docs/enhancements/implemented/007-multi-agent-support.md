---
status: implemented
---

# Multi-Agent Memory Support (Draft)

> **Status**: Implemented.

## Problem Summary
The memory-service currently assumes a single agent per conversation. All
agent-generated ChatMemory is stored in the `memory` channel with no notion of
which agent produced it, so multiple LangChain4j agents sharing a conversation
would overwrite or read each other's memory. We need to support multiple agents
participating in the same conversation, with each agent maintaining an
independent ChatMemory stream.

## Goals
- Allow multiple LangChain4j agents to participate in the same conversation
  without sharing memory messages.
- Identify the calling agent based on its API key (no explicit `agent_id`
  parameter in the API).
- Associate memory messages with the calling agent and filter memory reads
  to only that agent.
- Keep history messages and user-facing APIs unchanged.

## Non-Goals
- Introducing an explicit `agent_id` field in the public APIs.
- Changing user-visible message ordering or visibility rules.
- Building a full agent/client management API (can be a follow-up).
- Forcing backward compatibility with old API key configuration formats.

## Current State
- API keys are configured as a flat allowlist
  (`memory-service.api-keys`) in `ApiKeyManager`.
- `ApiKeyContext` only stores validity and the raw API key string.
- Messages have no client/agent attribution in either PostgreSQL
  (`messages` table) or MongoDB (`MongoMessage` documents).
- Memory retrieval (`MessageChannel.MEMORY`) is not agent-scoped:
  - `MessageRepository.findLatestMemoryEpoch` and `listMemoryMessagesByEpoch`
    fetch across all memory messages for the conversation.
  - Mongo equivalents behave the same.
- LangChain4j integration (`MemoryServiceChatMemoryStore`) reads/writes
  memory messages via the memory channel with an API key but cannot identify
  which agent produced earlier memory.

## Proposed Design

### 1) Agent Identity via API Key -> Client Id Mapping
Define a "client id" identity for agents. Each client id can have multiple
API keys. The server resolves the client id from the presented API key and
stores it in request context.

Proposed configuration approach (minimal surface change):
- Replace the flat `memory-service.api-keys` allowlist with a mapping:
  - `memory-service.api-keys.<client-id>=key1,key2`
Implementation notes:
- Extend `ApiKeyManager` to parse a map of client ids to keys and return
  `Optional<String> resolveClientId(String apiKey)`.
- Extend `ApiKeyContext` with `clientId` and expose `getClientId()`.
- Update `ApiKeyRequestFilter` and `GrpcApiKeyInterceptor` to set client id.
- Update logging to include `clientId` when present.

### 2) Message Attribution: client_id on Messages
Add a new nullable `client_id` field to messages, used to tag messages created
by agent-authenticated calls.

PostgreSQL:
- Add `client_id TEXT NULL` to the `messages` table.
- Add indexes to support memory-channel filtering, for example:
  - `(conversation_id, channel, client_id, epoch, created_at)`

MongoDB:
- Add `clientId` field to `MongoMessage`.
- Add an index on `(conversationId, channel, clientId, epoch, createdAt)`.

JPA / Panache models:
- `MessageEntity` -> add `clientId` column mapping.
- `MongoMessage` -> add `clientId` field.

### 3) Memory Channel Filtering by client_id
When reading memory messages, scope queries to the resolved client id:
- `findLatestMemoryEpoch` should be per `(conversation_id, client_id)`.
- `listMemoryMessagesByEpoch` should include `client_id` in the predicate.
- If `client_id` is not resolved (invalid/missing API key), behavior should
  match the current auth failure rules (403 or 401).

This applies to:
- `/v1/conversations/{conversationId}/messages?channel=memory`
- `/v1/conversations/{conversationId}/memory/messages/sync`
- Any agent context endpoints that include memory channel messages
  (HTTP + gRPC).

### 4) Message Creation Rules
When writing messages under agent authentication, always set `client_id` to the
resolved client id on the message, regardless of channel. This makes all
agent-originated messages attributable without adding any API parameters.

When writing messages under user authentication:
- `client_id` remains null.

### 5) OpenAPI and Client Implications
No OpenAPI changes are required because `client_id` is derived from the API
key and not sent by clients. However:
- Document the new API key configuration format.
- Update the OpenAPI description for memory sync endpoints to note that
  memory is scoped per client id.

### 6) Areas of the App Impacted
Security / Auth:
- `ApiKeyManager`, `ApiKeyContext`, `ApiKeyRequestFilter`,
  `GrpcApiKeyInterceptor`.

API Resources:
- `ConversationsResource` (memory list, memory sync, append message).
- `MessagesGrpcService` and `ConversationsGrpcService` if they return or
  accept memory channel content.

Store Layer:
- `MemoryStore` interface (pass through client id where needed).
- `PostgresMemoryStore` and `MongoMemoryStore`:
  - set client id on memory writes.
  - filter memory reads by client id.
  - update latest-epoch calculations to be client-scoped.

Persistence:
- `MessageEntity`, `MongoMessage`.
- `MessageRepository`, `MongoMessageRepository` query updates.
- Liquibase schema updates for PostgreSQL.

LangChain4j Integration:
- `MemoryServiceChatMemoryStore` (should work unchanged as long as API key
  uniquely identifies the agent client id).
- Any multi-agent deployments must provision distinct API keys per agent.

Observability:
- Request logging should include `clientId` for agent calls.
- Audit or access logs should not emit the raw API key.

### 7) Testing Plan
Prefer Cucumber scenarios for REST APIs:
- Two different API keys map to different client ids.
- Each client writes to the memory channel in the same conversation.
- Each client only reads its own memory messages.

Unit tests:
- `ApiKeyManager` resolves correct client id for mapped keys.
- Repository queries include `client_id` filtering and epoch scoping.

### 8) Rollout Plan
1) Add schema changes and code paths to store `client_id`.
2) Add API key mapping support.
3) Deploy with updated configuration.

## Open Questions
- Should summary messages (SUMMARY channel) be scoped per agent client id,
  or remain shared across agents? NO
- Do we want an admin API or database-backed model for managing client ids
  and API keys instead of config-only mapping? NO
