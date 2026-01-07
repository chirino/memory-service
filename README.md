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

### `memory-service`
The core HTTP API backend service that provides REST endpoints for:

- **Conversation Management**: List, create, get, and delete conversations
- **Message Operations**: List and append messages to conversations
- **Conversation Forking**: Fork conversations at any message and list conversation forks
- **Access Control & Sharing**: Manage conversation memberships, share conversations, transfer ownership
- **Summarization/Search**: For semantic search across all conversations the user has access to
- **Health & Monitoring**: Health check endpoint

### `memory-service-client`
OpenAPI-based Java client library that contains:
- OpenAPI specification (`openapi.yml`) - the source of truth for the API contract
- Generated Java REST client for integrating with the memory service
- Shared client filters and helpers

### `memory-service-extension`
Quarkus extension that simplifies integration by providing:
- **Dev Services** - Automatically starts the memory-service in Docker during development
- **LangChain4j Integration** - `MemoryServiceChatMemoryProvider` and `MemoryServiceChatMemory` for seamless LangChain4j integration
- **Conversation Recording** - `@ConversationAware` interceptor for automatic message persistence
- Automatic client configuration and URL wiring

### `agent`
Example agent application demonstrating how to use the memory service:
- LangChain4j-based AI agent (`CustomerSupportAgent`)
- React + TypeScript frontend SPA for chatting with the agent
- WebSocket and SSE streaming support
- OIDC authentication integration

### `quarkus-data-encryption`
Internal encryption extension for encrypting sensitive data at rest, with providers for plain (no-op), DEK, and Vault.

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
   docker build -t memory-service-service:latest .
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

### 3. Use LangChain4j Integration

The extension provides a `ChatMemoryProvider` that automatically integrates with LangChain4j:

```java
@ApplicationScoped
@RegisterAiService()
public interface MyAgent {
    Multi<String> chat(@MemoryId String memoryId, String userMessage);
}
```

The `@MemoryId` parameter automatically uses the conversation ID to retrieve and store messages via the memory service. No additional code needed!

### 4. Use Conversation Recording (Alternative Approach)

To provide conversation history to UI agents, use a `@ConversationAware` interceptor:

```java
@Path("/chat")
public class ChatResource {

    @Inject
    MyAgent agent;

    @POST
    @Path("/{conversationId}")
    @ConversationAware
    public Multi<String> chat(
        @PathParam("conversationId") @ConversationId String conversationId,
        @QueryParam("message") @UserMessage String userMessage
    ) {
        return agent.chat(conversationId, userMessage);
    }
}
```

The interceptor automatically:
- Stores the user message before calling your method
- Stores the agent response after your method completes

### 5. Direct API Access

You can also use the generated REST client directly:

```java
@Inject
@RestClient
ConversationsApi conversationsApi;

public void createConversation(String userId) {
    CreateConversationRequest request = new CreateConversationRequest();
    request.setUserId(userId);
    Conversation conversation = conversationsApi.createConversation(request);
}
```

### 6. Frontend Integration

The memory-service provides user-facing APIs that your frontend can use:

- `GET /v1/user/conversations` - List user's conversations
- `GET /v1/user/conversations/{id}/messages` - Get conversation messages
- `POST /v1/user/conversations/{id}/fork` - Fork a conversation
- `GET /v1/user/search` - Semantic search across conversations

See the example agent's frontend (`agent/src/main/webui/`) for a complete React implementation.

## Additional Resources

- See `memory-service/README.md` for memory-service specific documentation
- See `memory-service-extension/README.md` for extension specific documentation
- OpenAPI spec: `memory-service-client/src/main/openapi/openapi.yml`
