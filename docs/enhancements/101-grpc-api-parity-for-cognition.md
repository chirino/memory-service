---
status: proposed
---

# Enhancement 101: gRPC API Parity for Cognition

> **Status**: Proposed.

## Summary

Add the gRPC API parity needed for external cognition processors to use Memory Service without HTTP or SSE dependencies. The gRPC surface must match the behavior of the REST counterparts for event replay, worker checkpoints, archive handling, conditional memory updates, service-principal authorization, and TTL-backed indexed memory writes. Memory-search parity is handled by [100](100-enhanced-memory-search.md).

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

`EventStreamService.SubscribeEvents` remains the general event streaming RPC, but it must support an admin/service-principal mode equivalent to REST admin event replay.

```protobuf
message SubscribeEventsRequest {
  repeated bytes conversation_ids = 1;
  repeated string kinds = 2;
  optional string after_cursor = 3;
  optional string detail = 4; // "summary" or "full"
  optional EventScope scope = 5;
}

enum EventScope {
  EVENT_SCOPE_UNSPECIFIED = 0; // existing user/member-scoped behavior
  EVENT_SCOPE_AUTHORIZED = 1;  // user/member-scoped behavior
  EVENT_SCOPE_ADMIN = 2;       // admin/service-principal event stream
}
```

`EVENT_SCOPE_ADMIN` requires admin authorization and returns the same event set, cursor behavior, replay semantics, and archive visibility as REST `/v1/admin/events`. Non-admin callers requesting admin scope receive `403`. `conversation_ids` and `kinds` are filters, not authorization grants.

`EventNotification` should remain cursor-bearing. The cursor must be durable enough for restart catch-up and must match the REST admin event cursor semantics.

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
  google.protobuf.Struct checkpoint = 2;
}

message AdminCheckpoint {
  string client_id = 1;
  google.protobuf.Struct checkpoint = 2;
  google.protobuf.Timestamp updated_at = 3;
}
```

Behavior must match REST:

- checkpoints are encrypted at rest
- authenticated client ID, when present, must match `client_id`
- admin role is required unless an existing policy explicitly grants the operation
- missing checkpoints return gRPC `NOT_FOUND`
- malformed requests return `INVALID_ARGUMENT`

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
}

message UpdateMemoryRequest {
  repeated string namespace = 1;
  string key = 2;
  optional bool archived = 3;
  optional int64 expected_revision = 4;
}
```

When `expected_revision` is set, the write must only succeed if the active row for `(namespace, key)` has that revision. A mismatch returns gRPC `ABORTED` or `FAILED_PRECONDITION` consistently across datastores. `MemoryWriteResult` should include the resulting revision.

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

Memory write, update, get, search, and namespace-list requests may carry `RequestActor` where needed. Search-specific behavior is finalized in [100](100-enhanced-memory-search.md). Normal user callers omit it. Service principals can set it only when policy allows that principal to act for the target user and only within authorized namespace prefixes such as `["user", <sub>, "cognition.v1", ...]`.

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

For every feature listed here, REST remains the behavior oracle unless a later enhancement intentionally changes both contracts. gRPC errors should use idiomatic status codes while preserving the same authorization, validation, and data-visibility semantics.

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
    Given an admin service principal stores checkpoint {"lastEventCursor":"cursor-1"} for "cognition-worker-1" over gRPC
    When it reads checkpoint "cognition-worker-1" over gRPC
    Then the checkpoint contains "lastEventCursor" equal to "cursor-1"

  Scenario: gRPC conditional memory write detects conflicts
    Given a memory has revision 3
    When PutMemory is called over gRPC with expected_revision 2
    Then the gRPC status should be ABORTED or FAILED_PRECONDITION

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

### Unit / Integration Tests

- Proto validation tests cover required checkpoint client IDs, namespace prefixes, request actor fields, and invalid archive filters.
- Memory-search parity tests live in [100](100-enhanced-memory-search.md).
- Store-level tests verify revision increments and conditional write conflicts for PostgreSQL, SQLite, and MongoDB.
- Event stream tests verify admin scope, user scope, `kinds`, `conversation_ids`, `after_cursor`, and reconnect behavior.
- Policy tests verify service-principal `on_behalf_of_user_id` writes are constrained to allowed user namespaces.
- TTL/index tests verify gRPC writes are indexed before expiry and disappear after expiry.

## Tasks

- [ ] Update `contracts/protobuf/memory/v1/memory_service.proto` with admin event scope, checkpoint RPCs, memory revisions, conditional write fields, and request actor fields.
- [ ] Regenerate Go, Java, and Python gRPC stubs.
- [ ] Implement gRPC admin event scope using the same store/outbox path as REST admin events.
- [ ] Implement gRPC admin checkpoint service backed by the existing admin checkpoint store.
- [ ] Add revision fields to episodic memory store models and expose conditional write/update behavior through gRPC.
- [ ] Apply REST-equivalent memory archive semantics to gRPC memory get/list/update paths; search behavior is owned by [100](100-enhanced-memory-search.md).
- [ ] Implement service-principal `on_behalf_of_user_id` authorization for gRPC memory operations without exposing internal `clientId` in user-facing payloads.
- [ ] Verify gRPC TTL-backed memory writes are indexed, expire correctly, and expose write/get metadata consistently with REST.
- [ ] Add BDD and integration coverage for the scenarios above.
- [ ] Update [099](099-quarkus-cognition-processor.md) to depend on these gRPC substrate APIs for its Memory Service integration.

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
| `internal/service/` | gRPC handlers for events, checkpoints, memories, and write/get/list parity |
| `internal/plugin/store/*` | Store support for memory revisions, conditional writes, archive filters, checkpoints, TTL/index parity |
| `internal/episodic/` | Policy and request-context handling for service-principal on-behalf-of memory access |
| `internal/bdd/testdata/features/` | gRPC parity BDD scenarios |
| `internal/FACTS.md` | Record implementation gotchas discovered while adding gRPC parity |

## Verification

```bash
# Regenerate Go protobuf clients/stubs
task generate:go

# Regenerate Python gRPC stubs if protobuf package artifacts are impacted
task generate:python

# Run Go tests for affected services and stores
go test ./internal/... > test.log 2>&1
# Search for failures using Grep tool on test.log

# Compile Java modules that consume generated gRPC stubs
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-proto-quarkus -am compile
```

## Non-Goals

- adding cognition-specific status, rebuild, or retrieval-debug RPCs to Memory Service
- replacing REST APIs
- changing archive semantics beyond matching the current REST behavior
- exposing raw evidence, provider prompts, internal `clientId`, or provider cache keys through gRPC memory payloads

## Security Considerations

- Admin event scope and checkpoints require admin authorization or an explicitly equivalent service-principal policy.
- `on_behalf_of_user_id` must never let a service principal broaden access beyond configured namespace policies.
- gRPC safe attributes must match REST policy extraction and must not include encrypted memory values, raw evidence text, internal client metadata, provider prompts, or provider cache keys.
