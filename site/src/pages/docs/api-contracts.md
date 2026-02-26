---
layout: ../../layouts/DocsLayout.astro
title: API Contracts
description: Memory Service API contract specifications - OpenAPI and Protocol Buffers.
---

Memory Service provides both REST and gRPC APIs. The contract specifications are the source of truth for all client implementations.

## REST API (OpenAPI)

The REST API is defined using OpenAPI 3.0 specification.

- **OpenAPI Spec**: [openapi.yaml](https://petstore.swagger.io/?url=https://raw.githubusercontent.com/chirino/memory-service/refs/heads/main/memory-service-contracts/src/main/resources/openapi.yml)
- **Format**: OpenAPI 3.0 / YAML
- **Base Path**: `/api/v1`

You can use the OpenAPI specification to:

- Generate client SDKs in any language
- Import into API testing tools (Postman, Insomnia)
- Generate API documentation

## gRPC API (Protocol Buffers)

The gRPC API is defined using Protocol Buffers v3.

- **Proto File**: [memory-service.proto](https://github.com/chirino/memory-service/blob/main/memory-service-contracts/src/main/resources/memory/v1/memory_service.proto)
- **Format**: Protocol Buffers 3
- **Package**: `memoryservice.v1`

The gRPC API provides:

- High-performance binary protocol
- Bi-directional streaming for real-time updates
- Strong typing with generated stubs

## ID Formats

All resource identifiers in the Memory Service API are UUIDs (Universally Unique Identifiers). This applies to:

- Conversation IDs
- Entry IDs
- Fork reference IDs

**Note:** User IDs and client IDs are external identifiers and do not follow UUID format.

### REST API (JSON)

UUIDs are represented as strings in standard format:

```
xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

Example: `"550e8400-e29b-41d4-a716-446655440000"`

The OpenAPI specification uses `format: uuid` for these fields, which enables type-safe UUID handling in generated clients.

### gRPC API (Protocol Buffers)

UUIDs are represented as 16-byte binary values (big-endian). The most significant 64 bits come first, followed by the least significant 64 bits.

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

<!--
## Framework-Specific Clients

For Quarkus and Spring applications, pre-built clients are available:

- [Quarkus Extension](/docs/quarkus/) - Includes REST and gRPC clients
- [Spring Integration](/docs/spring/) - Spring Boot starter (coming soon)
-->
