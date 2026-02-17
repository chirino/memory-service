---
status: implemented
---

# UUID IDs in API Contracts

> **Status**: Implemented.

## Overview

This document proposes adding explicit UUID format specifications to the Memory Service API contracts (OpenAPI and Protocol Buffers). While the implementation already uses UUIDs internally, the API contracts do not communicate this to clients, leading to suboptimal client code generation and unclear documentation.

## Current State

### Internal Implementation

The Memory Service consistently uses `java.util.UUID` for all entity IDs:

| Entity | ID Field | Implementation |
|--------|----------|----------------|
| ConversationEntity | `id` | `UUID`, auto-generated via `UUID.randomUUID()` |
| EntryEntity | `id` | `UUID`, auto-generated via `UUID.randomUUID()` |
| ConversationGroupEntity | `id` | `UUID`, auto-generated via `UUID.randomUUID()` |
| TaskEntity | `id` | `UUID`, auto-generated via `UUID.randomUUID()` |
| ConversationOwnershipTransferEntity | `id` | `UUID`, auto-generated via `UUID.randomUUID()` |

All ID string parameters are parsed using `UUID.fromString()` throughout the codebase (see `PostgresMemoryStore.java` for ~25+ usages).

### API Contract Gaps

**OpenAPI (`openapi.yml`):**
- All ID fields use `type: string` without `format: uuid`
- Examples use incorrect ULID-style prefixed IDs: `"conv_01HF8XH1XABCD1234EFGH5678"`
- Actual API returns standard UUIDs: `"550e8400-e29b-41d4-a716-446655440000"`

**OpenAPI Admin (`openapi-admin.yml`):**
- Only 2 fields correctly specify `format: uuid` (eviction job ID)
- All other ID fields lack format specification

**Protocol Buffers (`memory_service.proto`):**
- All ID fields are `string` with no documentation about UUID format
- No field-level comments indicating expected format
- Uses inefficient string encoding (36 bytes) instead of binary (16 bytes)

## Goals

1. **Accurate documentation**: Make API contracts reflect that IDs are UUIDs
2. **Better code generation**: Enable clients to use native UUID types where available
3. **Correct examples**: Fix examples to use actual UUID format
4. **Efficient wire format**: Use binary encoding for UUIDs in gRPC (16 bytes vs 36 bytes)
5. **Type safety**: Prevent accidental use of arbitrary strings as IDs

## Affected ID Fields

### Conversation IDs
| Location | Field |
|----------|-------|
| Path parameters | `conversationId` |
| Schema: ConversationSummary | `id` |
| Schema: Conversation | `id`, `forkedAtConversationId` |
| Schema: ConversationMembership | `conversationId` |
| Schema: ConversationForkSummary | `conversationId`, `forkedAtConversationId` |
| Proto: Conversation | `id`, `forked_at_conversation_id` |
| Proto: ConversationSummary | `id` |
| Proto: ConversationMembership | `conversation_id` |
| Proto: ConversationForkSummary | `conversation_id`, `forked_at_conversation_id` |

### Entry IDs
| Location | Field |
|----------|-------|
| Path parameters | `entryId` |
| Query parameters | `after` (pagination cursor) |
| Schema: Entry | `id`, `conversationId` |
| Schema: Conversation | `forkedAtEntryId` |
| Schema: ConversationForkSummary | `forkedAtEntryId` |
| Schema: IndexTranscriptRequest | `conversationId`, `untilEntryId` |
| Schema: SearchRequest | `conversationIds[]`, `before` |
| Proto: Entry | `id`, `conversation_id` |
| Proto: Conversation | `forked_at_entry_id` |
| Proto: ConversationForkSummary | `forked_at_entry_id` |
| Proto: IndexTranscriptRequest | `conversation_id`, `until_entry_id` |
| Proto: SearchEntriesRequest | `conversation_ids[]`, `before` |

### Admin API IDs
| Location | Field |
|----------|-------|
| Path parameters | `conversationId`, `entryId`, `jobId` |
| Query parameters | `afterEntryId` |
| Various request/response schemas | (same patterns as user API) |

## Non-UUID IDs (No Changes Needed)

These fields are intentionally NOT UUIDs and should remain as plain strings:

| Field | Type | Notes |
|-------|------|-------|
| `userId` | String | External identity (e.g., OIDC subject) |
| `ownerUserId` | String | External identity |
| `clientId` | String | Agent/client identifier |
| `nextCursor` | String | Opaque pagination token |
| `page_token` | String | gRPC pagination token |

## Implementation

### Phase 1: OpenAPI Specification Updates

**File: `memory-service-contracts/src/main/resources/openapi.yml`**

1. Add `format: uuid` to all ID fields:
```yaml
# Example for ConversationSummary
ConversationSummary:
  type: object
  properties:
    id:
      type: string
      format: uuid
      description: Unique identifier for the conversation.
```

2. Add descriptions to ID fields explaining they are UUIDs

3. Fix all example values to use valid UUIDs:
```yaml
example:
  id: "550e8400-e29b-41d4-a716-446655440000"
  # Not: "conv_01HF8XH1XABCD1234EFGH5678"
```

4. Update path parameter schemas:
```yaml
parameters:
  - name: conversationId
    in: path
    required: true
    description: Unique identifier for the conversation (UUID format).
    schema:
      type: string
      format: uuid
```

**File: `memory-service-contracts/src/main/resources/openapi-admin.yml`**

Apply the same changes to admin API (most already uses `format: uuid` for job IDs).

### Phase 2: Protocol Buffers Updates

**File: `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`**

Change all UUID ID fields from `string` to `bytes` for efficient binary encoding. UUIDs are exactly 16 bytes, making `bytes` the natural representation:

```protobuf
message ConversationSummary {
  // Unique identifier (UUID as 16-byte big-endian binary)
  bytes id = 1;
  // ...
}

message Conversation {
  // Unique identifier (UUID as 16-byte big-endian binary)
  bytes id = 1;
  // ...
  // Entry ID where this conversation forked (empty if root)
  bytes forked_at_entry_id = 9;
  // Conversation ID from which this was forked (empty if root)
  bytes forked_at_conversation_id = 10;
}

message Entry {
  // Unique identifier (UUID as 16-byte big-endian binary)
  bytes id = 1;
  // Conversation this entry belongs to
  bytes conversation_id = 2;
  // ...
}
```

**Benefits of `bytes` over `string`:**
- **Size efficiency**: 16 bytes vs 36 bytes (56% smaller)
- **No parsing overhead**: Direct byte copy vs string parsing
- **Type safety**: Can't accidentally pass arbitrary strings
- **Standard representation**: RFC 4122 defines UUID as 128-bit value

**Wire format note:** Changing from `string` to `bytes` while keeping the same field number is a breaking change. Both use wire type 2 (length-delimited), but the content interpretation differs. Old clients will misinterpret the bytes as UTF-8 strings. Since this is a pre-release API, we accept this breaking change.

**Server-side conversion helpers:**

```java
// UUID to bytes (big-endian)
public static byte[] uuidToBytes(UUID uuid) {
    ByteBuffer buffer = ByteBuffer.allocate(16);
    buffer.putLong(uuid.getMostSignificantBits());
    buffer.putLong(uuid.getLeastSignificantBits());
    return buffer.array();
}

// Bytes to UUID
public static UUID bytesToUuid(byte[] bytes) {
    ByteBuffer buffer = ByteBuffer.wrap(bytes);
    long mostSig = buffer.getLong();
    long leastSig = buffer.getLong();
    return new UUID(mostSig, leastSig);
}

// For gRPC ByteString
public static ByteString uuidToByteString(UUID uuid) {
    return ByteString.copyFrom(uuidToBytes(uuid));
}

public static UUID byteStringToUuid(ByteString bytes) {
    return bytesToUuid(bytes.toByteArray());
}
```

**Update gRPC service implementations** to convert between `UUID` and `bytes`:

```java
// In GrpcConversationsService.java
@Override
public void getConversation(GetConversationRequest request,
                            StreamObserver<Conversation> responseObserver) {
    UUID conversationId = bytesToUuid(request.getConversationId().toByteArray());
    // ... fetch conversation ...
    Conversation.Builder response = Conversation.newBuilder()
        .setId(uuidToByteString(entity.getId()))
        // ...
    responseObserver.onNext(response.build());
    responseObserver.onCompleted();
}
```

### Phase 3: Site Documentation Updates

**File: `site/src/pages/docs/api-contracts.md`**

Add a section documenting ID formats:

```markdown
## ID Formats

All resource identifiers in the Memory Service API are UUIDs
(Universally Unique Identifiers). This applies to:
- Conversation IDs
- Entry IDs
- Fork reference IDs

**Note:** User IDs and client IDs are external identifiers and
do not follow UUID format.

### REST API (JSON)

UUIDs are represented as strings in standard format:

```
xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

Example: `"550e8400-e29b-41d4-a716-446655440000"`

### gRPC API (Protocol Buffers)

UUIDs are represented as 16-byte binary values (big-endian):

| Bytes 0-7 | Bytes 8-15 |
|-----------|------------|
| Most significant 64 bits | Least significant 64 bits |

**Java example:**
```java
// UUID to bytes
ByteBuffer buffer = ByteBuffer.allocate(16);
buffer.putLong(uuid.getMostSignificantBits());
buffer.putLong(uuid.getLeastSignificantBits());
byte[] bytes = buffer.array();

// Bytes to UUID
ByteBuffer buffer = ByteBuffer.wrap(bytes);
UUID uuid = new UUID(buffer.getLong(), buffer.getLong());
```

**Go example:**
```go
// UUID to bytes - google/uuid package stores as [16]byte
id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
bytes := id[:] // id is already [16]byte

// Bytes to UUID
id, err := uuid.FromBytes(bytes)
```
```

### Phase 4: Regenerate Clients

After updating contracts, regenerate all client code:

```bash
./mvnw compile
```

This will regenerate:
- Java REST client DTOs
- Java gRPC stubs

### Phase 5: Verify Tests Pass

Run the full test suite to ensure no regressions:

```bash
./mvnw test
```

## Impact Assessment

### Client Code Generation

**REST API (OpenAPI Generator):**

| Language | Before | After |
|----------|--------|-------|
| Java | `String` | `java.util.UUID` |
| TypeScript | `string` | `string` (with JSDoc) |
| Go | `string` | `uuid.UUID` (depends on config) |
| Python | `str` | `uuid.UUID` |

**gRPC (all languages):**

| Language | Before | After |
|----------|--------|-------|
| Java | `String` | `ByteString` (convert to UUID) |
| Go | `string` | `[]byte` (convert to uuid.UUID) |
| Python | `str` | `bytes` (convert to uuid.UUID) |
| TypeScript | `string` | `Uint8Array` |

### Breaking Changes

**REST API:** None - wire format remains the same (UUIDs serialized as JSON strings)

**gRPC API:** **Breaking change** - wire format changes from UTF-8 string to binary bytes
- Existing gRPC clients will fail to deserialize responses
- Clients must regenerate stubs from updated proto
- Server and clients must be updated together

**Generated REST Clients:**
- Java clients: `String` → `UUID` type change
- Other languages: varies by generator

**Generated gRPC Clients:**
- All languages: `string` → `bytes` type change
- Requires conversion code to work with UUID types

### Migration for Existing Clients

**REST clients:**

```java
// Before
String conversationId = "550e8400-e29b-41d4-a716-446655440000";
api.getConversation(conversationId);

// After
UUID conversationId = UUID.fromString("550e8400-e29b-41d4-a716-446655440000");
api.getConversation(conversationId);
```

**gRPC clients (Java):**

```java
// Before
GetConversationRequest request = GetConversationRequest.newBuilder()
    .setConversationId("550e8400-e29b-41d4-a716-446655440000")
    .build();

// After
UUID uuid = UUID.fromString("550e8400-e29b-41d4-a716-446655440000");
ByteBuffer buffer = ByteBuffer.allocate(16);
buffer.putLong(uuid.getMostSignificantBits());
buffer.putLong(uuid.getLeastSignificantBits());

GetConversationRequest request = GetConversationRequest.newBuilder()
    .setConversationId(ByteString.copyFrom(buffer.array()))
    .build();
```

**gRPC clients (Go):**

```go
// Before
req := &pb.GetConversationRequest{
    ConversationId: "550e8400-e29b-41d4-a716-446655440000",
}

// After
id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
req := &pb.GetConversationRequest{
    ConversationId: id[:], // UUID is already [16]byte
}
```

## Alternatives Considered

### 1. Keep IDs as Unformatted Strings
- **Pros:** No client migration needed
- **Cons:** Poor documentation, no type safety, confusing examples, inefficient

### 2. Use Custom OpenAPI Extension
- **Pros:** Non-breaking
- **Cons:** Not standard, generators may not support

### 3. Only Update Examples and Descriptions
- **Pros:** Non-breaking, better docs
- **Cons:** Miss code generation benefits, still inefficient for gRPC

### 4. Use `string` with Documentation in Proto (instead of `bytes`)
- **Pros:** Non-breaking for gRPC, simpler client code
- **Cons:** 56% larger wire format, parsing overhead, less type-safe

### 5. Use a Custom `Uuid` Message Type in Proto
```protobuf
message Uuid {
  fixed64 high = 1;
  fixed64 low = 2;
}
```
- **Pros:** Self-documenting, efficient (16 bytes + small overhead)
- **Cons:** More complex, requires wrapper code, non-standard

**Decision:** Use `format: uuid` in OpenAPI and `bytes` in Proto. The benefits of accurate contracts, type-safe clients, and efficient wire format outweigh the migration effort. Since the service is pre-release, breaking changes are acceptable.

## Checklist

### OpenAPI Updates
- [ ] Update `openapi.yml` with `format: uuid` for all ID fields
- [ ] Update `openapi.yml` examples to use valid UUIDs
- [ ] Update `openapi-admin.yml` with `format: uuid` for remaining ID fields

### Protocol Buffers Updates
- [ ] Change ID fields from `string` to `bytes` in `memory_service.proto`
- [ ] Add documentation comments explaining UUID binary format
- [ ] Create `UuidUtils` helper class for UUID ↔ bytes conversion

### Server Implementation Updates
- [ ] Update gRPC service implementations to use bytes for UUIDs
- [ ] Update DTO mappers for gRPC responses

### Documentation Updates
- [ ] Update `site/src/pages/docs/api-contracts.md` with ID format section
- [ ] Document gRPC UUID bytes format for client developers

### Verification
- [ ] Regenerate clients with `./mvnw compile`
- [ ] Run tests with `./mvnw test`
- [ ] Test gRPC clients with new bytes format
- [ ] Update changelog

## References

- [OpenAPI Format Specification](https://spec.openapis.org/oas/v3.1.0#format)
- [RFC 4122 - UUID URN Namespace](https://tools.ietf.org/html/rfc4122) - Section 4.1.2 defines the 128-bit binary layout
- [Proto3 Scalar Types](https://protobuf.dev/programming-guides/proto3/#scalar) - `bytes` type documentation
- [Proto3 Best Practices](https://protobuf.dev/programming-guides/dos-donts/)
