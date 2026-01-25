---
layout: ../../layouts/DocsLayout.astro
title: FAQ
description: Frequently asked questions about Memory Service.
---

Find answers to commonly asked questions about Memory Service.

## General

### What is Memory Service?

Memory Service is a backend service that provides persistent memory for AI agents. It stores conversation history, enables semantic search across conversations, and supports advanced features like conversation forking.

### Is Memory Service production-ready?

Memory Service is currently a proof of concept under active development. While it's functional for development and testing, we recommend evaluating it thoroughly before using in production environments.

### What databases are supported?

Memory Service supports:
- **PostgreSQL** (recommended) - with pgvector for semantic search
- **MongoDB** - with Atlas Vector Search support

### What vector stores are supported?

For semantic search capabilities:
- **pgvector** - PostgreSQL extension
- **MongoDB Atlas Vector Search**

## Installation & Setup

### How do I install Memory Service?

Add the Quarkus extension to your project:

```xml
<dependency>
  <groupId>io.github.chirino.memory-service</groupId>
  <artifactId>memory-service-extension</artifactId>
  <version>1.0.0</version>
</dependency>
```

See the [Getting Started](/docs/getting-started/) guide for detailed instructions.

### Do I need to run a separate service?

In development mode, the Quarkus Dev Services automatically start Memory Service in a Docker container. For production, you'll deploy Memory Service as a separate service.

### What Java version is required?

Memory Service requires Java 21 or later.

## Features

### What is conversation forking?

Conversation forking allows you to create a new conversation branch from any point in an existing conversation. This is useful for:
- Exploring alternative conversation paths
- A/B testing agent responses
- Debugging specific conversation states

### How does semantic search work?

Memory Service uses vector embeddings to enable semantic search across all stored conversations. When you store a message, it's automatically embedded using your configured embedding model. You can then search for semantically similar content across all conversations.

### Can multiple agents share conversations?

Yes! Memory Service supports access control with user-based ownership and sharing. You can configure fine-grained permissions to control which agents can read or write to specific conversations.

## Troubleshooting

### Dev Services aren't starting

Make sure Docker is running and accessible. Check the logs for specific error messages:

```bash
./mvnw quarkus:dev -Dquarkus.log.category."io.quarkus.devservices".level=DEBUG
```

### Connection timeouts

If you're experiencing connection timeouts, try increasing the timeout values:

```properties
memory-service.connect-timeout=60s
memory-service.read-timeout=120s
```

### Vector search returns no results

Ensure that:
1. Your vector store is properly configured
2. Messages have been embedded (check logs for embedding errors)
3. The search query is meaningful (single words may not match well)

## Contributing

### How can I contribute?

We welcome contributions! Check out the [GitHub repository](https://github.com/chirino/memory-service) for:
- Open issues
- Contribution guidelines
- Development setup instructions

### Where do I report bugs?

Please report bugs on [GitHub Issues](https://github.com/chirino/memory-service/issues).

### How do I request a feature?

Feature requests are welcome! Open a [GitHub Discussion](https://github.com/chirino/memory-service/discussions) to discuss your idea before creating an issue.
