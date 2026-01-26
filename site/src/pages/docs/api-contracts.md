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

<!-- 
## Framework-Specific Clients

For Quarkus and Spring applications, pre-built clients are available:

- [Quarkus Extension](/docs/quarkus/) - Includes REST and gRPC clients
- [Spring Integration](/docs/spring/) - Spring Boot starter (coming soon) 
-->
