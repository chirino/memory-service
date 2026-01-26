---
layout: ../../layouts/DocsLayout.astro
title: Configuration
description: Configure Memory Service databases, vector stores, and authentication using environment variables.
---

Memory Service is configured entirely through environment variables. This approach works consistently across all deployment methods—Docker, Kubernetes, or bare metal.

> **Note:** Environment variables follow Quarkus conventions. Property names like `memory-service.datastore.type` become `MEMORY_SERVICE_DATASTORE_TYPE` as environment variables (dots and hyphens become underscores, all uppercase).

## Memory Service Configuration

These are the core Memory Service configuration options:

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.datastore.type` | `postgres`, `mongo`, `mongodb` | `postgres` | Database backend for storing conversations |
| `memory-service.cache.type` | `none`, `redis`, `infinispan` | `none` | Cache backend for distributed caching (used by response resumer and future cache features) |
| `memory-service.vector.type` | `none`, `pgvector`, `postgres`, `mongo`, `mongodb` | `none` | Vector store for semantic search |

## Database Configuration

Memory Service supports multiple database backends for storing conversation data.

### PostgreSQL (Recommended)

```bash
# Select PostgreSQL as the datastore
MEMORY_SERVICE_DATASTORE_TYPE=postgres

# PostgreSQL connection
QUARKUS_DATASOURCE_DB_KIND=postgresql
QUARKUS_DATASOURCE_JDBC_URL=jdbc:postgresql://localhost:5432/memoryservice
QUARKUS_DATASOURCE_USERNAME=postgres
QUARKUS_DATASOURCE_PASSWORD=postgres
```

### MongoDB

```bash
# Select MongoDB as the datastore
MEMORY_SERVICE_DATASTORE_TYPE=mongo

# MongoDB connection
QUARKUS_MONGODB_CONNECTION_STRING=mongodb://localhost:27017
QUARKUS_MONGODB_DATABASE=memoryservice
```

## Cache Configuration

Memory Service uses a unified cache configuration for all cache-dependent features, including the response resumer. Configure the cache backend once, and all features will use it automatically.

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.cache.type` | `none`, `redis`, `infinispan` | `none` | Cache backend for distributed caching |
| `memory-service.cache.redis.client` | client name | default | Optional: specify a named Redis client |
| `memory-service.cache.infinispan.startup-timeout` | duration | `PT30S` | Startup timeout for Infinispan connection |

### Response Resumer Settings

The Response Resumer enables clients to reconnect to in-progress streaming responses after a network interruption. It automatically uses the configured cache backend.

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.response-resumer.enabled` | `true`, `false` | auto-detect | Enable/disable response resumer (auto-enabled when cache.type is redis or infinispan) |
| `memory-service.response-resumer.temp-dir` | path | system temp dir | Directory for temporary response files |
| `memory-service.response-resumer.temp-file-retention` | duration | `PT30M` | How long to retain temp files |
| `memory-service.grpc-advertised-address` | `host:port` | auto-detected | Address clients use to reconnect (for multi-instance deployments) |

### Redis Backend

```bash
# Enable Redis cache (response resumer will automatically use it)
MEMORY_SERVICE_CACHE_TYPE=redis

# Redis connection
QUARKUS_REDIS_HOSTS=redis://localhost:6379

# Optional: specify a named Redis client
MEMORY_SERVICE_CACHE_REDIS_CLIENT=my-redis-client

# Optional: disable response resumer even with cache enabled
MEMORY_SERVICE_RESPONSE_RESUMER_ENABLED=false
```

### Infinispan Backend

```bash
# Enable Infinispan cache (response resumer will automatically use it)
MEMORY_SERVICE_CACHE_TYPE=infinispan

# Infinispan connection
QUARKUS_INFINISPAN_CLIENT_HOSTS=localhost:11222
QUARKUS_INFINISPAN_CLIENT_USERNAME=admin
QUARKUS_INFINISPAN_CLIENT_PASSWORD=password

# Optional: startup timeout for Infinispan connection
MEMORY_SERVICE_CACHE_INFINISPAN_STARTUP_TIMEOUT=PT30S

# Optional: disable response resumer even with cache enabled
MEMORY_SERVICE_RESPONSE_RESUMER_ENABLED=false
```

## Vector Store Configuration

For semantic search capabilities, configure a vector store and embedding settings.

### Embedding Configuration

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory.embedding.type` | `none`, `hash` | `hash` | Embedding algorithm (`hash` is a simple built-in implementation) |
| `memory.embedding.dimension` | integer | `256` | Vector dimension size |

### pgvector (PostgreSQL)

When using PostgreSQL with the pgvector extension:

```bash
# Enable pgvector for semantic search
MEMORY_SERVICE_VECTOR_TYPE=pgvector

# Embedding configuration
MEMORY_EMBEDDING_TYPE=hash
MEMORY_EMBEDDING_DIMENSION=256
```

### MongoDB Atlas Vector Search

```bash
# Enable MongoDB vector search
MEMORY_SERVICE_VECTOR_TYPE=mongodb

# Embedding configuration
MEMORY_EMBEDDING_TYPE=hash
MEMORY_EMBEDDING_DIMENSION=256
```

## API Key Authentication

Memory Service supports API key authentication for trusted agents. Configure API keys by client ID:

```bash
# Format: MEMORY_SERVICE_API_KEYS_<CLIENT_ID>=key1,key2,...
MEMORY_SERVICE_API_KEYS_AGENT_A=agent-a-key-1,agent-a-key-2
MEMORY_SERVICE_API_KEYS_AGENT_B=agent-b-key-1
```

Clients include the API key in requests via the `X-API-Key` header.

## OIDC Authentication

Memory Service supports OIDC authentication via Keycloak or any compliant provider.

```bash
# OIDC configuration
QUARKUS_OIDC_AUTH_SERVER_URL=http://localhost:8180/realms/memory-service
QUARKUS_OIDC_CLIENT_ID=memory-service
QUARKUS_OIDC_CREDENTIALS_SECRET=your-client-secret
```

## Server Configuration

```bash
# HTTP port (default: 8080)
QUARKUS_HTTP_PORT=8080

# gRPC port (uses HTTP port when use-separate-server=false)
QUARKUS_GRPC_SERVER_USE_SEPARATE_SERVER=false

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
      # Datastore selection
      MEMORY_SERVICE_DATASTORE_TYPE: postgres

      # PostgreSQL connection
      QUARKUS_DATASOURCE_DB_KIND: postgresql
      QUARKUS_DATASOURCE_JDBC_URL: jdbc:postgresql://postgres:5432/memoryservice
      QUARKUS_DATASOURCE_USERNAME: postgres
      QUARKUS_DATASOURCE_PASSWORD: postgres

      # Cache with Redis (response resumer automatically enabled)
      MEMORY_SERVICE_CACHE_TYPE: redis
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
