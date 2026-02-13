---
layout: ../../layouts/DocsLayout.astro
title: Configuration
description: Configure Memory Service databases, vector stores, and authentication using environment variables.
---

Memory Service is configured entirely through environment variables. This approach works consistently across all deployment methods—Docker, Kubernetes, or bare metal.

> **Note:** Environment variables follow Quarkus conventions. Property names like `memory-service.datastore.type` become `MEMORY_SERVICE_DATASTORE_TYPE` as environment variables (dots and hyphens become underscores, all uppercase).

## Server Configuration

These are the core server configuration options:

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.datastore.type` | `postgres`, `mongo`, `mongodb` | `postgres` | Database backend for storing conversations |
| `memory-service.cache.type` | `none`, `redis`, `infinispan` | `none` | Cache backend for distributed caching (used by response resumer and future cache features) |
| `memory-service.vector.type` | `none`, `pgvector`, `postgres`, `mongo`, `mongodb` | `none` | Vector store for semantic search |
| `memory-service.temp-dir` | path | system temp dir | Directory for temporary files (attachment downloads, response resumer, etc.) |

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

Memory Service uses a unified cache configuration for all cache-dependent features, including the response resumer and memory entries cache. Configure the cache backend once, and all features will use it automatically.

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.cache.type` | `none`, `redis`, `infinispan` | `none` | Cache backend for distributed caching |
| `memory-service.cache.redis.client` | client name | default | Optional: specify a named Redis client |
| `memory-service.cache.infinispan.startup-timeout` | duration | `PT30S` | Startup timeout for Infinispan connection |

### Memory Entries Cache

When a cache backend is configured, Memory Service caches memory entries to reduce database load and improve GET/sync latency. The cache stores the complete list of memory entries at the latest epoch for each conversation/client pair.

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.cache.epoch.ttl` | duration | `PT10M` | TTL for cached memory entries (sliding window - refreshed on access) |

Features of the memory entries cache:

- **Automatic population**: Cache is populated on first read and updated after sync operations
- **Sliding TTL**: TTL is refreshed on every cache access (get or set)
- **In-memory pagination**: Cache stores complete entry list; pagination is applied in-memory
- **Graceful degradation**: Falls back to database queries if cache is unavailable

### Response Resumer Settings

The Response Resumer enables clients to reconnect to in-progress streaming responses after a network interruption. It automatically uses the configured cache backend.

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.response-resumer.enabled` | `true`, `false` | auto-detect | Enable/disable response resumer (auto-enabled when cache.type is redis or infinispan) |
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

## Attachment Storage

Configure file attachment storage, size limits, and lifecycle.

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.attachments.store` | `db`, `s3` | `db` | Storage backend for uploaded files |
| `memory-service.attachments.max-size` | memory size | `10M` | Maximum file size per upload (e.g., `10M`, `512K`, `1G`). The HTTP body size limit is auto-derived as 2x this value. |
| `memory-service.attachments.default-expires-in` | duration | `PT1H` | Default TTL for unlinked attachments |
| `memory-service.attachments.max-expires-in` | duration | `PT24H` | Maximum allowed TTL clients can request |
| `memory-service.attachments.cleanup-interval` | duration | `PT5M` | How often the cleanup job runs to delete expired unlinked attachments |
| `memory-service.attachments.download-url-expires-in` | duration | `PT5M` | Signed download URL expiry |

### S3 Storage

When using S3 as the attachment storage backend:

```bash
# Select S3 storage
MEMORY_SERVICE_ATTACHMENTS_STORE=s3

# S3 bucket configuration
MEMORY_SERVICE_ATTACHMENTS_S3_BUCKET=memory-service-attachments
```

See [Attachments](/docs/concepts/attachments/) for details on how attachments work.

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

## Admin Access Configuration

Memory Service provides `/v1/admin/*` APIs for platform administrators and auditors.
Access is controlled through role assignment, which can be configured via OIDC token roles,
explicit user lists, or API key client IDs. All three mechanisms are checked — if any
grants a role, the caller has that role.

### Roles

| Role | Access | Description |
|------|--------|-------------|
| `admin` | Read + Write | Full administrative access across all users. Implies `auditor` and `indexer`. |
| `auditor` | Read-only | View any user's conversations and search system-wide. Cannot modify data. |
| `indexer` | Index only | Index any conversation's transcript for search. Cannot view or modify other data. |

### Role Assignment

Roles can be assigned through three complementary mechanisms:

#### OIDC Role Mapping

Map OIDC token roles to internal Memory Service roles. This is useful when the OIDC
provider uses different role names (e.g., `administrator` instead of `admin`).

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.roles.admin.oidc.role` | _(none)_ | OIDC role name that maps to the internal `admin` role |
| `memory-service.roles.auditor.oidc.role` | _(none)_ | OIDC role name that maps to the internal `auditor` role |
| `memory-service.roles.indexer.oidc.role` | _(none)_ | OIDC role name that maps to the internal `indexer` role |

```bash
# Map OIDC "administrator" role to internal "admin" role
MEMORY_SERVICE_ROLES_ADMIN_OIDC_ROLE=administrator

# Map OIDC "manager" role to internal "auditor" role
MEMORY_SERVICE_ROLES_AUDITOR_OIDC_ROLE=manager

# Map OIDC "transcript-indexer" role to internal "indexer" role
MEMORY_SERVICE_ROLES_INDEXER_OIDC_ROLE=transcript-indexer
```

#### User-Based Assignment

Assign roles directly to user IDs (matched against the OIDC token principal name):

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.roles.admin.users` | _(empty)_ | Comma-separated list of user IDs with admin access |
| `memory-service.roles.auditor.users` | _(empty)_ | Comma-separated list of user IDs with auditor access |
| `memory-service.roles.indexer.users` | _(empty)_ | Comma-separated list of user IDs with indexer access |

```bash
MEMORY_SERVICE_ROLES_ADMIN_USERS=alice,bob
MEMORY_SERVICE_ROLES_AUDITOR_USERS=charlie,dave
MEMORY_SERVICE_ROLES_INDEXER_USERS=indexer-user
```

#### Client-Based Assignment (API Key)

Assign roles to API key client IDs, allowing agents or services to call admin APIs.
The client ID is resolved from the `X-API-Key` header via the existing API key configuration.

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.roles.admin.clients` | _(empty)_ | Comma-separated list of API key client IDs with admin access |
| `memory-service.roles.auditor.clients` | _(empty)_ | Comma-separated list of API key client IDs with auditor access |
| `memory-service.roles.indexer.clients` | _(empty)_ | Comma-separated list of API key client IDs with indexer access |

```bash
MEMORY_SERVICE_ROLES_ADMIN_CLIENTS=admin-agent
MEMORY_SERVICE_ROLES_AUDITOR_CLIENTS=monitoring-agent,audit-agent
MEMORY_SERVICE_ROLES_INDEXER_CLIENTS=indexer-service,summarizer-agent
```

### Audit Logging

All admin API calls are logged to a dedicated logger (`io.github.chirino.memory.admin.audit`).
Each request can include a `justification` field explaining why the admin action was taken.

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.admin.require-justification` | `true`, `false` | `false` | When `true`, all admin API calls must include a `justification` or receive `400 Bad Request` |

```bash
# Require justification for all admin operations
MEMORY_SERVICE_ADMIN_REQUIRE_JUSTIFICATION=true
```

To route admin audit logs to a separate file or external system, configure the Quarkus
logging category:

```bash
# Set admin audit log level
QUARKUS_LOG_CATEGORY__IO_GITHUB_CHIRINO_MEMORY_ADMIN_AUDIT__LEVEL=INFO
```

## Server Configuration

```bash
# HTTP port (default: 8080)
QUARKUS_HTTP_PORT=8080

# gRPC port (uses HTTP port when use-separate-server=false)
QUARKUS_GRPC_SERVER_USE_SEPARATE_SERVER=false

# Enable CORS
MEMORY_SERVICE_CORS_ENABLED=true
MEMORY_SERVICE_CORS_ORIGINS=http://localhost:3000
```

## Production Recommendations

For production deployments, consider the following environment variables:

### Connection Pooling

```bash
# Database connection pool
QUARKUS_DATASOURCE_JDBC_MAX_SIZE=20
QUARKUS_DATASOURCE_JDBC_MIN_SIZE=5
```

### Health Checks

```bash
# Enable health endpoints
QUARKUS_HEALTH_EXTENSIONS_ENABLED=true
```

### Logging

```bash
# Set log level
QUARKUS_LOG_LEVEL=INFO
QUARKUS_LOG_CATEGORY__IO_GITHUB_CHIRINO__LEVEL=DEBUG
```

## Monitoring

Memory Service exposes Prometheus metrics and provides admin stats endpoints that query Prometheus for aggregated metrics across all service replicas.

### Metrics Endpoint

Memory Service exposes metrics in Prometheus format at `/q/metrics`:

```bash
# Enable Prometheus metrics endpoint (enabled by default)
QUARKUS_MICROMETER_EXPORT_PROMETHEUS_ENABLED=true

# Metrics endpoint path (default: /q/metrics)
QUARKUS_MICROMETER_EXPORT_PROMETHEUS_PATH=/q/metrics
```

### Application Tag

All Memory Service metrics include an `application="memory-service"` tag. This tag helps:

- **Filter metrics** when multiple services are scraped by the same Prometheus
- **Build dashboards** that only show memory-service data
- **Configure alerts** specific to memory-service

Example PromQL queries using the application tag:

```promql
# Request rate for memory-service only
sum(rate(http_server_requests_seconds_count{application="memory-service"}[5m]))

# Store operation latency (P95) for memory-service
histogram_quantile(0.95, sum(rate(memory_store_operation_seconds_bucket{application="memory-service"}[5m])) by (le, operation))
```

### Prometheus Scrape Configuration

Configure Prometheus to scrape metrics from all Memory Service replicas:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'memory-service'
    scrape_interval: 15s
    metrics_path: /q/metrics
    static_configs:
      - targets: ['memory-service:8080']
    # For Kubernetes, use service discovery instead:
    # kubernetes_sd_configs:
    #   - role: pod
    #     selectors:
    #       - role: pod
    #         label: "app=memory-service"
```

To keep only Memory Service metrics (useful when scraping multiple services):

```yaml
scrape_configs:
  - job_name: 'memory-service'
    scrape_interval: 15s
    metrics_path: /q/metrics
    static_configs:
      - targets: ['memory-service:8080']
    # Keep only metrics with application="memory-service" tag
    metric_relabel_configs:
      - source_labels: [application]
        regex: memory-service
        action: keep
```

### Admin Stats Endpoints

Memory Service provides `/v1/admin/stats/*` endpoints that query Prometheus for pre-aggregated metrics. These endpoints require the Prometheus URL to be configured:

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.prometheus.url` | URL | _(none)_ | Prometheus server URL for admin stats queries |

```bash
# Configure Prometheus URL for admin stats endpoints
MEMORY_SERVICE_PROMETHEUS_URL=http://prometheus:9090
```

When `memory-service.prometheus.url` is not configured, admin stats endpoints return **501 Not Implemented**. All other Memory Service functionality works normally.

#### Available Stats Endpoints

| Endpoint | Description | Required Metrics |
|----------|-------------|------------------|
| `/v1/admin/stats/request-rate` | HTTP request rate (requests/sec) | `http_server_requests_seconds_count` |
| `/v1/admin/stats/error-rate` | 5xx error rate (percent) | `http_server_requests_seconds_count` |
| `/v1/admin/stats/latency-p95` | P95 response latency (seconds) | `http_server_requests_seconds_bucket` |
| `/v1/admin/stats/cache-hit-rate` | Cache hit rate (percent) | `memory_entries_cache_hits_total`, `memory_entries_cache_misses_total` |
| `/v1/admin/stats/db-pool-utilization` | DB connection pool usage (percent) | `agroal_active_count`, `agroal_available_count` |
| `/v1/admin/stats/store-latency-p95` | Store operation P95 latency by type | `memory_store_operation_seconds_bucket` |
| `/v1/admin/stats/store-throughput` | Store operations/sec by type | `memory_store_operation_seconds_count` |

### Required Metrics

For all admin stats endpoints to function correctly, Prometheus must scrape the following metrics from Memory Service:

#### HTTP Metrics (Automatic)

These are automatically provided by Quarkus Micrometer:

| Metric | Type | Description |
|--------|------|-------------|
| `http_server_requests_seconds_count` | Counter | Total HTTP requests by method, uri, status |
| `http_server_requests_seconds_sum` | Counter | Total request duration |
| `http_server_requests_seconds_bucket` | Histogram | Request duration distribution |

#### Cache Metrics (When Cache Enabled)

Available when `memory-service.cache.type` is `redis` or `infinispan`:

| Metric | Type | Description |
|--------|------|-------------|
| `memory_entries_cache_hits_total` | Counter | Cache hits for memory entries |
| `memory_entries_cache_misses_total` | Counter | Cache misses for memory entries |
| `memory_entries_cache_errors_total` | Counter | Cache errors for memory entries |

#### Database Pool Metrics (Automatic)

Automatically provided by Quarkus/Agroal for PostgreSQL:

| Metric | Type | Description |
|--------|------|-------------|
| `agroal_active_count` | Gauge | Active database connections |
| `agroal_available_count` | Gauge | Available database connections |
| `agroal_awaiting_count` | Gauge | Requests waiting for a connection |

#### Store Operation Metrics (Automatic)

Automatically provided by MeteredMemoryStore:

| Metric | Type | Description |
|--------|------|-------------|
| `memory_store_operation_seconds_count` | Counter | Store operations by operation type |
| `memory_store_operation_seconds_sum` | Counter | Total operation duration by type |
| `memory_store_operation_seconds_bucket` | Histogram | Operation duration distribution by type |

The `operation` label identifies the operation type (e.g., `createConversation`, `appendAgentEntries`, `getEntries`).

### Example: Full Monitoring Stack

```yaml
# docker-compose.yml
services:
  memory-service:
    image: ghcr.io/chirino/memory-service:latest
    environment:
      MEMORY_SERVICE_DATASTORE_TYPE: postgres
      QUARKUS_DATASOURCE_JDBC_URL: jdbc:postgresql://postgres:5432/memoryservice
      QUARKUS_DATASOURCE_USERNAME: postgres
      QUARKUS_DATASOURCE_PASSWORD: postgres
      # Enable admin stats to query Prometheus
      MEMORY_SERVICE_PROMETHEUS_URL: http://prometheus:9090
    ports:
      - "8080:8080"

  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
    ports:
      - "9090:9090"

  grafana:
    image: grafana/grafana:latest
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
    ports:
      - "3000:3000"
```

```yaml
# prometheus.yml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'memory-service'
    static_configs:
      - targets: ['memory-service:8080']
    metrics_path: /q/metrics
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

      # Cache with Redis (response resumer and memory entries cache automatically enabled)
      MEMORY_SERVICE_CACHE_TYPE: redis
      QUARKUS_REDIS_HOSTS: redis://redis:6379
      # Optional: memory entries cache TTL (default: 10 minutes)
      # MEMORY_SERVICE_CACHE_EPOCH_TTL: PT10M

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
