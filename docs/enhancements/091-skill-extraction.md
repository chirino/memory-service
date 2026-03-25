---
status: proposed
---

# Enhancement 091: Skill Extraction from Knowledge Clusters

> **Status**: Proposed.

## Summary

Extract reusable **skills** — procedural knowledge, decision patterns, and problem-solution mappings — from the knowledge clusters produced by [enhancement 090](090-adaptive-knowledge-clustering.md). An LLM runs *once per cluster* on the already-compact cluster IR (keywords + representative entries, ~500 tokens) rather than on raw conversation data (~15,000+ tokens). Skills are stored, versioned, and queryable, letting agents answer "how does this user handle X?" without re-reading any conversation history.

**Admin/user boundary**: Clusters are admin-only (centroids contain embedding-derived data that may be sensitive). Skills are the **user-facing knowledge API** — they expose abstracted procedural knowledge (titles, steps, descriptions) without raw embedding data. This makes skills safe to expose to end users and MCP tools.

## Motivation

### Clusters Tell You *What*. Skills Tell You *How*.

Enhancement 090 produces knowledge clusters: "this user talks about PostgreSQL optimization, Kubernetes deployment, Python migration." That's topical structure — it answers *what* topics exist.

But inside those clusters lies a richer signal: **how the user actually works**. A PostgreSQL cluster doesn't just contain the topic — it contains a repeatable procedure:

1. Run `EXPLAIN ANALYZE` on the slow query
2. Check for missing indexes
3. Create indexes with `CONCURRENTLY` for zero downtime
4. Consider read replicas for analytics workloads

That's a **skill** — a reusable piece of procedural knowledge that an agent can apply the next time a similar problem appears, without the user having to explain it again.

### Why LLM-Assisted, Not Pure Deterministic

Enhancement 090 is deliberately LLM-free: topic clustering and keyword extraction are geometric/statistical operations on embeddings. Skill extraction is fundamentally different:

- **Causal relationships** ("do X *because* Y") require comprehension, not distance calculations.
- **Sequential patterns** ("first X, then Y, then Z") need understanding of temporal and logical ordering within conversation turns.
- **Abstraction** ("this specific PostgreSQL fix generalizes to: always check indexes before adding replicas") requires reasoning beyond pattern matching.

Deterministic approaches (n-gram matching, action sequence extraction) produce low-quality, brittle results for this task. An LLM is the right tool — but the key is **where** it sits in the pipeline.

### The Efficiency Argument: LLM on Top of IR, Not on Raw Data

```
Without clusters:   150 entries  -->  ~15,000 tokens  -->  LLM extracts skills  -->  expensive, slow
With clusters:      5 clusters   -->  ~2,500 tokens   -->  LLM extracts skills  -->  cheap, fast
```

The LLM doesn't see raw conversation history. It sees the pre-structured cluster IR:
- Cluster label and keywords (from c-TF-IDF)
- Representative entries (closest to centroid)
- Cluster trend and member count

This is a **6x+ token reduction** before the LLM even starts. The extraction is also more reliable because the input is already organized by topic — the LLM doesn't need to figure out what the topic is, only what the procedures and patterns are.

### Skills Enable Proactive Agent Behavior

Without skills, an agent is reactive — it waits for the user to describe a problem, then helps. With skills, an agent can be proactive:

- "I see you're working on a slow SQL query. Based on your past approach, you usually start with EXPLAIN ANALYZE and check for missing indexes. Want me to do that?"
- "You've deployed 3 services to Kubernetes this month using the same Helm chart pattern. Here's a template based on your established workflow."
- "Last time you migrated a Flask app, you hit an issue with pytest-asyncio. I'll flag that early this time."

## Design

### 1. Skill Types

| Type | Description | Example |
|------|-------------|---------|
| **Procedure** | Ordered sequence of steps to accomplish a task | "To optimize a slow PostgreSQL query: 1) EXPLAIN ANALYZE, 2) check indexes, 3) CREATE INDEX CONCURRENTLY" |
| **Decision** | A pattern for choosing between alternatives | "When choosing between read replicas and query caching, this user prefers replicas for analytics and caching for OLTP" |
| **Tool Usage** | Preferred tools/commands for a task | "For container debugging, this user uses `kubectl describe pod`, then `kubectl logs -f`, then checks resource limits" |
| **Problem-Solution** | A known problem mapped to a proven solution | "OOMKilled pods → increase memory limits, then investigate memory leaks with pprof" |

### 2. Extraction Pipeline

```
Knowledge Cluster (from enhancement 090)
        |
        v
+----------------------------------+
| Cluster Summary Builder          |  Deterministic — no LLM
| Assemble extraction prompt:      |
| - cluster label + keywords       |
| - top-N representative entries   |
|   (closest to centroid)          |
| - cluster trend + member count   |
+----------------------------------+
        |
        v  (~500 tokens per cluster)
+----------------------------------+
| LLM Skill Extractor             |  One LLM call per cluster
| Structured output (JSON mode):   |
| - skill type                     |
| - title                          |
| - description                    |
| - steps (if procedure)           |
| - conditions (if decision)       |
| - confidence                     |
| - source entry IDs               |
+----------------------------------+
        |
        v
+----------------------------------+
| Skill Diff                       |  Deterministic — no LLM
| Compare new skills to existing:  |
| - semantic similarity of titles  |
| - source entry overlap           |
| → update, create, or keep        |
+----------------------------------+
        |
        v
+----------------------------------+
| Skill Store                      |
| - skill_id, user_id             |
| - source_cluster_id             |
| - type, title, description      |
| - steps[], conditions           |
| - confidence, version           |
| - created_at, updated_at        |
+----------------------------------+
```

### 3. When Extraction Runs

Skill extraction is **triggered by cluster changes**, not by a separate polling interval:

1. The clustering goroutine (enhancement 090) completes a cycle.
2. If any clusters were born or updated (new members, keyword changes), they are queued for skill extraction.
3. The skill extractor processes the queue, one LLM call per changed cluster.
4. Unchanged clusters are not re-processed — their skills remain as-is.

This means:
- **No LLM calls when nothing changes.** If no new conversations happen, no extraction runs.
- **LLM calls are proportional to topic changes, not to data volume.** Adding 100 entries that all fall into 2 existing clusters produces at most 2 LLM calls.
- **Extraction is eventually consistent.** Skills may lag behind the latest entries by one clustering cycle + extraction time.

### 4. Extraction Prompt

The LLM receives a structured prompt per cluster:

```
You are analyzing a knowledge cluster from a user's conversation history.
The cluster has been automatically identified by topic — your job is to
extract reusable skills: procedures, decisions, tool usage patterns, and
problem-solution mappings.

## Cluster
- Label: {{label}}
- Keywords: {{keywords}}
- Trend: {{trend}}
- Member count: {{member_count}}

## Representative Entries (closest to topic centroid)
{{#each centroid_entries}}
Entry {{@index}}:
  Role: {{role}}
  Content: {{content_preview}}
{{/each}}

## Instructions
Extract skills from these entries. For each skill, provide:
- type: "procedure" | "decision" | "tool_usage" | "problem_solution"
- title: short descriptive name
- description: one-sentence summary
- steps: ordered list (for procedures) or null
- conditions: when this applies (for decisions) or null
- confidence: "high" | "medium" | "low" based on how clearly
  the pattern appears in the entries

Return a JSON array of skills. Return an empty array if no clear
skills can be extracted.
```

The prompt is ~200 tokens of boilerplate + ~300 tokens of cluster content = ~500 tokens input. A typical response is ~200-500 tokens. Total cost per cluster: < 1,000 tokens.

### 5. Skill Diffing

When extraction re-runs for an updated cluster, the new skills must be compared to existing skills for that cluster:

| Case | Detection | Action |
|------|-----------|--------|
| **Same skill, refined** | Title similarity > 0.8 (embedding cosine) AND source cluster matches | Update existing skill, increment version |
| **New skill** | No existing skill with similar title from same cluster | Create new skill |
| **Skill no longer supported** | Existing skill's source cluster died or lost relevant entries | Mark confidence as `low`, keep for one more cycle, then delete |
| **Unchanged** | Title + steps match exactly | No-op |

### 6. Skill Data Model

```go
type Skill struct {
    ID              uuid.UUID
    UserID          string
    SourceClusterID uuid.UUID
    Type            string    // "procedure", "decision", "tool_usage", "problem_solution"
    Title           string
    Description     string
    Steps           []string  // ordered steps (for procedures), nil otherwise
    Conditions      string    // when this applies (for decisions), empty otherwise
    Confidence      string    // "high", "medium", "low"
    Version         int       // incremented on each refinement
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

### 7. Storage Schema

```sql
CREATE TABLE knowledge_skills (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           TEXT        NOT NULL,
    source_cluster_id UUID        NOT NULL REFERENCES knowledge_clusters(id) ON DELETE CASCADE,
    type              TEXT        NOT NULL,  -- procedure, decision, tool_usage, problem_solution
    title             TEXT        NOT NULL,
    description       TEXT        NOT NULL,
    steps             JSONB,                 -- ordered list for procedures
    conditions        TEXT,                  -- applicability conditions for decisions
    confidence        TEXT        NOT NULL DEFAULT 'medium',
    version           INTEGER     NOT NULL DEFAULT 1,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX knowledge_skills_user_idx     ON knowledge_skills (user_id);
CREATE INDEX knowledge_skills_cluster_idx  ON knowledge_skills (source_cluster_id);
CREATE INDEX knowledge_skills_type_idx     ON knowledge_skills (user_id, type);
```

### 8. REST API

#### GET /v1/knowledge/skills — List Skills

```
GET /v1/knowledge/skills?type=procedure&cluster_id=<uuid>
```

| Parameter | Type | Notes |
|-----------|------|-------|
| `type` | string | Filter by skill type |
| `cluster_id` | UUID | Filter by source cluster |
| `limit` | integer | Default 20, max 100 |

Response `200 OK`:

```json
{
  "skills": [
    {
      "id": "<uuid>",
      "type": "procedure",
      "title": "Optimize slow PostgreSQL queries",
      "description": "Step-by-step approach for diagnosing and fixing slow database queries.",
      "steps": [
        "Run EXPLAIN ANALYZE on the slow query",
        "Check for missing indexes on filtered/joined columns",
        "Create indexes using CONCURRENTLY for zero-downtime",
        "Consider read replicas for analytics workloads"
      ],
      "confidence": "high",
      "version": 2,
      "source_cluster": {
        "id": "<uuid>",
        "label": "postgresql, query, index"
      },
      "updated_at": "2026-03-24T14:30:00Z"
    }
  ]
}
```

#### POST /v1/knowledge/skills/search — Semantic Skill Search

```json
{
  "query": "how to handle slow database queries",
  "limit": 5
}
```

Returns skills ranked by semantic similarity to the query (embed query, compare to skill title/description embeddings).

#### GET /v1/knowledge/skills/{skillId} — Skill Detail

Returns the full skill with source cluster context and entry citations.

### 9. MCP Tools

| Tool | Description |
|------|-------------|
| `list_skills` | List extracted skills for the user. Optional filters: type, cluster_id. |
| `search_skills` | Semantic search over skills by query. Returns ranked results. |
| `trigger_skill_extraction` | Force skill extraction for all changed clusters. |

### 10. Configuration

| Setting | Default | Notes |
|---------|---------|-------|
| `knowledge.skills.enabled` | false | Feature gate (requires `knowledge.clustering.enabled`) |
| `knowledge.skills.llm_provider` | `openai` | LLM provider for extraction |
| `knowledge.skills.llm_model` | `gpt-4o-mini` | Model for extraction (cheap + structured output) |
| `knowledge.skills.max_entries_per_prompt` | 5 | Representative entries sent to LLM per cluster |
| `knowledge.skills.max_skills_per_cluster` | 10 | Maximum skills extracted per cluster |
| `knowledge.skills.min_cluster_members` | 5 | Don't extract skills from very small clusters |

### 11. LLM Provider Interface

```go
type SkillLLM interface {
    ExtractSkills(ctx context.Context, prompt string) ([]ExtractedSkill, error)
}
```

Implementations:
- OpenAI (gpt-4o-mini with JSON mode)
- Anthropic (Claude Haiku with tool use for structured output)
- Configurable via `knowledge.skills.llm_provider` + `knowledge.skills.llm_model`

This reuses the existing embedding provider pattern (`internal/registry/embed/plugin.go`) but for generative calls. It could also reuse the OpenAI client already present in the embed plugin.

## Testing

### Cucumber BDD Scenarios

```gherkin
Feature: Skill Extraction from Knowledge Clusters

  Background:
    Given a memory service is running with clustering and skill extraction enabled
    And user "alice" is authenticated

  Scenario: Skills are extracted from a mature cluster
    Given alice has a knowledge cluster about "PostgreSQL optimization" with 8 members
    And the cluster has representative entries about EXPLAIN ANALYZE, indexes, and replicas
    When skill extraction runs for the cluster
    Then at least one skill exists with type "procedure"
    And the skill title contains "PostgreSQL" or "query" or "optimization"
    And the skill has at least 2 steps

  Scenario: Skills update when cluster grows
    Given alice has a skill "Optimize slow queries" at version 1
    When alice adds entries about "query plan caching" to the same topic
    And the clustering goroutine completes a cycle
    And skill extraction runs for the updated cluster
    Then the skill version is 2
    And the skill steps may include the new pattern

  Scenario: Skills are not extracted from small clusters
    Given alice has a knowledge cluster with only 2 members
    When skill extraction runs
    Then no skills are extracted for that cluster

  Scenario: Skills are user-scoped
    Given alice has extracted skills
    And user "bob" is authenticated
    When bob lists skills
    Then bob sees no skills from alice

  Scenario: Skill search returns relevant results
    Given alice has a procedure skill about "Kubernetes deployment"
    When alice searches skills for "how to deploy a service"
    Then the results include the "Kubernetes deployment" skill

  Scenario: Skills are deleted when source cluster dies
    Given alice has a skill linked to cluster "X"
    When cluster "X" dies in a clustering cycle
    Then the skill linked to cluster "X" is deleted

  Scenario: No LLM calls when clusters are unchanged
    Given alice has existing clusters with extracted skills
    And no new entries have been added
    When the clustering goroutine completes a cycle
    Then no skill extraction LLM calls are made
```

### E2E Integration: Agent Uses Skills Proactively

```gherkin
Feature: Proactive agent behavior via skills

  Scenario: Agent suggests a known procedure
    # Alice has worked with PostgreSQL optimization before.
    # Skills have been extracted from her knowledge clusters.

    Given alice has a procedure skill:
      | title | Optimize slow PostgreSQL queries |
      | steps | 1. EXPLAIN ANALYZE the query     |
      |       | 2. Check for missing indexes     |
      |       | 3. CREATE INDEX CONCURRENTLY     |
      |       | 4. Consider read replicas        |

    # A new conversation starts. Alice mentions a slow query.
    When alice starts a new conversation with "My orders query is taking 5 seconds"

    # The agent queries skills before responding.
    And the agent calls GET /v1/knowledge/skills/search with query "slow query"
    Then the agent receives the "Optimize slow PostgreSQL queries" skill

    # The agent can now proactively suggest the user's established workflow
    # instead of starting from scratch.
    And the agent response includes steps from the skill
    And the total tokens consumed is less than querying all raw entries
```

### Unit Tests (Go)

- `internal/knowledge/skill_extractor_test.go`
  - `TestBuildExtractionPrompt` — prompt contains cluster label, keywords, and representative entries
  - `TestParseSkillResponse` — valid JSON response produces skills
  - `TestParseSkillResponse_Empty` — empty array response produces no skills
  - `TestParseSkillResponse_Malformed` — graceful handling of malformed LLM output
  - `TestSkillDiff_NewSkill` — skill not matching any existing produces create
  - `TestSkillDiff_UpdatedSkill` — similar title + same cluster produces update with version bump
  - `TestSkillDiff_UnchangedSkill` — identical title + steps produces no-op
  - `TestSkillDiff_OrphanedSkill` — skill whose cluster died is marked for deletion
- `internal/knowledge/skill_store_test.go`
  - `TestSaveAndLoadSkills` — round-trip persistence
  - `TestSkillsUserScoped` — user isolation

## Tasks

### Phase 1 — Skill Extractor Core

- [ ] Define `SkillLLM` interface (`internal/knowledge/skill_llm.go`)
- [ ] Implement OpenAI skill extractor (JSON mode, gpt-4o-mini)
- [ ] Build extraction prompt assembler (cluster → prompt)
- [ ] Parse structured LLM response into `[]ExtractedSkill`
- [ ] Skill diff logic (new/update/delete by title similarity + cluster match)

### Phase 2 — Storage and Lifecycle

- [ ] Add `knowledge_skills` PostgreSQL table
- [ ] Implement SkillStore interface and PostgreSQL backend
- [ ] Wire skill extraction into clusterer (post-diff hook, queue changed clusters)
- [ ] Feature gate configuration (`knowledge.skills.enabled`)
- [ ] Skill deletion on source cluster death (FK cascade)

### Phase 3 — REST API

- [ ] `GET /v1/knowledge/skills` — list skills with filtering
- [ ] `GET /v1/knowledge/skills/{id}` — skill detail
- [ ] `POST /v1/knowledge/skills/search` — semantic skill search
- [ ] Add endpoints to `contracts/openapi/openapi.yml`
- [ ] User-scoped access control

### Phase 4 — MCP Tools

- [ ] MCP tool `list_skills`
- [ ] MCP tool `search_skills`
- [ ] MCP tool `trigger_skill_extraction`

### Phase 5 — Additional LLM Providers

- [ ] Anthropic (Claude Haiku) skill extractor
- [ ] Configurable provider selection via config

## Files to Modify

| File | Change |
|------|--------|
| `internal/knowledge/skill_llm.go` | **new** — SkillLLM interface |
| `internal/knowledge/skill_extractor.go` | **new** — prompt builder, response parser, diff logic |
| `internal/knowledge/skill_extractor_test.go` | **new** — unit tests |
| `internal/knowledge/skill_store.go` | **new** — SkillStore interface |
| `internal/knowledge/skill_postgres_store.go` | **new** — PostgreSQL implementation |
| `internal/knowledge/clusterer.go` | Add post-diff hook to queue changed clusters for skill extraction |
| `internal/plugin/store/postgres/db/schema.sql` | Add `knowledge_skills` table |
| `internal/plugin/route/knowledge/knowledge.go` | Add skill REST endpoints |
| `internal/cmd/mcp/tools.go` | Add skill MCP tools |
| `internal/config/config.go` | Add `knowledge.skills.*` settings |
| `internal/cmd/serve/serve.go` | Add skill extraction CLI flags |
| `internal/cmd/serve/server.go` | Wire skill extractor |
| `contracts/openapi/openapi.yml` | Add `/v1/knowledge/skills/*` endpoints |

## Verification

```bash
# Compile
go build ./...

# Unit tests
go test ./internal/knowledge/... -v -count=1

# Full test suite
task test:go > test.log 2>&1
```

## Non-Goals

- Real-time skill extraction on the hot path — extraction runs as a background process triggered by cluster changes.
- User-editable skills — skills are derived state, not user-authored. Users curate knowledge through conversations; the system derives skills from those conversations.
- Cross-user skill sharing — skills are user-scoped. Sharing mechanisms (e.g., team skill libraries) are a future enhancement.
- Deterministic-only extraction — skill extraction intentionally uses an LLM because causal/sequential pattern recognition requires comprehension.

## Design Decisions

**Why LLM-assisted instead of pure deterministic?**
Skill extraction requires understanding causal relationships ("do X because Y"), temporal ordering ("first X, then Y"), and abstraction ("this specific fix generalizes to..."). These are comprehension tasks, not distance calculations. Deterministic approaches (n-gram matching, action sequence extraction) produce brittle, low-quality results. The LLM is the right tool — but it runs on pre-structured cluster IR (~500 tokens), not raw conversation data (~15,000 tokens).

**Why one LLM call per cluster, not per entry?**
Enhancement 090 has already organized entries by topic. The LLM doesn't need to figure out what the topic is — it receives a labeled cluster with representative entries. This means each LLM call is small, focused, and cheap. A user with 10 clusters costs ~10,000 tokens total for full skill extraction, versus 150,000+ tokens to process all raw entries.

**Why trigger on cluster changes, not on a timer?**
Skill extraction is expensive (LLM calls). Running it only when clusters actually change (new members, keyword shifts) avoids wasted calls. If a user doesn't add new conversations for a week, zero LLM calls happen during that week.

**Why structured JSON output?**
Skills must be stored as structured data (title, steps, conditions). Using JSON mode / tool use for structured output avoids fragile parsing of free-text LLM responses. Both OpenAI (JSON mode) and Anthropic (tool use) support this reliably.

**Why FK cascade on cluster deletion?**
Skills are derived from clusters. If a cluster dies (its entries are deleted or the topic dissolves), the skills derived from it are no longer grounded in evidence. Cascade deletion keeps the skill store clean automatically.

## Open Questions

1. **Skill embedding**: Should skills get their own embeddings (title + description) for semantic search? Or is searching by source cluster sufficient?

2. **Skill confidence decay**: Should skill confidence decrease over time if the source cluster becomes `decaying`? This would deprioritize stale skills without deleting them.

3. **Multi-cluster skills**: Some skills span multiple clusters (e.g., "deploy a Python service to Kubernetes" touches both the Python and K8s clusters). Should the extractor detect cross-cluster patterns, or is per-cluster extraction sufficient?

4. **User feedback loop**: Should users be able to thumbs-up/thumbs-down extracted skills to improve future extraction? This would require storing feedback and adjusting the prompt or post-processing.

5. **Cost guardrails**: Should there be a per-user or per-cycle token budget for skill extraction LLM calls? This prevents runaway costs for users with many rapidly-changing clusters.
