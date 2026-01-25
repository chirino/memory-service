---
layout: ../../../layouts/DocsLayout.astro
title: Quarkus REST Client
description: Using the Memory Service REST client in Quarkus applications.
---

The Quarkus extension provides a type-safe REST client for interacting with Memory Service.

## Setup

The REST client is included in the extension:

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-extension</artifactId>
  <version>1.0.0</version>
</dependency>
```

## Injecting the Client

```java
@Inject
MemoryServiceClient memoryClient;
```

## Conversations API

### List Conversations

```java
ListConversationsResponse response = memoryClient.conversations()
    .list(ListRequest.builder()
        .limit(20)
        .offset(0)
        .build());

for (Conversation conv : response.getConversations()) {
    System.out.println(conv.getId());
}
```

### Create Conversation

```java
Conversation conv = memoryClient.conversations()
    .create(CreateConversationRequest.builder()
        .id("my-conversation")
        .metadata(Map.of("topic", "support"))
        .build());
```

### Get Conversation

```java
Conversation conv = memoryClient.conversations()
    .get("my-conversation");
```

### Update Conversation

```java
memoryClient.conversations()
    .update("my-conversation", UpdateRequest.builder()
        .metadata(Map.of("status", "resolved"))
        .build());
```

### Delete Conversation

```java
memoryClient.conversations()
    .delete("my-conversation");
```

### Fork Conversation

```java
Conversation forked = memoryClient.conversations()
    .fork("original-id", ForkRequest.builder()
        .newId("forked-conversation")
        .atMessage(5)
        .build());
```

## Messages API

### Add Message

```java
Message msg = memoryClient.messages("my-conversation")
    .add(AddMessageRequest.builder()
        .type(MessageType.USER)
        .content("Hello, how can you help?")
        .metadata(Map.of("client", "web"))
        .build());
```

### List Messages

```java
ListMessagesResponse response = memoryClient.messages("my-conversation")
    .list(ListMessagesRequest.builder()
        .limit(100)
        .build());

for (Message msg : response.getMessages()) {
    System.out.println(msg.getType() + ": " + msg.getContent());
}
```

### Get Message

```java
Message msg = memoryClient.messages("my-conversation")
    .get("msg-123");
```

## Search API

### Semantic Search

```java
SearchResponse response = memoryClient.search()
    .query(SearchRequest.builder()
        .query("How do I configure authentication?")
        .limit(10)
        .minScore(0.7)
        .build());

for (SearchResult result : response.getResults()) {
    System.out.println(result.getScore() + ": " + result.getContent());
}
```

### Filtered Search

```java
SearchResponse response = memoryClient.search()
    .query(SearchRequest.builder()
        .query("error handling")
        .conversationIds(List.of("conv-1", "conv-2"))
        .messageTypes(List.of(MessageType.AI))
        .after(Instant.parse("2024-01-01T00:00:00Z"))
        .build());
```

## Error Handling

```java
try {
    Conversation conv = memoryClient.conversations().get("unknown-id");
} catch (NotFoundException e) {
    // Conversation not found
} catch (MemoryServiceException e) {
    // Other API error
    System.err.println("Error: " + e.getErrorCode() + " - " + e.getMessage());
}
```

## Configuration

```properties
# Memory Service URL (auto-configured with Dev Services)
memory-service.url=http://localhost:8080

# Timeouts
memory-service.connect-timeout=30s
memory-service.read-timeout=60s

# TLS
memory-service.tls.verify=true
```

## Next Steps

- [gRPC Client](/docs/quarkus/grpc-client/) - For streaming and high-performance use cases
- [LangChain4j Integration](/docs/quarkus/langchain4j/) - ChatMemory provider
