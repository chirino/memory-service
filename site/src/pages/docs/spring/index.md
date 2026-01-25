---
layout: ../../../layouts/DocsLayout.astro
title: Spring Integration
description: Integrate Memory Service with Spring Boot applications.
---

Spring Boot integration for Memory Service is currently under development.

## Planned Features

- **Spring Boot Starter** - Auto-configuration for Memory Service client
- **Spring AI Integration** - ChatMemory provider for Spring AI
- **REST Client** - Type-safe REST client using Spring WebClient
- **gRPC Client** - gRPC stubs for Spring applications

## Coming Soon

The Spring integration will include:

### Spring Boot Starter

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-spring-boot-starter</artifactId>
  <version>1.0.0</version>
</dependency>
```

### Configuration

```yaml
memory-service:
  url: http://localhost:8080
  connect-timeout: 30s
  read-timeout: 60s
```

### ChatMemory Provider

```java
@Autowired
ChatMemoryProvider memoryProvider;

public ChatMemory getMemory(String conversationId) {
    return memoryProvider.get(conversationId);
}
```

## Current Status

Check the [GitHub repository](https://github.com/chirino/memory-service) for the latest development status and to contribute to the Spring integration.

## Alternative: Direct API Access

In the meantime, you can use the Memory Service APIs directly:

- [API Contracts](/docs/api-contracts/) - OpenAPI and gRPC specifications
- Generate clients using the contract specifications

## Get Involved

Interested in contributing to the Spring integration? Check out:

- [GitHub Issues](https://github.com/chirino/memory-service/issues) - Feature requests and bug reports
- [Contributing Guide](https://github.com/chirino/memory-service/blob/main/CONTRIBUTING.md) - How to contribute
