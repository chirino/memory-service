---
status: implemented
---

# Enhancement 104: Admin Episodic Memory Exploration

> **Status**: Implemented.

## Summary

Add explicit admin and auditor read APIs for exploring episodic memories across all users, plus matching gRPC APIs for non-browser admin tooling. Keep the existing user-facing `/v1/memories` API policy-governed, and make the admin policy bypass explicit, audited, and read-only for auditors.

## Motivation

The project currently exposes broad admin APIs for conversations, entries, attachments, events, checkpoints, eviction, and stats. Episodic memories are different: `/admin/v1/memories/*` only exposes operational endpoints for policy management, force delete, indexing, and usage counters, and those handlers require the `admin` role.

An admin frontend needs a clean way to browse and inspect memory values across users without relying on the user-facing `/v1/memories/search` behavior. The current public memory API is not a good admin substrate because:

- direct `GET /v1/memories` still follows normal memory authz policy
- admin-wide search depends on default OPA filter behavior rather than an explicit admin contract
- there is no admin `GET` by memory UUID
- there is no admin list endpoint for all memories or a selected memory namespace prefix
- auditors cannot use the existing `/admin/v1/memories/*` endpoints
- admin audit enforcement currently targets `/v1/admin/...`, while memory admin routes use `/admin/v1/...`

This enhancement adds a dedicated exploration surface for administrative inspection while preserving the user-facing memory API semantics.

## Design

### REST Admin API

Add read-only admin/auditor endpoints for memory exploration:

| Method | Path | Role | Purpose |
| --- | --- | --- | --- |
| `GET` | `/admin/v1/memories` | admin or auditor | List memory items across users with filters and cursor pagination. |
| `GET` | `/admin/v1/memories/{id}` | admin or auditor | Get one memory by UUID, including value and metadata. |
| `POST` | `/admin/v1/memories/search` | admin or auditor | Search memory values by namespace prefix, safe attributes, and optional semantic query. |
| `GET` | `/admin/v1/memory-namespaces` | admin or auditor | Browse namespaces across users. |

Rename the existing operational endpoints so `GET /admin/v1/memories/{id}` can use the canonical resource path without conflicting with static `GET /admin/v1/memories/...` routes in Gin:

| Method | Path | Role |
| --- | --- | --- |
| `DELETE` | `/admin/v1/memories/{id}` | admin |
| `GET` | `/admin/v1/memory-policies` | admin |
| `PUT` | `/admin/v1/memory-policies` | admin |
| `GET` | `/admin/v1/memory-index/status` | admin |
| `POST` | `/admin/v1/memory-index/trigger` | admin |
| `GET` | `/admin/v1/memory-usage` | admin |
| `GET` | `/admin/v1/memory-usage/top` | admin |

The new list/get read APIs bypass the normal user memory OPA authz policy because admin/auditor access is itself the authorization decision. They must not call the user-facing `getMemory` or namespace handlers and rely on special admin policy behavior. Instead, they should call explicit admin store methods.

List, search, and namespace browsing operate over the current/latest memory row per `(namespace, key)`, applying the same `archived=exclude|include|only` archive-state model as public memory APIs. They should not enumerate superseded historical update rows by default. Direct ID reads are the exception: `GET /admin/v1/memories/{id}` may return any retained memory row, including an archived, superseded, or tombstoned row. Tombstoned rows have already had encrypted value bytes cleared, so they can return metadata with `value` omitted or null. Hard-deleted rows return `404`.

### List Memories

`GET /admin/v1/memories` supports these query parameters:

| Parameter | Required | Notes |
| --- | --- | --- |
| `namespacePrefix` | no | Repeated namespace prefix segments. |
| `keyPrefix` | no | Optional key prefix filter. |
| `archived` | no | `exclude`, `include`, or `only`; default `exclude`. |
| `createdAfter` | no | RFC 3339 lower bound. |
| `createdBefore` | no | RFC 3339 upper bound. |
| `expiresBefore` | no | RFC 3339 upper bound for TTL-backed items. |
| `includeUsage` | no | Include usage counters without incrementing them. |
| `limit` | no | Default `50`, maximum `200`. |
| `afterCursor` | no | Opaque pagination cursor. |
| `justification` | conditional | Required when admin justification enforcement is enabled. |

Episodic memory rows currently track `createdAt`, `expiresAt`, internal `archivedAt`, and `revision`; they do not have a separate `updatedAt` field. Updated memories are represented as a newly written active row with a new `createdAt` and incremented `revision`, while the previous row is archived. Results are ordered newest-first by `createdAt`, then `id` descending. `afterCursor` follows that order and should be opaque.

Example response:

```json
{
  "items": [
    {
      "id": "018f4f63-2d3c-7b90-9f5f-8dc6b7a5a111",
      "namespace": ["user", "alice", "prefs"],
      "key": "theme",
      "value": { "color": "dark" },
      "attributes": {
        "namespace": "user",
        "sub": "alice"
      },
      "createdAt": "2026-05-26T12:00:00Z",
      "expiresAt": null,
      "archivedAt": null,
      "archived": false,
      "revision": 1,
      "usage": {
        "fetchCount": 12,
        "lastFetchedAt": "2026-05-26T12:30:00Z"
      }
    }
  ],
  "afterCursor": "opaque"
}
```

### Get Memory By ID

`GET /admin/v1/memories/{id}` returns the same `AdminMemoryItem` shape as list items. It must return archived memories by ID unless they have been hard-deleted. This endpoint does not increment fetch counters because admin inspection should not affect agent-facing usage metrics.

This enhancement intentionally moves static operational and namespace routes out from under `/admin/v1/memories/...`. The project is pre-release and these routes are not a stable external surface, so changing them is cleaner than adding a non-resource path such as `/admin/v1/memories/by-id/{id}`. Existing BDD and generated-client references to the old operational paths must be updated in the same implementation. With the static `GET` routes moved to `/admin/v1/memory-policies`, `/admin/v1/memory-index/...`, `/admin/v1/memory-usage...`, and `/admin/v1/memory-namespaces`, Gin can safely register `GET /admin/v1/memories/{id}`.

Missing memories return `404`.

### Admin Memory Search

`POST /admin/v1/memories/search` should behave like public `POST /v1/memories/search`: same request model, same defaults, same archive filtering, same semantic-search behavior, same attribute filter semantics, same bounded top-k behavior, and no usage counter increments. The only default difference is authorization scope: admin/auditor callers may search across all users instead of being narrowed to the caller's user namespace.

Request:

```json
{
  "namespace_prefix": ["user", "alice"],
  "as_user_id": "alice",
  "query": "deployment preference",
  "filter": {
    "memoryKind": { "$in": ["preference", "procedure"] }
  },
  "archived": "exclude",
  "include_usage": true,
  "limit": 25
}
```

Behavior:

- admin or auditor role is required
- when `as_user_id` / `asUserId` is omitted, caller-supplied namespace and attribute filters narrow an admin-wide result set and no user-scoped OPA filter injection is applied
- when `as_user_id` / `asUserId` is set, the server evaluates normal memory search policy as that target user, including namespace narrowing and OPA filter injection, so results match what the target user would see from public `POST /v1/memories/search`
- REST uses the same snake_case JSON field names as public memory search (`namespace_prefix`, `include_usage`) plus `as_user_id`; generated Go names may still be camel-cased internally
- `as_user_id` is an admin search inspection mode, not an authorization grant; only admin/auditor callers may set it
- semantic search uses the same vector index and ranking semantics as public memory search
- attribute-only search returns newest-first bounded results
- usage counters may be included but are not incremented, matching public memory search
- no `afterCursor` is supported on search

If [100](100-enhanced-memory-search.md) changes the filter language or gRPC search contract before this enhancement is implemented, this admin search endpoint should reuse that final normalized filter representation.

### Namespace Browser

`GET /admin/v1/memory-namespaces` supports:

| Parameter | Required | Notes |
| --- | --- | --- |
| `namespacePrefix` | no | Repeated namespace prefix segments. |
| `suffix` | no | Repeated namespace suffix segments. |
| `maxDepth` | no | Same meaning as public namespace listing. |
| `archived` | no | `exclude`, `include`, or `only`; default `exclude`. |
| `limit` | no | Default `200`, maximum `1000`. |
| `afterCursor` | no | Opaque cursor if namespace listing needs pagination. |

This powers an admin frontend tree/browser without exposing or depending on normal user policy injection.

### gRPC Contract

Add a separate admin service instead of overloading the policy-governed `MemoriesService`.

```protobuf
service AdminMemoriesService {
  rpc ListMemories(AdminListMemoriesRequest) returns (AdminListMemoriesResponse);
  rpc GetMemory(AdminGetMemoryRequest) returns (AdminMemoryItem);
  rpc SearchMemories(AdminSearchMemoriesRequest) returns (AdminSearchMemoriesResponse);
  rpc ListNamespaces(AdminListMemoryNamespacesRequest) returns (AdminListMemoryNamespacesResponse);

  rpc DeleteMemory(AdminDeleteMemoryRequest) returns (google.protobuf.Empty);
  rpc GetMemoryUsage(AdminGetMemoryUsageRequest) returns (MemoryUsage);
  rpc ListTopMemoryUsage(AdminListTopMemoryUsageRequest) returns (ListTopMemoryUsageResponse);
  rpc GetMemoryIndexStatus(AdminGetMemoryIndexStatusRequest) returns (MemoryIndexStatusResponse);
}
```

Read RPCs require admin or auditor. Mutation and operational RPCs require admin.

The current proto has admin-only memory operational RPCs on `MemoriesService`: `GetMemoryIndexStatus`, `GetMemoryUsage`, and `ListTopMemoryUsage`. Under the repo's pre-release no-compatibility rule, implementation should move those admin-only RPCs to `AdminMemoriesService` rather than permanently exposing the same admin operations from both `MemoriesService` and `AdminMemoriesService`.

Use admin-specific request messages so the auth semantics are unambiguous:

```protobuf
message AdminListMemoriesRequest {
  repeated string namespace_prefix = 1;
  optional string key_prefix = 2;
  ArchiveFilter archived = 3;
  optional google.protobuf.Timestamp created_after = 4;
  optional google.protobuf.Timestamp created_before = 5;
  optional google.protobuf.Timestamp expires_before = 6;
  bool include_usage = 7;
  int32 limit = 8;
  optional string after_cursor = 9;
  optional string justification = 10;
}

message AdminGetMemoryRequest {
  bytes id = 1;
  bool include_usage = 2;
  optional string justification = 3;
}

message AdminDeleteMemoryRequest {
  bytes id = 1;
  optional string justification = 2;
}

message AdminGetMemoryUsageRequest {
  repeated string namespace = 1;
  string key = 2;
  optional string justification = 3;
}

message AdminListTopMemoryUsageRequest {
  repeated string prefix = 1;
  MemoryUsageSort sort = 2;
  int32 limit = 3;
  optional string justification = 4;
}

message AdminGetMemoryIndexStatusRequest {
  optional string justification = 1;
}

message AdminSearchMemoriesRequest {
  repeated string namespace_prefix = 1;
  optional string key_prefix = 2;
  string query = 3;
  optional google.protobuf.Struct filter = 4;
  ArchiveFilter archived = 5;
  bool include_usage = 6;
  int32 limit = 7;
  optional string justification = 8;
  optional string as_user_id = 9;
}

message AdminListMemoryNamespacesRequest {
  repeated string namespace_prefix = 1;
  repeated string suffix = 2;
  int32 max_depth = 3;
  ArchiveFilter archived = 4;
  int32 limit = 5;
  optional string after_cursor = 6;
  optional string justification = 7;
}

message AdminMemoryItem {
  bytes id = 1;
  repeated string namespace = 2;
  string key = 3;
  google.protobuf.Struct value = 4;
  optional google.protobuf.Struct attributes = 5;
  google.protobuf.Timestamp created_at = 6;
  optional google.protobuf.Timestamp expires_at = 7;
  optional google.protobuf.Timestamp archived_at = 8;
  bool archived = 9;
  optional MemoryUsage usage = 10;
  int64 revision = 11;
}

message AdminListMemoriesResponse {
  repeated AdminMemoryItem items = 1;
  optional string after_cursor = 2;
}

message AdminSearchMemoriesResponse {
  repeated AdminMemoryItem items = 1;
}

message AdminListMemoryNamespacesResponse {
  repeated MemoryNamespace namespaces = 1;
  optional string after_cursor = 2;
}
```

The `AdminSearchMemoriesRequest.filter` field should reuse the final [100](100-enhanced-memory-search.md) filter semantics. If [100](100-enhanced-memory-search.md) is still proposed when this enhancement is implemented, implement both using one normalized filter model.

### Store Interfaces

Add explicit admin read methods to `registry/episodic`:

```go
type AdminMemoryQuery struct {
    NamespacePrefix []string
    KeyPrefix       string
    Archived        ArchiveFilter
    CreatedAfter    *time.Time
    CreatedBefore   *time.Time
    ExpiresBefore   *time.Time
    IncludeUsage    bool
    Limit           int
    AfterCursor     string
}

type AdminMemoryPage struct {
    Items       []MemoryItem
    AfterCursor string
}

type AdminMemorySearchQuery struct {
    NamespacePrefix []string
    KeyPrefix       string
    Query           string
    Filter          AttributeFilter
    Archived        ArchiveFilter
    IncludeUsage    bool
    Limit           int
}

type AdminNamespaceQuery struct {
    NamespacePrefix []string
    Suffix          []string
    MaxDepth        int
    Archived        ArchiveFilter
    Limit           int
    AfterCursor     string
}

type AdminNamespacePage struct {
    Namespaces  [][]string
    AfterCursor string
}

type AdminMemoryStore interface {
    AdminListMemories(ctx context.Context, query AdminMemoryQuery) (AdminMemoryPage, error)
    AdminSearchMemories(ctx context.Context, query AdminMemorySearchQuery) ([]MemoryItem, error)
    AdminListNamespaces(ctx context.Context, query AdminNamespaceQuery) (AdminNamespacePage, error)
}
```

`EpisodicStore.AdminGetMemoryByID(ctx, uuid.UUID)` already exists and is implemented for PostgreSQL, SQLite, and MongoDB. The REST/gRPC read handlers should reuse it and optionally load usage counters separately when `includeUsage` / `include_usage` is requested. New list and namespace methods should be implemented directly instead of reusing user-scoped policy injection. Admin-wide search may use explicit admin store methods or may reuse the same lower-level search pipeline as public memory search with policy injection disabled. `asUserId` / `as_user_id` search should reuse the public search policy path with an effective `PolicyContext.UserID` equal to the target user while preserving the authenticated admin/auditor caller for authorization and audit logs. PostgreSQL, MongoDB, and SQLite should all support the same filters and cursor ordering.

### Auditing And Justification

Admin memory value reads are sensitive. The audit and justification policy must cover both admin route prefixes:

- `/v1/admin/...`
- `/admin/v1/...`

When `MEMORY_SERVICE_ADMIN_REQUIRE_JUSTIFICATION=true`, admin memory requests under the audited prefixes must reject missing justification consistently with the existing `/v1/admin/...` behavior. REST enforcement should stay in `AdminAuditMiddleware`; handlers should not hard-code a second justification check. gRPC `AdminMemoriesService` methods must enforce the same requirement because they do not pass through the REST middleware. Both REST and gRPC should log the caller, role, method/RPC, target ID or query scope, status, and justification.

Admin frontend requests should send `X-Justification` or `?justification=` for REST. gRPC requests use the `justification` field.

## Security Considerations

Admin memory exploration returns decrypted memory values across users. The implementation must:

- require admin or auditor role for all read endpoints
- require admin role for deletion, policy changes, index triggers, and usage administration
- mount `/admin/v1/memories`, `/admin/v1/memories/search`, and `/admin/v1/memory-namespaces` with auditor-or-admin authorization, then apply admin-only checks to mutation and operational memory routes
- audit all successful and failed admin memory reads
- not increment user-facing memory usage counters for admin inspection
- keep the normal `/v1/memories` OPA policy behavior unchanged
- ensure `as_user_id` / `asUserId` admin search applies the same OPA namespace and attribute-filter constraints that public search would apply for that target user
- avoid exposing admin read endpoints through frontend-safe agent API clients
- preserve attachment and event stream authorization boundaries; this enhancement only covers episodic memories

Auditors should be read-only. If an auditor needs signed export/download support later, add a separate enhancement with explicit retention and audit controls.

## Non-Goals

- Building the admin frontend UI.
- Adding cognition-specific debug or rebuild APIs.
- Replacing `/v1/memories` or changing user-facing memory authz policy.
- Adding gRPC policy bundle upload/download unless a concrete non-HTTP operator needs it.
- Exporting all memories as a bulk dump.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Admin episodic memory exploration
  Scenario: Auditor can list memories across users
    Given Alice has an episodic memory under namespace ["user", "alice", "prefs"]
    And Bob has an episodic memory under namespace ["user", "bob", "prefs"]
    And I am authenticated as auditor user "charlie"
    When I call GET "/admin/v1/memories?namespacePrefix=user&limit=10"
    Then the response status should be 200
    And the response body "items" should include memory keys "theme" and "timezone"

  Scenario: Auditor can get a memory value by ID
    Given Alice has an episodic memory with value { "color": "dark" }
    And I am authenticated as auditor user "charlie"
    When I call GET "/admin/v1/memories/${memoryId}"
    Then the response status should be 200
    And the response body field "value.color" should be "dark"

  Scenario: Auditor cannot delete a memory
    Given Alice has an episodic memory
    And I am authenticated as auditor user "charlie"
    When I call DELETE "/admin/v1/memories/${memoryId}"
    Then the response status should be 403

  Scenario: Admin can search memories across users
    Given Alice and Bob have indexed episodic memories
    And I am authenticated as admin user "alice"
    When I call POST "/admin/v1/memories/search" with body:
      """
      {
        "namespace_prefix": ["user"],
        "query": "deployment",
        "limit": 10
      }
      """
    Then the response status should be 200
    And the response body "items" should have at least 1 item

  Scenario: Admin can search as a target user
    Given Alice has an indexed episodic memory under namespace ["user", "alice", "prefs"]
    And Bob has an indexed episodic memory under namespace ["user", "bob", "prefs"]
    And I am authenticated as admin user "alice"
    When I call POST "/admin/v1/memories/search" with body:
      """
      {
        "namespace_prefix": ["user"],
        "as_user_id": "bob",
        "limit": 10
      }
      """
    Then the response status should be 200
    And the response body "items" should include Bob's memory
    And the response body "items" should not include Alice's memory

  Scenario: Admin memory reads require justification when enabled
    Given admin justification is required
    And I am authenticated as admin user "alice"
    When I call GET "/admin/v1/memories/${memoryId}"
    Then the response status should be 400
    When I call GET "/admin/v1/memories/${memoryId}?justification=investigation"
    Then the response status should be 200
```

### gRPC Scenarios

```gherkin
Feature: gRPC admin episodic memory exploration
  Scenario: Auditor lists memories over gRPC
    Given I am authenticated as auditor user "charlie" over gRPC
    When I send gRPC request "AdminMemoriesService/ListMemories" with namespace_prefix ["user"]
    Then the gRPC response should contain memory items from multiple users

  Scenario: Non-admin user cannot use admin memory RPCs
    Given I am authenticated as user "bob" over gRPC
    When I send gRPC request "AdminMemoriesService/ListMemories" with namespace_prefix ["user"]
    Then the gRPC status should be PERMISSION_DENIED

  Scenario: Admin memory inspection does not increment usage counters
    Given Alice has an episodic memory with fetch_count 0
    And I am authenticated as admin user "alice" over gRPC
    When I send gRPC request "AdminMemoriesService/GetMemory" for that memory ID
    Then the memory usage fetch_count should still be 0

  Scenario: Admin searches as a target user over gRPC
    Given Alice and Bob have indexed episodic memories
    And I am authenticated as admin user "alice" over gRPC
    When I send gRPC request "AdminMemoriesService/SearchMemories" with namespace_prefix ["user"] and as_user_id "bob"
    Then the gRPC response should contain Bob's memory
    And the gRPC response should not contain Alice's memory
```

## Tasks

- [x] Add admin memory read schemas and endpoints to `contracts/openapi/openapi-admin.yml`, including renamed operational paths for policies, index controls, usage, and namespaces.
- [x] Add `AdminMemoriesService` and request/response messages to `contracts/protobuf/memory/v1/memory_service.proto`.
- [x] Move existing admin-only memory operational RPCs off `MemoriesService` and onto `AdminMemoriesService`.
- [x] Regenerate REST and gRPC generated code.
- [x] Add admin episodic memory list/search query/page types and store methods, reusing the existing `AdminGetMemoryByID` method for direct ID reads.
- [x] Implement admin list/get/search for PostgreSQL.
- [x] Implement admin list/get/search for SQLite.
- [x] Implement admin list/get/search for MongoDB.
- [x] Add REST route handlers under `/admin/v1/memories`, `/admin/v1/memory-namespaces`, `/admin/v1/memory-policies`, `/admin/v1/memory-index`, and `/admin/v1/memory-usage`.
- [x] Add gRPC `AdminMemoriesService` server.
- [x] Implement `as_user_id` / `asUserId` admin search by applying the public memory search policy path with the target user as the effective policy user.
- [x] Extend admin audit/justification middleware to include `/admin/v1/...`.
- [x] Ensure admin memory reads do not increment usage counters.
- [x] Add REST BDD coverage for admin, auditor, and forbidden users.
- [x] Add gRPC BDD coverage for admin, auditor, and forbidden users.
- [x] Update site docs for admin memory exploration.

## Files to Modify

| File | Changes |
| --- | --- |
| `contracts/openapi/openapi-admin.yml` | Add admin memory list/get/search/namespaces read endpoints and schemas; rename existing operational memory routes; clarify role split. |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add `AdminMemoriesService` and admin memory messages. |
| `internal/generated/admin/*` | Regenerate generated admin REST server/client types. |
| `internal/generated/pb/memory/v1/*` | Regenerate protobuf and gRPC types. |
| `internal/registry/episodic/plugin.go` | Add admin list/search query/page interfaces and request types; keep the existing `AdminGetMemoryByID` method as the direct-read primitive. |
| `internal/plugin/store/postgres/*episodic*` | Implement admin memory reads and pagination. |
| `internal/plugin/store/sqlite/*episodic*` | Implement admin memory reads and pagination. |
| `internal/plugin/store/mongo/*episodic*` | Implement admin memory reads and pagination. |
| `internal/plugin/route/memories/memories.go` | Add REST admin read handlers, rename operational admin routes, and preserve admin-only mutation handlers. |
| `internal/grpc/server.go` | Add `AdminMemoriesService` implementation and role checks. |
| `internal/security/logging.go` | Audit and enforce justification for `/admin/v1/...` as well as `/v1/admin/...`. |
| `internal/bdd/testdata/features/memories-rest.feature` | Add REST scenarios for admin memory exploration. |
| `internal/bdd/testdata/features-grpc/*.feature` | Add gRPC scenarios for `AdminMemoriesService`. |
| `site/src/pages/docs/concepts/memories.md` | Document admin memory exploration separately from user memory APIs. |
| `internal/FACTS.md` | Update implementation facts if route prefixes, role split, or audit behavior diverge from this proposal. |

## Verification

```bash
# Regenerate contracts if required by the implementation workflow
task generate

# Go compile and tests for broad API/store changes
go build ./... > go-build.log 2>&1
go test ./internal/bdd -run TestFeatures -count=1 > bdd.log 2>&1

# Java clients/contracts after OpenAPI/protobuf changes
./java/mvnw -f java/pom.xml compile > java-compile.log 2>&1

# Search logs for failures
rg -n "FAIL|ERROR|panic|BUILD FAILURE" go-build.log bdd.log java-compile.log
```
