---
layout: ../../layouts/DocsLayout.astro
title: Configuration
description: Configure Memory Service databases, vector stores, and authentication using environment variables.
---

Memory Service is configured entirely through environment variables. This approach works consistently across all deployment methods—Docker, Kubernetes, or bare metal.

## Database Configuration

Memory Service supports multiple database backends for storing conversation data.

### PostgreSQL (Recommended)

```bash
# PostgreSQL connection
QUARKUS_DATASOURCE_DB_KIND=postgresql
QUARKUS_DATASOURCE_JDBC_URL=jdbc:postgresql://localhost:5432/memoryservice
QUARKUS_DATASOURCE_USERNAME=postgres
QUARKUS_DATASOURCE_PASSWORD=postgres

# Enable automatic schema creation
QUARKUS_HIBERNATE_ORM_DATABASE_GENERATION=update
```

### MongoDB

```bash
# MongoDB connection
QUARKUS_DATASOURCE_DB_KIND=mongodb
QUARKUS_MONGODB_CONNECTION_STRING=mongodb://localhost:27017
QUARKUS_MONGODB_DATABASE=memoryservice
```

## Cache Configuration

Memory Service uses caching to improve performance.

### Redis

```bash
# Redis connection
QUARKUS_REDIS_HOSTS=redis://localhost:6379
```

### Infinispan

```bash
# Infinispan connection
QUARKUS_INFINISPAN_CLIENT_HOSTS=localhost:11222
QUARKUS_INFINISPAN_CLIENT_USERNAME=admin
QUARKUS_INFINISPAN_CLIENT_PASSWORD=password
```

## Vector Store Configuration

For semantic search capabilities, configure a vector store.

### pgvector (PostgreSQL)

When using PostgreSQL with the pgvector extension:

```bash
# Enable pgvector for semantic search
MEMORY_SERVICE_VECTOR_STORE_TYPE=pgvector
MEMORY_SERVICE_VECTOR_STORE_DIMENSION=1536

# Embedding model configuration
MEMORY_SERVICE_EMBEDDING_MODEL=text-embedding-ada-002
OPENAI_API_KEY=your-api-key
```

### MongoDB Atlas Vector Search

```bash
# MongoDB vector search
MEMORY_SERVICE_VECTOR_STORE_TYPE=mongodb
MEMORY_SERVICE_VECTOR_STORE_MONGODB_INDEX=vector_index
MEMORY_SERVICE_VECTOR_STORE_DIMENSION=1536
```

## Authentication Configuration

Memory Service supports OIDC authentication via Keycloak or any compliant provider.

```bash
# OIDC configuration
QUARKUS_OIDC_AUTH_SERVER_URL=http://localhost:8180/realms/memory-service
QUARKUS_OIDC_CLIENT_ID=memory-service
QUARKUS_OIDC_CREDENTIALS_SECRET=your-client-secret

# Enable authentication
MEMORY_SERVICE_AUTH_ENABLED=true
```

## Server Configuration

```bash
# HTTP port (default: 8080)
QUARKUS_HTTP_PORT=8080

# gRPC port (default: 9000)
QUARKUS_GRPC_SERVER_PORT=9000

# Enable CORS
QUARKUS_HTTP_CORS=true
QUARKUS_HTTP_CORS_ORIGINS=http://localhost:3000
```

## Production Recommendations

For production deployments, consider the following environment variables:

### Connection Pooling

```bash
# Database connection pool
QUARKUS_DATASOURCE_JDBC_MAX_SIZE=20
QUARKUS_DATASOURCE_JDBC_MIN_SIZE=5
```

### Health Checks and Metrics

```bash
# Enable health endpoints
QUARKUS_HEALTH_EXTENSIONS_ENABLED=true

# Enable Prometheus metrics
QUARKUS_MICROMETER_EXPORT_PROMETHEUS_ENABLED=true
```

### Logging

```bash
# Set log level
QUARKUS_LOG_LEVEL=INFO
QUARKUS_LOG_CATEGORY__IO_GITHUB_CHIRINO__LEVEL=DEBUG
```

## Example: Docker Compose

```yaml
services:
  memory-service:
    image: ghcr.io/chirino/memory-service:latest
    environment:
      # Database
      QUARKUS_DATASOURCE_DB_KIND: postgresql
      QUARKUS_DATASOURCE_JDBC_URL: jdbc:postgresql://postgres:5432/memoryservice
      QUARKUS_DATASOURCE_USERNAME: postgres
      QUARKUS_DATASOURCE_PASSWORD: postgres
      
      # Cache
      QUARKUS_REDIS_HOSTS: redis://redis:6379
      
      # Authentication
      QUARKUS_OIDC_AUTH_SERVER_URL: http://keycloak:8180/realms/memory-service
      QUARKUS_OIDC_CLIENT_ID: memory-service
      QUARKUS_OIDC_CREDENTIALS_SECRET: ${OIDC_SECRET}
    depends_on:
      - postgres
      - redis
```

## Next Steps

- Learn about [Core Concepts](/docs/concepts/conversations/)
- Explore [Deployment Options](/docs/deployment/docker/)
