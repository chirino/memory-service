---
status: implemented
---

# Enhancement 101: gRPC API Parity for Cognition

> **Status**: Implemented.

## Summary

Add the gRPC API parity needed for external cognition processors to use Memory Service without HTTP or SSE dependencies. The gRPC surface must match the behavior of the REST counterparts for existing event replay, worker checkpoint, archive, and TTL-backed indexed memory behavior. It also adds the missing generic gRPC substrate pieces cognition needs for conditional memory updates and service-principal on-behalf-of authorization. Memory-search parity is handled by [100](100-enhanced-memory-search.md).

## Motivation

[099](099-quarkus-cognition-processor.md) should be able to run as a standalone Quarkus worker using only generated gRPC clients. Today the proto has useful conversation, entry, memory, search, and event-stream services, but the full cognition workflow still depends on REST-only or under-specified behavior:

- replayable event consumption exists in REST admin APIs, while `EventStreamService.SubscribeEvents` is documented as membership-scoped
- admin checkpoints exist only in `contracts/openapi/openapi-admin.yml`
- [100](100-enhanced-memory-search.md) owns memory-search parity, including gRPC cursor/filter/order behavior
- cognition consolidation needs revision-aware compare-and-set semantics
- memory archive filters, TTL writes, indexing, and safe attributes must behave the same through gRPC as through REST
- a service principal must be able to write scoped cognition memories on behalf of conversation owners without leaking cross-user access

The goal is not to add cognition-specific admin/debug APIs. The goal is to make the generic substrate gRPC APIs complete enough that cognition can stay outside the Go server and still use the same governed behavior as REST clients.

## Design

### Event Stream Admin Parity

`EventStreamService.SubscribeEvents` remains the general event streaming RPC, but it must support an admin/auditor/service-principal mode equivalent to REST admin event replay.

```protobuf
message SubscribeEventsRequest {
  repeated bytes conversation_ids = 1;
  repeated string kinds = 2;
  optional string after_cursor = 3;
  optional string detail = 4; // "summary" or "full"
  optional EventScope scope = 5;
  optional string justification = 6;
}

enum EventScope {
  EVENT_SCOPE_UNSPECIFIED = 0; // existing user/member-scoped behavior
  EVENT_SCOPE_AUTHORIZED = 1;  // user/member-scoped behavior
  EVENT_SCOPE_ADMIN = 2;       // admin/auditor/service-principal event stream
}
```

`EVENT_SCOPE_ADMIN` requires the same authorization as REST `GET /v1/admin/events`: admin or auditor role for event reads, with service-principal credentials mapped to one of those roles by deployment policy. Non-authorized callers requesting admin scope receive gRPC `PERMISSION_DENIED`. `justification` is logged the same way as the REST `justification` query parameter and is required when admin justification enforcement is enabled. `after_cursor` maps to REST's `after` query parameter. `kinds` matches the REST admin filter. `conversation_ids` is an additional gRPC narrowing filter for callers that only want selected conversations; it is not an authorization grant.

`EventNotification` should remain cursor-bearing. The cursor must be durable enough for restart catch-up and must match the REST admin event cursor semantics.

When `after_cursor` is set and outbox replay is disabled or unsupported by the configured datastore, gRPC returns `UNIMPLEMENTED`, matching the REST admin event replay behavior. The sentinel cursor `start` replays from the oldest retained event, as defined by the outbox contract.

### Worker Checkpoints

Add a gRPC checkpoint service equivalent to `GET/PUT /v1/admin/checkpoints/{clientId}`.

```protobuf
service AdminCheckpointService {
  rpc GetCheckpoint(GetCheckpointRequest) returns (AdminCheckpoint);
  rpc PutCheckpoint(PutCheckpointRequest) returns (AdminCheckpoint);
}

message GetCheckpointRequest {
  string client_id = 1;
}

message PutCheckpointRequest {
  string client_id = 1;
  string content_type = 2;
  google.protobuf.Value value = 3;
}

message AdminCheckpoint {
  string client_id = 1;
  string content_type = 2;
  google.protobuf.Value value = 3;
  google.protobuf.Timestamp updated_at = 4;
}
```

Behavior must match REST:

- checkpoints are encrypted at rest
- authenticated client ID, when present, must match `client_id`
- admin role is required unless an existing policy explicitly grants the operation
- missing checkpoints return gRPC `NOT_FOUND`
- authenticated-client-ID mismatches return gRPC `NOT_FOUND`, matching REST's indistinguishable unknown-checkpoint behavior
- missing checkpoint storage returns gRPC `UNIMPLEMENTED`
- malformed requests return `INVALID_ARGUMENT`
- `content_type` and `value` are required; the service stores `value` as processor-defined JSON and does not validate processor-specific fields

### Revision-Aware Memory Writes

Expose memory revisions so processors can consolidate idempotently without overwriting concurrent updates.

```protobuf
message MemoryItem {
  bytes id = 1;
  repeated string namespace = 2;
  string key = 3;
  google.protobuf.Struct value = 4;
  optional google.protobuf.Struct attributes = 5;
  optional double score = 6;
  string created_at = 7;
  optional string expires_at = 8;
  optional MemoryUsage usage = 9;
  bool archived = 10;
  int64 revision = 11;
}

message PutMemoryRequest {
  repeated string namespace = 1;
  string key = 2;
  google.protobuf.Struct value = 3;
  int32 ttl_seconds = 4;
  map<string, string> index = 5;
  optional int64 expected_revision = 6;
  optional RequestActor actor = 7;
}

message MemoryWriteResult {
  bytes id = 1;
  repeated string namespace = 2;
  string key = 3;
  optional google.protobuf.Struct attributes = 4;
  string created_at = 5;
  optional string expires_at = 6;
  int64 revision = 7;
}

message UpdateMemoryRequest {
  repeated string namespace = 1;
  string key = 2;
  optional bool archived = 3;
  optional int64 expected_revision = 4;
  optional RequestActor actor = 5;
}
```

When `expected_revision` is set, the write must only succeed if the active row for `(namespace, key)` exists and has that revision. A revision mismatch or missing active row returns gRPC `ABORTED` consistently across datastores. Omitting `expected_revision` keeps today's unconditional upsert behavior. Revisions are monotonic per logical `(namespace, key)`, start at `1` for the first active version, and increment by one for each successful put or archive update. Archived historical rows expose the resulting revision assigned when they were archived.

### Archive Semantics

All gRPC memory APIs must match REST archive behavior:

- `ArchiveFilter` values map to REST `exclude|include|only`
- omitted archive filters default to `exclude`
- archived memories remain readable with `include` and `only`
- search archive behavior is defined by [100](100-enhanced-memory-search.md)
- get and namespace listing apply the same archive filter as REST
- `UpdateMemory(archived=true)` archives the memory item instead of hard-deleting it
- TTL expiration and eviction behavior must not be confused with archive state

### Service Principal and On-Behalf-Of Scope

Add explicit gRPC request context for service-principal writes that need to operate under a user-owned namespace. The server still enforces the same episodic-memory policy used by REST.

```protobuf
message RequestActor {
  optional string on_behalf_of_user_id = 1;
}
```

Memory write, update, get, search, and namespace-list requests may carry `RequestActor` where needed. Search-specific behavior is finalized in [100](100-enhanced-memory-search.md); if [100](100-enhanced-memory-search.md) adds `order` and `after_cursor`, `SearchMemoriesRequest.actor` should use field `9`. `GetMemoryRequest.actor` should use field `5`, and `ListMemoryNamespacesRequest.actor` should use field `5`.

Normal user callers omit `actor`. Service principals can set `on_behalf_of_user_id` to evaluate the request using that user as the effective policy user; namespace policy must still allow the resulting effective user within authorized prefixes such as `["user", <sub>, "cognition.v1", ...]`.

Actor handling should keep policy input simple:

1. Derive one effective policy user: `actor.on_behalf_of_user_id` when present, otherwise the authenticated user.
2. Build policy context with that effective `user_id`, plus the authenticated `ClientID` and roles.
3. Evaluate normal read/write/update/search policy against the effective identity.

`on_behalf_of_user_id` is not passed through to OPA as a separate input field. The policy sees the same shape as normal user requests: effective `user_id`, `client_id`, and roles.

This must not expose internal `clientId` metadata in user-facing memory payloads. Admin APIs may still see internal metadata where existing REST admin APIs allow it.

### TTL and Index Parity

`PutMemoryRequest.ttl_seconds` already exists in protobuf, but gRPC behavior must be verified and tested to match REST:

- TTL-backed memories receive `expires_at`
- expired memories are not returned by get/search/list after expiry
- TTL-backed memories are indexed before expiry; search visibility is specified in [100](100-enhanced-memory-search.md)
- OPA attributes are extracted from TTL-backed memory values/index payloads the same way as durable memories
- search usage behavior is specified in [100](100-enhanced-memory-search.md)
- direct `GetMemory` increments usage counters exactly as REST direct fetches do

## Design Decisions

### Keep Cognition APIs Out Of The Substrate

No `CognitionAdminService` is added here. Runtime status, rebuild triggers, and retrieval-debug output belong to the standalone processor, not Memory Service. Memory Service only needs generic substrate APIs that are complete and governed.

### Match REST Exactly

For every feature here that already exists in REST, REST remains the behavior oracle unless a later enhancement intentionally changes both contracts. gRPC errors should use idiomatic status codes while preserving the same authorization, validation, and data-visibility semantics. Revision-aware writes and `RequestActor` are new gRPC substrate capabilities for cognition; they should not be described as REST parity until a REST contract explicitly adopts them.

### Prefer Generic Admin Checkpoints

Checkpoints are not cognition-specific. Keeping them as generic admin client checkpoints lets event processors, indexers, and other background clients reuse the same durable cursor storage.

## Testing

### Cucumber Scenarios

```gherkin
Feature: gRPC parity for cognition processors
  Scenario: Admin service principal replays events over gRPC
    Given an admin service principal is authenticated over gRPC
    When it subscribes to events with scope ADMIN and after_cursor "start"
    Then it receives conversation and entry events with durable cursors

  Scenario: Non-admin callers cannot request admin event scope
    Given Alice is authenticated over gRPC without admin privileges
    When Alice subscribes to events with scope ADMIN
    Then the gRPC status should be PERMISSION_DENIED

  Scenario: gRPC checkpoints match REST checkpoint behavior
    Given an admin service principal stores checkpoint value {"lastEventCursor":"cursor-1"} with content type "application/vnd.memory-service.checkpoint+json;v=1" for "cognition-worker-1" over gRPC
    When it reads checkpoint "cognition-worker-1" over gRPC
    Then the checkpoint content_type is "application/vnd.memory-service.checkpoint+json;v=1"
    And the checkpoint value contains "lastEventCursor" equal to "cursor-1"

  Scenario: gRPC conditional memory write detects conflicts
    Given a memory has revision 3
    When PutMemory is called over gRPC with expected_revision 2
    Then the gRPC status should be ABORTED

  Scenario: gRPC archive filters match REST
    Given a memory is archived
    When GetMemory is called over gRPC with archived ONLY
    Then the archived memory is returned
    When GetMemory is called over gRPC with archived EXCLUDE
    Then the archived memory is not returned

  Scenario: Service principal writes only authorized cognition namespaces
    Given the cognition service principal can write ["user","alice","cognition.v1"]
    When it writes ["user","alice","cognition.v1","facts"] on behalf of "alice" over gRPC
    Then the write succeeds
    When it writes ["user","alice","private"] on behalf of "alice" over gRPC
    Then the gRPC status should be PERMISSION_DENIED

  Scenario: TTL-backed gRPC memories are indexed before expiry
    Given a memory is written over gRPC with ttl_seconds 3600 and index text "deployment cache"
    When the write response is returned
    Then it includes expires_at
    And the memory is queued for indexing before expiry
```

### Unit / Integration Coverage

- Generated-code compile coverage covers the protobuf additions for checkpoint client IDs, request actor fields, revisions, and event scope values.
- Memory-search parity tests live in [100](100-enhanced-memory-search.md).
- Focused store-level tests verify revision increments and conditional write/archive conflicts on SQLite; Go build coverage verifies the PostgreSQL and Mongo implementations compile against the shared interface.
- Existing event-stream coverage continues to verify user scope, `kinds`, `after_cursor`, and reconnect behavior; admin scope and `conversation_ids` are implemented in the shared gRPC event handler path.
- Policy implementation evaluates authorization against a single effective `user_id`; `on_behalf_of_user_id` is not passed through to OPA as a separate input.
- Existing TTL/index behavior is preserved through the same gRPC `PutMemory` store path.

## Tasks

- [x] Update `contracts/protobuf/memory/v1/memory_service.proto` with admin event scope, checkpoint RPCs, memory revisions, conditional write fields, and request actor fields.
- [x] Regenerate Go and Python gRPC stubs; validate Java/Quarkus protobuf generation by compiling the Maven proto module.
- [x] Implement gRPC admin event scope using the same store/outbox path as REST admin events.
- [x] Implement gRPC admin checkpoint service backed by the existing admin checkpoint store.
- [x] Add revision fields to episodic memory store models and expose conditional write/update behavior through gRPC.
- [x] Apply REST-equivalent memory archive semantics to gRPC memory get/list/update paths; search behavior is owned by [100](100-enhanced-memory-search.md).
- [x] Extend gRPC episodic memory requests with `RequestActor`; policy input remains the effective `user_id`, authenticated client ID, and roles.
- [x] Verify gRPC TTL-backed memory writes continue through the existing put/index/get paths and expose write/get metadata consistently with REST.
- [x] Add focused store-level coverage for revision increments and conditional write/archive conflicts.
- [x] Update [099](099-quarkus-cognition-processor.md) to depend on these gRPC substrate APIs for its Memory Service integration.

## Files to Modify

| File | Change |
| --- | --- |
| `docs/enhancements/101-grpc-api-parity-for-cognition.md` | This enhancement doc |
| `docs/enhancements/099-quarkus-cognition-processor.md` | Reference this enhancement for gRPC-only substrate prerequisites |
| `docs/enhancements/100-enhanced-memory-search.md` | Keep gRPC search contract aligned if field names or behavior change during implementation |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add/adjust gRPC messages and services |
| `internal/generated/` | Regenerated Go protobuf code |
| `java/quarkus/memory-service-proto-quarkus/` | Regenerated Java/Quarkus protobuf stubs |
| `python/` | Regenerated Python gRPC stubs if protobuf artifacts are impacted |
| `internal/grpc/` and `internal/service/eventstream/` | gRPC handlers and shared replay helpers for events, checkpoints, memories, and write/get/list parity |
| `internal/plugin/store/*` | Store support for memory revisions, conditional writes, archive filters, checkpoints, TTL/index parity |
| `internal/episodic/` | Policy and request-context handling for service-principal on-behalf-of memory access |
| `internal/bdd/testdata/features/` | gRPC parity BDD scenarios |
| `internal/FACTS.md` | Record implementation gotchas discovered while adding gRPC parity |

## Verification

```bash
# Regenerate Go protobuf clients/stubs
go generate ./...

# Regenerate Python gRPC stubs if protobuf package artifacts are impacted
docker run --rm -e GRPC_TOOLS_VERSION=1.74.0 -v "$PWD:/workspace" -w /workspace/python astral/uv:python3.11-bookworm ./scripts/generate-grpc-stubs.sh

# Run focused Go tests for affected services and stores
go test -tags sqlite_fts5 ./internal/plugin/store/sqlite ./internal/grpc ./internal/episodic > go-focused.log 2>&1

# Compile all Go packages
go build ./... > go-build.log 2>&1

# Compile regenerated Python gRPC stubs
python3 -m compileall python/langchain/memory_service_langchain/grpc > python-compile.log 2>&1

# Compile Java modules that consume generated gRPC stubs
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-proto-quarkus -am compile > java-proto-compile.log 2>&1
```

## Non-Goals

- adding cognition-specific status, rebuild, or retrieval-debug RPCs to Memory Service
- replacing REST APIs
- changing archive semantics beyond matching the current REST behavior
- exposing raw evidence, provider prompts, internal `clientId`, or provider cache keys through gRPC memory payloads

## Security Considerations

- Admin event scope requires admin or auditor authorization, matching REST admin event reads; checkpoints require admin authorization or an explicitly equivalent service-principal policy.
- `on_behalf_of_user_id` must never let a service principal broaden access beyond configured namespace policies.
- gRPC safe attributes must match REST policy extraction and must not include encrypted memory values, raw evidence text, internal client metadata, provider prompts, or provider cache keys.
