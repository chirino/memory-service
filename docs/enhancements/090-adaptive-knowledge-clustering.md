---
status: partial
---

# Enhancement 090: Adaptive Knowledge Clustering

> **Status**: Partial — Phases 1-4 (core engine, keywords, trends, API + MCP) implemented.

## Summary

Add an adaptive knowledge layer that automatically discovers structure from stored conversation entries and episodic memories using density-based clustering on existing embeddings. The system produces a queryable intermediate representation (IR) of emergent topics, concepts, and trends — without LLM calls — letting AI agents consume high-quality, pre-structured knowledge instead of re-reading raw data.

## Motivation

### The Raw Data Problem

Today the memory-service stores two kinds of data:

1. **Conversation entries** — ordered message logs (user, assistant, tool calls). To extract meaning, an AI agent must fetch entries, deserialize them, and feed the full text into an LLM to re-derive facts on every retrieval. This is token-expensive, slow, and produces non-deterministic results.

2. **Episodic memories** ([enhancement 068](partial/068-namespaced-episodic-memory.md)) — structured key-value pairs that agents write explicitly. The agent must decide what to store and when, adding cognitive load to every interaction.

Both approaches share the same limitation: **knowledge structure must be imposed from outside** — either by an LLM re-reading everything, or by an agent explicitly choosing what matters. Neither approach lets the data speak for itself.

### Let the Data's Own Structure Surface What Matters

The memory-service already generates vector embeddings for entries and episodic memories. These embeddings encode semantic meaning in high-dimensional space. Entries about similar topics naturally cluster together in that space — a Python discussion sits near other Python entries, a deployment decision sits near infrastructure conversations.

This geometric structure is **already present in the data**. No one needs to label it, define schemas for it, or ask an LLM to extract it. The embeddings themselves contain the signal. What's missing is a mechanism to make that latent structure explicit and queryable.

Density-based clustering algorithms like DBSCAN can discover these natural groupings without predefined categories or fixed cluster counts. Topics emerge, grow, merge, and decay organically based on what users actually talk about — not based on what someone anticipated they would talk about.

### Why This Can Be Done Programmatically

The key insight is that **semantic similarity is a geometric property, not a linguistic one**. Once text is embedded into a vector, determining whether two pieces of content are about the same topic is a distance calculation — not a comprehension task. This is why clustering works:

- **Embedding models have already done the hard work.** The embedding step (which the memory-service already performs) compresses linguistic meaning into vectors. After that, all operations are pure linear algebra and statistics.
- **Cluster membership is a distance threshold, not a judgment call.** DBSCAN determines whether entries belong to the same cluster by measuring density-reachability — points within a distance threshold that have enough neighbors form clusters. This is a deterministic geometric operation.
- **Topic labels can be derived from cluster contents.** Once a cluster exists, its representative keywords can be extracted via c-TF-IDF (class-based term frequency-inverse document frequency) — a straightforward statistical method that identifies which words are most distinctive to that cluster versus others. No LLM needed.
- **Temporal dynamics are arithmetic.** Detecting that a topic is growing (more entries per unit time), drifting (centroid moving), or decaying (no new entries) is simple time-series math on cluster metadata.

The result is a system that transforms raw embeddings into structured, labeled, evolving topic clusters using only CPU-bound algorithms. Every step is deterministic: the same embeddings produce the same clusters.

### LLMs Sit on Top, Not Inside

This design inverts the typical architecture:

```
Traditional:  Raw Data  -->  LLM extracts facts  -->  Structured Knowledge
Proposed:     Raw Data  -->  Embeddings (exists)  -->  Clustering (deterministic)  -->  Structured IR
                                                                                          |
                                                                                          v
                                                                                     LLM consumes
                                                                                     high-quality
                                                                                     pre-structured
                                                                                     knowledge
```

Instead of using an LLM as the extraction engine (expensive, non-deterministic, token-hungry), the LLM becomes a **consumer of already-structured data**. When an agent needs context, it queries the clustering layer and receives:

- A list of relevant topic clusters with keywords and representative entries
- Temporal trends (what's new, what's drifting, what's stable)
- Cross-cluster relationships (which topics co-occur)

The LLM can then reason over this compact, high-quality IR rather than re-reading hundreds of raw entries. This is both cheaper (fewer tokens) and more reliable (deterministic extraction, consistent results).

### Comparison with LangMem

LangChain's [langmem](https://github.com/langchain-ai/langmem) library takes the LLM-inside approach:

| Dimension | LangMem | This Enhancement |
|-----------|---------|-----------------|
| Extraction method | LLM-based (Claude/GPT call per conversation) | Deterministic clustering on existing embeddings |
| Cost per extraction | LLM API call | CPU-only (embeddings already exist) |
| Determinism | Non-deterministic (LLM output varies across runs) | Deterministic (same embeddings produce same clusters) |
| Adaptiveness | Static extraction prompt | Clusters evolve, merge, split, and decay with data |
| Knowledge structure | Predefined memory types (semantic, episodic, procedural) | Emergent — structure discovered from data, not prescribed |
| Consolidation | LLM re-reads memories to merge/deduplicate | Algorithmic — cluster merging by centroid proximity |
| When it runs | Hot path (agent decides) or background LLM call | Background goroutine, no LLM call |

Both approaches are valid for different use cases. LangMem excels when an LLM's judgment is needed to determine relevance. This enhancement excels when the goal is to surface structure at scale without per-query LLM costs.

## Design

### 1. Clustering Algorithm

The clustering layer uses a density-based algorithm operating on the embeddings that the memory-service already generates and stores.

**Algorithm selection criteria:**

| Criterion | Requirement |
|-----------|------------|
| No predefined K | Must discover cluster count from data density |
| Noise-tolerant | Must handle outlier entries without forcing them into clusters |
| High-dimensional | Must perform well on embedding vectors (384–1536 dimensions) |
| Existing Go implementation | Prefer algorithms with battle-tested libraries |

**Starting algorithm: DBSCAN**

DBSCAN (Density-Based Spatial Clustering of Applications with Noise) is the initial choice because:

- **Go implementations exist** — `github.com/mpraski/clusters` provides a tested DBSCAN with support for custom distance functions (cosine similarity on embeddings).
- **No predefined K** — discovers cluster count from data density.
- **Noise handling** — outlier entries are classified as noise, not forced into clusters.
- **Well-understood** — decades of research, documented hyperparameter tuning strategies.
- **Two hyperparameters** — `epsilon` (neighborhood radius) and `minPts` (minimum points to form a cluster).

DBSCAN is a batch algorithm, but this is acceptable because clusters are **per-user**. A typical user has hundreds to low thousands of embeddings — DBSCAN processes these in milliseconds. The clustering goroutine re-runs DBSCAN only for users with new embeddings since the last cycle.

**Future upgrade paths:**

| Algorithm | When to consider |
|-----------|-----------------|
| DenStream | If per-user data volumes grow large enough that batch re-clustering becomes costly (true online, O(1) per point) |
| FISHDBC | If HDBSCAN-quality hierarchical clustering is needed with incremental updates |
| HDBSCAN | If cluster quality needs improvement and batch cost is acceptable |

### 2. Data Flow

```
Entry/Memory appended
        |
        v
Embedding generated (already exists - indexer pipeline)
        |
        v
Embedding stored in vector table (already exists)
        |
        v
+---------------------------+
| Clustering Goroutine      |  <-- new, polls indexed embeddings
| (periodic or on-notify)   |
+---------------------------+
        |
        v
+---------------------------+
| Cluster Metadata Store    |  <-- new table
| - cluster_id              |
| - centroid vector          |
| - keywords (c-TF-IDF)     |
| - member_count            |
| - created_at, updated_at  |
| - trend (growing/stable/  |
|   decaying)               |
+---------------------------+
        |
        v
Queryable via REST API + MCP tools
```

The clustering goroutine follows the same pattern as the existing episodic memory indexer ([enhancement 068](partial/068-namespaced-episodic-memory.md), Phase 5): a background goroutine polls for new embeddings at a configurable interval and processes them in batches.

### 3. Cluster Metadata

Each cluster maintains:

| Field | Type | Description |
|-------|------|-------------|
| `id` | UUID | Stable cluster identifier |
| `user_id` | string | Owner — clusters are per-user |
| `centroid` | vector | Mean embedding of cluster members |
| `keywords` | string[] | Top-N representative terms via c-TF-IDF |
| `label` | string | Auto-generated label from top keywords (e.g., "python, deployment, docker") |
| `member_count` | integer | Number of entries/memories in the cluster |
| `trend` | enum | `growing`, `stable`, `decaying` — based on recent append rate |
| `created_at` | timestamp | When the cluster first formed |
| `updated_at` | timestamp | Last time the cluster absorbed a new member |
| `source_type` | enum | `entries`, `memories`, `mixed` — which data feeds this cluster |

### 4. Cluster Lifecycle

Clusters are not static. The clustering algorithm naturally handles lifecycle transitions:

- **Birth**: DBSCAN discovers a new dense region in the embedding space. A new cluster is created with metadata.
- **Growth**: On re-clustering, the cluster absorbs new semantically similar entries. Centroid shifts. Keywords update. Trend is `growing`.
- **Stability**: Cluster membership is unchanged across re-clustering cycles. Trend becomes `stable`.
- **Drift**: Centroid moves significantly between cycles — the topic is evolving. Keywords update to reflect new content.
- **Merge**: Two previously separate clusters are unified by DBSCAN when new entries bridge the gap between them. Metadata is combined.
- **Split**: A cluster divides into two when internal density drops (entries diverge over time).
- **Decay**: A cluster stops receiving new entries. After a configurable period, trend becomes `decaying`. Decaying clusters are not deleted — they remain queryable but are deprioritized in results.

Since DBSCAN is batch, lifecycle transitions are detected by **diffing** the current cycle's output against the previous cycle's stored clusters. Cluster identity is preserved across cycles by majority-member overlap — if a new DBSCAN cluster shares >50% of its members with an existing stored cluster, it is treated as the same cluster (updated, not replaced).

### 5. Keyword Extraction

Each cluster's representative keywords are extracted using **c-TF-IDF** (class-based term frequency-inverse document frequency):

1. Concatenate the text content of all entries in a cluster into a single "class document."
2. Compute TF-IDF where each cluster is treated as one document in the corpus.
3. The highest-scoring terms per cluster are the terms that are most distinctive to that cluster versus all others.

This is a pure statistical operation — no LLM needed. The keywords serve as human-readable labels and as query targets for agents asking "what topics exist?"

### 6. REST API

All cluster endpoints are **admin-only** (`/admin/v1/knowledge/`). Clusters contain centroids derived from embeddings which may encode sensitive content. The user-facing knowledge API will be skills (see [enhancement 091](091-skill-extraction.md)), which expose abstracted procedural knowledge without raw embedding data.

#### GET /admin/v1/knowledge/clusters — List Clusters (admin)

Requires admin role.

```
GET /admin/v1/knowledge/clusters?user_id=alice&trend=growing
```

| Parameter | Type | Notes |
|-----------|------|-------|
| `user_id` | string | Filter by user; omit to list all users' clusters |
| `trend` | string | Filter by `growing`, `stable`, `decaying`; omit for all |
| `source_type` | string | Filter by `entries`, `memories`, `mixed` |
| `limit` | integer | Default 20, max 100 |

Response `200 OK`:

```json
{
  "clusters": [
    {
      "id": "<uuid>",
      "label": "python, deployment, docker",
      "keywords": ["python", "deployment", "docker", "container", "CI"],
      "member_count": 47,
      "trend": "growing",
      "created_at": "2026-01-15T10:00:00Z",
      "updated_at": "2026-03-24T14:30:00Z",
      "source_type": "mixed"
    }
  ]
}
```

#### GET /admin/v1/knowledge/clusters/{clusterId} — Cluster Detail (admin)

Returns cluster metadata plus representative entries (closest to centroid).

Response `200 OK`:

```json
{
  "id": "<uuid>",
  "label": "python, deployment, docker",
  "keywords": ["python", "deployment", "docker", "container", "CI"],
  "member_count": 47,
  "trend": "growing",
  "centroid_entries": [
    {
      "entry_id": "<uuid>",
      "conversation_id": "<uuid>",
      "content_preview": "We decided to containerize the Python service...",
      "distance": 0.12
    }
  ],
  "related_clusters": [
    {
      "cluster_id": "<uuid>",
      "label": "infrastructure, kubernetes",
      "similarity": 0.78
    }
  ]
}
```

#### POST /admin/v1/knowledge/search — Semantic Cluster Search (admin)

Find clusters relevant to a query.

```json
{
  "query": "how do we deploy Python services?",
  "limit": 5
}
```

Response `200 OK`:

```json
{
  "results": [
    {
      "cluster_id": "<uuid>",
      "label": "python, deployment, docker",
      "similarity": 0.91,
      "member_count": 47,
      "centroid_entries": [
        {
          "entry_id": "<uuid>",
          "content_preview": "We decided to containerize the Python service...",
          "distance": 0.12
        }
      ]
    }
  ]
}
```

#### GET /admin/v1/knowledge/trends — What's Changing (admin)

Returns clusters with notable recent activity: new clusters, fastest growing, drifting topics, decaying topics.

```json
{
  "new_clusters": [ ... ],
  "growing": [ ... ],
  "drifting": [ ... ],
  "decaying": [ ... ]
}
```

#### POST /admin/v1/knowledge/trigger — Force Clustering Cycle

Triggers an immediate clustering cycle without waiting for the background interval.

Response `200 OK`:

```json
{
  "users_processed": 1,
  "clusters_born": 3,
  "clusters_updated": 0,
  "clusters_died": 0,
  "failures": 0
}
```

### 7. MCP Tools

No MCP tools for clusters — the MCP server is user-facing and cluster data is admin-only. MCP tools for the user-facing knowledge API will be added as part of skill extraction ([enhancement 091](091-skill-extraction.md)).

### 8. Access Control

Cluster APIs require **admin role** (`security.RequireAdminRole()`). Centroids are derived from embeddings which encode semantic content — exposing them to regular users could leak sensitive information.

- **Cluster endpoints**: admin-only. Admins can query any user's clusters via `user_id` parameter or list all.
- **Skill endpoints** (future, [enhancement 091](091-skill-extraction.md)): user-facing. Skills are abstracted procedural knowledge (titles, steps, descriptions) that do not expose raw embedding data.
- The clustering goroutine operates on the full dataset. The admin API layer provides full visibility.

### 9. Configuration

| Setting | Default | Notes |
|---------|---------|-------|
| `knowledge.clustering.enabled` | false | Feature gate |
| `knowledge.clustering.algorithm` | `dbscan` | `dbscan` (future: `denstream`, `fishdbc`) |
| `knowledge.clustering.interval` | 60s | How often the clustering goroutine runs |
| `knowledge.clustering.epsilon` | 0.3 | DBSCAN neighborhood radius (cosine distance) |
| `knowledge.clustering.min_points` | 3 | DBSCAN minimum points to form a cluster |
| `knowledge.clustering.decay_after` | 30d | Time with no new members before trend becomes `decaying` |
| `knowledge.clustering.keywords_count` | 10 | Number of c-TF-IDF keywords per cluster |
| `knowledge.clustering.merge_threshold` | 0.15 | Centroid distance below which clusters merge |
| `knowledge.clustering.source` | `all` | `entries`, `memories`, or `all` |

### 10. Storage Schema

#### PostgreSQL

```sql
CREATE TABLE knowledge_clusters (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         TEXT        NOT NULL,
    label           TEXT        NOT NULL,
    keywords        JSONB       NOT NULL DEFAULT '[]'::jsonb,
    centroid        vector(N),  -- same dimension as embedding model
    member_count    INTEGER     NOT NULL DEFAULT 0,
    trend           SMALLINT    NOT NULL DEFAULT 0,  -- 0=growing, 1=stable, 2=decaying
    source_type     SMALLINT    NOT NULL DEFAULT 0,  -- 0=entries, 1=memories, 2=mixed
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX knowledge_clusters_user_idx   ON knowledge_clusters (user_id);
CREATE INDEX knowledge_clusters_trend_idx  ON knowledge_clusters (user_id, trend);

-- Membership: which entries/memories belong to which cluster
CREATE TABLE knowledge_cluster_members (
    cluster_id      UUID        NOT NULL REFERENCES knowledge_clusters(id) ON DELETE CASCADE,
    source_id       UUID        NOT NULL,  -- entry_id or memory_id
    source_type     SMALLINT    NOT NULL,  -- 0=entry, 1=memory
    distance        REAL        NOT NULL,  -- distance from centroid at assignment time
    assigned_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (cluster_id, source_id)
);

CREATE INDEX knowledge_members_source_idx ON knowledge_cluster_members (source_id);
```

## How to Test (E2E Demo)

### Prerequisites

- Docker (for PostgreSQL)
- Go toolchain
- An OpenAI API key (for real embeddings) OR use the local hash embedder (lower quality clusters but works offline)

### Step 1 — Start the service with clustering enabled

```bash
task dev:memory-service-pgvector
```

This uses pgvector (not Qdrant) for the vector store, enables clustering, and starts all required Docker containers. Requires `OPENAI_API_KEY` env var for real embeddings.

Wait for the service to be ready on `:8082`.

> **Note**: Clustering currently only supports pgvector. The default `task dev:memory-service` uses Qdrant and will silently skip clustering. Qdrant support is tracked in Phase 6.

### Step 2 — Create conversations about distinct topics

> **Important**: The `indexedContent` field is required for the BackgroundIndexer to generate embeddings. Without it, entries have `indexed_content = NULL` and the indexer skips them. Auth requires both `Authorization: Bearer` and `X-API-Key` headers (see [issue #181](https://github.com/chirino/memory-service/issues/181)).

```bash
API="http://localhost:8082"
H1="Authorization: Bearer agent-api-key-1"
H2="X-API-Key: agent-api-key-1"
CT="Content-Type: application/json"

# Topic 1: Python migration
CONV1=$(curl -s -X POST "$API/v1/conversations" -H "$H1" -H "$H2" -H "$CT" \
  -d '{"title":"Python Flask to FastAPI migration"}' | jq -r '.id')
echo "CONV1=$CONV1"

curl -s -X POST "$API/v1/conversations/$CONV1/entries" -H "$H1" -H "$H2" -H "$CT" \
  -d '{"contentType":"history","indexedContent":"migrate Flask app to FastAPI SQLAlchemy models async routes ORM patterns","content":[{"role":"USER","text":"We need to migrate our Flask app to FastAPI. What about SQLAlchemy models and async routes?"},{"role":"ASSISTANT","text":"Start with route definitions. SQLAlchemy models can stay mostly unchanged. FastAPI works well with existing ORM patterns."}]}'

# Topic 2: Kubernetes deployment
CONV2=$(curl -s -X POST "$API/v1/conversations" -H "$H1" -H "$H2" -H "$CT" \
  -d '{"title":"K8s resource limits and HPA"}' | jq -r '.id')
echo "CONV2=$CONV2"

curl -s -X POST "$API/v1/conversations/$CONV2/entries" -H "$H1" -H "$H2" -H "$CT" \
  -d '{"contentType":"history","indexedContent":"Kubernetes resource limits pod OOMKilled container spec memory leaks pprof","content":[{"role":"USER","text":"How do I set resource limits in a Kubernetes deployment? Pod keeps getting OOMKilled."},{"role":"ASSISTANT","text":"Add resources.limits and resources.requests to the container spec. Increase memory limit or check for memory leaks."}]}'

# Topic 3: Database performance
CONV3=$(curl -s -X POST "$API/v1/conversations" -H "$H1" -H "$H2" -H "$CT" \
  -d '{"title":"PostgreSQL query optimization"}' | jq -r '.id')
echo "CONV3=$CONV3"

curl -s -X POST "$API/v1/conversations/$CONV3/entries" -H "$H1" -H "$H2" -H "$CT" \
  -d '{"contentType":"history","indexedContent":"PostgreSQL queries slow orders table missing index customer_id EXPLAIN ANALYZE CREATE INDEX CONCURRENTLY","content":[{"role":"USER","text":"PostgreSQL queries are slow on the orders table. Missing index on customer_id."},{"role":"ASSISTANT","text":"CREATE INDEX CONCURRENTLY idx_orders_customer ON orders(customer_id). Use EXPLAIN ANALYZE to verify the plan."}]}'
```

### Step 3 — Wait for the BackgroundIndexer to generate embeddings

The BackgroundIndexer runs every 30s. It picks up entries with `indexed_content IS NOT NULL AND indexed_at IS NULL`, calls the embedder, and stores vectors in `entry_embeddings`.

```bash
echo "Waiting 35s for BackgroundIndexer..."
sleep 35

# Verify embeddings were created
docker exec -i $(docker ps -q -f name=postgres) psql -U postgres -d memory_service \
  -c "SELECT COUNT(*) FROM entry_embeddings;"
# Should show 3
```

### Step 4 — Trigger clustering

```bash
curl -s -X POST "$API/admin/v1/knowledge/trigger" -H "$H1" -H "$H2" | jq
```

Expected output:
```json
{
  "users_processed": 1,
  "clusters_born": 3,
  "clusters_updated": 0,
  "clusters_died": 0,
  "failures": 0
}
```

### Step 5 — View the emerged clusters

```bash
curl -s "$API/admin/v1/knowledge/clusters" -H "$H1" -H "$H2" | jq
```

Expected output (labels/keywords depend on embedding model):
```json
{
  "clusters": [
    {
      "id": "...",
      "label": "python, fastapi, migration",
      "keywords": ["python", "fastapi", "migration", "flask", "sqlalchemy"],
      "member_count": 2,
      "trend": "growing",
      "source_type": "entries",
      "created_at": "...",
      "updated_at": "..."
    },
    {
      "id": "...",
      "label": "kubernetes, deployment, resources",
      "keywords": ["kubernetes", "deployment", "resources", "memory", "oomkilled"],
      "member_count": 2,
      "trend": "growing",
      "source_type": "entries"
    },
    {
      "id": "...",
      "label": "postgresql, index, query",
      "keywords": ["postgresql", "index", "query", "orders", "customer"],
      "member_count": 2,
      "trend": "growing",
      "source_type": "entries"
    }
  ]
}
```

### Step 6 — Add more data and watch clusters evolve

Add more entries to existing topics, wait for indexing, and trigger again:

```bash
# Add more Python content
curl -s -X POST "$API/v1/conversations/$CONV1/entries" -H "$H1" -H "$H2" -H "$CT" \
  -d '{"contentType":"history","indexedContent":"Python tests failing migration pytest-asyncio async routes asyncio_mode","content":[{"role":"USER","text":"Python tests failing after migration. pytest-asyncio not configured for async routes."},{"role":"ASSISTANT","text":"Install pytest-asyncio and add the asyncio_mode=auto setting."}]}'

# Wait for BackgroundIndexer
sleep 35

# Trigger re-clustering
curl -s -X POST "$API/admin/v1/knowledge/trigger" -H "$H1" -H "$H2" | jq

# Check: Python cluster member_count should increase
curl -s "$API/admin/v1/knowledge/clusters" -H "$H1" -H "$H2" | jq '.clusters[] | select(.label | test("python"))'
```

### What to Look For

| Signal | What it means |
|--------|--------------|
| Distinct clusters form for each topic | DBSCAN epsilon/minPts are well-tuned for the embedding model |
| Keywords match the topic content | c-TF-IDF is extracting distinctive terms correctly |
| `member_count` increases after adding related content | Cluster diff is matching by member overlap |
| `trend` changes from `growing` to `decaying` | Decay timeout is working |
| No clusters form | Epsilon too small, or not enough entries (need >= `minPts` per topic) |
| Everything in one cluster | Epsilon too large — reduce it |

### Unit Tests

```bash
# Run all knowledge unit tests (26 tests)
go test ./internal/knowledge/... -v -count=1
```

## Testing

### Cucumber BDD Scenarios

```gherkin
Feature: Adaptive Knowledge Clustering

  Background:
    Given a memory service is running with clustering enabled
    And user "alice" is authenticated

  Scenario: Clusters emerge from conversation entries
    Given alice has a conversation with 10 entries about "Python testing"
    And alice has a conversation with 8 entries about "Kubernetes deployment"
    When the clustering goroutine completes a cycle
    Then at least 2 knowledge clusters exist for alice
    And one cluster has keywords containing "python" or "test"
    And another cluster has keywords containing "kubernetes" or "deploy"

  Scenario: New entry is assigned to existing cluster
    Given a knowledge cluster exists with label containing "python"
    When alice appends an entry about "Python type hints"
    And the clustering goroutine completes a cycle
    Then the "python" cluster member count has increased

  Scenario: Cluster trend reflects activity
    Given a knowledge cluster exists with trend "growing"
    When no new entries are added for the configured decay period
    And the clustering goroutine completes a cycle
    Then the cluster trend is "decaying"

  Scenario: Clusters are user-scoped
    Given alice has knowledge clusters
    And user "bob" is authenticated
    When bob lists knowledge clusters
    Then bob sees no clusters from alice

  Scenario: Search returns relevant clusters
    Given alice has a cluster with keywords containing "docker"
    When alice searches knowledge for "container orchestration"
    Then the results include the "docker" cluster

  Scenario: Cluster detail includes representative entries
    Given alice has a knowledge cluster with at least 5 members
    When alice gets the cluster detail
    Then the response includes centroid entries sorted by distance
    And the response includes related clusters

  Scenario: Episodic memories contribute to clusters
    Given alice puts memories about "machine learning" in namespace ["user","alice","notes"]
    When the clustering goroutine completes a cycle
    Then a knowledge cluster exists with keywords containing "machine" or "learning"
    And the cluster source type is "memories"

  Scenario: Clusters merge when topics converge
    Given alice has a cluster about "Docker" and a cluster about "containers"
    When the clustering goroutine detects centroid distance below merge threshold
    Then the two clusters merge into one
    And the merged cluster keywords include terms from both originals
```

### E2E Integration: Knowledge-Driven Agent Context

```gherkin
Feature: Knowledge-driven agent context

  # This scenario demonstrates the practical advantage of adaptive clustering.
  # Without this feature, an agent would need to fetch and re-read all entries
  # across all conversations to understand what the user has been working on.
  # With clustering, the agent gets pre-structured, labeled topic groups in
  # a single query — fewer tokens, faster responses, deterministic results.

  Background:
    Given a memory service is running with clustering enabled
    And user "alice" is authenticated
    And the embedding model is configured

  Scenario: Agent discovers user's knowledge landscape without reading raw entries
    # Alice has had many conversations over the past weeks.
    # The topics are never explicitly labeled — they emerge from content.

    # Week 1: Alice discusses Python migration
    Given alice has a conversation "conv-1" with entries:
      | role      | content                                                    |
      | user      | We need to migrate our Flask app to FastAPI                |
      | assistant | I'd recommend starting with the route definitions...       |
      | user      | What about the SQLAlchemy models?                          |
      | assistant | You can keep them mostly unchanged, FastAPI works well...  |
    And alice has a conversation "conv-2" with entries:
      | role      | content                                                    |
      | user      | Our Python tests are failing after the migration           |
      | assistant | Check if pytest-asyncio is configured for async routes...  |

    # Week 2: Alice works on Kubernetes deployment
    And alice has a conversation "conv-3" with entries:
      | role      | content                                                    |
      | user      | How do I set resource limits in a K8s deployment?          |
      | assistant | Add resources.limits and resources.requests to the spec... |
      | user      | The pod keeps getting OOMKilled                            |
      | assistant | Increase the memory limit or check for memory leaks...     |
    And alice has a conversation "conv-4" with entries:
      | role      | content                                                    |
      | user      | Our Helm chart needs a horizontal pod autoscaler           |
      | assistant | Add an HPA resource targeting CPU utilization...           |

    # Week 3: Alice explores database performance
    And alice has a conversation "conv-5" with entries:
      | role      | content                                                    |
      | user      | PostgreSQL queries are slow on the orders table            |
      | assistant | Let's look at the query plan with EXPLAIN ANALYZE...       |
      | user      | Missing index on customer_id                               |
      | assistant | CREATE INDEX CONCURRENTLY idx_orders_customer ON orders... |
    And alice has a conversation "conv-6" with entries:
      | role      | content                                                    |
      | user      | Should we add read replicas for the reporting queries?     |
      | assistant | Yes, route read-heavy analytics to a replica...            |

    # The clustering goroutine processes all embeddings
    When the clustering goroutine completes a full cycle

    # --- THE ADVANTAGE ---
    # A new agent session starts. The agent needs context about alice.
    # WITHOUT clustering: fetch all 6 conversations, deserialize ~14 entries,
    #   send all of them to an LLM to understand what alice works on.
    #   Cost: ~2000 tokens of raw conversation, LLM call to summarize.
    # WITH clustering: one API call returns structured topics.

    Then GET /v1/knowledge/clusters returns at least 3 clusters for alice
    And the clusters include one with keywords matching "python|fastapi|migration"
    And the clusters include one with keywords matching "kubernetes|k8s|deployment|helm"
    And the clusters include one with keywords matching "postgresql|database|index|query"

    # Agent can now ask targeted follow-up questions per cluster
    When GET /v1/knowledge/clusters/{python-cluster-id}
    Then the response includes centroid entries from "conv-1" and "conv-2"
    And the response includes related clusters linking to the "database" cluster

    # Agent can search for specific knowledge across clusters
    When POST /v1/knowledge/search with query "performance issues"
    Then the results include the "database" cluster with high similarity
    And the results include the "kubernetes" cluster (OOMKilled = performance)
    And each result includes representative entries the agent can cite

    # Agent can check what's currently active
    When GET /v1/knowledge/trends
    Then the "database" cluster appears in the "growing" section
    And the "python" cluster appears in the "stable" section

  Scenario: Knowledge query vs raw entry retrieval — token comparison
    # This scenario quantifies the advantage.

    Given alice has 20 conversations with a total of 150 entries
    And the clustering goroutine has completed

    # Raw approach: agent fetches all entries
    When all 150 entries are serialized to text
    Then the total token count exceeds 15000

    # Knowledge approach: agent queries clusters
    When GET /v1/knowledge/clusters returns clusters for alice
    Then the response token count is less than 500
    And each cluster contains enough context to decide if deeper retrieval is needed

    # The agent only fetches raw entries for the ONE relevant cluster,
    # reducing token usage by 90%+ for the initial context-building step.
```

### Unit Tests (Go)

- `internal/knowledge/dbscan_test.go`
  - `TestDBSCAN_FormsClusters` — distinct embedding groups produce separate clusters
  - `TestDBSCAN_Noise` — isolated embeddings are classified as noise
  - `TestDBSCAN_AllNoise` — all points too far apart, no clusters
  - `TestDBSCAN_SingleCluster` — all points within epsilon
  - `TestDBSCAN_Empty` — empty input
  - `TestCosineDistance_*` — cosine distance correctness (identical, orthogonal, opposite, empty, mismatched)
  - `TestComputeCentroid` / `TestComputeCentroid_Empty` — centroid calculation
  - `TestDiffClusters_MatchesByOverlap` — majority-member overlap preserves cluster identity
  - `TestDiffClusters_DetectsNewCluster` — new dense region creates birth
  - `TestDiffClusters_DetectsDeath` — disappeared cluster detected
  - `TestDiffClusters_DetectsMerge` — two clusters merging
  - `TestDiffClusters_LowOverlapBirth` — low overlap treated as new cluster
- `internal/knowledge/keywords_test.go`
  - `TestExtractKeywords_TwoClusters` — c-TF-IDF produces expected top terms for known cluster contents
  - `TestExtractKeywords_DistinctiveTermsRankHigher` — shared terms rank below distinctive terms
  - `TestExtractKeywords_SingleCluster` — single cluster keyword extraction
  - `TestExtractKeywords_Empty` / `TestExtractKeywords_ZeroTopN` — edge cases
  - `TestGenerateLabel` / `TestGenerateLabel_FewerThanMax` — label generation
  - `TestTokenize_FiltersStopWordsAndShortTokens` — tokenizer correctness
  - `TestKeywordStrings` — keyword string extraction

## Tasks

### Phase 1 — Core Clustering Engine

- [x] Implement DBSCAN algorithm with cosine distance in Go (`internal/knowledge/dbscan.go`)
- [x] Implement cluster diff logic (majority-member overlap matching)
- [x] Add `knowledge_clusters` and `knowledge_cluster_members` PostgreSQL tables (`internal/plugin/store/postgres/db/schema.sql`)
- [x] PostgreSQL KnowledgeStore (`internal/knowledge/postgres_store.go`)
- [x] Background clustering goroutine (`internal/knowledge/clusterer.go`)
- [x] Feature gate configuration (`knowledge.clustering.enabled`) in `internal/config/config.go` and `internal/cmd/serve/serve.go`
- [x] Wire clustering goroutine into server startup (`internal/cmd/serve/server.go`)

### Phase 2 — Keyword Extraction and Labeling

- [x] Implement c-TF-IDF keyword extraction (`internal/knowledge/keywords.go`)
- [x] Auto-generate cluster labels from top keywords
- [x] Keyword refresh on cluster membership change (wired into clusterer)
- [x] LoadTextsForSourceIDs store method for keyword extraction

### Phase 3 — Cluster Lifecycle

- [x] Trend detection (growing/stable/decaying) based on `updated_at` vs decay threshold
- [x] Cluster birth/death/update via diff
- [ ] Centroid drift detection (track centroid movement between cycles)

### Phase 4 — Admin REST API

- [x] `GET /admin/v1/knowledge/clusters` — list clusters with admin role check and `user_id` filter
- [x] `POST /admin/v1/knowledge/trigger` — force clustering cycle with admin role check
- [ ] `GET /admin/v1/knowledge/clusters/{id}` — cluster detail with representative entries
- [ ] `POST /admin/v1/knowledge/search` — semantic search over clusters
- [ ] `GET /admin/v1/knowledge/trends` — recent cluster activity
- [ ] Add endpoints to `contracts/openapi/openapi-admin.yml`

### Phase 5 — gRPC Parity

- [ ] Add knowledge RPC definitions to `contracts/protobuf/memory/v1/memory_service.proto`
- [ ] Implement gRPC handlers
- [ ] Add BDD coverage for gRPC knowledge endpoints

### Phase 6 — Qdrant Backend Support

Currently clustering only works with pgvector (queries `entry_embeddings` table directly). Add support for Qdrant so clustering works with the default dev mode vector store.

- [ ] Abstract embedding retrieval behind the `VectorStore` interface (or a new `EmbeddingReader` interface)
- [ ] Implement Qdrant embedding retrieval (list points by user/conversation)
- [ ] Remove the `cfg.VectorType == "pgvector"` guard from server.go
- [ ] Test with `task dev:memory-service` (Qdrant mode)

### Phase 7 — MongoDB Backend

- [ ] Implement cluster storage for MongoDB
- [ ] Adapt clustering goroutine for MongoDB embedding retrieval

### Phase 7 — Admin API

- [ ] `GET /admin/v1/knowledge/status` — cluster count, pending embeddings, last cycle time
- [ ] `POST /admin/v1/knowledge/rebuild` — trigger full re-clustering
- [ ] Add endpoints to `contracts/openapi/openapi-admin.yml`

## Files to Modify

| File | Change | Status |
|------|--------|--------|
| `internal/knowledge/dbscan.go` | **new** — DBSCAN with cosine distance, cluster diffing | Done |
| `internal/knowledge/dbscan_test.go` | **new** — 17 unit tests | Done |
| `internal/knowledge/keywords.go` | **new** — c-TF-IDF keyword extraction | Done |
| `internal/knowledge/keywords_test.go` | **new** — 9 unit tests | Done |
| `internal/knowledge/clusterer.go` | **new** — background clustering goroutine with keyword refresh | Done |
| `internal/knowledge/store.go` | **new** — KnowledgeStore interface | Done |
| `internal/knowledge/postgres_store.go` | **new** — PostgreSQL implementation | Done |
| `internal/plugin/store/postgres/db/schema.sql` | Add `knowledge_clusters` and `knowledge_cluster_members` tables | Done |
| `internal/plugin/route/knowledge/knowledge.go` | **new** — Admin REST handlers for list clusters + trigger (RequireAdminRole) | Done |
| `internal/config/config.go` | Add `KnowledgeClustering*` settings | Done |
| `internal/cmd/serve/serve.go` | Add clustering CLI flags | Done |
| `internal/cmd/serve/server.go` | Wire clustering goroutine + knowledge routes | Done |
| `internal/plugin/store/mongo/knowledge_store.go` | **new** — MongoDB storage for cluster metadata | Pending |
| `internal/grpc/server.go` | Add knowledge gRPC handlers | Pending |
| `contracts/openapi/openapi.yml` | Add `/v1/knowledge/*` endpoints and schemas | Pending |
| `contracts/openapi/openapi-admin.yml` | Add `/admin/v1/knowledge/*` endpoints | Pending |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add knowledge RPC definitions | Pending |

## Verification

```bash
# Compile
go build ./...

# Unit tests (26 tests)
go test ./internal/knowledge/... -v -count=1

# BDD integration tests (Postgres)
go test ./internal/bdd -run TestFeaturesPg -count=1

# Full test suite (capture output)
task test:go > test.log 2>&1
# Search for failures using Grep tool on test.log
```

## Non-Goals

- Replacing the existing episodic memory store ([enhancement 068](partial/068-namespaced-episodic-memory.md)) — clusters are derived state, not a new source of truth.
- LLM-based extraction — the entire clustering pipeline is CPU-only.
- Real-time per-entry clustering on the hot path — clustering runs as a background process.
- Cross-user clustering — clusters are always user-scoped.

## Design Decisions

**Why deterministic clustering instead of LLM extraction?**
Embedding models have already compressed linguistic meaning into vectors. After embedding, determining semantic similarity is a distance calculation, not a comprehension task. Clustering exploits this geometric structure using pure linear algebra — it is cheaper (no API calls), deterministic (same input produces same output), and adaptive (clusters evolve with data). The LLM's role moves from "extraction engine" to "consumer of pre-structured knowledge," where it can reason over compact, high-quality cluster data rather than re-reading hundreds of raw entries.

**Why DBSCAN as the starting algorithm?**
DBSCAN has existing Go implementations (`github.com/mpraski/clusters`), is well-understood with decades of research, requires only two hyperparameters (epsilon, minPts), and naturally handles noise. Since clusters are per-user (hundreds to low thousands of embeddings), batch re-clustering is fast enough. Incremental algorithms like DenStream (superior benchmarks on text embeddings but no Go implementation) are documented as upgrade paths if per-user data volumes grow large enough to warrant them.

**Why c-TF-IDF for keywords instead of LLM-generated labels?**
c-TF-IDF is the same statistical method used by BERTopic. It identifies terms that are distinctively frequent in one cluster compared to all others — a pure frequency calculation. This produces consistent, reproducible labels without LLM calls. An LLM can always be layered on top to produce more natural labels from these keywords if desired.

**Why clusters are derived state, not a source of truth?**
Clusters can be fully rebuilt from embeddings at any time. This means the clustering algorithm can be swapped, parameters can be tuned, and bugs can be fixed without data loss. The source of truth remains the entries and memories stored in the existing tables.

**Why user-scoped, not global?**
Each user's knowledge landscape is different. A cluster that's meaningful for one user (e.g., "Python deployment patterns") may not exist for another. User scoping also aligns with the existing access control model and avoids leaking information across user boundaries.

## Open Questions

1. **Embedding source priority**: Should the clustering goroutine consume entry embeddings, memory embeddings, or both by default? Processing both gives a richer picture but may produce noisy mixed clusters.

2. **DBSCAN hyperparameters**: The epsilon (neighborhood radius) and minPts (minimum cluster size) significantly affect cluster granularity. Should we provide sensible defaults and let admins tune, or implement an auto-tuning mechanism (e.g., k-distance graph elbow detection)?

3. **Python client**: Should `memory-service-langgraph` expose cluster queries? This could enable LangGraph agents to query the knowledge layer as part of their retrieval pipeline.
