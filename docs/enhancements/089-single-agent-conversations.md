---
status: proposed
---

# Enhancement 089: Single-Agent Conversations

> **Status**: Proposed.

## Summary

Simplify the agent lineage model by storing conversation-level `clientId` on every conversation and using optional conversation-level `agentId` when a conversation belongs to a specific logical agent. Keep explicit parent/child conversation lineage with `startedByConversationId` and `startedByEntryId`, and keep conversation-list `ancestry` filters for roots vs child conversations.

## Motivation

Enhancement [088](partial/088-agent-conversation-lineage.md) assumes multiple logical agents may participate in the same conversation. That assumption adds complexity in several places:

1. `agentId` is stored and validated on every entry write even though the dominant orchestration pattern is "start a new conversation for another agent".
2. Context isolation becomes `(conversationId, clientId, agentId)` instead of being naturally scoped by the conversation itself.
3. Entry list and sync APIs require agent-specific parameters and validation that exist only to support multiple agents sharing one conversation.
4. The data model allows ambiguous ownership questions such as "which agent does this conversation belong to?" even though the real workflow already answers that at conversation creation time.

The intended orchestration model is simpler:

- a conversation is the working memory and transcript for one client and optionally one logical agent,
- when that agent needs another agent, it starts a child conversation,
- the child conversation records which parent conversation and parent entry started it, and
- child conversations remain discoverable through `ancestry` filters and direct child-listing APIs.

This keeps the lineage model but removes per-entry agent attribution as a first-class concern.

## Design

### Core rule

Each conversation has exactly one `clientId` and at most one logical `agentId`.

- Agent-to-agent delegation always creates a new conversation.
- Another agent does not "join" an existing conversation as a peer author.
- User-authored and system-authored entries may still exist inside the conversation.
- When `agentId` is set, those entries belong to the conversation's single logical agent context.

### Conversation model

Move agent identity from entry scope to conversation scope.

Add these nullable conversation fields:

| Field | Type | Meaning |
|------|------|---------|
| `agentId` | string | Optional logical agent label for this conversation |
| `clientId` | string | Authenticated app/system client identity associated with this conversation |
| `startedByConversationId` | uuid | Parent conversation that started this child conversation |
| `startedByEntryId` | uuid | Parent entry that triggered creation of this child conversation |

Semantics:

- `agentId` is optional and conversation-scoped, not entry-scoped.
- `clientId` is conversation-scoped and is always set at conversation creation time from the authenticated client context.
- Every conversation has an associated `clientId`.
- Existing conversations without `agentId` are not converted into agent-attributed conversations after creation. If a different agent-specific conversation is needed later, it must be created as a new conversation explicitly or as a new child conversation.
- `startedByConversationId` and `startedByEntryId` remain conversation lineage fields and are not copied to entries.
- `clientId` is an internal/admin field used for agent-context authorization and should not be exposed in normal user-facing conversation payloads.
- Creating a conversation, whether through an explicit conversation-create API or auto-creation on first append to a new conversation ID, requires authenticated client context so the service can derive and persist `conversation.clientId` at creation time. `clientId` is not supplied as a request field.

### Entry model

Remove agent and client attribution from the public entry model.

- Remove `agentId` from `Entry` and `CreateEntryRequest`.
- Remove `clientId` from `Entry` responses.
- Context writes use the target conversation's stored agent/client identity; history writes continue to follow normal conversation access rules.

`userId` stays on entries because user authorship is still a per-entry property. A conversation may contain:

- user-authored history entries,
- agent-authored history entries from the conversation's optional owning agent, and
- context entries associated with the conversation.

### Should `clientId` move to conversation level?

Yes.

`clientId` should move to the conversation even though `agentId` is optional:

- context isolation should be defined by the conversation, not by repeated per-entry tags,
- sync/list APIs no longer need a caller-supplied `agentId`,
- caching keys can collapse from `(conversationId, clientId)` or `(conversationId, clientId, agentId)` to just `conversationId`, and
- every conversation should have one authenticated app/system identity associated with it.

This does not change authentication. The request is still authenticated as a client. The change is that the conversation stores its associated client identity for authorization and auditing, and entry persistence no longer repeats that value on each row.

### Conversation creation rules

There are now two conversation creation cases:

#### 1. Root conversation

- Created with required conversation-level `clientId`.
- `agentId` is optional.
- `clientId` is derived from the authenticated client and persisted on the conversation.
- Creating a root conversation requires authenticated client context.
- All context entries in that conversation belong to that conversation's `clientId`, and when `agentId` is set they also belong to that single `(conversation.agentId, conversation.clientId)` pair.

#### 2. Child conversation

- Created by first append into a new conversation ID with `startedByConversationId` and optional `startedByEntryId`.
- May establish the child conversation's `agentId`.
- `clientId` is derived from the authenticated caller and stored on the child conversation.
- Creating the child conversation requires authenticated client context.
- Memberships and owner are copied from the parent conversation as in Enhancement 088.

### Conversation mutation rules

Conversation-level `clientId` should be immutable after creation. Conversation-level `agentId`, when set, should also be immutable after creation.

That avoids a hard class of bugs:

- cached context becoming attached to the wrong client or wrong agent,
- mixed entry provenance after reassignment, and
- ambiguity around whether older entries belong to the previous or current agent.

Once created, conversation appends and updates must not change the stored `clientId`.
If reassignment is ever needed later, it should be modeled as a new conversation, not an in-place update.

### API changes

#### Conversations

Keep:

- `startedByConversationId`
- `startedByEntryId`
- `ancestry=roots|children|all`
- `GET /v1/conversations/{conversationId}/children`

Add optional `agentId` to the user-level `Conversation` object:

```yaml
Conversation:
  properties:
    agentId:
      type: string
      nullable: true
```

`ConversationSummary` does not need to expose `agentId` unless a concrete listing use case requires it.

Admin conversation payloads should additionally expose:

```yaml
AdminConversation:
  properties:
    clientId:
      type: string
      nullable: true
```

`agentId` may be exposed on the normal agent-facing conversation APIs. `clientId` should be admin-only, matching the current posture where entry `clientId` is stored for service behavior but is not part of the normal user-facing data model.

User-level REST APIs:

- must not expose `clientId` on conversation objects,
- must not accept `clientId` on create/update/append requests, and
- continue to derive `clientId` from authenticated client context when creating conversations.

Admin APIs:

- should expose conversation `clientId`, and
- should continue to avoid accepting caller-supplied `clientId` when normal creation semantics derive it from auth context.

For write APIs, the service should support one of these approaches:

1. `CreateConversationRequest.agentId` as an optional field for explicit conversation creation.
2. `CreateEntryRequest.agentId` only as an optional creation-time convenience when auto-creating a new conversation.

Option 2 is acceptable as a transitional request shape, but the persisted model should remain conversation-level. Once the conversation exists, append-entry requests should not accept `agentId` or `clientId`, and they must not modify the stored `clientId`.

#### Entries

Simplify entry APIs:

- `GET /entries` no longer accepts `agentId`.
- `POST /entries` no longer accepts `agentId` except optionally when auto-creating a new conversation.
- `POST /entries/sync` no longer requires `agentId`.
- Entry responses no longer expose `agentId` or `clientId`.

#### gRPC

Mirror the same simplification:

- move `agent_id` and `client_id` to `Conversation`,
- remove `agent_id` from `Entry`,
- remove `agent_id` from `ListEntriesRequest`,
- remove the sync requirement that depends on request-level `agent_id`.

### Context isolation

Context becomes conversation-scoped by `conversationId`, with authorization controlled by `conversation.clientId`.

Old model:

- `(conversationId, clientId)` before Enhancement 088
- `(conversationId, clientId, agentId)` in Enhancement 088

New model:

- `conversationId`

Epoch sequences become per-conversation instead of per-client or per-agent-within-client.

This is the largest simplification in the proposal. It aligns the persistence model with the actual orchestration model: one conversation-scoped context stream authorized by one `clientId`, with optional `agentId` metadata.

### Validation rules

For agent-authenticated writes:

- creating a conversation, or auto-creating one by appending the first entry to a new conversation ID, requires authenticated client context so `conversation.clientId` can be derived and set,
- context-memory reads and writes require `conversation.clientId` to match the authenticated client,
- history reads and history appends follow normal conversation access rules and do not require `conversation.clientId` to match,
- if the conversation already has `agentId`, agent-authenticated appends operate under that conversation identity and cannot override it,
- entry writes cannot override stored conversation identity values.

For user-authenticated writes:

- users may append normal history entries subject to conversation access rules,
- users cannot set or mutate conversation `agentId` or `clientId` through entry writes.

For child conversation creation:

- `startedByConversationId` must reference a writable parent conversation,
- `startedByEntryId`, when present, must be visible in the parent ancestry,
- the child conversation may set an `agentId`,
- the child `clientId` is always derived from the authenticated client, not caller input.

### Access control

Agent-context access control should be based on authenticated `clientId`, not on `agentId`.

- authenticated client context is required when creating conversations in agent flows so the service can bind the conversation to a client at creation time.
- `clientId` is the security boundary for context/memory operations after creation.
- `agentId` is logical attribution within that client namespace and must not be treated as sufficient authorization.
- This is separate from user membership rules. All clients may read or append history messages as long as the authenticated user token grants access to those conversations.

Rules:

1. Creating a conversation, including auto-creating a new conversation on first append, requires authenticated client context so the service can derive and persist `conversation.clientId`.
2. When an agent creates a conversation, that conversation's `clientId` is set from the authenticated client and becomes immutable.
3. Context read/write operations are allowed only when `conversation.clientId` matches the authenticated client.
4. Shared user membership does allow cross-client history access when the authenticated user token has normal access to the conversation. History reads and history appends follow conversation membership and user access rules rather than requiring `conversation.clientId` to match the current client.
5. Admin APIs may inspect and manage conversations regardless of `clientId`.
6. Parent/child lineage does not weaken the client boundary. Starting or viewing a child conversation does not by itself grant a different client the ability to read or write that child conversation's context.
7. A child conversation may validly have a different `clientId` than its parent conversation. The child's `clientId` is derived from the authenticated client that creates that child conversation, and client access checks apply independently to the parent and child.

This keeps the client boundary precise and avoids accidental cross-client context access through lineage relationships while preserving user-token-based history sharing.

### Storage changes

Model changes:

- add `agent_id` and `client_id` columns/fields to conversations,
- remove `agent_id` and `client_id` columns/fields from entries where practical,
- update indexes and cache keys to use conversation identity instead of entry identity.

The data model change is explicit: `client_id` moves onto conversations in storage. It becomes part of admin-facing conversation representations, but it is not added to user-level REST conversation objects or request payloads.

Query simplifications:

- context retrieval no longer filters by request `agentId`,
- context sync no longer groups by `clientId` or `agentId`,
- list-entry handlers lose agent-specific validation branches.

### Compatibility and migration

This repo is pre-release and datastores are frequently reset, so no backward-compatibility layer is required.

The implementation should:

- delete entry-level `agentId` contract fields,
- delete entry-level `clientId` response fields,
- rewrite context scoping to conversation scope, and
- update framework proxies to treat agent identity as a conversation property.
- update the existing migration (db will be reset)

## Testing

BDD coverage should focus on the new invariant that every conversation has one `clientId` and at most one optional `agentId`.

```gherkin
Feature: Conversation-level client identity

  Scenario: Root conversation exposes optional agent identity
    Given an authenticated client "planner-app"
    When I create conversation "planner-root" with agentId "planner"
    Then the conversation field "agentId" should be "planner"
    And syncing context for conversation "planner-root" as client "planner-app" should succeed

  Scenario: Root conversation can be created without agentId
    Given an authenticated client "planner-app"
    When I create conversation "root-no-agent" without agentId
    Then the request should succeed
    And syncing context for conversation "root-no-agent" as client "planner-app" should succeed

  Scenario: Child conversation keeps lineage and can set child agent identity
    Given conversation "parent" owned by agent "planner"
    And parent entry "delegate-entry" exists in conversation "parent"
    When I append the first entry to new conversation "child" with:
      | startedByConversationId | parent         |
      | startedByEntryId        | delegate-entry |
      | agentId                 | researcher     |
    Then the child conversation field "startedByConversationId" should be "parent"
    And the child conversation field "startedByEntryId" should be "delegate-entry"
    And the child conversation field "agentId" should be "researcher"

  Scenario: Sync does not require agentId
    Given conversation "child" has clientId "planner-app"
    When I sync context for conversation "child"
    Then the request should succeed

  Scenario: Context is isolated per conversation instead of per agentId
    Given conversation "a" is owned by agent "planner"
    And conversation "b" is owned by agent "researcher"
    When I sync context independently for both conversations
    Then each conversation should have its own latest epoch

  Scenario: A different client can still append history when the user has access
    Given conversation "child" has clientId "planner-app"
    And I am authenticated as client "other-app"
    When I append a history entry to conversation "child"
    Then the request should succeed

  Scenario: A different client cannot sync context with a mismatched client identity
    Given conversation "child" has clientId "planner-app"
    And I am authenticated as client "other-app"
    When I sync context for conversation "child"
    Then the request should be rejected

  Scenario: Conversation listing can still filter by ancestry
    Given root conversation "parent"
    And child conversation "child" started from "parent"
    When I list conversations with ancestry "roots"
    Then I should see "parent"
    And I should not see "child"
    When I list conversations with ancestry "children"
    Then I should see "child"
```

Unit/store tests should cover:

- conversation-level persistence of required `clientId` and optional `agentId`,
- removal of entry-level `agentId`/`clientId` fields,
- sync/list context behavior with per-conversation epoch state,
- child conversation creation with lineage plus optional child identity,
- creation of root conversations with `clientId` derived from authenticated client context,
- validation that existing conversation identity cannot be overridden on append,
- rejection of context read/write requests when authenticated `clientId` does not match `conversation.clientId`,
- acceptance of history reads/appends across different clients when the authenticated user token has conversation access,
- support for parent and child conversations using different `clientId` values with independent enforcement,
- validation that existing conversations cannot be converted in place to a different `clientId` or `agentId`.

## Tasks

- [x] Create required conversation-level `clientId` and optional conversation-level `agentId` fields in OpenAPI and protobuf contracts
- [x] Remove entry-level `agentId` from OpenAPI and protobuf entry models and requests
- [x] Remove entry-level `clientId` from public entry responses
- [ ] Update append-entry semantics so `agentId` is only used when auto-creating a new conversation, or add explicit create-conversation support for conversations with optional `agentId`
- [x] Rewrite context sync/list logic to use conversation-scoped epochs
- [x] Add conversation-level `clientId` and optional `agentId` fields to the Go model and store abstractions
- [x] Update Go store creation paths to persist conversation-level identity for explicit and auto-created conversations
- [x] Update Go route validation so conversation creation requires authenticated client context and sync no longer requires `agentId`
- [x] Update framework proxies and examples to treat agent identity as conversation-level
- [x] Add BDD coverage for single-agent conversation invariants
- [x] Update related design/docs pages that still describe multi-agent sharing via entry-level `clientId`

## Files to Modify

| File | Change |
|------|--------|
| `docs/enhancements/089-single-agent-conversations.md` | New proposal describing the simplified model |
| `docs/enhancements/partial/088-agent-conversation-lineage.md` | Cross-reference this simplification proposal and narrow remaining scope if adopted |
| `contracts/openapi/openapi.yml` | Add optional conversation `agentId`, keep `clientId` out of user REST schemas, and simplify entry APIs |
| `contracts/openapi/openapi-admin.yml` | Expose conversation-level `clientId` and optional `agentId` for admin APIs |
| `contracts/protobuf/memory/v1/memory_service.proto` | Move required `client_id` and optional `agent_id` to conversation messages and simplify entry RPCs |
| `internal/model/model.go` | Add conversation-level identity fields and remove entry-level identity fields |
| `internal/plugin/route/entries/entries.go` | Remove per-entry `agentId` validation, enforce immutable conversation identity, and apply `clientId` checks only to context operations while leaving history access membership-based |
| `internal/plugin/route/conversations/conversations.go` | Support conversation creation with required `clientId` context and optional `agentId` |
| `internal/plugin/store/postgres/postgres.go` | Persist conversation-level identity and simplify context queries |
| `internal/plugin/store/sqlite/sqlite.go` | Persist conversation-level identity and simplify context queries |
| `internal/plugin/store/mongo/mongo.go` | Persist conversation-level identity and simplify context queries |
| `internal/plugin/store/postgres/db/schema.sql` | Add conversation identity columns and drop entry identity columns |
| `internal/plugin/store/sqlite/db/schema.sql` | Add conversation identity columns and drop entry identity columns |
| `docs/entry-data-model.md` | Rewrite memory/context scoping docs from client-entry scope to conversation scope |
| `docs/design.md` | Update API and memory scoping overview |

## Verification

```bash
# Compile Go packages affected by the model/route/store changes
go build ./...

# Run targeted BDD coverage once scenarios exist
go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1
```

## Design Decisions

#### Keep `startedByEntryId`

The parent entry reference remains useful even when `agentId` is optional and stored at conversation scope. It gives the service and clients a stable pointer to the delegation event that created the child conversation, which helps UI navigation, orchestration debugging, and auditability.

#### Keep `ancestry`

`ancestry` still provides value because parent/child conversations remain a first-class structure even after removing multi-agent sharing inside a single conversation. It is still useful to separate top-level user-visible threads from agent-spawned child work.

#### Move `clientId` to the conversation

`clientId` should move to the conversation because every conversation is created within an authenticated client context, and `clientId` records that conversation-level association for later authorization and auditing. Keeping it on entries would preserve old complexity without preserving a meaningful capability.

#### Enforce agent access by `clientId`

`clientId` should be required to establish conversations and should gate context-memory access after creation, but it should not replace normal user-token history sharing. This prevents unrelated agent clients from reading or mutating another client's context while preserving standard conversation history access rules. `agentId` remains useful for logical labeling when present, but it is not the trust boundary.

## Non-Goals

- Supporting multiple logical agents writing distinct context streams into the same conversation
- Supporting in-place reassignment of a conversation from one agent/client identity to another
- Preserving backward compatibility with the entry-level identity shape

## Open Questions

1. If append-entry is kept as the creation path for child conversations, should `CreateEntryRequest.agentId` remain as a creation-only convenience field or should child conversations with `agentId` require an explicit conversation-create call first?
