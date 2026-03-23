---
status: proposed
---

# Enhancement 089: Single-Agent Conversations

> **Status**: Proposed.

## Summary

Simplify the agent lineage model by standardizing on one logical agent per conversation. Store agent identity and agent client identity on the conversation instead of on each entry, keep explicit parent/child conversation lineage with `startedByConversationId` and `startedByEntryId`, and keep conversation-list `ancestry` filters for roots vs child conversations.

## Motivation

Enhancement [088](partial/088-agent-conversation-lineage.md) assumes multiple logical agents may participate in the same conversation. That assumption adds complexity in several places:

1. `agentId` is stored and validated on every entry write even though the dominant orchestration pattern is "start a new conversation for another agent".
2. Context isolation becomes `(conversationId, clientId, agentId)` instead of being naturally scoped by the conversation itself.
3. Entry list and sync APIs require agent-specific parameters and validation that exist only to support multiple agents sharing one conversation.
4. The data model allows ambiguous ownership questions such as "which agent does this conversation belong to?" even though the real workflow already answers that at conversation creation time.

The intended orchestration model is simpler:

- a conversation is the working memory and transcript for exactly one logical agent,
- when that agent needs another agent, it starts a child conversation,
- the child conversation records which parent conversation and parent entry started it, and
- child conversations remain discoverable through `ancestry` filters and direct child-listing APIs.

This keeps the lineage model but removes per-entry agent attribution as a first-class concern.

## Design

### Core rule

Each conversation has at most one logical agent identity.

- Agent-to-agent delegation always creates a new conversation.
- Another agent does not "join" an existing conversation as a peer author.
- User-authored and system-authored entries may still exist inside the conversation, but they belong to the conversation's single logical agent context.

### Conversation model

Move agent identity from entry scope to conversation scope.

Add these nullable conversation fields:

| Field | Type | Meaning |
|------|------|---------|
| `agentId` | string | Logical agent that owns and operates within this conversation |
| `clientId` | string | Authenticated app/system client identity for that agent conversation |
| `startedByConversationId` | uuid | Parent conversation that started this child conversation |
| `startedByEntryId` | uuid | Parent entry that triggered creation of this child conversation |

Semantics:

- `agentId` is conversation-scoped, not entry-scoped.
- `clientId` is also conversation-scoped for agent conversations.
- User-created conversations may leave both `agentId` and `clientId` null until an agent first creates or claims the conversation.
- `startedByConversationId` and `startedByEntryId` remain conversation lineage fields and are not copied to entries.

### Entry model

Remove agent and client attribution from the public entry model.

- Remove `agentId` from `Entry` and `CreateEntryRequest`.
- Remove `clientId` from `Entry` responses.
- Entry writes inherit the effective agent/client identity from the target conversation when the caller is an authenticated agent client.

`userId` stays on entries because user authorship is still a per-entry property. A conversation may contain:

- user-authored history entries,
- agent-authored history entries from the conversation's owning agent, and
- context entries for the conversation's owning agent.

### Should `clientId` move to conversation level?

Yes.

If the service adopts one-agent-per-conversation, `clientId` should move to the conversation for the same reason `agentId` should:

- context isolation should be defined by the conversation, not by repeated per-entry tags,
- sync/list APIs no longer need a caller-supplied `agentId`,
- caching keys can collapse from `(conversationId, clientId)` or `(conversationId, clientId, agentId)` to just `conversationId`, and
- an agent conversation should have one authenticated app/system identity associated with it.

This does not change authentication. The request is still authenticated as a client. The change is that the conversation stores which client identity owns the agent side of that conversation, and entry persistence no longer repeats that value on each row.

### Conversation creation rules

There are now three conversation creation cases:

#### 1. User-root conversation

- Created without `agentId` or `clientId`.
- Behaves like a normal user conversation until an agent begins operating in it.

#### 2. Agent-root conversation

- Created with conversation-level `agentId`.
- `clientId` is derived from the authenticated client and persisted on the conversation.
- All context entries in that conversation belong to that single `(conversation.agentId, conversation.clientId)` pair.

#### 3. Child conversation

- Created by first append into a new conversation ID with `startedByConversationId` and optional `startedByEntryId`.
- Must also establish the child conversation's `agentId`.
- `clientId` is derived from the authenticated caller and stored on the child conversation.
- Memberships and owner are copied from the parent conversation as in Enhancement 088.

### Conversation mutation rules

Conversation-level `agentId` and `clientId` should be immutable after the conversation first becomes agent-owned.

That avoids a hard class of bugs:

- cached context becoming attached to the wrong agent,
- mixed entry provenance after reassignment, and
- ambiguity around whether older entries belong to the previous or current agent.

If reassignment is ever needed later, it should be modeled as a new conversation, not an in-place update.

### API changes

#### Conversations

Keep:

- `startedByConversationId`
- `startedByEntryId`
- `ancestry=roots|children|all`
- `GET /v1/conversations/{conversationId}/children`

Add conversation-level identity fields to `Conversation` and `ConversationSummary`:

```yaml
Conversation:
  properties:
    agentId:
      type: string
      nullable: true
    clientId:
      type: string
      nullable: true
```

For write APIs, the service should support one of these approaches:

1. `CreateConversationRequest.agentId` for explicit conversation creation.
2. `CreateEntryRequest.agentId` only as a creation-time convenience when auto-creating a new conversation.

Option 2 is acceptable as a transitional request shape, but the persisted model should remain conversation-level. Once the conversation exists, append-entry requests should not accept `agentId` or `clientId`.

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

Context becomes conversation-scoped.

Old model:

- `(conversationId, clientId)` before Enhancement 088
- `(conversationId, clientId, agentId)` in Enhancement 088

New model:

- `conversationId`

Epoch sequences become per-conversation instead of per-client or per-agent-within-client.

This is the largest simplification in the proposal. It aligns the persistence model with the actual orchestration model: one active agent context per conversation.

### Validation rules

For agent-authenticated writes:

- if the conversation already has `clientId`, it must match the authenticated client,
- if the conversation already has `agentId`, appends operate under that conversation identity,
- entry writes cannot override either value.

For user-authenticated writes:

- users may append normal history entries subject to conversation access rules,
- users cannot set or mutate conversation `agentId` or `clientId` through entry writes.

For child conversation creation:

- `startedByConversationId` must reference a writable parent conversation,
- `startedByEntryId`, when present, must be visible in the parent ancestry,
- the child conversation must have an `agentId`,
- the child `clientId` is always derived from the authenticated client, not caller input.

### Access control

Agent access control should be based on authenticated `clientId`, not on `agentId`.

- `clientId` is the security boundary for agent callers.
- `agentId` is logical attribution within that client namespace and must not be treated as sufficient authorization.

Rules:

1. An agent-authenticated caller may access an agent-owned conversation only when `conversation.clientId` matches the authenticated client.
2. When an agent creates a conversation, that conversation's `clientId` is set from the authenticated client and becomes immutable.
3. Shared user membership does not grant cross-client agent access. A user may be able to read or write a conversation through normal membership rules while a different agent client is still forbidden from agent API access to that same conversation.
4. Admin APIs may inspect and manage conversations regardless of `clientId`.
5. Parent/child lineage does not weaken the ownership boundary. If client `A` creates a child conversation for work done by client `B`, then that child conversation belongs to client `B`; client `A` should receive child results through explicit application/runtime handoff, not by acting as client `B`.

This keeps agent ownership crisp and avoids accidental cross-client access through shared user permissions or lineage relationships.

### Storage changes

Model changes:

- add `agent_id` and `client_id` columns/fields to conversations,
- remove `agent_id` and `client_id` columns/fields from entries where practical,
- update indexes and cache keys to use conversation identity instead of entry identity.

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

## Testing

BDD coverage should focus on the new invariant that a conversation belongs to one agent.

```gherkin
Feature: Single-agent conversations

  Scenario: Agent-owned conversation exposes conversation-level identity
    Given an authenticated client "planner-app"
    When I create conversation "planner-root" with agentId "planner"
    Then the conversation field "agentId" should be "planner"
    And the conversation field "clientId" should be "planner-app"

  Scenario: Child conversation keeps lineage and sets child agent identity
    Given conversation "parent" owned by agent "planner"
    And parent entry "delegate-entry" exists in conversation "parent"
    When I append the first entry to new conversation "child" with:
      | startedByConversationId | parent         |
      | startedByEntryId        | delegate-entry |
      | agentId                 | researcher     |
    Then the child conversation field "startedByConversationId" should be "parent"
    And the child conversation field "startedByEntryId" should be "delegate-entry"
    And the child conversation field "agentId" should be "researcher"

  Scenario: Sync does not require agentId for agent-owned conversation
    Given conversation "child" is owned by agent "researcher"
    When I sync context for conversation "child"
    Then the request should succeed

  Scenario: Context is isolated per conversation instead of per agentId
    Given conversation "a" is owned by agent "planner"
    And conversation "b" is owned by agent "researcher"
    When I sync context independently for both conversations
    Then each conversation should have its own latest epoch

  Scenario: An agent cannot append using a mismatched client identity
    Given conversation "child" has clientId "planner-app"
    And I am authenticated as client "other-app"
    When I append an entry to conversation "child"
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

- conversation-level persistence of `agentId` and `clientId`,
- removal of entry-level `agentId`/`clientId` fields,
- sync/list context behavior with per-conversation epoch state,
- child conversation creation with lineage plus child identity,
- validation that existing conversation identity cannot be overridden on append,
- rejection of agent API access when authenticated `clientId` does not match the conversation owner client.

## Tasks

- [ ] Create new conversation-level `agentId` and `clientId` fields in OpenAPI and protobuf contracts
- [ ] Remove entry-level `agentId` from OpenAPI and protobuf entry models and requests
- [ ] Remove entry-level `clientId` from public entry responses
- [ ] Update append-entry semantics so `agentId` is only used when auto-creating a new conversation, or add explicit create-conversation support for agent-owned conversations
- [ ] Rewrite context sync/list logic to use conversation-scoped epochs
- [ ] Update stores and indexes for conversation-level identity
- [ ] Update route validation to enforce immutable conversation identity and client match rules
- [ ] Update framework proxies and examples to treat agent identity as conversation-level
- [ ] Add BDD coverage for single-agent conversation invariants
- [ ] Update related design/docs pages that still describe multi-agent sharing via entry-level `clientId`

## Files to Modify

| File | Change |
|------|--------|
| `docs/enhancements/089-single-agent-conversations.md` | New proposal describing the simplified model |
| `docs/enhancements/partial/088-agent-conversation-lineage.md` | Cross-reference this simplification proposal and narrow remaining scope if adopted |
| `contracts/openapi/openapi.yml` | Move `agentId`/`clientId` to conversation schemas and simplify entry APIs |
| `contracts/openapi/openapi-admin.yml` | Mirror conversation-level identity changes for admin APIs |
| `contracts/protobuf/memory/v1/memory_service.proto` | Move identity fields to conversation messages and simplify entry RPCs |
| `internal/model/model.go` | Add conversation-level identity fields and remove entry-level identity fields |
| `internal/plugin/route/entries/entries.go` | Remove per-entry `agentId` validation and enforce conversation identity rules |
| `internal/plugin/route/conversations/conversations.go` | Support agent-owned conversation creation and expose conversation identity |
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

The parent entry reference remains useful even with one agent per conversation. It gives the service and clients a stable pointer to the delegation event that created the child conversation, which helps UI navigation, orchestration debugging, and auditability.

#### Keep `ancestry`

`ancestry` still provides value because parent/child conversations remain a first-class structure even after removing multi-agent sharing inside a single conversation. It is still useful to separate top-level user-visible threads from agent-spawned child work.

#### Move `clientId` to the conversation

`clientId` should move for the same reason `agentId` moves: under the new invariant, it describes the agent context that owns the conversation, not individual entry rows. Keeping it on entries would preserve old complexity without preserving a meaningful capability.

#### Enforce agent access by `clientId`

If one agent client owns one conversation, then agent-side authorization should follow the conversation's `clientId`. This prevents unrelated agent clients from reading or mutating another client's context just because a user membership or parent/child lineage relationship exists. `agentId` remains useful for logical labeling, but it is not the trust boundary.

## Non-Goals

- Supporting multiple logical agents writing distinct context streams into the same conversation
- Supporting in-place reassignment of a conversation from one agent/client identity to another
- Preserving backward compatibility with the entry-level identity shape

## Open Questions

1. Should a user-root conversation be allowed to transition once into an agent-owned conversation, or should agent-owned conversations only be created explicitly as agent conversations?
2. If append-entry is kept as the creation path for child conversations, should `CreateEntryRequest.agentId` remain as a creation-only convenience field or should agent-owned child conversations require an explicit conversation-create call first?
