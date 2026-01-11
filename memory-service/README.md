# Memory Service

The core HTTP API backend service for storing and managing conversation history for AI agents.

## Configuration

The memory-service is configured via Quarkus configuration properties, which can be set in `application.properties` or via environment variables (using the `QUARKUS_` prefix).

### Core Service Configuration

#### HTTP Server

```properties
# HTTP port (default: 8080)
quarkus.http.port=8081

# Enable HTTP access logging
quarkus.http.access-log.enabled=true
quarkus.log.category."io.quarkus.http.access-log".level=INFO

# Logging level for memory service
quarkus.log.category."io.github.chirino.memory".level=DEBUG
```

#### OpenAPI

```properties
# OpenAPI specification path
quarkus.smallrye-openapi.path=/q/openapi
```

### Data Store Configuration

The memory-service supports multiple data store backends. Configure which one to use:

```properties
# Data store type: postgres, mongo, mongodb (default: postgres)
memory-service.datastore.type=postgres
```

#### PostgreSQL Configuration

When using PostgreSQL (`memory-service.datastore.type=postgres`):

```properties
# PostgreSQL datasource
quarkus.datasource.db-kind=postgresql
quarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/memory_service
quarkus.datasource.username=memory
quarkus.datasource.password=memory

# Liquibase migration
quarkus.liquibase.migrate-at-start=true
quarkus.liquibase.change-log=db/changelog/db.changelog-master.yaml

# Use JSONB for JSON mappings
hibernate.type.preferred_json_format=jsonb
```

#### MongoDB Configuration

When using MongoDB (`memory-service.datastore.type=mongo` or `memory-service.datastore.type=mongodb`):

```properties
# MongoDB connection
quarkus.mongodb.connection-string=mongodb://memory:memory@localhost:27017/memory_service?authSource=admin
quarkus.mongodb.database=memory_service

# Liquibase for MongoDB (optional)
quarkus.liquibase-mongodb.migrate-at-start=false
quarkus.liquibase-mongodb.change-log=db/changelog-mongodb/db.changelog-master.yaml
```

### Cache Configuration

Configure caching for conversation data:

```properties
# Cache type: none, redis, infinispan (default: none)
memory-service.cache.type=none
```

**Note**: Currently only `none` (no-op cache) is implemented. Redis and Infinispan support is planned.

When using Redis (future):

```properties
# Redis connection
quarkus.redis.hosts=redis://localhost:6379
```

### Vector Store Configuration

Configure semantic search vector storage:

```properties
# Vector store type: none, pgvector, postgres, mongo, mongodb (default: none)
memory-service.vector.type=none
```

#### PgVector Configuration

When using PgVector (`memory-service.vector.type=pgvector` or `memory-service.vector.type=postgres`):

- Requires PostgreSQL as the data store
- Uses the same PostgreSQL connection as configured for the data store
- Automatically uses the `message_embeddings` table for storing embeddings

#### MongoDB Vector Store

When using MongoDB vector store (`memory-service.vector.type=mongo` or `memory-service.vector.type=mongodb`):

- Requires MongoDB as the data store
- Uses MongoDB's native vector search capabilities

### Embedding Configuration

Configure text embedding generation for vector search:

```properties
# Embedding type: none, hash (default: hash)
memory.embedding.type=hash

# Embedding dimension (default: 256)
memory.embedding.dimension=256
```

**Note**: Currently only `hash` (simple hash-based embedding) is implemented. External embedding services (OpenAI, etc.) are planned.

### Authentication & Authorization

#### OIDC / Keycloak Configuration

The service uses OIDC for authentication:

```properties
# OIDC auth server URL
quarkus.oidc.auth-server-url=http://localhost:8080/realms/memory-service

# OIDC client ID
quarkus.oidc.client-id=memory-service-client

# OIDC client secret (use environment variable in production)
quarkus.oidc.credentials.secret=${KEYCLOAK_CLIENT_SECRET:change-me}

# OIDC application type
quarkus.oidc.application-type=service

# OIDC roles source
quarkus.oidc.roles.source=accesstoken

# Require authentication for all endpoints
quarkus.http.auth.permission.authenticated.paths=/*
quarkus.http.auth.permission.authenticated.policy=authenticated
```

#### API Key Authentication

For agent-to-service communication, API keys can be configured:

```properties
# Comma-separated list of API keys for trusted agents
memory-service.api-keys=agent-key-1,agent-key-2
```

When configured, agents can authenticate using the `X-API-Key` header. Endpoints annotated with `@RequireApiKey` will require this header.

**Note**: If `memory-service.api-keys` is not set or empty, API key authentication is effectively disabled.

### Data Encryption Configuration

The service supports encrypting sensitive data at rest using the `quarkus-data-encryption` extension.

#### Provider Configuration

```properties
# Ordered list of encryption providers (first is used for new encryption)
data.encryption.providers=plain

# Configure each provider
data.encryption.provider.plain.type=plain
```

#### Plain Provider (No Encryption)

Default provider that performs no encryption (useful for development):

```properties
data.encryption.providers=plain
data.encryption.provider.plain.type=plain
```

#### DEK Provider (Local Key Encryption)

For production encryption using a local Data Encryption Key:

```properties
data.encryption.providers=dek
data.encryption.provider.dek.type=dek

# Primary 32-byte AES-256 DEK (Base64-encoded)
data.encryption.dek.key=BASE64_PRIMARY_KEY

# Optional: Additional decryption keys for key rotation (Base64-encoded)
data.encryption.dek.decryption-keys=BASE64_OLD_KEY_1,BASE64_OLD_KEY_2
```

#### Vault Provider

For encryption using HashiCorp Vault Transit:

```properties
data.encryption.providers=vault
data.encryption.provider.vault.type=vault

# Vault configuration (uses Quarkiverse Vault)
quarkus.vault.url=http://localhost:8200
quarkus.vault.authentication.client-token.vault-token=your-token
quarkus.vault.kv-secret-engine-mount-path=secret
```

See the `quarkus-data-encryption/README.md` for detailed encryption configuration.

## Environment Variables

All Quarkus configuration properties can be overridden using environment variables by prefixing with `QUARKUS_` and converting dots to underscores. For example:

```bash
# Override HTTP port
export QUARKUS_HTTP_PORT=8081

# Override datasource URL
export QUARKUS_DATASOURCE_JDBC_URL=jdbc:postgresql://postgres:5432/memory_service

# Override memory service configuration
export MEMORY_SERVICE_DATASTORE_TYPE=postgres
export MEMORY_SERVICE_CACHE_TYPE=none
export MEMORY_SERVICE_VECTOR_TYPE=pgvector
export MEMORY_SERVICE_API_KEYS=key1,key2

# Override encryption
export DATA_ENCRYPTION_PROVIDERS=dek
export DATA_ENCRYPTION_DEK_KEY=base64-encoded-key

# Override OIDC configuration
export QUARKUS_OIDC_AUTH_SERVER_URL=http://keycloak:8080/realms/memory-service
export QUARKUS_OIDC_CREDENTIALS_SECRET=your-secret
export KEYCLOAK_CLIENT_SECRET=your-secret
```

## Configuration Profiles

Quarkus supports configuration profiles using the `%profile.` prefix:

### Development Profile (`%dev`)

```properties
%dev.quarkus.http.port=8081
%dev.quarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/memory_service
%dev.quarkus.liquibase.migrate-at-start=true
```

### Production Profile (`%prod`)

```properties
%prod.quarkus.datasource.jdbc.url=jdbc:postgresql://postgres:5432/memory_service
%prod.quarkus.oidc.auth-server-url=http://keycloak:8080/realms/memory-service
```

### Test Profile (`%test`)

```properties
%test.quarkus.datasource.devservices.enabled=true
%test.quarkus.liquibase.migrate-at-start=true
%test.memory-service.api-keys=test-agent-key
```

## Running the Service

### Development Mode

```bash
./mvnw quarkus:dev -pl memory-service
```

This will:
- Start the service on `http://localhost:8081` (default dev port)
- Use Dev Services to automatically start PostgreSQL, Keycloak, etc.
- Enable live reload on code changes

### Production Mode

Build and run:

```bash
# Build the application
./mvnw package -pl memory-service

# Run the application
java -jar memory-service/target/quarkus-app/quarkus-run.jar
```

Or use Docker:

```bash
# Build the Docker image
docker build -t memory-service-service:latest .

# Run with docker-compose
docker compose up service
```

## Example Configuration

### Minimal Development Setup

```properties
# Use PostgreSQL with Dev Services
memory-service.datastore.type=postgres
memory-service.cache.type=none
memory-service.vector.type=none

# Plain encryption (no-op)
data.encryption.providers=plain
data.encryption.provider.plain.type=plain

# OIDC (Dev Services will start Keycloak)
quarkus.oidc.auth-server-url=http://localhost:8080/realms/memory-service
quarkus.oidc.client-id=memory-service-client
quarkus.oidc.credentials.secret=change-me
```

### Production Setup with Encryption

```properties
# PostgreSQL
memory-service.datastore.type=postgres
quarkus.datasource.jdbc.url=jdbc:postgresql://postgres:5432/memory_service
quarkus.datasource.username=memory
quarkus.datasource.password=memory

# PgVector for semantic search
memory-service.vector.type=pgvector

# Redis cache
memory-service.cache.type=redis
quarkus.redis.hosts=redis://redis:6379

# DEK encryption
data.encryption.providers=dek
data.encryption.provider.dek.type=dek
data.encryption.dek.key=${DATA_ENCRYPTION_DEK_KEY}

# OIDC
quarkus.oidc.auth-server-url=http://keycloak:8080/realms/memory-service
quarkus.oidc.client-id=memory-service-client
quarkus.oidc.credentials.secret=${KEYCLOAK_CLIENT_SECRET}

# API keys for agents
memory-service.api-keys=${MEMORY_SERVICE_API_KEYS}
```

## See Also

- [Main README](../README.md) - Project overview and usage
- [AGENTS.md](../AGENTS.md) - Detailed project guidelines
- [OpenAPI Specification](../memory-service-client/src/main/openapi/openapi.yml) - API contract
- [quarkus-data-encryption README](../quarkus-data-encryption/README.md) - Encryption extension documentation
