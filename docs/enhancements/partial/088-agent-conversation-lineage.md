---
status: partial
---

# Enhancement 088: Agent Conversation Lineage & Attribution

> **Status**: Partial.

See also [089](089-single-agent-conversations.md) for the partial single-agent simplification that keeps lineage but standardizes on one logical agent per conversation.

## Summary

Add first-class support for agent-to-agent conversations by introducing explicit logical `agentId` attribution on entries, agent-scoped context isolation within a client, and explicit parent/child lineage between conversations. This lets one agent start a sub-agent conversation by using the existing append-entry API, copy the parent conversation's memberships, atomically create the child conversation with the first explicit entry to the sub-agent, and later list child conversations.

The implementation also includes developer-facing documentation and framework support:

- a site concept page explaining parent/child agent workflows,
- per-framework guide pages showing how to use the workflow through the framework proxy APIs and existing memory-service APIs, and
- runnable examples that simulate an agent-to-agent conversation.

Admin APIs should gain parallel support for the same parent/child conversation metadata and listing/filtering capabilities.

## Motivation

The current model handles:

- user-visible history entries,
- agent-scoped context entries, and
- fork lineage for alternate conversation branches.

It does not cleanly model a common orchestration pattern where one agent invokes another agent as a tool and opens a new conversation for that sub-task.

That creates three gaps:

1. **No sender identity for logical agent-authored history entries**. `userId` identifies the human user associated with an entry, but it does not answer which logical agent authored a history entry.
2. **Current `clientId` is the wrong abstraction for multi-agent apps**. It identifies the authenticated app/system client, but one client may host many logical agents. Reusing `clientId` as the agent identity would prevent isolating memory/context between those agents.
3. **No explicit parent/child conversation lineage**. Forking is about branching visible conversation history; it is not the same as "agent A started conversation B while working inside conversation C".
4. **No child-conversation listing for orchestration visibility**. Applications cannot ask which conversations were spawned from a conversation, or filter top-level conversations separately from child conversations.

Desired behavior:

- An initiating agent can start a new conversation with another agent.
- The child conversation copies the parent's membership settings.
- The new message sent to the sub-agent is created atomically with the child conversation and becomes the first entry in that conversation.
- Any agent with normal access can still append new messages to an existing conversation.
- Appending to an existing conversation does not create or modify any parent/child relationship.
- Entries in that child conversation can identify which agent authored them.
- Multiple logical agents inside the same authenticated client can keep isolated context/history.
- The service can list child conversations started from a conversation.
- Conversation listing can filter roots vs child conversations.
- Framework packages expose the new APIs through their existing proxy abstractions.
- The site includes end-to-end examples showing a parent agent delegating work to a sub-agent.

## Design

### Terminology

- **Client**: the authenticated app/system identity used for authn/authz and rate limiting.
- **Agent attribution**: which logical agent authored a given entry.
- **Started conversation**: a new conversation created by an agent while working in another conversation.
- **Parent conversation**: the conversation from which a child conversation was started.

This proposal is intentionally separate from forking. A fork creates an alternate branch in the same conversation group. A started conversation creates a new child conversation for agent orchestration.

### Public API Model Changes

The Agent API and Admin API should stay aligned for these fields and filters unless there is a security reason to differ.

#### Entry

Add a nullable `agentId` field to `Entry` and `CreateEntryRequest`.

```yaml
Entry:
  properties:
    userId:
      type: string
      nullable: true
    agentId:
      type: string
      nullable: true
      description: Agent that authored this entry, when applicable.
```

Semantics:

- `userId` continues to identify the human user associated with the entry.
- `agentId` identifies the logical agent sender for agent-authored entries.
- User-authored history entries typically have `agentId: null`.
- Agent-authored history entries set `agentId`.
- Context entries may also expose `agentId` for consistency.

Implementation note:

- The service already persists `clientId` on entries. That value should keep representing the authenticated client/app identity.
- This proposal adds a distinct persisted `agentId` for logical agent identity.
- `clientId` and `agentId` may be the same in simple deployments, but they are not equivalent concepts.
- `agentId` is client-scoped, not globally unique across the whole service.

#### Conversation

Add nullable lineage fields to `Conversation` and child-conversation append requests.

```yaml
CreateEntryRequest:
  properties:
    startedByConversationId:
      type: string
      format: uuid
    startedByEntryId:
      type: string
      format: uuid

Conversation:
  properties:
    startedByConversationId:
      type: string
      format: uuid
      nullable: true
    startedByEntryId:
      type: string
      format: uuid
      nullable: true
```

Semantics:

- `startedByConversationId` identifies the parent conversation.
- `startedByEntryId` identifies the source parent entry that caused the child conversation to be started.
- `startedByEntryId` must belong to the visible ancestry of `startedByConversationId`, including entries inherited through that conversation's fork lineage.
- When `startedByConversationId` is set, the service copies the parent's conversation memberships into the child conversation during creation.
- The child conversation does not inherit or copy parent history entries.

This relation is independent of `forkedAtConversationId` / `forkedAtEntryId`.

Lineage is only established when a new conversation is created with `startedByConversationId`. Appending entries to an existing conversation never creates, updates, or removes parent/child lineage.

The Admin API should expose the same lineage fields on conversation and entry payloads.

### Entry Attribution Rules

For REST and gRPC agent-authenticated writes:

- `clientId` continues to be filled from the authenticated client identity.
- `agentId` is required for writes that should participate in logical agent attribution or agent-scoped context isolation.
- If `agentId` is omitted on a history write, the entry is treated as client-authored but not agent-attributed.
- If `agentId` is omitted on a context write, the request is rejected because context must be isolated per logical agent.
- Agents may append to any existing conversation they can normally write to; this is orthogonal to whether the conversation has a parent or children.
- The server treats `agentId` as a caller-supplied logical identifier scoped within the authenticated `clientId`.

For user-authenticated writes:

- `agentId` must be omitted or null.

This keeps client authentication separate from logical agent identity while allowing one client to host many agents.

### Agent-Scoped Context Isolation

Context synchronization and retrieval must be isolated by `(conversationId, clientId, agentId)`, not just `(conversationId, clientId)`.

That means:

- Two agents under the same authenticated client do not see each other's context entries.
- History entries remain conversation-visible according to normal access rules.
- Context APIs require `agentId` whenever the caller is an authenticated client.

Example:

- `clientId = "assistant-platform"`
- `agentId = "planner"`
- `agentId = "researcher"`

Both logical agents can operate in the same conversation, but each gets its own isolated context epochs and sync state.

### Conversation Creation Rules

When appending the first entry to a new child conversation with `startedByConversationId`:

1. The caller must have write access to the parent conversation.
2. The caller uses the existing append-entry API against a new conversation ID and includes `startedByConversationId`.
3. The request entry becomes the first entry that will be sent to the sub-agent.
4. The service atomically creates the child conversation in a new conversation group and appends that first entry.
5. The child conversation copies the parent conversation's `ownerUserId`.
6. The service copies the parent's membership settings to the child conversation group.
7. The service does not copy or inherit any parent history entries.

This is allowed even when the caller is not the parent owner. A caller with write access may create the child conversation, and the child still inherits the parent conversation's ownership.

For existing conversations, normal append behavior remains unchanged:

- any user or agent with write access may add new entries,
- child conversations may receive entries from agents other than the one that started them, and
- these later appends do not affect `startedByConversationId` / `startedByEntryId`.

Copying is preferred over dynamic ancestry traversal because:

- started conversations are not alternate branches,
- copied memberships preserve the same access envelope by default, and
- tree/list APIs should not need fork-style ancestry rules to reconstruct child history, and
- the sub-agent conversation should begin with the delegating message that created it, not a replay of the parent transcript, and
- atomic creation avoids empty child conversations with lineage metadata but no initiating message.

### Child Conversation Listing APIs

Add child-conversation listing APIs distinct from fork APIs.

REST:

```text
GET /v1/conversations/{conversationId}/children
```

Query parameters:

- `afterCursor`: optional child conversation ID cursor for pagination.
- `limit`: optional page size limit.

Admin REST:

```text
GET /v1/admin/conversations/{conversationId}/children
```

Query parameters:

- `afterCursor`: optional child conversation ID cursor for pagination.
- `limit`: optional page size limit.

Suggested response shape:

```json
{
  "data": [
    {
      "conversationId": "child-id",
      "title": "Research sub-agent",
      "startedByConversationId": "parent-id",
      "startedByEntryId": "entry-id",
      "createdAt": "2026-03-23T12:00:00Z"
    }
  ],
  "afterCursor": null
}
```

gRPC:

- `ListChildConversations`

The API should only return conversations visible to the authenticated caller under existing access rules.

Visibility rules for child listing:

- only direct children of the requested parent conversation are returned.
- child conversations are ordered by `createdAt` ascending, with `conversationId` ascending as the deterministic tie-breaker.
- `afterCursor` is the last returned child `conversationId`; the server resolves that conversation and continues strictly after its `(createdAt, conversationId)` sort position.
- pagination follows the same cursor/limit pattern as other conversation-list APIs.

### Conversation List Filtering

The main conversation listing API should gain a filter for parented vs root conversations.

REST:

```text
GET /v1/conversations?ancestry=roots
GET /v1/conversations?ancestry=children
GET /v1/conversations?ancestry=all
```

gRPC:

- add an enum such as `ConversationAncestryFilter`

Semantics:

- `roots` returns only conversations with no `startedByConversationId`; this is the default.
- `children` returns only conversations with a non-null `startedByConversationId`.
- `all` returns both root and child conversations.

Composition with the existing conversation-list `mode` parameter:

- listing first filters by `ancestry`,
- the existing `mode` grouping/selection is then applied to the filtered set, and
- this keeps `ancestry` orthogonal to fork-tree selection instead of replacing it.

Examples:

- `ancestry=children&mode=all` returns all child conversations visible to the caller.
- `ancestry=children&mode=latest-fork` first keeps only child conversations, then returns the most recently updated visible child conversation from each fork tree.
- `ancestry=roots&mode=latest-fork` first keeps only non-child conversations, then returns the most recently updated visible root conversation from each fork tree.

This keeps the default conversation list focused on top-level conversations while still allowing child conversations to be discovered when needed.

The Admin API should gain the same filtering capability for administrative listings, for example:

```text
GET /v1/admin/conversations?ancestry=roots
GET /v1/admin/conversations?ancestry=children
GET /v1/admin/conversations?ancestry=all
```

### Framework Proxy APIs

The framework packages should expose the new server capabilities through the existing proxy concept so application code does not need to hand-roll raw REST calls.

Expected proxy surface additions:

- create a started child conversation from a parent conversation together with its first entry,
- create a started child conversation from a parent conversation together with its first entry using the existing append-entry API,
- append/list entries with `agentId`,
- sync/list context with explicit `agentId`, and
- retrieve child conversations, and
- filter conversation listing to roots, children, or all.

Admin clients should also be able to inspect child conversations and parent-filtered listings through the Admin API surface.

Examples:

- Java Spring and Quarkus proxy controllers/clients should surface started-conversation creation, child listing, and parent-filtered conversation listing.
- Python LangChain proxy helpers should expose high-level methods for starting a child conversation and operating as a named logical agent.
- TypeScript helpers should expose the same workflow for Vercel AI examples when that package supports the feature.

The proxy APIs should make the common workflow straightforward:

1. parent agent receives a task in conversation `A`
2. parent agent appends the first entry to new child conversation `B` with `startedByConversationId=A` in one call
3. parent agent invokes the sub-agent through the framework proxy
4. sub-agent reads the first delegating entry from `B`
5. sub-agent writes history/context using its own `agentId`

### Documentation and Examples

This enhancement should ship with site docs and runnable tutorial checkpoints, not just backend APIs.

Required documentation deliverables:

- a concept page under `site/src/pages/docs/concepts/` explaining:
  - the difference between `clientId` and `agentId`
  - parent/child agent conversations vs conversation forking
  - no copied history and copied memberships
  - root-vs-child conversation listing behavior
  - how parent/child behavior is expressed through existing APIs such as append entry, list conversations, and list entries
  - when to use child conversations vs writing directly into one conversation
- framework guide pages under each supported framework docs section explaining how to use the workflow through that framework's proxy API and how those proxy calls map onto the underlying existing APIs
- runnable checkpoint examples that imitate an agent-to-agent conversation, with a parent agent delegating to a sub-agent

The examples should follow the site's existing checkpoint/tutorial style:

- copy the nearest prior checkpoint as a starting point,
- add `<TestScenario>` and `<CurlTest>` coverage to the docs pages,
- keep examples incremental, and
- show concrete proxy API usage rather than pseudocode-only snippets, and
- show the corresponding existing REST/gRPC API usage for the same parent/child workflow where helpful.

### Data Model

#### Conversations

Add nullable columns / fields:

```sql
ALTER TABLE conversations
    ADD COLUMN started_by_conversation_id UUID NULL REFERENCES conversations(id),
    ADD COLUMN started_by_entry_id UUID NULL REFERENCES entries(id);

CREATE INDEX idx_conversations_started_by_conversation_id
    ON conversations (started_by_conversation_id);
```

Deletion semantics:

- deleting a conversation still deletes its fork tree under the existing rules,
- deleting a parent conversation also cascade-deletes all direct and indirect started child conversations, and
- deleting a child conversation deletes that child's own descendants in addition to its fork tree.

#### Entries

Entries should persist both:

- `client_id` for authenticated app/system identity, and
- `agent_id` for logical agent identity.

Required work:

- add `agent_id` storage to entry records/documents,
- expose `agentId` in public contracts,
- validate `agentId` on write,
- update context queries to scope by `client_id` and `agent_id`, and
- document the semantics for history vs context entries.

### API Examples

#### Start a child conversation from an agent using append entry

```json
{
  "startedByConversationId": "08e1a5a0-2254-4fb7-a66d-8e5dfce241d1",
  "startedByEntryId": "8d09f1e8-e5cd-4f38-9725-f0411043b224",
  "channel": "history",
  "contentType": "history",
  "agentId": "planner-agent",
  "content": [
    {
      "role": "USER",
      "text": "Ask the researcher to evaluate options."
    }
  ]
}
```

#### Append an agent-authored history entry

```json
{
  "channel": "history",
  "contentType": "history",
  "agentId": "planner-agent",
  "content": [
    {
      "role": "USER",
      "text": "Ask the researcher to evaluate options."
    }
  ]
}
```

### Design Decisions

#### Use `agentId` in public APIs instead of `agent`

`agentId` matches the existing naming style of `userId`, `conversationId`, and `forkedAtConversationId`. A generic `agent` field is more ambiguous because it could imply an embedded object instead of an identifier.

#### Keep `clientId` and `agentId` separate

`clientId` is the authenticated app/system identity. `agentId` is the logical actor inside that client. `agentId` is only meaningful within a `clientId` namespace. Conflating them breaks multi-agent applications that route many logical agents through one client credential set.

#### Keep started-conversation lineage separate from fork lineage

Forks represent alternate branches of one conversation. Started conversations represent orchestration relationships between conversations. Combining them would blur semantics and make tree queries harder to reason about.

#### Create child conversations atomically with their first entry

The child conversation should begin with the explicit delegating message sent to the sub-agent. Replaying parent history would blur the boundary between the parent and child tasks and would make the sub-agent transcript harder to reason about. Making creation and first entry atomic avoids orphaned child conversations with lineage metadata but no initiating message.

#### Teach the feature through proxy APIs, not only raw HTTP

Most users consume memory-service through framework packages and their proxy abstractions. If the site docs only show raw REST examples, users will still have to rediscover how to integrate the feature in Spring, Quarkus, Python, or TypeScript. The framework proxy layer should therefore be part of the design, examples, and docs scope.

The docs should also make clear that these parent/child features are additions to existing APIs rather than a separate API family. Readers should be able to see how:

- append entry starts a child conversation when used with a new conversation ID plus `startedByConversationId`,
- conversation listing exposes root-vs-child filtering,
- child listing hangs off the existing conversation resource model, and
- normal entry listing continues to work for child conversations.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Agent conversation lineage

  Scenario: Agent starts a child conversation by appending the first entry
    Given I am authenticated as agent with API key "planner-agent-key"
    And a conversation exists with history entries:
      | role | text                  |
      | USER | Plan my release work. |
      | AI   | I will decompose it.  |
    When I append an entry to a new conversation with:
      """
      {
        "startedByConversationId": "${conversationId}",
        "channel": "history",
        "contentType": "history",
        "agentId": "planner-agent",
        "content": [
          {
            "role": "USER",
            "text": "Ask the researcher to evaluate options."
          }
        ]
      }
      """
    Then the response status is 201
    And the response body should contain "startedByConversationId"
    When I list entries for the new conversation
    Then the response body field "data[0].content[0].text" should be "Ask the researcher to evaluate options."
    And the new conversation owner should match the parent conversation owner
    And the new conversation memberships should match the parent conversation memberships

  Scenario: startedByEntryId may reference an entry visible through a fork tree
    Given conversation "parent" is part of a fork tree
    And entry "source-entry" is visible in the ancestry of conversation "parent"
    When I append an entry to a new child conversation from "parent" with "startedByEntryId" set to "source-entry"
    Then the response status is 201
    And the response body field "startedByEntryId" should be "${sourceEntryId}"

  Scenario: Starting a child conversation requires write access to the parent
    Given I only have read access to conversation "${conversationId}"
    When I append an entry to a new conversation with:
      """
      {
        "startedByConversationId": "${conversationId}",
        "channel": "history",
        "contentType": "history",
        "content": [
          {
            "role": "USER",
            "text": "Ask the researcher"
          }
        ]
      }
      """
    Then the response status is 403

  Scenario: Agent-authored history entry exposes agentId
    Given I am authenticated as agent with API key "planner-agent-key"
    When I append a history entry as the agent
    Then the response status is 201
    And the response body field "agentId" should be "planner-agent"

  Scenario: Appending to an existing conversation does not change lineage
    Given conversation "child" was started from conversation "parent"
    And I have write access to conversation "child"
    When I append a history entry to conversation "child"
    Then the response status is 201
    And the conversation "child" should still reference the same "startedByConversationId"

  Scenario: Two logical agents under one client have isolated context
    Given I am authenticated as agent with API key "shared-client-key"
    When I sync context for agent "planner"
    And I sync context for agent "researcher"
    Then listing context for agent "planner" should not return entries for agent "researcher"

  Scenario: Child conversation listing returns direct started conversations
    Given conversation "parent" has child conversations "child-a" and "child-b"
    When I list child conversations for "parent"
    Then the response status is 200
    And the response body field "data[0].startedByConversationId" should be "${parentConversationId}"

  Scenario: Child conversation listing excludes invisible direct children
    Given conversation "parent" has child conversation "hidden-child"
    And I cannot access conversation "hidden-child"
    When I list child conversations for "parent"
    Then the response status is 200
    And the response body should not contain "hidden-child"

  Scenario: Conversation listing defaults to roots only
    Given I can access root conversation "root-conversation"
    And I can access child conversation "child-conversation"
    When I list conversations
    Then the response status is 200
    And the response body should contain "root-conversation"
    And the response body should not contain "child-conversation"

  Scenario: Conversation listing can return only child conversations
    Given I can access root conversation "root-conversation"
    And I can access child conversation "child-conversation"
    When I list conversations with ancestry "children"
    Then the response status is 200
    And the response body should not contain "root-conversation"
    And the response body should contain "child-conversation"

  Scenario: Conversation listing can return both root and child conversations
    Given I can access root conversation "root-conversation"
    And I can access child conversation "child-conversation"
    When I list conversations with ancestry "all"
    Then the response status is 200
    And the response body should contain "root-conversation"
    And the response body should contain "child-conversation"
```

### Unit / Integration Tests

- Route validation for `agentId`, `startedByConversationId`, and parent write-access enforcement
- Route validation for atomic child creation with required first entry
- Route tests showing normal appends do not mutate lineage
- Route/store tests validating `startedByEntryId` belongs to the visible ancestry of `startedByConversationId`
- Context sync/list tests scoped by `clientId` plus `agentId`
- Store tests for conversation child lookup by `started_by_conversation_id`
- Store tests verifying child conversations start with only the atomic first entry and no inherited history
- Store tests verifying child conversations copy parent ownership
- Store tests verifying child conversations copy parent memberships
- Store tests verifying deleting a parent conversation cascade-deletes all descendant child conversations
- Child-listing API tests verifying only direct visible children are returned
- Child-listing API tests verifying cursor/limit pagination
- Conversation-list tests for `ancestry=roots|children|all`
- Conversation-list tests verifying `ancestry` filtering happens before `mode` grouping
- gRPC tests for child-listing APIs, conversation `ancestry` filters, and agent-attributed entries

### Site / Tutorial Tests

- Concept page and framework guide pages should include `<TestScenario>` / `<CurlTest>` coverage where applicable
- New checkpoint apps should simulate a parent agent delegating to a sub-agent through the framework proxy
- Site tests should verify child creation writes the first delegating message atomically, that the child can be listed from the parent children API, and that default conversation listing hides child conversations unless requested

## Non-Goals

- Real-time linkage showing every tool call between the parent and child agent
- Automatic propagation of future parent history into existing child conversations
- A scheduler/execution engine for actually invoking sub-agents
- A dedicated recursive conversation tree endpoint in this phase

## Open Questions

### Simplification candidate: one logical agent per conversation

If we standardize on "a conversation belongs to one logical agent, and involving another agent always starts another conversation", we can simplify this design substantially:

- move logical agent identity from `Entry.agentId` to a conversation-level field,
- scope context isolation by conversation instead of `(conversationId, clientId, agentId)`,
- remove `agentId` query/write requirements from entry and sync APIs,
- keep parent/child conversation lineage for orchestration, and
- reconsider whether `startedByEntryId` and list-level `ancestry=children` filtering still justify their added complexity.

The current partial implementation still reflects the more general multi-agent-per-conversation design.

1. Should copied memberships be an exact snapshot of the parent at creation time, or should some roles be normalized for child conversations?
2. Which framework packages are in scope for the first documentation/example rollout: Spring, Quarkus, Python LangChain, Python LangGraph, and TypeScript, or a narrower set?

## Tasks

- [x] Add `agentId` to OpenAPI and protobuf entry schemas
- [x] Add agent-scoped context parameters/requirements to OpenAPI and protobuf sync/list APIs
- [x] Add conversation lineage fields to OpenAPI and protobuf entry/conversation schemas
- [x] Add conversation list `ancestry` filtering to OpenAPI and protobuf list-conversations schemas
- [x] Add parallel Admin API lineage fields, child-listing endpoints, and `ancestry` filters
- [x] Implement REST and gRPC validation for agent attribution and atomic started-conversation creation via append entry
- [x] Add persistence fields and indexes for `agent_id` and started conversation lineage
- [x] Scope context storage and retrieval by `clientId` plus `agentId`
- [x] Copy parent ownership on child conversation creation
- [x] Implement child membership copying on conversation creation
- [x] Ensure child conversations start with the atomic first entry and no inherited history
- [x] Cascade-delete started child-conversation trees when deleting a parent or child conversation
- [x] Add REST and gRPC child-listing APIs
- [x] Ensure child-listing returns only direct visible children
- [x] Add child-listing pagination parameters and cursor handling
- [ ] Expose started-conversation, `agentId`, child-listing, and `ancestry`-filtered conversation listing through framework proxy abstractions
- [ ] Add BDD coverage for lineage, copied history, child listing, and conversation `ancestry` filters
- [x] Add a site concept page for agent/sub-agent workflows
- [ ] Add framework guide pages for the new workflow
- [ ] Document how the new parent/child behavior is expressed through the existing append-entry, list-conversations, and list-entries APIs
- [ ] Add runnable checkpoint examples imitating an agent-to-agent conversation
- [ ] Add site test coverage for the new concept and framework pages
- [ ] Update example clients/docs to demonstrate parent/child agent conversations

## Files to Modify

| File | Purpose |
|------|---------|
| `docs/enhancements/partial/088-agent-conversation-lineage.md` | Proposed design and implementation plan |
| `contracts/openapi/openapi.yml` | Add `agentId`, append-entry child-conversation lineage fields, child-listing endpoints, and conversation `ancestry` filters |
| `contracts/openapi/openapi-admin.yml` | Add parallel Admin API lineage fields, child-listing endpoints, and conversation `ancestry` filters |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add `agent_id`, context isolation inputs, append-entry lineage fields, child-listing RPCs, and conversation `ancestry` filters |
| `internal/model/model.go` | Add conversation lineage fields and distinct entry `agentId` storage |
| `internal/plugin/route/conversations/conversations.go` | Add child-listing endpoints and conversation `ancestry` filters |
| `internal/plugin/route/entries/entries.go` | Validate and populate `agentId` on entry writes |
| `internal/plugin/route/memories/memories.go` | Require and use `agentId` for context isolation within a client |
| `internal/grpc/server.go` | Implement gRPC entry attribution, context isolation, child-listing RPCs, and conversation `ancestry` filters |
| `internal/plugin/store/postgres/db/schema.sql` | Add conversation lineage columns/indexes |
| `internal/plugin/store/sqlite/db/schema.sql` | Add conversation lineage columns/indexes |
| `internal/plugin/store/postgres/postgres.go` | Persist/query `agent_id`, conversation lineage, copied ownership/memberships, and context isolation |
| `internal/plugin/store/sqlite/sqlite.go` | Persist/query `agent_id`, conversation lineage, copied ownership/memberships, and context isolation |
| `internal/plugin/store/mongo/mongo.go` | Persist/query `agent_id`, lineage, copied ownership/memberships, and context isolation for Mongo |
| `internal/bdd/testdata/features/entries-rest.feature` | REST coverage for `agentId` semantics |
| `internal/bdd/testdata/features/forking-rest.feature` | Add separate lineage scenarios or split to a new feature |
| `internal/bdd/testdata/features-grpc/entries-grpc.feature` | gRPC coverage for `agentId` semantics |
| `site/src/pages/docs/concepts/agent-subagent-workflows.mdx` | Concept page for client vs agent identity, child conversations, and `ancestry` filtering |
| `site/src/pages/docs/spring/agent-subagent-workflows.mdx` | Spring guide showing proxy-based parent/sub-agent workflow |
| `site/src/pages/docs/quarkus/agent-subagent-workflows.mdx` | Quarkus guide showing proxy-based parent/sub-agent workflow |
| `site/src/pages/docs/python-langchain/agent-subagent-workflows.mdx` | Python LangChain guide showing proxy-based parent/sub-agent workflow |
| `site/src/pages/docs/python-langgraph/agent-subagent-workflows.mdx` | Python LangGraph guide showing proxy-based parent/sub-agent workflow |
| `site/src/pages/docs/typescript-vecelai/agent-subagent-workflows.mdx` | TypeScript guide showing proxy-based parent/sub-agent workflow |
| `site/src/components/DocsSidebar.astro` | Add concept and framework guide navigation entries |
| `java/spring/examples/doc-checkpoints/*` | Add/update checkpoint app for parent/sub-agent workflow via proxy controller |
| `java/quarkus/examples/doc-checkpoints/*` | Add/update checkpoint app for parent/sub-agent workflow via proxy resource |
| `python/examples/langchain/doc-checkpoints/*` | Add/update checkpoint app for parent/sub-agent workflow via proxy helper |
| `python/examples/langgraph/doc-checkpoints/*` | Add/update checkpoint app for parent/sub-agent workflow |
| `typescript/examples/vecelai/doc-checkpoints/*` | Add/update checkpoint app for parent/sub-agent workflow |
| `python/langchain/memory_service_langchain/proxy.py` | Expose child-conversation and tree APIs in the Python proxy helper |

## Verification

```bash
# Docs-only change in this proposal; no code generation or compile step required yet.
# If implementation starts, regenerate contracts and run affected Go BDD coverage.
go test ./internal/bdd -run 'TestFeatures.*(Entries|Forking|Conversations)' -count=1
```
