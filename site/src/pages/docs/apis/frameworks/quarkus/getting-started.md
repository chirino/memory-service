---
layout: ../../../../layouts/DocsLayout.astro
title: Getting Started with Quarkus
description: Learn how to set up Memory Service in your Quarkus project.
---

This guide will walk you through setting up Memory Service in your Quarkus project.

## Prerequisites

Before you begin, make sure you have:

- **Java 21+** installed
- **Maven** (or use the included `./mvnw` wrapper)
- **Docker** (for Dev Services and production deployment)
- An **OpenAI API key** (or compatible endpoint)

## Installation

### 1. Add the Extension Dependency

Add the Memory Service extension to your Quarkus project's `pom.xml`:

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-extension</artifactId>
  <version>1.0.0</version>
</dependency>
```

### 2. Configure the Service

In your `application.properties`:

```properties
# Point to your memory-service instance
memory-service.url=http://localhost:8080

# Or let Dev Services handle it automatically (default)
# Dev Services will start the service in Docker if not configured
```

### 3. Use the ChatMemory Integration

Inject the `ChatMemoryProvider` into your agent:

```java
@Inject
ChatMemoryProvider memoryProvider;

public void chat(String conversationId, String userMessage) {
    ChatMemory memory = memoryProvider.get(conversationId);

    // Add messages to memory
    memory.add(UserMessage.from(userMessage));

    // Memory is automatically persisted
}
```

## Running the Example Agent

The repository includes a complete example agent you can run locally.

### 1. Clone and Build

```bash
git clone https://github.com/chirino/memory-service.git
cd memory-service
./mvnw install -DskipTests
```

### 2. Build the Docker Image

```bash
docker build -t ghcr.io/chirino/memory-service:latest .
```

### 3. Run the Agent

```bash
export OPENAI_API_KEY=your-api-key-here
./mvnw -pl examples/agent-quarkus quarkus:dev
```

This will:
- Start the agent application on `http://localhost:8080`
- Automatically start Memory Service in a Docker container (via Dev Services)
- Start PostgreSQL, Keycloak, and other dependencies
- Serve the React frontend at the root URL

### 4. Access the Application

Open `http://localhost:8080` in your browser, sign in with `bob/bob`, and start chatting!

## Project Structure

The Memory Service project is organized as follows:

| Module | Description |
|--------|-------------|
| `memory-service` | Core HTTP and gRPC service implementation |
| `memory-service-contracts` | OpenAPI + proto sources of truth |
| `quarkus/memory-service-extension` | Quarkus extension with Dev Services |
| `examples/agent-quarkus` | Example LangChain4j-based agent |
| `examples/agent-webui` | React/Vite SPA frontend |

## Next Steps

- Learn about [Configuration](/docs/configuration/) options
- Understand [Core Concepts](/docs/concepts/conversations/)
- Explore the [REST API](/docs/integrations/rest-api/)