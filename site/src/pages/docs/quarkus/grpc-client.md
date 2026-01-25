---
layout: ../../../layouts/DocsLayout.astro
title: Quarkus gRPC Client
description: Using the Memory Service gRPC client in Quarkus applications.
---

The Quarkus extension provides a gRPC client for high-performance communication with Memory Service.

## Setup

Add the gRPC client dependency:

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-proto-quarkus</artifactId>
  <version>1.0.0</version>
</dependency>
```

## Configuration

```properties
quarkus.grpc.clients.memory-service.host=localhost
quarkus.grpc.clients.memory-service.port=9000
```

## Injecting the Client

### Blocking Client

```java
@GrpcClient("memory-service")
MemoryServiceGrpc.MemoryServiceBlockingStub client;
```

### Async Client

```java
@GrpcClient("memory-service")
MemoryServiceGrpc.MemoryServiceStub asyncClient;
```

## Conversations

### Get Conversation

```java
Conversation conv = client.getConversation(
    GetConversationRequest.newBuilder()
        .setId("my-conversation")
        .build()
);
```

### Create Conversation

```java
Conversation conv = client.createConversation(
    CreateConversationRequest.newBuilder()
        .setId("my-conversation")
        .putMetadata("topic", "support")
        .build()
);
```

### List Conversations

```java
ListConversationsResponse response = client.listConversations(
    ListConversationsRequest.newBuilder()
        .setLimit(20)
        .build()
);

for (Conversation conv : response.getConversationsList()) {
    System.out.println(conv.getId());
}
```

### Fork Conversation

```java
Conversation forked = client.forkConversation(
    ForkConversationRequest.newBuilder()
        .setId("original-id")
        .setNewId("forked-conversation")
        .setAtMessage(5)
        .build()
);
```

## Streaming Messages

### Server Streaming - Get All Messages

```java
asyncClient.getMessages(
    GetMessagesRequest.newBuilder()
        .setConversationId("my-conversation")
        .build(),
    new StreamObserver<Message>() {
        @Override
        public void onNext(Message message) {
            System.out.println("Received: " + message.getContent());
        }

        @Override
        public void onError(Throwable t) {
            t.printStackTrace();
        }

        @Override
        public void onCompleted() {
            System.out.println("Stream completed");
        }
    }
);
```

### Real-time Message Updates

Subscribe to new messages as they arrive:

```java
asyncClient.streamMessages(
    StreamMessagesRequest.newBuilder()
        .setConversationId("my-conversation")
        .setFromSequence(lastKnownSequence)
        .build(),
    new StreamObserver<Message>() {
        @Override
        public void onNext(Message message) {
            // Handle new message in real-time
            updateUI(message);
        }

        @Override
        public void onError(Throwable t) {
            // Handle reconnection
            reconnect();
        }

        @Override
        public void onCompleted() {
            // Stream ended
        }
    }
);
```

## Error Handling

```java
try {
    Conversation conv = client.getConversation(request);
} catch (StatusRuntimeException e) {
    switch (e.getStatus().getCode()) {
        case NOT_FOUND:
            // Conversation not found
            break;
        case UNAUTHENTICATED:
            // Missing or invalid credentials
            break;
        case PERMISSION_DENIED:
            // Access denied
            break;
        default:
            throw e;
    }
}
```

## TLS Configuration

For production, enable TLS:

```properties
quarkus.grpc.clients.memory-service.tls.enabled=true
quarkus.grpc.clients.memory-service.tls.trust-certificate-pem.certs=ca.pem
```

## When to Use gRPC

Choose gRPC over REST when you need:

- **Streaming** - Real-time message updates
- **High throughput** - Binary protocol is more efficient
- **Strong typing** - Generated stubs catch errors at compile time
- **Bi-directional communication** - Full duplex streaming

## Next Steps

- [REST Client](/docs/quarkus/rest-client/) - For simpler use cases
- [LangChain4j Integration](/docs/quarkus/langchain4j/) - ChatMemory provider
