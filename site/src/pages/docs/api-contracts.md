---
layout: ../../layouts/DocsLayout.astro
title: API Contracts
description: Memory Service API contract specifications - OpenAPI and Protocol Buffers.
---

Memory Service provides both REST and gRPC APIs. The contract specifications are the source of truth for all client implementations.

## REST API (OpenAPI)

The REST API is defined using OpenAPI 3.0 specification.

- **OpenAPI Spec**: [openapi.yaml](https://github.com/chirino/memory-service/blob/main/memory-service-contracts/src/main/resources/openapi.yaml)
- **Format**: OpenAPI 3.0 / YAML
- **Base Path**: `/api/v1`

You can use the OpenAPI specification to:
- Generate client SDKs in any language
- Import into API testing tools (Postman, Insomnia)
- Generate API documentation

### Endpoints Overview

| Resource | Operations |
|----------|------------|
| `/conversations` | Create, list, get, update, delete conversations |
| `/conversations/{id}/messages` | Add, list, get messages |
| `/conversations/{id}/fork` | Fork a conversation |
| `/search` | Semantic search across conversations |

## gRPC API (Protocol Buffers)

The gRPC API is defined using Protocol Buffers v3.

- **Proto File**: [memory-service.proto](https://github.com/chirino/memory-service/blob/main/memory-service-contracts/src/main/proto/memory-service.proto)
- **Format**: Protocol Buffers 3
- **Package**: `memoryservice.v1`

The gRPC API provides:
- High-performance binary protocol
- Bi-directional streaming for real-time updates
- Strong typing with generated stubs

### Services Overview

| Service | Methods |
|---------|---------|
| `MemoryService` | Conversation and message management |
| `SearchService` | Semantic search operations |

## Using the Contracts

### Generate REST Client

```bash
# Using OpenAPI Generator
openapi-generator generate \
  -i https://raw.githubusercontent.com/chirino/memory-service/main/memory-service-contracts/src/main/resources/openapi.yaml \
  -g java \
  -o ./generated-client
```

### Generate gRPC Client

```bash
# Using protoc
protoc \
  --java_out=./generated \
  --grpc-java_out=./generated \
  memory-service.proto
```

## Framework-Specific Clients

For Quarkus and Spring applications, pre-built clients are available:

- [Quarkus Extension](/docs/quarkus/) - Includes REST and gRPC clients
- [Spring Integration](/docs/spring/) - Spring Boot starter (coming soon)
