---
layout: ../../../layouts/DocsLayout.astro
title: Quarkus Extension
description: Integrate Memory Service with Quarkus applications.
---

The Memory Service Quarkus extension provides seamless integration with Quarkus applications, including automatic Dev Services, client injection, and LangChain4j support.

## Installation

Add the extension to your `pom.xml`:

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-extension</artifactId>
  <version>1.0.0</version>
</dependency>
```

## Dev Services

In development mode, the extension automatically starts Memory Service in Docker:

```properties
# Dev Services are enabled by default in dev mode
# Customize if needed:
quarkus.memory-service.devservices.enabled=true
quarkus.memory-service.devservices.image-name=ghcr.io/chirino/memory-service:latest
quarkus.memory-service.devservices.port=8081
```

No additional configuration needed - just run your app!

## Client Injection

Inject the Memory Service client directly:

```java
@Inject
MemoryServiceClient memoryClient;

public void createConversation(String id) {
    memoryClient.conversations().create(
        CreateConversationRequest.builder()
            .id(id)
            .build()
    );
}
```

## ChatMemory Provider

For LangChain4j integration, inject the `ChatMemoryProvider`:

```java
@Inject
ChatMemoryProvider memoryProvider;

@Inject
ChatLanguageModel model;

public String chat(String conversationId, String userMessage) {
    ChatMemory memory = memoryProvider.get(conversationId);

    // Add user message
    memory.add(UserMessage.from(userMessage));

    // Get AI response
    AiMessage response = model.generate(memory.messages()).content();

    // Store AI response
    memory.add(response);

    return response.text();
}
```

## Configuration Reference

| Property | Description | Default |
|----------|-------------|---------|
| `memory-service.url` | Service URL | (Dev Services) |
| `memory-service.connect-timeout` | Connection timeout | `30s` |
| `memory-service.read-timeout` | Read timeout | `60s` |
| `memory-service.tls.verify` | Verify TLS certificates | `true` |
| `quarkus.memory-service.devservices.enabled` | Enable Dev Services | `true` in dev |
| `quarkus.memory-service.devservices.port` | Dev Services port | random |

## Advanced Usage

### Custom Memory Window

Limit the number of messages returned:

```java
ChatMemory memory = memoryProvider.get(conversationId);
// Configure window size
memory.windowSize(20);
```

### Metadata

Attach metadata to conversations:

```java
memoryClient.conversations().update(
    conversationId,
    UpdateRequest.builder()
        .metadata(Map.of(
            "topic", "support",
            "priority", "high"
        ))
        .build()
);
```

### Health Checks

The extension adds automatic health checks:

```bash
curl http://localhost:8080/q/health
```

```json
{
  "status": "UP",
  "checks": [
    {
      "name": "Memory Service connection",
      "status": "UP"
    }
  ]
}
```

## Next Steps

- Learn about [LangChain4j Integration](/docs/integrations/langchain4j/)
- Explore the [REST API](/docs/integrations/rest-api/)
