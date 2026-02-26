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

```bash
MEMORY_SERVICE_DB_KIND=postgres
MEMORY_SERVICE_DB_URL=postgres://postgres:postgres@localhost:5432/memoryservice?sslmode=disable
MEMORY_SERVICE_DB_MIGRATE_AT_START=true

# Connection pool
MEMORY_SERVICE_DB_MAX_OPEN_CONNS=20
MEMORY_SERVICE_DB_MAX_IDLE_CONNS=5
```

### pgvector Extension

Enable pgvector for semantic search:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

Configure in Memory Service:

```bash
MEMORY_SERVICE_VECTOR_KIND=pgvector
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

```bash
MEMORY_SERVICE_DB_KIND=mongo
MEMORY_SERVICE_DB_URL=mongodb://admin:password@localhost:27017/memoryservice
```

## Embedding Configuration

Configure the embedding model for vector generation:

### OpenAI

```bash
MEMORY_SERVICE_EMBEDDING_KIND=openai
MEMORY_SERVICE_EMBEDDING_OPENAI_MODEL_NAME=text-embedding-ada-002
MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY=${OPENAI_API_KEY}
MEMORY_SERVICE_EMBEDDING_OPENAI_DIMENSIONS=1536
```

### Azure OpenAI

Use the OpenAI provider with the Azure base URL:

```bash
MEMORY_SERVICE_EMBEDDING_KIND=openai
MEMORY_SERVICE_EMBEDDING_OPENAI_BASE_URL=https://your-resource.openai.azure.com/openai/deployments/text-embedding-ada-002
MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY=${AZURE_OPENAI_API_KEY}
```

### Local Model

The built-in local provider uses all-MiniLM-L6-v2 (384 dimensions) with no external API calls:

```bash
MEMORY_SERVICE_EMBEDDING_KIND=local
```

## Performance Tuning

### Connection Pooling

```bash
# PostgreSQL / MongoDB
MEMORY_SERVICE_DB_MAX_OPEN_CONNS=50
MEMORY_SERVICE_DB_MAX_IDLE_CONNS=10
```

### Batch Operations

```bash
# Background vector indexer batch size
MEMORY_SERVICE_VECTOR_INDEXER_BATCH_SIZE=100
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
