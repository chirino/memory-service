# Memory Service

A persistent memory service for AI agents that stores and manages conversation history, enabling agents to maintain context across sessions, replay conversations, fork conversations at any point, and perform semantic search across all conversations.

## Features

- **Persistent conversation storage** - All messages are stored with full context and metadata
- **Conversation replay** - Replay any conversation in the exact order messages occurred
- **Conversation forking** - Fork a conversation at any message to explore alternative paths
- **Semantic search** - Search across all conversations using vector similarity
- **Access control** - User-based ownership and sharing with fine-grained permissions
- **Multi-database support** - Works with PostgreSQL and MongoDB

## Project Status

This is a proof of concept (POC) currently under development.

## Documentation

Visit the [Memory Service Documentation](https://chirino.github.io/memory-service/) for complete guides:

- **[Getting Started](https://chirino.github.io/memory-service/docs/getting-started/)** - Deploy Memory Service using Docker Compose
- **[Quarkus Integration](https://chirino.github.io/memory-service/docs/quarkus/getting-started/)** - Integrate with Quarkus LangChain4j agents
- **[Configuration](https://chirino.github.io/memory-service/docs/configuration/)** - Service configuration options
- **[Core Concepts](https://chirino.github.io/memory-service/docs/concepts/conversations/)** - Understanding conversations, messages, and forking

## License

Apache 2.0
