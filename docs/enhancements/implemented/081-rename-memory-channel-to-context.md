---
status: implemented
---

# Enhancement 081: Rename the Conversation `memory` Channel to `context`

> **Status**: Implemented.

## Summary

Rename the conversation entry channel value `memory` to `context` across the Agent/Admin contracts, generated clients, Go server, language integrations, tests, and docs. This change applies only to conversation entry channels; the episodic `/v1/memories` APIs remain named `memory`.

## Motivation

The current `memory` channel name is overloaded:

- The service already exposes a separate episodic memory surface under `/v1/memories`.
- Conversation entries in the `memory` channel are really the agent's working context or checkpoint state, not the same resource as episodic memories.
- Docs and examples currently have to explain both "memory channel" and "memory API" in the same product, which is avoidable ambiguity.

Using `context` for conversation-scoped agent state makes the distinction clearer:

- `history` remains the user-visible transcript.
- `context` becomes the agent-managed working state inside a conversation.
- `/v1/memories` continues to mean namespaced episodic memories.

Because the project is pre-release, the old `memory` channel should be removed rather than deprecated.

## Design

### 1. Contract Rename

Rename the conversation channel enum value from `memory` to `context`.

| Surface | Current | New |
|---|---|---|
| OpenAPI `Channel` enum | `memory` | `context` |
| Admin OpenAPI `Channel` enum | `memory` | `context` |
| Protobuf `Channel` enum | `MEMORY` | `CONTEXT` |
| Generated REST clients | `"memory"` | `"context"` |

No compatibility alias is provided. Requests using `memory` should fail validation after the rename lands.

### 2. Conversation Entry Semantics Stay the Same

Only the name changes. Existing behavior stays intact:

- `history` is still the user-visible conversation channel.
- `context` remains client-scoped for agent/internal entries.
- Epoch behavior is unchanged; it is still used only for the `context` channel.
- `POST /v1/conversations/{conversationId}/entries/sync` keeps the same path and behavior, but its summaries, descriptions, examples, and generated operation names should use `context` terminology instead of `memory`.

### 3. Documentation and Example Language

Update all docs and examples that currently describe checkpoint, LangChain/LangGraph state, Spring AI memory, or LangChain4j memory as being stored in the `memory` channel.

Use the following wording consistently:

- `history` channel: user-visible conversation turns
- `context` channel: agent-managed conversation state/checkpoint data
- `/v1/memories`: episodic memory API

This avoids turning "memory" into a catch-all term for two unrelated storage models.

### 4. Server and Integration Changes

Update all server-side string comparisons, enum mappings, validation messages, and default examples to use `context`.

Expected changes include:

- Go model constants and request validation
- REST and gRPC enum/string translation
- datastore epoch handling comments and error messages
- Spring, Quarkus, and Python integration code that currently reads or writes channel `memory`
- BDD step definitions and fixture payloads

### 5. Generated Artifacts

After the contract rename:

- regenerate Go OpenAPI bindings under `internal/generated/**`
- regenerate protobuf bindings under `internal/generated/pb/**`
- regenerate frontend client types under `frontends/chat-frontend/src/client/**`
- regenerate any Java/Python artifacts that are derived from the contracts

Generated files should not be hand-edited.

### 6. Scope Boundaries

In scope:

- conversation entry channel rename from `memory` to `context`
- epoch-related text for conversation entry context syncing/listing
- examples, docs, and tests that refer to the conversation entry channel

Out of scope:

- renaming the product, repository, or service
- renaming `/v1/memories`, `MemoriesService`, or episodic memory concepts
- changing sync/epoch behavior
- changing storage layout beyond what is required to accept/store the new channel value

## Testing

### Automated Checks

- Go build and targeted Go test coverage for entry listing/sync, wrapper routing, and gRPC mappings.
- Java compile for Spring and Quarkus modules that reference `Channel`.
- Python compile checks for LangChain/LangGraph integrations.
- Frontend build after regenerating the client.
- Site docs tests to confirm snippets and curl examples use `context`.

### BDD Coverage (Gherkin)

```gherkin
Feature: Context channel naming

  Scenario: REST APIs accept the context channel
    Given a conversation exists
    When I append an entry with content "checkpoint" and channel "CONTEXT" and contentType "test.v1"
    And I call GET "/v1/conversations/${conversationId}/entries?channel=context"
    Then the response status should be 200
    And the response should contain an entry with channel "context"

  Scenario: Sync entries requires the context channel
    Given a conversation exists
    When I sync context entries with request:
      """
      {
        "userId": "agent",
        "channel": "context",
        "contentType": "test.v1",
        "content": [{"text":"state"}]
      }
      """
    Then the response status should be 200

  Scenario: Legacy memory channel values are rejected
    Given a conversation exists
    When I append an entry with content "old value" and channel "MEMORY" and contentType "test.v1"
    Then the response status should be 400

  Scenario: gRPC channel enums use CONTEXT
    Given a gRPC client for the entries API
    When it appends and lists entries with channel CONTEXT
    Then the calls should succeed without MEMORY enum references
```

## Tasks

- [x] Rename the OpenAPI and admin OpenAPI `Channel` enum value from `memory` to `context`.
- [x] Rename the protobuf `Channel` enum value from `MEMORY` to `CONTEXT`.
- [x] Update conversation entry docs, examples, summaries, and schema descriptions from "memory channel/epoch" to "context channel/epoch".
- [x] Regenerate Go, frontend, Java, and Python artifacts derived from the contracts.
- [x] Update Go server model constants, validation, enum mapping, and sync/list logic to use `context`.
- [x] Update Spring, Quarkus, and Python integrations to read/write `context`.
- [x] Update BDD steps, REST/gRPC feature files, and docs-test curl fixtures to use `context`.
- [x] Update affected `FACTS.md` files after implementation to record the canonical channel name.
- [x] Run targeted build/test/doc verification for all changed modules.

## Implementation Notes

- The external contract now accepts only `context` for conversation-scoped agent state. Legacy `memory` channel values are rejected by validation.
- `/v1/conversations/{conversationId}/entries/sync` kept its path and behavior, but its contract wording and generated operation names now use `context`.
- Internal helper names such as `MemoryEntriesCache` and `syncMemory` were left in place where they are not externally observable. This keeps the change scoped to the public contract and integration surfaces.
- While implementing the rename, sync on newly auto-created conversations exposed a stale-cache edge case. Postgres, SQLite, and Mongo now invalidate cached latest entries before computing sync state for an auto-created conversation.

## Files to Modify

| File(s) | Planned change |
|---|---|
| `contracts/openapi/openapi.yml` | Rename `Channel.memory` to `context`; update list/sync descriptions, examples, and operation text |
| `contracts/openapi/openapi-admin.yml` | Rename admin `Channel.memory` to `context` |
| `contracts/protobuf/memory/v1/memory_service.proto` | Rename enum `MEMORY` to `CONTEXT`; update comments mentioning memory channel/epoch |
| `internal/model/model.go` | Rename the Go channel constant from `ChannelMemory` to `ChannelContext` |
| `internal/grpc/server.go` | Update enum mapping, validation errors, and sync/list handling for `context` |
| `internal/plugin/route/entries/entries.go` | Update REST validation messages and wrapper route terminology |
| `internal/plugin/store/postgres/postgres.go` | Update channel checks/comments for epoch and cache handling |
| `internal/plugin/store/sqlite/sqlite.go` | Update channel checks/comments for epoch and cache handling |
| `internal/plugin/store/mongo/mongo.go` | Update channel checks/comments for epoch and cache handling |
| `internal/generated/api/**`, `internal/generated/admin/**`, `internal/generated/pb/**` | Regenerate generated Go bindings after contract updates |
| `frontends/chat-frontend/src/client/**` | Regenerate frontend REST client/types to expose `context` |
| `java/quarkus/memory-service-extension/runtime/**` | Update `Channel` usage and docs/comments in Quarkus integration |
| `java/spring/memory-service-spring-boot-autoconfigure/**` | Update `Channel` usage and docs/comments in Spring integration |
| `python/langchain/memory_service_langchain/**` | Update checkpoint saver and helper code to use `context` |
| `python/langchain/memory_service_langchain/langgraph/**` | Update any channel-specific docs/examples if needed |
| `internal/bdd/steps_entries.go` and `internal/bdd/testdata/features*.feature` | Rename BDD steps/scenarios/fixtures from memory-channel wording to context-channel wording |
| `internal/sitebdd/testdata/curl-examples/**` | Update generated curl example payloads/assertions that mention the old channel |
| `site/src/pages/docs/**` | Replace conversation-channel documentation and examples with `context` terminology |
| `internal/FACTS.md`, `python/FACTS.md`, `java/quarkus/FACTS.md`, `java/spring/FACTS.md` | Update module facts if they mention the old canonical channel name |

## Verification

```bash
# Go bindings / server
go build ./...
go test ./internal/bdd -run 'TestFeatures(Entries|Grpc)' -count=1 > test.log 2>&1

# Java compile
./java/mvnw -f java/pom.xml compile -pl quarkus/memory-service-extension,spring/memory-service-spring-boot-autoconfigure -am

# Python integrations
python3 -m compileall python/langchain python/langgraph

# Frontend client build
cd frontends/chat-frontend && npm run build

# Site docs tests
go test -tags='site_tests sqlite_fts5' ./internal/sitebdd/ -run TestSiteDocs -count=1 > site.log 2>&1
```

## Non-Goals

- Renaming the service's episodic memory APIs, models, or storage plugins.
- Introducing a temporary `memory`/`context` dual-acceptance compatibility period.
- Reworking agent checkpoint formats or content payload schemas.
- Changing fork semantics, search behavior, or response recording behavior.

## Design Decisions

- Use `context` only for the conversation entry channel, because that is the narrowest change that resolves the terminology conflict.
- Keep `/v1/memories` unchanged so `memory` remains reserved for the episodic memory product surface.
- Rename validation and doc language to `context epoch` even if some internal types continue using `memory` temporarily during the implementation, to keep the external contract consistent first.

## Outcome

- REST, admin REST, gRPC, generated clients, language integrations, BDD fixtures, and site docs now use `context` as the canonical conversation entry channel.
- Episodic `/v1/memories` APIs and related storage concepts remain unchanged.
