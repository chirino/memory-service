---
layout: ../../../layouts/DocsLayout.astro
title: LangChain4j Integration
description: Use Memory Service with LangChain4j for AI agent development.
---

Memory Service provides native integration with LangChain4j, the Java library for building AI applications.

## Setup

With the Quarkus extension, LangChain4j integration is automatic:

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-extension</artifactId>
  <version>1.0.0</version>
</dependency>
<dependency>
  <groupId>io.quarkiverse.langchain4j</groupId>
  <artifactId>quarkus-langchain4j-openai</artifactId>
  <version>0.8.0</version>
</dependency>
```

## ChatMemoryProvider

The `ChatMemoryProvider` creates conversation-specific memory instances:

```java
@Inject
ChatMemoryProvider memoryProvider;

public ChatMemory getMemory(String conversationId) {
    return memoryProvider.get(conversationId);
}
```

Each call to `get()` returns a memory instance backed by Memory Service storage.

## Building an AI Service

Use LangChain4j's declarative AI service pattern:

```java
@RegisterAiService
public interface Assistant {

    @SystemMessage("You are a helpful assistant.")
    String chat(@MemoryId String conversationId, @UserMessage String message);
}
```

The `@MemoryId` annotation automatically routes messages to the correct conversation.

## Manual Integration

For more control, use the ChatMemory directly:

```java
@Inject
ChatLanguageModel model;

@Inject
ChatMemoryProvider memoryProvider;

public String chat(String conversationId, String userMessage) {
    // Get or create conversation memory
    ChatMemory memory = memoryProvider.get(conversationId);

    // Add user message to memory
    UserMessage user = UserMessage.from(userMessage);
    memory.add(user);

    // Generate response with full conversation context
    Response<AiMessage> response = model.generate(memory.messages());

    // Store AI response
    memory.add(response.content());

    return response.content().text();
}
```

## Tool Support

Memory Service stores tool execution results:

```java
@Tool("Get the current weather")
public String getWeather(String city) {
    return weatherService.getCurrent(city);
}

// Tool results are automatically persisted to conversation memory
```

## Streaming Responses

For streaming chat responses:

```java
@Inject
StreamingChatLanguageModel streamingModel;

public Multi<String> streamChat(String conversationId, String message) {
    ChatMemory memory = memoryProvider.get(conversationId);
    memory.add(UserMessage.from(message));

    return Multi.createFrom().emitter(emitter -> {
        streamingModel.generate(
            memory.messages(),
            new StreamingResponseHandler<AiMessage>() {
                StringBuilder fullResponse = new StringBuilder();

                @Override
                public void onNext(String token) {
                    fullResponse.append(token);
                    emitter.emit(token);
                }

                @Override
                public void onComplete(Response<AiMessage> response) {
                    memory.add(AiMessage.from(fullResponse.toString()));
                    emitter.complete();
                }

                @Override
                public void onError(Throwable error) {
                    emitter.fail(error);
                }
            }
        );
    });
}
```

## Memory Window

Configure how many messages to include in context:

```java
ChatMemory memory = memoryProvider.get(conversationId);
memory.windowSize(50);  // Last 50 messages
```

## RAG Integration

Combine Memory Service with RAG (Retrieval Augmented Generation):

```java
@Inject
SearchService searchService;

public String chatWithRAG(String conversationId, String question) {
    // Search for relevant context
    List<SearchResult> context = searchService.search(
        SearchRequest.builder()
            .query(question)
            .limit(5)
            .build()
    );

    // Build enhanced prompt
    String contextText = context.stream()
        .map(SearchResult::getContent)
        .collect(Collectors.joining("\n\n"));

    ChatMemory memory = memoryProvider.get(conversationId);
    memory.add(SystemMessage.from("Context:\n" + contextText));
    memory.add(UserMessage.from(question));

    // Generate response
    AiMessage response = model.generate(memory.messages()).content();
    memory.add(response);

    return response.text();
}
```

## Next Steps

- Explore the [REST API](/docs/integrations/rest-api/)
- Learn about [gRPC API](/docs/integrations/grpc/)
