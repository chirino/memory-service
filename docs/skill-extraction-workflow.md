# Skill Extraction Workflow

This document walks through the end-to-end workflow from raw conversations to extracted skills. Both the Memory Service and the Skill Extractor run as Fly.io apps sharing a Fly Postgres database. All user-facing interactions happen through the MCP server — no direct curl/REST calls needed.

## Architecture

```
Claude Code / Agent
       │
       │  stdio (JSON-RPC)
       v
  MCP Server (memory-service-mcp)
       │
       │  HTTPS
       v
┌──────────────────────────────────────────────┐
│  Fly.io                                      │
│                                              │
│  ┌──────────────────┐  ┌──────────────────┐  │
│  │ Memory Service   │  │ Skill Extractor  │  │
│  │ (Go)             │  │ (Quarkus)        │  │
│  │                  │  │                  │  │
│  │ • Conversations  │  │ • Polls clusters │  │
│  │ • Entries        │  │ • LLM extraction │  │
│  │ • Embeddings     │  │ • LLM verify     │  │
│  │ • Clustering     │  │ • Writes skills  │  │
│  │ • Skills (read)  │  │   as memories    │  │
│  └────────┬─────────┘  └────────┬─────────┘  │
│           │                     │             │
│           └──────────┬──────────┘             │
│                      │                        │
│             ┌────────┴────────┐               │
│             │  Fly Postgres   │               │
│             │  (pgvector)     │               │
│             └─────────────────┘               │
└──────────────────────────────────────────────┘
```

## Deployment

### Prerequisites

- [flyctl](https://fly.io/docs/flyctl/install/) installed and authenticated (`fly auth login`)
- An OpenAI API key for embeddings and skill extraction

### Deploy the Memory Service

```bash
# First-time setup
./deploy/fly/deploy.sh

# Redeploy after code changes
./deploy/fly/deploy.sh deploy-only
```

The deploy script outputs your API key. Save it — you'll need it for `.env` and the skill extractor.

The `fly.toml` must enable clustering and pgvector (upgrade from the default SQLite config):

```toml
[env]
  MEMORY_SERVICE_DB_KIND = "postgres"
  MEMORY_SERVICE_DB_MIGRATE_AT_START = "true"

  MEMORY_SERVICE_VECTOR_KIND = "pgvector"
  MEMORY_SERVICE_VECTOR_MIGRATE_AT_START = "true"
  MEMORY_SERVICE_EMBEDDING_KIND = "openai"
  MEMORY_SERVICE_SEARCH_SEMANTIC_ENABLED = "true"

  MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_ENABLED = "true"
  MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_EPSILON = "0.3"
  MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_MIN_PTS = "3"

  MEMORY_SERVICE_ROLES_ADMIN_CLIENTS = "cognition-processor"
  MEMORY_SERVICE_ROLES_INDEXER_CLIENTS = "agent"
```

Set secrets (Postgres URL is attached automatically by Fly):

```bash
fly secrets set --app memory-service-poc \
  MEMORY_SERVICE_API_KEYS_AGENT=<your-agent-key> \
  MEMORY_SERVICE_API_KEYS_COGNITION_PROCESSOR=<your-cognition-key> \
  OPENAI_API_KEY=<your-openai-key> \
  MEMORY_SERVICE_ENCRYPTION_DEK_KEY=$(openssl rand -hex 32)
```

### Deploy the Skill Extractor

The skill extractor runs as a separate Fly app pointing at the memory service:

```bash
fly apps create skill-extractor-poc --machines

fly secrets set --app skill-extractor-poc \
  MEMORY_SERVICE_URL=https://memory-service-poc.fly.dev \
  SKILL_EXTRACTOR_API_KEY=<your-cognition-key> \
  OPENAI_API_KEY=<your-openai-key>

fly deploy --app skill-extractor-poc \
  --config ./deploy/fly/skill-extractor.toml \
  --dockerfile ./java/quarkus/skill-extractor-quarkus/Dockerfile
```

### Configure the MCP Server

Add to your `.env` file (or set in your shell):

```bash
MEMORY_SERVICE_URL=https://memory-service-poc.fly.dev
MEMORY_SERVICE_API_KEY=<your-agent-key>
```

The `.mcp.json` in the repo root picks these up automatically:

```json
{
  "mcpServers": {
    "memory-service": {
      "command": "memory-service-mcp",
      "env": {
        "MEMORY_SERVICE_URL": "${MEMORY_SERVICE_URL}",
        "MEMORY_SERVICE_API_KEY": "${MEMORY_SERVICE_API_KEY}"
      }
    }
  }
}
```

## Workflow

### Step 1: Save Development Sessions (MCP)

As you work with Claude Code, the MCP server saves session notes to the memory service. These become the raw material for skill extraction.

Use the `save_session_notes` MCP tool:

```
Save these session notes:
- Title: "Optimized PostgreSQL queries for the orders table"
- Notes: "Ran EXPLAIN ANALYZE on the slow orders query. Found missing
  index on customer_id. Created index using CONCURRENTLY for zero
  downtime. Considered read replicas for analytics workloads but
  deferred — query latency dropped from 5s to 50ms with the index."
- Tags: "postgresql, performance, indexing"
```

The MCP server creates a conversation, appends entries with the notes, and indexes the content — all in one call.

### Step 2: Build Up Knowledge Over Sessions

Over multiple sessions, patterns emerge. Each `save_session_notes` call adds to the corpus:

```
Session: "Fixed OOMKilled pods in staging"
Notes: "Pods were OOMKilled due to unbounded query result sets.
  Added LIMIT clauses and pagination. Increased memory limits
  from 256Mi to 512Mi as a stopgap. Set up pprof to monitor
  memory allocation patterns."
Tags: "kubernetes, debugging, memory"
```

```
Session: "Deployed new payment service to K8s"
Notes: "Used the standard Helm chart pattern. Set resource limits
  based on load test results. Added readiness probe on /health.
  Rolled out with maxUnavailable=0 for zero-downtime deployment."
Tags: "kubernetes, deployment, helm"
```

### Step 3: Search Past Sessions (MCP)

Use the `search_sessions` MCP tool to find relevant past work:

```
Search sessions for: "kubernetes deployment"
```

This returns matching sessions ranked by semantic similarity.

### Step 4: Automatic Pipeline (No User Action Required)

Behind the scenes, the memory service and skill extractor work together:

1. **Embedding generation** — The BackgroundIndexer generates vectors for indexed entries using OpenAI embeddings.

2. **Knowledge clustering** — The clustering goroutine groups semantically similar entries into clusters (e.g., "PostgreSQL optimization", "Kubernetes operations"). Runs automatically after each embedding batch.

3. **Skill extraction** — The Quarkus skill extractor polls for changed clusters every 10 minutes. For each mature cluster (5+ members):
   - Fetches the cluster's representative entry texts
   - Sends them to `gpt-4o-mini` for structured extraction
   - A second LLM call verifies that extracted skills are supported by evidence
   - Verified skills are written back as episodic memories

### Step 5: Skills Surface Automatically

The next time you or an agent searches the memory service, extracted skills appear alongside session notes in the search results.

Use the `search_sessions` MCP tool:

```
Search sessions for: "how to handle slow database queries"
```

The results now include both raw session notes and structured skills:

```json
{
  "kind": "skill",
  "type": "procedure",
  "title": "Optimize slow PostgreSQL queries",
  "description": "Step-by-step approach for diagnosing and fixing slow queries.",
  "steps": [
    "Run EXPLAIN ANALYZE on the slow query",
    "Check for missing indexes on filtered/joined columns",
    "Create indexes using CONCURRENTLY for zero-downtime",
    "Consider read replicas for analytics workloads"
  ],
  "confidence": "high",
  "provenance": {
    "cluster_id": "4f4ae4c4-...",
    "entry_ids": ["515ce8a0-...", "a23bf901-..."]
  }
}
```

### Step 6: Proactive Agent Behavior

With skills in the memory store, agents can be proactive without re-reading old conversations:

1. **User**: "My orders query is taking forever"
2. **Agent** searches skills via MCP: `search_sessions("slow query optimization")`
3. **Agent** receives the matching procedure skill
4. **Agent** responds: "Based on your past approach, you usually start with EXPLAIN ANALYZE and check for missing indexes. Want me to do that?"

### Step 7: Review Past Sessions (MCP)

Use `list_sessions` and `get_session` to browse history:

```
List my recent sessions (limit 10)
```

```
Get session details for conversation ID: abc123-...
```

Use `append_note` to add follow-up information:

```
Append to session abc123-...:
"Update: the read replica approach worked well for the analytics dashboard.
 Query load on primary dropped by 40%."
```

## MCP Tools Reference

| Tool | Description |
|------|-------------|
| `save_session_notes` | Save a new session with title, notes, and tags |
| `search_sessions` | Semantic search over past sessions and extracted skills |
| `list_sessions` | List recent sessions by date |
| `get_session` | Retrieve full content of a specific session |
| `append_note` | Add follow-up notes to an existing session |

## Configuration Reference

### Memory Service (Fly.io)

| Setting | Default | Description |
|---------|---------|-------------|
| `MEMORY_SERVICE_DB_KIND` | `sqlite` | Set to `postgres` for Fly Postgres |
| `MEMORY_SERVICE_VECTOR_KIND` | `sqlite` | Set to `pgvector` for clustering |
| `MEMORY_SERVICE_EMBEDDING_KIND` | `local` | Set to `openai` for real embeddings |
| `MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_ENABLED` | `false` | Enable clustering |
| `MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_EPSILON` | `0.3` | DBSCAN distance threshold |
| `MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_MIN_PTS` | `3` | Minimum points per cluster |
| `MEMORY_SERVICE_ROLES_ADMIN_CLIENTS` | — | Set to `cognition-processor` |

### Skill Extractor (Fly.io)

| Setting | Default | Description |
|---------|---------|-------------|
| `MEMORY_SERVICE_URL` | — | URL of the Memory Service Fly app |
| `SKILL_EXTRACTOR_API_KEY` | — | Admin API key for memory service access |
| `SKILL_EXTRACTION_ENABLED` | `true` | Enable/disable extraction |
| `SKILL_EXTRACTION_SCHEDULE` | `0 */10 * * * ?` | Polling schedule |
| `SKILL_EXTRACTION_MIN_MEMBERS` | `5` | Minimum cluster size for extraction |
| `SKILL_EXTRACTOR_MODEL` | `gpt-4o-mini` | LLM model for extraction/verification |
| `OPENAI_API_KEY` | — | OpenAI API key |

## Data Flow Summary

| Stage | Where | Input | Output | LLM? |
|-------|-------|-------|--------|------|
| Session save | MCP tool → Memory Service | Session notes | Conversation entries + index | No |
| Embedding | Memory Service (background) | `indexed_content` text | pgvector embeddings | No |
| Clustering | Memory Service (background) | Embedding vectors | Knowledge clusters | No |
| Skill extraction | Skill Extractor → LLM | Cluster representative texts | Candidate skills | Yes |
| Skill verification | Skill Extractor → LLM | Candidates + evidence | Verified skills | Yes |
| Skill storage | Skill Extractor → Memory Service | Verified skills | Episodic memories | No |
| Skill retrieval | MCP tool → Memory Service | Search query | Ranked results | No |
