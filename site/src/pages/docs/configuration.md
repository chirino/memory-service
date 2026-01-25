---
layout: ../../layouts/DocsLayout.astro
title: Configuration
description: Configure Memory Service databases, vector stores, and client settings.
---

Memory Service supports extensive configuration options for databases, vector stores, and service behavior.

## Client Configuration

When using the Quarkus extension, configure the client in your `application.properties`:

```properties
# Memory Service URL (required unless using Dev Services)
memory-service.url=http://localhost:8080

# Connection timeout (default: 30s)
memory-service.connect-timeout=30s

# Read timeout (default: 60s)
memory-service.read-timeout=60s

# Enable TLS verification (default: true)
memory-service.tls.verify=true
```

## Database Configuration

Memory Service supports multiple database backends for storing conversation data.

### PostgreSQL (Recommended)

```properties
# PostgreSQL connection
quarkus.datasource.db-kind=postgresql
quarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/memoryservice
quarkus.datasource.username=postgres
quarkus.datasource.password=postgres

# Enable automatic schema creation
quarkus.hibernate-orm.database.generation=update
```

### MongoDB

```properties
# MongoDB connection
quarkus.mongodb.connection-string=mongodb://localhost:27017
quarkus.mongodb.database=memoryservice
```

## Vector Store Configuration

For semantic search capabilities, configure a vector store.

### pgvector (PostgreSQL)

```properties
# Enable pgvector extension
memory-service.vector-store.type=pgvector
memory-service.vector-store.dimension=1536

# Embedding model
memory-service.embedding.model=text-embedding-ada-002
memory-service.embedding.api-key=${OPENAI_API_KEY}
```

### MongoDB Atlas Vector Search

```properties
# MongoDB vector search
memory-service.vector-store.type=mongodb
memory-service.vector-store.mongodb.index=vector_index
memory-service.vector-store.dimension=1536
```

## Authentication Configuration

Memory Service supports OIDC authentication via Keycloak or any compliant provider.

```properties
# OIDC configuration
quarkus.oidc.auth-server-url=http://localhost:8180/realms/memory-service
quarkus.oidc.client-id=memory-service
quarkus.oidc.credentials.secret=${OIDC_SECRET}

# Enable authentication
memory-service.auth.enabled=true
```

## Dev Services

The Quarkus extension includes Dev Services for local development. These automatically start required services in Docker.

```properties
# Enable/disable Dev Services (default: true in dev mode)
quarkus.memory-service.devservices.enabled=true

# Custom Docker image
quarkus.memory-service.devservices.image-name=ghcr.io/chirino/memory-service:latest

# Port configuration
quarkus.memory-service.devservices.port=8081
```

## Environment Variables

All configuration properties can be set via environment variables:

| Property | Environment Variable |
|----------|---------------------|
| `memory-service.url` | `MEMORY_SERVICE_URL` |
| `memory-service.connect-timeout` | `MEMORY_SERVICE_CONNECT_TIMEOUT` |
| `quarkus.datasource.jdbc.url` | `QUARKUS_DATASOURCE_JDBC_URL` |

## Production Recommendations

For production deployments:

1. **Use connection pooling** - Configure appropriate pool sizes for your database
2. **Enable TLS** - Always use encrypted connections
3. **Set resource limits** - Configure memory and CPU limits for containers
4. **Use external secrets** - Store credentials in a secrets manager
5. **Enable monitoring** - Use the built-in health checks and metrics

```properties
# Production configuration example
quarkus.datasource.jdbc.max-size=20
quarkus.datasource.jdbc.min-size=5

# Health checks
quarkus.health.extensions.enabled=true

# Metrics
quarkus.micrometer.export.prometheus.enabled=true
```

## Next Steps

- Learn about [Core Concepts](/docs/concepts/conversations/)
- Explore [Deployment Options](/docs/deployment/docker/)
