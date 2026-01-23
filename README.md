# Memory Service

A persistent memory service for AI agents that stores and manages conversation history, enabling agents to maintain context across sessions, replay conversations, fork conversations at any point, and perform semantic search across all conversations.

## What is Memory Service?

Memory Service is a backend service designed to solve the memory problem for AI agents. It stores all messages exchanged between agents, users, and LLMs in a structured, queryable format. The service provides:

- **Persistent conversation storage** - All messages are stored with full context and metadata
- **Conversation replay** - Replay any conversation in the exact order messages occurred
- **Conversation forking** - Fork a conversation at any message to explore alternative paths
- **Semantic search** - Search across all conversations using vector similarity
- **Access control** - User-based ownership and sharing with fine-grained permissions
- **Multi-database support** - Works with PostgreSQL, MongoDB, and Cassandra
- **Vector store integration** - Supports pgvector, Milvus, and MongoDB for semantic search

## Project Status

This is a proof of concept (POC) currently under development.

## Project Modules

This is a multi-module Maven project built on Quarkus (Java 21). The modules are:

- `memory-service`: Core HTTP and gRPC service implementation.
- `memory-service-contracts`: OpenAPI + proto sources of truth (no generated code).
- `quarkus/memory-service-rest-quarkus`: OpenAPI-based Java REST client and helpers (config keys remain `memory-service-client.*`).
- `quarkus/memory-service-proto-quarkus`: Quarkus gRPC stubs generated from the shared proto.
- `quarkus/memory-service-extension`: Quarkus extension providing dev services, client wiring, and LangChain4j helpers.
- `quarkus/quarkus-data-encryption/*`: Encryption extension family (runtime/deployment/DEK/Vault).
- `examples/agent-quarkus`: LangChain4j-based agent example that serves the SPA.
- `examples/agent-webui`: React/Vite SPA shared by agents.

## Building and Running the Example Agent

### Prerequisites
- Java 21+
- Maven (or use the included `./mvnw` wrapper)
- Docker (for Dev Services and production deployment)
- Node.js and npm (for the frontend)

### Quick Start

1. **Build the project:**
   ```bash
   ./mvnw install -DskipTests
   ```

2. **Build the Docker image:**
   ```bash
   docker build -t ghcr.io/chirino/memory-service:latest .
   ```

3. **Run the example agent:**

   ```bash
   export OPENAI_API_KEY=...
   ./mvnw -pl agent clean quarkus:dev
   ```

You can also `export OPENAI_BASE_URL=...` to run against a different OpenAI-compatible endpoint.

This will:
- Start the agent application on `http://localhost:8080`
- Automatically start the memory-service in a Docker container (via Dev Services)
- Start PostgreSQL, Keycloak, and other dependencies automatically
- Serve the React frontend at the root URL

4. **Access the application:**
   - Open `http://localhost:8080` in your browser
   - Sign in with user/password: bob/bob
   - Start chatting with the agent!

## Using Memory Service in Your Own Agents

### 1. Add the Extension Dependency

Add the `memory-service-extension` to your Quarkus project:

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-extension</artifactId>
  <version>999-SNAPSHOT</version>
</dependency>
```

### 2. Configure the Memory Service URL

In `application.properties`:

```properties
# Point to your memory-service instance
memory-service.url=http://localhost:8080

# Or let Dev Services handle it automatically (default)
# (Dev Services will start the service in Docker if not configured)
```

### 3. Use LangChain4j ChatMemory Integration

The extension provides a `ChatMemoryProvider` that automatically integrates with LangChain4j:

```java
@ApplicationScoped
@RegisterAiService()
public interface MyAgent {
    Multi<String> chat(@MemoryId String memoryId, String userMessage);
}
```

The `@MemoryId` parameter automatically uses the conversation ID to retrieve and store messages via the memory service.

### 4. Multi-Agent Memory Support

The memory service supports multiple agents participating in the same conversation. Each
agent maintains its own `memory` channel stream, and memory reads/writes are scoped to the
calling agent’s client id, which is derived from the presented API key. This prevents one
agent from seeing or overwriting another agent’s memory while still sharing the same
conversation history.

Configure per-agent API keys using the `memory-service.api-keys.<client-id>` mapping:

```properties
memory-service.api-keys.agent-a=agent-a-key-1,agent-a-key-2
memory-service.api-keys.agent-b=agent-b-key-1
```

Agents authenticate by sending the API key in the `X-API-Key` HTTP header.

### 5. Conversation History Recording

To provide conversation history to UI agents, use a `HistoryRecordingAgent` that wraps your agent with the `@RecordConversation` interceptor:

```java
@ApplicationScoped
public class HistoryRecordingAgent {

    private final Agent agent;

    @Inject
    public HistoryRecordingAgent(Agent agent) {
        this.agent = agent;
    }

    @RecordConversation
    public Multi<String> chat(
            @ConversationId String conversationId, @UserMessage String userMessage) {
        return agent.chat(conversationId, userMessage);
    }
}
```

The `@RecordConversation` interceptor automatically:
- Stores the user message before calling your method
- Stores the agent response after your method completes

Use this wrapper in your REST endpoints instead of calling the agent directly.


### 6. Direct API Access

Agents can also use the generated REST client directly:

```java
@Inject
ConversationsApiBuilder conversationsApiBuilder;

public void createConversation() {
    CreateConversationRequest request = new CreateConversationRequest();
    request.setTitle("My Conversation");
    Conversation conversation = conversationsApiBuilder.build().createConversation(request);
}
```

**Note:** The API key is automatically configured by dev services. If `memory-service-client.api-key` is not explicitly set, the dev services will generate a random API key and configure it both in the started container (as `MEMORY_SERVICE_API_KEYS_AGENT`) and in your application configuration (as `memory-service-client.api-key`). The `ConversationsApiBuilder` uses that configuration when building clients. (The configuration prefix remains `memory-service-client.*` even though the module is now `memory-service-rest-quarkus`.)

### 7. Frontend Integration

See the example agent's frontend (`examples/agent-webui/`) for a complete React implementation.

The React app calls `MemoryServiceProxyResource` (`examples/agent-quarkus/src/main/java/example/MemoryServiceProxyResource.java`) to view and manage historical conversation state, including listing conversations, retrieving messages, and forking conversations. For sending new messages to the agent and receiving streaming responses, the frontend uses `AgentWebSocket` (`examples/agent-quarkus/src/main/java/example/AgentWebSocket.java`).


### 8. Agent Response Resumption

When streaming agent responses (e.g., via WebSocket), clients may disconnect before receiving the complete response. The memory-service extension supports resuming interrupted responses by buffering streaming tokens and tracking where that buffer is locaed in a cache backend (Redis or Infinispan).

Enable response resumption by setting:

```properties
memory-service.response-resumer=redis
```

Or use Infinispan:

```properties
memory-service.response-resumer=infinispan
quarkus.infinispan-client.server-list=localhost:11222
quarkus.infinispan-client.cache.response-resumer.configuration=<distributed-cache><encoding media-type="application/x-protostream"/></distributed-cache>
```

When enabled, the `@RecordConversation` interceptor automatically buffers streaming responses:

- For streaming methods that return `Multi<String>`, the interceptor wraps the stream using `ConversationStreamAdapter.wrap()`, which:
  - Forwards each token to the original caller
  - Records each token to a per-node temp file and registers the owner in Redis (key: `response:{conversationId}`)
  - Tracks the cumulative byte offset as the resume position
  - On completion, stores the full message and marks the conversation as completed

- The `ResponseResumer` interface provides:
  - `recorder(conversationId)`: Creates a recorder that buffers tokens as they stream
  - `replay(conversationId, resumePosition)`: Replays cached tokens from a specific byte offset
  - `check(conversationIds, bearerToken)`: Checks which conversations have responses currently in progress

The example agent application provides two endpoints to support resumption:

- **`ResumeResource`** (`/v1/conversations/resume-check`): A REST endpoint that accepts a list of conversation IDs and returns which ones have responses in progress. The frontend can use this to detect conversations that were interrupted.

- **`ResumeWebSocket`** (`/customer-support-agent/{conversationId}/ws/{resumePosition}`): A WebSocket endpoint that replays cached tokens from a specific resume position. The frontend stores the last received byte offset in local storage and reconnects with it after a page reload or network interruption.

When `memory-service.response-resumer=none` is set, the extension uses a no-op `ResponseResumer` that doesn't buffer responses, and resumption is unavailable.

Response resumption relies on a per-node advertised address for redirects. Configure `memory-service.grpc-advertised-address=host:port` in clustered deployments. Temp file behavior can be tuned with `memory-service.response-resumer.temp-dir` and `memory-service.response-resumer.temp-file-retention`.

## Additional Resources

- See `memory-service/README.md` for memory-service specific documentation
- See `quarkus/memory-service-extension/README.md` for quarkus extension specific documentation
- OpenAPI spec: `memory-service-contracts/src/main/resources/openapi.yml`
- Protobuf spec: `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`
