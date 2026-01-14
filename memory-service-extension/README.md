# Memory Service Quarkus Extension (runtime + deployment)

This module provides a Quarkus extension that makes it easy to integrate
applications with the Memory Service. It contains:

- A generated REST client (`memory-service-client`) and dev services
- LangChain4j chat-memory integration
- **Conversation recording support** via interceptor + annotations

This document focuses on the conversation recording feature.

## Conversation recording overview

The extension can automatically persist **user** and **agent** messages to the
Memory Service without any boilerplate in your endpoints.

Key pieces (package `io.github.chirino.memory.history`):

- `@RecordConversation` – interceptor binding for methods you want recorded
- `@ConversationId` – parameter annotation for the conversation id
- `@UserMessage` – parameter annotation for the user’s input text
- `ConversationStore` – implementation backed by `ConversationsApiBuilder`
- `ConversationInterceptor` – interceptor that wires it all together

The interceptor is transport‑agnostic and works with both synchronous
responses and streaming via Mutiny `Multi<String>`.

## How it works

When a method annotated with `@RecordConversation` is invoked:

1. `ConversationInterceptor` scans parameters for:
   - `@ConversationId String conversationId`
   - `@UserMessage String userMessage`
2. It persists the user message via `ConversationStore.appendUserMessage`.
3. It calls the original method (`InvocationContext.proceed()`).
4. Depending on the return type:
   - **Non‑streaming**: the result is converted to `String` and stored via
     `ConversationStore.appendAgentMessage`, then `markCompleted` is called.
   - **Streaming (`Multi<String>`)**: the result is wrapped in
     `ConversationStreamAdapter.wrap(conversationId, multi, store)` which:
     - forwards tokens to the caller,
     - (optionally) records partial tokens via `appendPartialAgentMessage`,
     - on completion, records the full agent message and calls `markCompleted`.

If no `ConversationStore` bean is available, the interceptor becomes a no‑op
and simply calls `ctx.proceed()`.

### Response resumption (optional)

Streaming responses can be cached in Redis or Infinispan so WebSocket clients
can resume a token stream after reconnecting:

- Enable with `memory-service.response-resumer=redis`. By default the
  extension uses the default Redis client; pick a different one via
  `memory-service.response-resumer.redis.client=<client-name>`.
- Enable with `memory-service.response-resumer=infinispan` and configure
  `quarkus.infinispan-client.server-list=localhost:11222`.
- Tokens are written to a Redis stream key `conversation:response:{id}` with
  IDs derived from the cumulative UTF-8 byte offset of the response payload.
- The sample WebSocket endpoint now exposes
  `/customer-support-agent/{conversationId}/ws/{resumePosition}`. The
  `resumePosition` path param defaults to `0` and determines where playback
  starts in the cached stream.
- The sample frontend keeps the last resume position in local storage and
  reconnects with it so long-running responses can continue after a page
  reload or network hiccup.

## ConversationStore behavior

The extension ships with an `@ApplicationScoped` default implementation:

- Class: `io.github.chirino.memory.history.api.ConversationStore`
- Depends on the generated REST client:
  - `io.github.chirino.memory.history.runtime.ConversationsApiBuilder` (builds clients per call)

Behavior:

- `appendUserMessage(conversationId, content)`
  - Builds a `CreateUserMessageRequest` with the given `content`
  - Calls `conversationsApi.appendConversationMessage(conversationId, request)`
- `appendAgentMessage(conversationId, content)`
  - Currently uses the **same** endpoint (`CreateUserMessageRequest` +
    `appendConversationMessage`) so that agent outputs are recorded in the
    conversation history. The Memory Service backend distinguishes “user vs
    agent” based on authentication context.

`appendPartialAgentMessage` and `markCompleted` are no‑ops for now. You can
override this by providing your own `ConversationStore` implementation.

## Dev Services for memory-service

The extension can automatically start a **Memory Service** container in
development and tests using Quarkus Dev Services.

Behavior (from `DevServicesMemoryServiceProcessor`):

- Starts a `memory-service-service:latest` container on a random port.
- Reuses the dev PostgreSQL and Keycloak containers started by your app.
- Exposes a random API key to the container via `MEMORY_SERVICE_API_KEYS`.
- Publishes configuration back to your app:
  - `memory-service-client.url` – base URL of the dev Memory Service
  - `memory-service-client.api-key` – generated API key (if you didn’t set one)

Dev Services are activated when:

- Docker is available, **and**
- `memory-service-client.url` is **not** set.

To point your app at an existing Memory Service instead of starting Dev
Services, set for example:

```properties
memory-service-client.url=https://your-memory-service.example.com
memory-service-client.api-key=YOUR_API_KEY
```

To disable Dev Services globally, use standard Quarkus Dev Services config
such as:

```properties
quarkus.devservices.enabled=false
```

## Configuring the REST client

The `ConversationStore` relies on the existing `memory-service-client`
Quarkus REST client. In most setups this is already configured as part of the
project’s parent POM and `application.properties`.

At a minimum, ensure you have:

- A base URL for the Memory Service (set by dev services or env vars)
- An API key if required by your deployment

You typically configure two things:

1. A **logical client URL** for the generated client:

```properties
memory-service-client.url=${MEMORY_SERVICE_URL}
memory-service-client.api-key=${MEMORY_SERVICE_API_KEY}
```

2. The underlying **Quarkus REST client** for the specific interface (if you
want to override the default):

```properties
memory-service-client.url=${MEMORY_SERVICE_URL}
```

In dev and tests, the `DevServicesMemoryServiceProcessor` starts a
`memory-service-service:latest` container and wires the URLs and API keys
automatically, so you typically don’t need extra config.

## Using @RecordConversation in your code

To enable recording for a service method, annotate it with `@RecordConversation`
and mark parameters appropriately:

```java
import io.github.chirino.memory.history.annotations.RecordConversation;
import io.github.chirino.memory.history.annotations.ConversationId;
import io.github.chirino.memory.history.annotations.UserMessage;
import io.smallrye.mutiny.Multi;

import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class ChatService {

    @RecordConversation
    public Multi<String> chat(
            @ConversationId String conversationId,
            @UserMessage String message) {
        // Call your agent here (LangChain4j, OpenAI, etc)
        // and return a Multi<String> stream of tokens.
    }
}
```

Example WebSocket endpoint that uses the `ChatService`:

```java
@ServerEndpoint("/chat/{conversationId}")
public class ChatSocket {

    @Inject
    ChatService service;

    @OnMessage
    void onMessage(
            String message,
            @PathParam("conversationId") String conversationId,
            Session session) {
        service.chat(conversationId, message)
                .subscribe().with(session.getAsyncRemote()::sendText);
    }
}
```

Every call to `chat(...)` will now:

- Append the user message to the conversation.
- Append the streamed agent message to the same conversation.
- Work for both synchronous and streaming responses.

## Customizing ConversationStore

If you want to change how messages are persisted (e.g. add metadata, change
visibility, or store partial tokens), provide your own CDI bean:

```java
@ApplicationScoped
public class CustomConversationStore extends ConversationStore {
    // override appendUserMessage / appendAgentMessage / appendPartialAgentMessage / markCompleted
}
```

Because the interceptor injects `Instance<ConversationStore>`, your custom
implementation will be picked up automatically and used instead of the
default, as long as it is a normal CDI bean and there is no ambiguity.

## Notes

- The interceptor fails fast if either `@ConversationId` or `@UserMessage`
  is missing from an intercepted method, to avoid silently losing history.
- The implementation is intentionally decoupled from LangChain4j types; it
  deals only in `String` content so you can plug in any agent stack.
