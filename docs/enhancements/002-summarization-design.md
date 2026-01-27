# Summarization Design

## Goals
- Extend the conversation summarization flow to persist a title, summary content, and summary coverage metadata.
- Add a summarization timestamp at the conversation level, tied to the last message included in the summary.
- Add an optional vector embeddings pipeline with pluggable vector stores, starting with pgvector.
- Require agent API key for summarization operations; no user identity is propagated.
- Keep the memory-service responsible for embedding generation and storage.
 - Track when summaries are ingested into the vector store for resilient retries.

## Requirements
- The `/v1/conversations/{conversationId}/summaries` endpoint must accept (no backward-compatibility constraints):
  - `title` for the conversation (string).
  - `summary` content (string).
  - `untilMessageId` (string), the last message included in the summary.
  - `summarizedAt` (timestamp), the timestamp of the last message included.
- Add a new API endpoint to handle both title and summary (or extend the existing summaries endpoint).
- Store `vectorized_at` on the `conversations` table to track when the latest summary was ingested into the vector store.
- Store coverage metadata (`untilMessageId`, `summarizedAt`) inside the SUMMARY message content.
- Summarization requests are treated as system background jobs:
  - Do not carry end-user identity.
  - Require a valid agent API key.
- Vector embeddings:
  - Pluggable vector stores.
  - First implementation: pgvector.
  - Memory-service owns embedding generation and storage.
  - If no vector store is enabled, skip embedding generation and keep `vectorized_at` unset.

## Constraints
- Current OpenAPI contract and client code must be updated when changing requests or endpoints.
- Security model for background summarization must be enforced server-side using API key checks.
- Summaries are not user-visible in message lists (MEMORY channel behavior).
- Keep data encryption and content handling consistent with existing message storage.
- Maintain compatibility with existing datastores (PostgreSQL, MongoDB) and caches (Redis, Infinispan).

## Assumptions
- Agent service triggers summarization and can access conversation HISTORY messages.
- The memory-service will store summary messages in a SUMMARY channel.
- A vector store integration will be added under `memory-service` without requiring agent-side embeddings.
- The conversation title can be updated from the summarization request (no existing public API for title update).
- The plain-text summary is stored as a SUMMARY message, and only the latest summary is needed for runtime context building.

## Detailed Plan

### 1) OpenAPI Contract Updates
- Update `memory-service-contracts/src/main/resources/openapi.yml`:
  - Extend `CreateSummaryRequest` with:
    - `title` (string) for conversation title.
    - `summary` (string), replacing `content`.
    - `summarizedAt` (timestamp string, RFC 3339).
    - `untilMessageId` (string), stored in summary metadata.
  - Update example payloads to include title + summary.
- If a new endpoint is required (instead of extending `/summaries`), define it in the OpenAPI spec and add corresponding client generation steps.

### 2) Database Schema Changes
- Postgres:
  - Add `vectorized_at TIMESTAMPTZ NULL` to `conversations` table.
  - Update `memory-service/src/main/resources/db/schema.sql` to include the new column.
- JPA entity:
  - Add `vectorizedAt` field to `ConversationEntity`.
  - Ensure it is mapped to `vectorized_at`.
- If MongoDB schemas require equivalent fields, update their models and storage logic.

### 3) API Resource Changes (Memory Service)
- Update `ConversationsResource` to:
  - Accept `title`, `summary`, `untilMessageId`, and `summarizedAt` in the request.
  - Enforce API key access for summarization (no user identity).
  - Populate an internal `CreateSummaryRequest` with summary content and metadata.
- Add or update logic to update the conversation title when the summary is created.
- If the vector store is enabled, enqueue or execute embedding generation and update `vectorized_at` on success.
- For any new endpoint, follow existing patterns for logging and error handling.

### 4) Store Layer Updates
- Extend `createSummary` in the memory store implementations:
  - Accept and persist summary content in a SUMMARY message.
  - Update conversation title (encrypted) when provided.
  - Update `vectorized_at` only when embeddings are successfully stored.
- Store coverage metadata (untilMessageId, summarizedAt) in the SUMMARY message content.

### 5) Embeddings Pipeline (Memory Service)
- Introduce an embeddings service abstraction:
  - Responsible for producing embeddings from summary text (and optionally title).
  - Pluggable models (OpenAI, local, etc.) via configuration.
- Extend vector store interface to include upsert for message embeddings:
  - Store embeddings for summary messages (and optionally other message types later).
- Implement pgvector store:
  - Add schema table `message_embeddings` (already defined in `schema.sql`, ensure it is activated when pgvector is configured).
  - Create a repository layer to insert/query embeddings.
  - Update search code path to use vector similarity when pgvector is enabled.
- Decide on embedding granularity:
  - Embeddings should be created from summary text.
  - Keep PII-stripped summary as the source of embedding text.
- When vector store is disabled:
  - Skip embedding generation entirely.
  - Leave `vectorized_at` null.
  - Continue to store the summary text for future vectorization.

### 6) Agent Summarization Flow
- Agent generates:
  - Title.
  - Summary text (PII stripped).
  - `untilMessageId` and `summarizedAt` (from the last message in the range).
- Agent sends the summary as plain text to memory-service.
- Memory-service handles embedding generation and storage.

### 7) Auth & Access Control
- Summarization endpoint:
  - Require agent API key (`apiKeyContext.hasValidApiKey()` pattern).
  - If no API key, return 403.
  - Do not rely on `currentUserId()` for summarization.
- Keep user-facing APIs unchanged.

### 8) Client Regeneration
- Regenerate Java client:
  - `./mvnw -pl quarkus/memory-service-rest-quarkus clean compile`.
- Regenerate frontend client:
  - `cd agent-webui && npm install && npm run generate`.
- Update any agent/frontend code using the summary API.

### 9) Tests
- Add/extend tests for:
  - Summarization endpoint auth (agent key required).
  - Summary request includes title/summary and updates conversation title.
  - Summary messages (SUMMARY channel) created and searchable (keyword search fallback).
  - Vector embeddings are written when pgvector is enabled.
  - `vectorized_at` updates on success and remains unchanged on failure.

## Key Design Decisions
- The agent sends plain text summary, not embeddings.
  - Avoids tight coupling and centralizes embeddings in memory-service.
  - Allows vector store implementation to evolve without agent changes.
- Do not preserve backward compatibility for summary requests; rename fields as needed.
- Store the plain-text summary as a SUMMARY message and keep only the latest summary per conversation for context building.
  - Older summary messages can be pruned or overwritten based on storage policy.
  - Coverage metadata lives in the SUMMARY message content, not a separate table.

## Additional Concerns
- Summary quality over time:
  - Incremental summarization risks compounding errors and losing details.
  - Consider a hybrid approach: use the last summary plus recent history only when the new content is small. Periodically re-summarize from full history if quality drifts.
- Data encryption:
  - Ensure summary text and titles are encrypted using existing mechanisms.
- Backfill/migration:
  - Older conversations will not have `summarized_at` or title updates; handle nulls gracefully.
- Vector store availability:
  - Provide clear fallback if pgvector is not configured (keyword search only).
  - Use `vectorized_at` to retry ingestion when the vector store recovers.
- Rate limits:
  - Summarization and embedding generation can be expensive; consider async job processing and retries.
