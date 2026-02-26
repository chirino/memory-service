---
layout: ../../../layouts/DocsLayout.astro
title: Database Setup
description: Configure databases and vector stores for Memory Service.
---

Memory Service supports multiple database backends. This guide covers setup and configuration for each option.

## PostgreSQL (Recommended)

PostgreSQL with pgvector is the recommended setup for most deployments.

### Installation

Using Docker:

```bash
docker run -d \
  --name postgres \
  -e POSTGRES_DB=memoryservice \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -p 5432:5432 \
  pgvector/pgvector:pg18
```

### Configuration

```properties
quarkus.datasource.db-kind=postgresql
quarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/memoryservice
quarkus.datasource.username=postgres
quarkus.datasource.password=postgres

# Schema management
quarkus.hibernate-orm.database.generation=update

# Connection pool
quarkus.datasource.jdbc.max-size=20
quarkus.datasource.jdbc.min-size=5
```

### pgvector Extension

Enable pgvector for semantic search:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

Configure in Memory Service:

```properties
memory-service.vector-store.type=pgvector
memory-service.vector-store.dimension=1536
memory-service.vector-store.index-type=ivfflat
memory-service.vector-store.lists=100
```

### Index Types

| Type      | Description                        | Best For    |
| --------- | ---------------------------------- | ----------- |
| `ivfflat` | Inverted file with flat storage    | General use |
| `hnsw`    | Hierarchical navigable small world | High recall |

```sql
-- Create HNSW index for better performance
CREATE INDEX ON entries USING hnsw (embedding vector_cosine_ops);
```

## MongoDB

MongoDB is supported for teams already using MongoDB infrastructure.

### Installation

```bash
docker run -d \
  --name mongodb \
  -e MONGO_INITDB_ROOT_USERNAME=admin \
  -e MONGO_INITDB_ROOT_PASSWORD=password \
  -p 27017:27017 \
  mongo:7
```

### Configuration

```properties
quarkus.mongodb.connection-string=mongodb://admin:password@localhost:27017
quarkus.mongodb.database=memoryservice
```

## Embedding Configuration

Configure the embedding model for vector generation:

### OpenAI

```properties
memory-service.embedding.provider=openai
memory-service.embedding.model=text-embedding-ada-002
memory-service.embedding.api-key=${OPENAI_API_KEY}
memory-service.embedding.dimension=1536
```

### Azure OpenAI

```properties
memory-service.embedding.provider=azure
memory-service.embedding.azure.endpoint=https://your-resource.openai.azure.com
memory-service.embedding.azure.deployment=text-embedding-ada-002
memory-service.embedding.azure.api-key=${AZURE_OPENAI_API_KEY}
```

### Local Model

```properties
memory-service.embedding.provider=local
memory-service.embedding.local.model-path=/models/all-MiniLM-L6-v2
memory-service.embedding.dimension=384
```

## Performance Tuning

### Connection Pooling

```properties
# PostgreSQL
quarkus.datasource.jdbc.max-size=50
quarkus.datasource.jdbc.min-size=10
quarkus.datasource.jdbc.initial-size=10
quarkus.datasource.jdbc.acquisition-timeout=30

# MongoDB
quarkus.mongodb.max-pool-size=100
quarkus.mongodb.min-pool-size=10
```

### Batch Operations

```properties
# Batch embedding requests
memory-service.embedding.batch-size=100

# Batch database inserts
memory-service.storage.batch-size=50
```

## Backup and Recovery

### PostgreSQL

```bash
# Backup
pg_dump -h localhost -U postgres memoryservice > backup.sql

# Restore
psql -h localhost -U postgres memoryservice < backup.sql
```

### MongoDB

```bash
# Backup
mongodump --uri="mongodb://localhost:27017" --db=memoryservice --out=backup/

# Restore
mongorestore --uri="mongodb://localhost:27017" backup/
```

## Next Steps

- Return to [Configuration](/docs/configuration/)
- Learn about [Docker Deployment](/docs/deployment/docker/)
