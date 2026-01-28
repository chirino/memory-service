# 017 - Hide Conversation Groups from Public API Contracts

## Summary

Remove `conversationGroupId` from all public API contracts (OpenAPI specs, protobuf definitions) while keeping it as an internal implementation detail in the data layer. API consumers should only deal with `conversationId` values; the service resolves group membership internally.

## Motivation

The `conversationGroupId` is an internal concept that ties together a root conversation and all its forks. From an API consumer's perspective, this ID is:

1. **Redundant** - Every API endpoint already accepts `conversationId` as a path parameter, and the service internally resolves the group. Consumers never need to pass `conversationGroupId` to any endpoint.
2. **A leaky abstraction** - It exposes storage-level grouping that users don't need to reason about. Conversations, forks, memberships, and messages are all addressed by `conversationId`.
3. **Confusing** - Having two ID fields (`id` and `conversationGroupId`) on a `Conversation` response raises questions about when to use which.

Since the service can always look up the group given a conversation ID, there is no functional reason to expose it.

## Current State

`conversationGroupId` appears in the following contract locations:

### OpenAPI (`openapi.yml`)

| Schema                    | Field                | Usage                             |
|---------------------------|----------------------|-----------------------------------|
| `Conversation`            | `conversationGroupId`| Returned in response body         |
| `ConversationMembership`  | `conversationGroupId`| Returned in response body         |
| `ConversationForkSummary` | `conversationGroupId`| Returned in response body         |

### OpenAPI (`openapi-admin.yml`)

| Schema                    | Field                | Usage                             |
|---------------------------|----------------------|-----------------------------------|
| `AdminConversation`       | `conversationGroupId`| Returned in response body         |
| `ConversationMembership`  | `conversationGroupId`| Returned in response body         |

### Protobuf (`memory_service.proto`)

| Message                    | Field                    | Field # |
|----------------------------|--------------------------|---------|
| `Conversation`             | `conversation_group_id`  | 8       |
| `ConversationMembership`   | `conversation_group_id`  | 1       |
| `ConversationForkSummary`  | `conversation_group_id`  | 2       |

### Key observation

**No API endpoint accepts `conversationGroupId` as input.** It is only ever returned in responses. All endpoints use `conversationId` as the path parameter, and the store layer resolves the group internally. This means removing it is purely a response-shape change with no impact on request semantics.

## Proposed Changes

### 1. OpenAPI specs

Remove `conversationGroupId` from:
- `Conversation` schema in `openapi.yml`
- `ConversationMembership` schema in `openapi.yml` - replace with `conversationId` (see Memberships section below)
- `ConversationForkSummary` schema in `openapi.yml`
- `AdminConversation` schema in `openapi-admin.yml`
- `ConversationMembership` schema in `openapi-admin.yml` - replace with `conversationId`

### 2. Protobuf definitions

Remove `conversation_group_id` from:
- `Conversation` message
- `ConversationMembership` message - replace with `conversation_id`
- `ConversationForkSummary` message

For proto field number hygiene, mark removed field numbers as `reserved` to prevent accidental reuse.

### 3. Memberships: Replace `conversationGroupId` with `conversationId`

The `ConversationMembership` schema currently identifies the conversation scope via `conversationGroupId`. Since we're hiding that concept, this field should be replaced with `conversationId`.

Internally, memberships are still stored per group (so sharing applies to the root and all forks). The REST/gRPC layer will translate: when returning a membership, it populates `conversationId` with the conversation ID that was used in the request path (e.g., `GET /v1/conversations/{conversationId}/memberships`).

This is consistent with how the API already works - all membership endpoints already take `conversationId` as a path parameter.

### 4. Deletion semantics: Document cascade behavior

Currently, deleting a conversation soft-deletes the entire conversation group (root + all forks). With `conversationGroupId` hidden, this cascade needs to be clearly documented:

> **Deleting a conversation deletes all conversations in the same fork tree** (the root conversation and all its forks). Memberships and messages associated with these conversations are also deleted.

This should be added to:
- The `DELETE /v1/conversations/{conversationId}` operation description in `openapi.yml`
- The `DELETE /v1/admin/conversations/{id}` operation description in `openapi-admin.yml`
- The `DeleteConversation` RPC description in the proto file

### 5. Java DTOs and mappers

- Remove `conversationGroupId` from `ConversationDto`, `ConversationForkSummaryDto`
- Replace `conversationGroupId` with `conversationId` in `ConversationMembershipDto`
- Update `ConversationsResource` mapper methods (`toConversation`, `toClientConversationMembership`, `toClientConversationForkSummary`)
- Update `AdminResource` mapper methods
- Update `GrpcDtoMapper`

### 6. Store layer

No changes needed. The store layer continues to use `conversationGroupId` internally. The DTO-to-client-model translation simply stops exposing it.

### 7. Frontend impact

The frontend (`agent-webui`) currently receives `conversationGroupId` from the `Conversation` response and threads it through component state (`ConversationState`, `ConversationRootProps`). However, it **never sends it back to any API endpoint**. It is only used for:

- State identity checks (detecting when the conversation changes)
- Being passed through context to child components

After removal:
- The generated TypeScript client types will no longer include `conversationGroupId`
- `chat-panel.tsx` line extracting `conversationGroupId` from `conversationQuery.data` will be removed
- `conversation.tsx` state, reducer, props, and hooks will drop the `conversationGroupId` field
- State identity checks can rely solely on `conversationId` (which they effectively already do)

**The frontend will become simpler** - no impact on functionality.

### 8. Site documentation (`site/src/pages/docs/`)

Two concept pages have direct `conversationGroupId` references that must be rewritten. Several other pages discuss forking behavior and need minor updates or review.

#### Files requiring changes

**`concepts/conversations.md`** — 2 references

- **Line 39**: Example JSON response includes `"conversationGroupId": "conv_01HF8XH1XABCD1234EFGH5678"`. Remove this field from the example.
- **Line 75**: Property table row `conversationGroupId | Group ID shared by forked conversations`. Remove this row.
- **Deletion section** (lines 57-62): Add a note about cascade behavior:
  > Deleting a conversation deletes all conversations in the same fork tree (the root and all its forks), along with their messages and memberships.

**`concepts/forking.md`** — 7 references, major rewrite needed

- **Fork Properties table** (line 49): Remove the `conversationGroupId` row.
- **"Conversation Groups" section** (lines 52-99): This entire section explains `conversationGroupId` as a public concept. It should be **replaced** with a shorter section explaining that forks are internally grouped and that the `/forks` endpoint lets you discover related conversations without needing a group ID. Suggested replacement:

  > ## Fork Trees
  >
  > When you create a conversation, the service internally groups it with any future forks. When you fork a conversation, the new fork is linked to the original. All conversations that share a common ancestor belong to the same fork tree.
  >
  > You don't need to know about this grouping directly. Use the `/forks` endpoint on any conversation to discover all related conversations in the tree.
  >
  > Deleting any conversation in a fork tree deletes the entire tree (root and all forks), along with associated messages and memberships.

- **Line 99**: "This returns all conversations that share the same `conversationGroupId`" — reword to "This returns all conversations in the same fork tree".
- **Line 124**: Same change — reword to remove the `conversationGroupId` reference.
- **ASCII tree diagrams** (lines 60-77): Remove or replace. These diagrams illustrate `conversationGroupId` as the tree root. Replace with a simpler diagram showing the fork tree by conversation IDs and `forkedAtConversationId` relationships.

#### Files requiring minor review (no `conversationGroupId` but discuss related behavior)

**`quarkus/advanced-features.mdx`** (lines 18-65): Describes forking behavior. No `conversationGroupId` references, but review for consistency. The fork response example on line 61 mentions `forkedAtConversationId` — confirm it no longer mentions `conversationGroupId`. Currently clean.

**`spring/advanced-features.mdx`** (lines 18-55): Same — describes forking. Currently clean, no changes needed.

**`quarkus/rest-client.mdx`**, **`quarkus/grpc-client.mdx`**, **`spring/rest-client.mdx`**, **`spring/grpc-client.mdx`**: These show client code for fork and membership operations. They don't reference `conversationGroupId` directly, but if the `ConversationMembership` response type changes to include `conversationId` instead, any membership examples in these docs should be reviewed. Currently they don't show membership response bodies, so no changes expected.

**`faq.mdx`**, **`index.mdx`**, **`changelog.md`**, **`getting-started.md`**: Mention forking at a high level. No `conversationGroupId` references. No changes needed.

### 9. Cucumber tests and step definitions

Six feature files and the step definitions class reference `conversationGroupId`. The changes fall into three categories: modifying existing assertions, updating test infrastructure, and adding new scenarios to cover behavior that was previously implicit.

#### 9a. Tests that need assertion modifications (response shape changes)

**`conversations-rest.feature`** — 3 scenarios affected

- **"Create a conversation"** (line 26): Remove `"conversationGroupId": "${response.body.conversationGroupId}"` from the response assertion. The response should no longer contain this field.
- **"Create a conversation with metadata"** (line 51): Same removal.
- **"Get a conversation"** (line 119): Same removal.
- **"Soft delete cascades to conversation group and memberships"** (line 167): This scenario currently does `set "groupId" to "${conversationGroupId}"` and then uses `${groupId}` in SQL assertions against `conversation_groups` and `conversation_memberships` tables. Since `conversationGroupId` is no longer in the API response, the step definition must resolve the group ID internally (see 9d below). The SQL assertions themselves stay — they are validating internal DB state, not API contracts.

**`forking-rest.feature`** — 1 scenario affected

- **"Fork a conversation at a message"** (line 33): Remove `"conversationGroupId": "${response.body.conversationGroupId}"` from the fork response assertion. The `forkedAtMessageId` and `forkedAtConversationId` fields remain.

**`sharing-rest.feature`** — 4 scenarios affected

- **"Share a conversation with a user"** (line 22): Replace `"conversationGroupId": "${response.body.conversationGroupId}"` with `"conversationId": "${conversationId}"` in the membership response assertion.
- **"Share a conversation with reader access"** (line 41): Same replacement.
- **"Share a conversation with manager access"** (line 60): Same replacement.
- **"List conversation memberships"** (lines 83, 89): Replace `"conversationGroupId": "${response.body.data[N].conversationGroupId}"` with `"conversationId": "${conversationId}"` for each membership entry.
- **"Update membership access level"** (line 118): Replace `"conversationGroupId": "${response.body.conversationGroupId}"` with `"conversationId": "${conversationId}"`.

**`sharing-grpc.feature`** — 2 scenarios affected

- **"Share a conversation with a user via gRPC"** (line 22): Change the text proto assertion from `conversation_group_id: "${response.body.conversationGroupId}"` to `conversation_id: "${conversationId}"`.
- **"Update membership access level via gRPC"** (line 85): Same change.

#### 9b. Tests that need no changes (internal-only references)

**`eviction-rest.feature`** — uses `${conversationGroupId}` only via `set "groupId" to "${conversationGroupId}"` and then in SQL queries against internal tables (`conversation_groups`, `conversation_memberships`, `messages`). These SQL assertions validate internal DB behavior, not API contracts, so the SQL stays unchanged. However, the step that populates `${conversationGroupId}` into the context needs updating (see 9c below).

Specific scenarios:
- **"Evict conversation groups past retention period"** (line 8): `set "oldGroupId" to "${conversationGroupId}"`
- **"Cascade deletes child records"** (line 116): `set "groupId" to "${conversationGroupId}"`
- **"Evict soft-deleted memberships"** (line 138): `set "groupId" to "${conversationGroupId}"`
- **"Evict multiple resource types in single request"** (lines 178, 181): `set "groupAId" / "groupBId" to "${conversationGroupId}"`
- **"Cascade deletes memberships and ownership transfers"** (line 248): `set "groupId" to "${conversationGroupId}"`
- **"Vector store task contains correct group ID"** (line 279): `set "groupId" to "${conversationGroupId}"`

These can continue to work as-is if the step definition still populates `conversationGroupId` in the context from internal data (see 9c below).

**`task-queue.feature`** — uses `conversationGroupId` only in task body JSON (`{"conversationGroupId": "test-group-123"}`). This is an internal task payload, not an API contract. No changes needed.

#### 9c. Step definition changes (`StepDefinitions.java`)

**Line 226** — `contextVariables.put("conversationGroupId", conversation.getConversationGroupId())`:
After removal from the DTO, `ConversationDto.getConversationGroupId()` will no longer exist. The step definition needs to resolve the group ID from the database directly. Options:
1. Query `conversation_groups` by conversation ID to get the group ID and put it in `contextVariables` (only needed for eviction/soft-delete SQL assertions).
2. Add an internal helper on the store that returns the group ID for a conversation (test-only, not exposed in API).

This keeps `${conversationGroupId}` available as a test-infrastructure variable for SQL-level assertions while removing it from the API contract.

**Lines 2838, 2929, 2957, 3002, 3206** — These are all internal references in step definitions that deal with tasks, SQL queries, and test setup. They use `conversationGroupId` as a database concept, not as an API field. No changes needed.

#### 9d. New test scenarios to add

The following new scenarios should be added to validate behavior that was previously implicit or untested in the context of this change:

**`conversations-rest.feature`** — add:

1. **"Conversation response does not contain conversationGroupId"**: Create a conversation, assert the response body does NOT contain a `conversationGroupId` field. This is a negative assertion that acts as a regression guard.

2. **"Deleting a conversation deletes all forks"**: Create a conversation, add messages, fork it, then delete the original conversation. Verify that the forked conversation is also deleted (returns 404 on GET). This tests the cascade deletion behavior that was previously discoverable via the shared `conversationGroupId` but now needs explicit documentation and testing.

3. **"Deleting a fork deletes the entire fork tree"**: Create a conversation, fork it, then delete the fork (not the root). Verify that the root conversation is also deleted. This validates that deleting any member of the fork tree cascades to the whole group.

**`conversations-grpc.feature`** — add:

4. **"Conversation response does not contain conversation_group_id via gRPC"**: Create a conversation via gRPC, verify the `conversation_group_id` field is empty/default (empty string for proto3). This ensures the gRPC contract matches.

5. **"Deleting a conversation deletes all forks via gRPC"**: gRPC equivalent of scenario 2 above.

**`forking-rest.feature`** — add:

6. **"Fork response does not contain conversationGroupId"**: Fork a conversation, assert the response does NOT contain `conversationGroupId`. Regression guard for the fork endpoint.

7. **"Forked conversation shares membership with root"**: Fork a conversation, then verify that the membership from the root applies to the fork. Share the root with "bob", then verify "bob" can GET the forked conversation. This tests that fork-tree-scoped sharing still works without `conversationGroupId` being visible.

**`forking-grpc.feature`** — add:

8. **"Fork response does not contain conversation_group_id via gRPC"**: gRPC equivalent of scenario 6.

**`sharing-rest.feature`** — add:

9. **"Membership response contains conversationId instead of conversationGroupId"**: Share a conversation, assert the response contains `conversationId` and does NOT contain `conversationGroupId`. Regression guard.

10. **"Sharing via a fork applies to all conversations in the fork tree"**: Create a conversation, fork it, share the fork with "bob". Verify "bob" can also access the root conversation. This confirms that sharing still operates at the group level without the group being visible.

11. **"Listing memberships from a fork returns the same memberships as the root"**: Create a conversation, fork it, list memberships from the fork's endpoint. Verify the memberships match those listed from the root.

**`sharing-grpc.feature`** — add:

12. **"Membership response contains conversation_id instead of conversation_group_id via gRPC"**: gRPC equivalent of scenario 9.

**`admin-rest.feature`** — add:

13. **"Admin conversation response does not contain conversationGroupId"**: Admin gets a conversation, asserts no `conversationGroupId` in the response. Regression guard for admin API.

14. **"Admin membership response contains conversationId"**: Admin lists memberships, asserts `conversationId` is present and `conversationGroupId` is absent.

#### 9e. Summary table of test changes

| Feature file              | Scenarios modified | Scenarios added | Change type                                     |
|---------------------------|--------------------|-----------------|--------------------------------------------------|
| `conversations-rest`      | 4                  | 3               | Remove field from assertions; add cascade/negative tests |
| `conversations-grpc`      | 0                  | 2               | Add negative assertion and cascade test          |
| `forking-rest`            | 1                  | 2               | Remove field; add negative and membership test   |
| `forking-grpc`            | 0                  | 1               | Add negative assertion                           |
| `sharing-rest`            | 5                  | 3               | Replace field; add negative and cross-fork tests |
| `sharing-grpc`            | 2                  | 1               | Replace field; add negative assertion            |
| `admin-rest`              | 0                  | 2               | Add negative assertions                          |
| `eviction-rest`           | 0 (feature file)   | 0               | Step definition change only (group ID resolution)|
| `task-queue`              | 0                  | 0               | No changes (internal task payloads)              |
| **Total**                 | **12**             | **14**          |                                                  |

## What Stays Internal

The following internal usages are **not affected** and will continue to use conversation group IDs:

| Layer              | Usage                                                            |
|--------------------|------------------------------------------------------------------|
| Database schema    | `conversation_groups` table, FK columns in other tables          |
| JPA entities       | `ConversationEntity.conversationGroup`, `ConversationMembershipEntity` composite key |
| MongoDB models     | `MongoConversation.conversationGroupId`, `MongoConversationMembership.conversationGroupId` |
| Store impls        | `PostgresMemoryStore`, `MongoMemoryStore` internal group lookups |
| Vector store       | `deleteByConversationGroupId()` for embedding cleanup            |
| Task queue         | Task payloads for background vector deletion                     |

## Migration Considerations

Since this project has not yet been released, there are no backward-compatibility concerns. This is a contract simplification before the first release.

## Alternatives Considered

### Keep `conversationGroupId` as read-only metadata

We could keep it in responses as an opaque "tree ID" that lets clients group related conversations without calling the forks endpoint. However:
- The `/v1/conversations/{conversationId}/forks` endpoint already serves this purpose
- The `forkedAtConversationId` field on `Conversation` already lets clients navigate the tree
- Adding another ID increases cognitive load for API consumers

### Rename to `forkTreeId` or `rootConversationId`

More descriptive, but still exposes an internal concept. If a client wants to know the root conversation, they can follow `forkedAtConversationId` or use the forks endpoint. The root conversation's `forkedAtConversationId` is `null`, making it identifiable.

## Decision

Remove `conversationGroupId` / `conversation_group_id` from all public contracts. Replace it with `conversationId` in `ConversationMembership`. Document cascade deletion semantics. Keep it as a purely internal implementation detail.
