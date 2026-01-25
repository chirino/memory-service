---
layout: ../../../layouts/DocsLayout.astro
title: gRPC API
description: Memory Service gRPC API for high-performance communication.
---

Memory Service provides a gRPC API for high-performance, strongly-typed communication.

## Overview

The gRPC API offers:
- **High performance** - Binary protocol, HTTP/2, streaming
- **Strong typing** - Protocol buffer contracts
- **Bi-directional streaming** - Real-time message delivery
- **Language support** - Generate clients for any language

## Connection

```
host: localhost
port: 9000 (default gRPC port)
```

## Proto Definition

The service definition is available in the `memory-service-contracts` module:

```protobuf
syntax = "proto3";

package memoryservice.v1;

service MemoryService {
  // Conversations
  rpc CreateConversation(CreateConversationRequest) returns (Conversation);
  rpc GetConversation(GetConversationRequest) returns (Conversation);
  rpc ListConversations(ListConversationsRequest) returns (ListConversationsResponse);
  rpc DeleteConversation(DeleteConversationRequest) returns (Empty);
  rpc ForkConversation(ForkConversationRequest) returns (Conversation);

  // Messages
  rpc AddMessage(AddMessageRequest) returns (Message);
  rpc GetMessages(GetMessagesRequest) returns (stream Message);
  rpc StreamMessages(StreamMessagesRequest) returns (stream Message);

  // Search
  rpc Search(SearchRequest) returns (SearchResponse);
}
```

## Using the Quarkus Client

Add the gRPC client dependency:

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-proto-quarkus</artifactId>
  <version>1.0.0</version>
</dependency>
```

Inject and use the client:

```java
@GrpcClient("memory-service")
MemoryServiceGrpc.MemoryServiceBlockingStub client;

public Conversation getConversation(String id) {
    return client.getConversation(
        GetConversationRequest.newBuilder()
            .setId(id)
            .build()
    );
}
```

Configure the client:

```properties
quarkus.grpc.clients.memory-service.host=localhost
quarkus.grpc.clients.memory-service.port=9000
```

## Streaming Messages

### Server Streaming

Get all messages for a conversation:

```java
@GrpcClient("memory-service")
MemoryServiceGrpc.MemoryServiceStub asyncClient;

public void streamMessages(String conversationId) {
    asyncClient.getMessages(
        GetMessagesRequest.newBuilder()
            .setConversationId(conversationId)
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
}
```

### Real-time Updates

Subscribe to new messages in a conversation:

```java
asyncClient.streamMessages(
    StreamMessagesRequest.newBuilder()
        .setConversationId(conversationId)
        .setFromSequence(lastKnownSequence)
        .build(),
    new StreamObserver<Message>() {
        @Override
        public void onNext(Message message) {
            // Handle new message
            updateUI(message);
        }

        @Override
        public void onError(Throwable t) {
            // Handle reconnection
            reconnect();
        }

        @Override
        public void onCompleted() {
            // Stream ended (conversation deleted?)
        }
    }
);
```

## Message Types

```protobuf
message Message {
  string id = 1;
  string conversation_id = 2;
  MessageType type = 3;
  string content = 4;
  google.protobuf.Timestamp timestamp = 5;
  map<string, string> metadata = 6;
}

enum MessageType {
  MESSAGE_TYPE_UNSPECIFIED = 0;
  MESSAGE_TYPE_USER = 1;
  MESSAGE_TYPE_AI = 2;
  MESSAGE_TYPE_SYSTEM = 3;
  MESSAGE_TYPE_TOOL_EXECUTION = 4;
}
```

## Error Handling

gRPC errors use standard status codes:

| Status | Description |
|--------|-------------|
| `NOT_FOUND` | Conversation or message not found |
| `INVALID_ARGUMENT` | Bad request parameters |
| `UNAUTHENTICATED` | Missing or invalid credentials |
| `PERMISSION_DENIED` | Access not allowed |
| `INTERNAL` | Server error |

Handle errors in your client:

```java
try {
    Conversation conv = client.getConversation(request);
} catch (StatusRuntimeException e) {
    if (e.getStatus().getCode() == Status.Code.NOT_FOUND) {
        // Handle not found
    } else {
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

## Next Steps

- Learn about [Docker Deployment](/docs/deployment/docker/)
- Explore [Database Setup](/docs/deployment/databases/)
