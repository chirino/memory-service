---
status: proposed
---

# Enhancement 099: Quarkus + LangChain4j Cognition Processor

> **Status**: Proposed.

## Summary

Build a reference cognition processor as a standalone Quarkus application that uses LangChain4j as its model abstraction. The processor consumes substrate events from Memory Service over gRPC, builds bounded evidence packs, runs LangChain4j-backed structured extraction and verification, and writes durable derived memories plus short-lived retrieval/cache state back through gRPC episodic memory APIs. Agent applications retrieve cognition output through enhanced memory search.

## Motivation

`docs/memory-cognition.md` defines a clean architectural split between the Memory Service substrate and a pluggable cognition layer that interprets, consolidates, and injects memory. The substrate is the durable system of record; cognition is meant to evolve faster, run as one or more replaceable processors, and be evaluated against shared evidence.

Today the JVM ecosystem already has the building blocks for a high-quality cognition runtime:

- the Memory Service has gRPC conversation, entry, event, search, and episodic-memory services, with the remaining gRPC parity work tracked by [101](101-grpc-api-parity-for-cognition.md)
- generated gRPC stubs exist in `java/quarkus/memory-service-proto-quarkus`
- `io.quarkiverse.langchain4j:quarkus-langchain4j-openai` (already used by `java/quarkus/examples/chat-quarkus`) gives us declarative `@RegisterAiService` interfaces, structured JSON output, prompt caching, and a pluggable model layer
- Quarkus provides the operational primitives we need (scheduler, gRPC clients, virtual threads, dev services, health, metrics)

What is missing is a concrete cognition processor implementation that turns substrate events into durable derived memories and retrieval-ready working notes with replayable provenance. The failure mode to avoid is a naive "run one prompt after every append and save the answer" design, which produces low-precision memory because it:

- extracts from too much raw text and too little curated evidence
- has weak provenance and poor debuggability
- creates duplicate or contradictory memories on replay
- couples prompt assembly to extraction latency
- makes benchmarking across processors difficult

A high-quality cognition processor must optimize for precision first, then recall. Memories must be supported by cited evidence, scoped correctly, and worth surfacing again later.

## Design

### Recommendation

Build the reference processor as a **standalone Quarkus application** (`java/quarkus/cognition-processor-quarkus`) that talks to the Memory Service entirely through gRPC APIs. The same pipeline runs in two operational modes:

- **Worker mode** (default): subscribes to the gRPC event stream, persists a replay cursor through gRPC checkpoints, processes coalesced scope jobs, and writes derived memories through gRPC memory APIs.
- **Replay mode**: rebuilds memory state from a stored cursor or a selected event/time window.

Durable facts, preferences, procedures, decisions, rolling summaries, bridge notes, topic notes, and any cached retrieval candidates are all stored as memory items under fixed cognition namespaces. Agent applications fetch the next-turn memory material with enhanced memory search, using the substrate-level retrieval improvements defined by [100](100-enhanced-memory-search.md). This keeps cognition responsible for producing better memory, while the Memory Service remains the single retrieval API for memory products.

The standalone worker authenticates as a dedicated **service principal** (for example `cognition-processor`) and writes cognition memories through gRPC memory APIs on behalf of conversation owners. The built-in episodic memory policy only allows `["user", <subject>, ...]` access for the authenticated subject, so deploying this processor requires the service-principal and on-behalf-of gRPC policy work from [101](101-grpc-api-parity-for-cognition.md). That policy is a Phase 1 prerequisite, not optional later hardening.

### Processor Contract

The processor exposes two cooperating capabilities:

1. **Asynchronous consolidation** — observe substrate events, coalesce them into impacted scopes, extract and verify candidate memories, upsert/archive derived memories idempotently.
2. **Retrieval materialization** — write short-lived summaries, bridge notes, topic notes, and candidate memories so the normal memory search API can retrieve next-turn context without running cognition code on the agent hot path.

```java
public interface CognitionProcessor {
    String runtimeId();

    Uni<Void> process(ScopeJob job);
}
```

`ScopeJob` is a coalesced unit of work. The initial scope is one conversation; later phases may add user-level or `(user, agent)` scope jobs once conversation identity migration in [089](partial/089-single-agent-conversations.md) is more complete.

### Module Layout

```
java/quarkus/
  cognition-processor-quarkus/             # standalone Quarkus application
    src/main/java/io/github/chirino/cognition/
      runtime/                             # Processor impl, scope jobs, scheduler
      evidence/                            # Evidence pack builders + registry
      extractor/                           # LangChain4j AiService extractors
      verifier/                            # LangChain4j AiService verifiers
      consolidator/                        # Idempotent merge / supersede / archive
      retrieval/                           # Search payload shaping and retrieval metadata
      cache/                               # Cognition cache namespace helpers
      remote/                              # gRPC event, checkpoint, conversation, entry, and memory clients
      config/                              # @ConfigMapping types
    src/main/resources/
      application.properties
    src/test/java/...                      # unit + Quarkus integration tests
```

The processor depends on the existing modules:

- `java/quarkus/memory-service-proto-quarkus` — generated gRPC stubs

LangChain4j is wired via `io.quarkiverse.langchain4j:quarkus-langchain4j-openai`. The actual provider is pluggable: switching to Anthropic or Gemini is a configuration change once their Quarkus extensions are added as optional dependencies.

### Event Ingress and Scheduling

The processor consumes replayable business events from the substrate, reduces them to impacted scopes instead of processing one event at a time, enqueues coalesced jobs with retry semantics, and processes those jobs out of band.

Triggers:

- `entry.created` and `entry.updated` events
- `conversation.created` and `conversation.updated` events when archive or fork changes visible transcript shape
- manual rebuild requests for a user, conversation group, or namespace
- optional periodic sweeps for user-owned episodic memory namespaces that should invalidate cognition state

Phase 1 does not depend on episodic memory lifecycle events as a primary trigger source. The replayable gRPC admin event stream defined by [101](101-grpc-api-parity-for-cognition.md) covers conversation, entry, response, and membership events but not memory-lifecycle events. The processor therefore treats same-user episodic memories as pull-time evidence during a scope job, and uses manual rebuilds or periodic sweeps when user-authored memory changes must force reprocessing.

Implementation details:

- A `GrpcAdminEventClient` consumes `EventStreamService.SubscribeEvents` with admin scope, exponential backoff, and resume-from-cursor semantics.
- The processor must use gRPC checkpoint APIs from [101](101-grpc-api-parity-for-cognition.md) to persist its event progress. On startup it calls `AdminCheckpointService.GetCheckpoint` and, when a checkpoint exists, subscribes with `after_cursor=<lastEventCursor>` so restart catch-up begins near the last processed event instead of replaying the full retained window. A missing checkpoint means first run; subscribe from the configured bootstrap position.
- After events are accepted into dirty debounce state or a dispatchable scope-job queue, the processor writes a checkpoint with `AdminCheckpointService.PutCheckpoint`. The checkpoint value includes at least `lastEventCursor`, `updatedAt`, `runtimeId`, `runtimeVersion`, the highest event timestamp observed, and the current bounded set of dirty conversation debounce windows. `{workerId}` is supplied by `cognition.worker.id` and identifies one logical processor instance, not one container replica.
- Checkpoint writes may be batched on a configurable cadence, but shutdown and idle transitions should flush the latest accepted cursor plus any open debounce windows. The processor must not advance the checkpoint past an event that has not been reduced into durable checkpoint state, a durable/retryable job, or a completed scope job, or restart could skip work.
- Incoming events are reduced to dirty conversation debounce windows keyed by conversation ID. Each window records the first and latest accepted event cursors, first and latest source entry IDs when available, entry count, first observed time, latest observed time, and the due time computed from the debounce configuration. Duplicate events for the same conversation extend the existing window rather than creating an immediate LLM job.
- A debounce scheduler promotes dirty conversation windows into `ScopeJob` records after `cognition.scheduler.debounce-delay`, when `cognition.scheduler.max-batch-age` is reached, or when `cognition.scheduler.max-batch-entries` is reached. This lets active conversations batch several new entries into one extraction/verifier cycle while bounding cognition freshness lag.
- Checkpoint-embedded debounce windows are intentionally bounded. If the serialized checkpoint would exceed `cognition.scheduler.max-checkpoint-windows`, the processor should promote the oldest due windows before accepting more event cursors rather than letting checkpoint state grow without limit.
- Scope jobs are dispatched on virtual threads (`@RunOnVirtualThread`) so blocking gRPC calls do not consume reactive event-loop capacity.
- `@Scheduled` triggers run optional periodic sweeps and rebuild requests.

### Evidence Pack Builder

Before any LLM call, the processor builds a bounded evidence pack. This is the core quality control step.

Evidence packs include:

- a stable, periodically compacted conversation evidence base when one exists
- a small recent transcript delta for the impacted conversation, loaded through `EntriesService.ListEntries`
- the latest per-conversation `context` checkpoint if present
- relevant episodic memories under the same user scope, queried through gRPC memory search
- related derived memories already written by this runtime
- optional knowledge-cluster signals from [090](090-adaptive-knowledge-clustering.md) when clustering is enabled

Evidence packs are not just the last user prompt and not the full raw conversation history. The processor uses a three-layer prompt shape:

1. **Stable stage prefix** — extractor/verifier instructions, JSON schema, output rules, and safety constraints.
2. **Stable conversation evidence base** — a periodically compacted, cited summary of the conversation state that changes only when the transcript delta crosses configurable size or age thresholds.
3. **Dynamic evidence delta** — the newest entries since the compacted base, retrieved memories, related cognition memories, and any per-job scoring hints.

This preserves multi-turn evidence while keeping most repeated conversation context in a cache-friendly prefix. The dynamic delta should stay small enough that normal event-driven jobs do not resend the full conversation.

Evidence packs are aggressively bounded:

- deduplicate repeated content
- drop low-signal assistant boilerplate
- keep only cited tool outputs or excerpts, not full logs
- cap the compacted evidence base and recent transcript delta before extraction
- include stable identifiers for every source item

Before any heuristic stage, the bounded evidence text is normalized into prose:

- strip fenced code blocks and obviously code-like lines
- keep natural-language lines that mention commands as part of prose
- avoid extracting durable memory directly from shell transcripts, stack traces, or source blocks unless a later model-assisted stage explicitly cites them

Evidence pack assembly is exposed as a registry of CDI-managed `@ApplicationScoped` `EvidenceLoader` beans so additional sources can be plugged in without changing the runtime. The registry supports a `cognition.profile` selector so replay and benchmark runs can swap loaders side by side.

#### Compacted evidence bases

Full evidence packs are rebuilt from the substrate on every scope job and held only in memory while the extractor and verifier run. The exception is the **compacted conversation evidence base**, a TTL-backed cognition cache entry that stores a concise, cited, normalized view of older conversation evidence so repeated extraction calls can reuse a stable prefix.

The compacted evidence base is stored under `evidence-base:<conversation-id>` in the cognition cache namespace. It is not a raw transcript dump. It contains concise natural-language claims, source identifiers, token counts, and compaction metadata. It must omit fenced source blocks, full tool logs, provider prompts, and provider request/response payloads.

The evidence base is refreshed when any of these conditions hold:

- the uncached transcript delta exceeds `cognition.evidence.delta.max-entries` or `cognition.evidence.delta.max-tokens`
- the evidence base is older than `cognition.evidence.compaction.max-age`
- a replay or manual rebuild explicitly requests recompaction
- relevant source entries or memories have been archived or materially updated

After compaction, subsequent extractor/verifier calls include the stable stage prefix, the compacted evidence base, and only the small delta since the base cursor. This is intentionally cache-friendly for providers that support prompt-prefix caching, while still allowing correctness to depend on source citations rather than opaque cached content.

What is persisted is the minimum needed to make extraction replayable and auditable:

- a **`provenance.source_hash`** computed over the canonicalized evidence pack — written onto every durable cognition memory so consolidation can no-op on replay when the same evidence produces the same candidate
- **citation identifiers** — `provenance.conversation_ids`, `provenance.entry_ids`, and `provenance.memory_ids` on every durable cognition memory, pointing back at the substrate rows the candidate was supported by
- **runtime attribution** — `runtime.id` and `runtime.version` so a given memory can be traced to the processor build that produced it
- **compaction attribution** — `provenance.evidence_base_id` and `provenance.evidence_base_hash` when a candidate used a compacted evidence base

This is sufficient for replay (recompute the pack, hash, compare) and for audit (follow citations back to the substrate). It deliberately leaves out raw full transcripts, the prose-normalization decisions, and the per-stage prompt that the model actually saw.

If a deployment needs deeper post-hoc inspection, two opt-in extensions are available without changing the durable storage shape:

- **debug evidence dumps** — gated by `cognition.debug.persist-evidence=true`, the runtime can write the assembled pack to the cognition cache namespace under `evidence:<conversation-id>:<source-hash>` with a short TTL. This is intended for troubleshooting bad extractions, not steady-state operation.
- **evidence manifests** — an optional compact manifest (ordered source IDs, per-source token counts, normalization flags) embedded in `provenance` alongside `source_hash`. Small, replayable, and free of raw content.

Both are explicit non-defaults so steady-state runs do not accumulate raw redundant copies of substrate data.

### Model-Backed Extraction Pipeline

The processor uses a staged pipeline, not a single monolithic prompt. All durable memory extraction is model-backed; deterministic extractors only produce short-lived cache aids.

Stages:

1. **Structured extraction** — one batched LangChain4j `@RegisterAiService` call covers `fact`, `preference`, `procedure`, `problem_solution`, and `decision` candidates over the bounded evidence pack. A separate batched call produces a `topic_summary` cache note. Strict JSON output is enforced via LangChain4j's response schema support and verified again with a Jackson validator.
2. **Verification** — one batched verifier call checks all durable candidates against their cited evidence, rejects unsupported or weakly-supported items, and normalizes language into concise, stable statements.
3. **Deterministic consolidation** — verified candidates are compared against existing derived memories, duplicates are merged, stale or contradicted items are superseded, and freshness/confidence updates rewrite in place rather than appending.
4. **Cache-only heuristic notes** — lightweight deterministic extractors produce cache-only `bridge` and `topic` notes for retrieval scoring. These are not durable memories and do not require a model provider.

LangChain4j AiService interfaces:

```java
@RegisterAiService(modelName = "memory")
public interface DurableMemoryExtractor {

    @SystemMessage(fromResource = "/prompts/durable-extractor-system.md")
    @UserMessage(fromResource = "/prompts/durable-extractor-user.md")
    DurableExtractionResponse extract(EvidencePack pack);
}

@RegisterAiService(modelName = "memory")
public interface DurableMemoryVerifier {

    @SystemMessage(fromResource = "/prompts/durable-verifier-system.md")
    DurableVerificationResponse verify(VerificationRequest request);
}

@RegisterAiService(modelName = "topic-summary")
public interface TopicSummaryExtractor {

    @SystemMessage(fromResource = "/prompts/topic-summary-system.md")
    TopicSummaryResponse summarize(TopicSummaryRequest request);
}
```

Prompt layout is token-aware and cache-friendly:

- one batched durable-extraction prompt with a stable system prefix, the stable compacted evidence base, then the small dynamic evidence delta
- one batched durable-verification prompt with a stable system prefix, the same compacted evidence base, then candidates plus cited dynamic evidence
- one separate `topic_summary` prompt because its output contract is different and its prompt prefix is stable on its own

Each AiService can specify a stable per-stage and per-conversation evidence-base cache identifier so provider adapters can opt in to native prompt-prefix caching:

- OpenAI prompt cache keys via `quarkus.langchain4j.openai.chat-model.user` plus `prompt-cache-key`
- Anthropic cache breakpoints on static system blocks
- Gemini cached system-instruction content

> **Caching is opt-in and must be evaluated, not assumed.** Provider prompt caching pays off when the stable stage prefix plus compacted evidence base is reused across enough extraction, verification, and topic-summary calls to amortize cache write cost. The processor therefore makes repeated conversation evidence cache-friendly by design, but keeps provider caching disabled by default until measured.
>
> The benchmark harness should record per-stage cache hit rate, cached vs. uncached token cost, and end-to-end latency so we can decide per stage and per provider whether caching is on. Default the `cognition.langchain4j.<stage>.prompt-cache.enabled` flags to `false` until the data shows a stage benefits.

Models are routed by **named model configurations** in `application.properties` so durable extraction, verification, and topic summarization can target different models or providers if quality/cost tradeoffs require it.

### Cache-Only Bridge and Topic Notes

Not all retrieval aids should become durable user memories. The processor supports cache-only working notes that improve memory retrieval but never enter the durable store unless an extractor explicitly upgrades them:

- **bridge notes** — explicit current-focus, goal, concern, and relevant background phrases extracted from user turns via lightweight heuristics; conversation-scoped; TTL-backed
- **topic notes** — short TTL-backed retrieval aids built from stronger bridge/procedure-style cues and recent topical phrases

Both live in the cognition cache namespace and are indexed for memory search so query vocabulary can match short-lived working context.

### Memory Types

The initial durable memory types stay narrow:

| Type | Durability | Notes |
| --- | --- | --- |
| `fact` | durable | Stable user/project facts backed by explicit evidence |
| `preference` | durable | Repeated user preferences or defaults |
| `procedure` | durable | Reusable steps or workflows; can feed [091](partial/091-skill-extraction.md) |
| `problem_solution` | durable | Reusable issue-resolution patterns; can feed [091](partial/091-skill-extraction.md) |
| `decision` | durable | Reusable decision rules or selection criteria; can feed [091](partial/091-skill-extraction.md) |
| `bridge` | cache-only | Short-lived current-focus / concern / goal / background notes used only for retrieval |
| `topic` | cache-only | Short-lived topic diary note summarizing recent conversation themes for retrieval only |
| `summary` | rolling | Conversation or project summary stored in a short-lived cognition cache |

Graph memories, relationship graphs, and broad autonomous world models are explicit non-goals for the first iteration.

### Storage Strategy

The processor reuses the existing substrate instead of creating a separate derived-memory datastore.

#### Durable outputs: memory APIs

Derived durable memories are stored under fixed user-owned namespaces so existing governance, namespace-depth limits, archive semantics, and vector indexing still apply:

```text
["user", <sub>, "cognition.v1", "facts"]
["user", <sub>, "cognition.v1", "preferences"]
["user", <sub>, "cognition.v1", "procedures"]
["user", <sub>, "cognition.v1", "problem_solutions"]
["user", <sub>, "cognition.v1", "decisions"]
```

`runtime.id` remains part of the stored memory payload for attribution and debugging, but it does not partition the namespace layout.

Recommended value shape:

```json
{
  "kind": "fact",
  "title": "Preferred editor",
  "statement": "The user prefers Neovim for local editing.",
  "scope": {
    "level": "user",
    "conversation_group_id": "optional-uuid"
  },
  "confidence": "high",
  "freshness": "stable",
  "provenance": {
    "conversation_ids": ["uuid"],
    "entry_ids": ["uuid"],
    "memory_ids": [],
    "source_hash": "sha256:..."
  },
  "runtime": {
    "id": "quarkus-reference-v1",
    "version": 1
  },
  "observed_at": "2026-04-29T12:00:00Z",
  "updated_at": "2026-04-29T12:00:00Z"
}
```

Recommended index payload:

```json
{
  "statement": "prefers neovim local editing terminal workflow",
  "title": "preferred editor neovim"
}
```

This gives us encrypted durable values, existing namespace scoping and archive semantics, vector search via the current episodic memory indexing, and no new core storage engine.

#### Short-lived outputs: TTL-backed cognition cache

Short-lived cognition cache entries hold compacted evidence bases, rolling conversation summaries, retrieval hints, and per-conversation working notes that have not been promoted to durable memory (including cache-only bridge notes).

API-compatible cache namespace shape:

```text
["user", <sub>, "cognition.v1", "cache"]
```

Key prefixes are `evidence-base:<conversation-id>`, `summary:<conversation-id>`, `bridge:<conversation-id>`, `topic:<conversation-id>`, and `candidate:<conversation-id>`. The shared `["user", <sub>, "cognition.v1"]` prefix lets agents retrieve all cognition output with one memory search request while keeping each memory kind in a distinct child namespace. The four-segment layout stays within the default `EpisodicMaxDepth=5`.

External Quarkus workers cannot generically write conversation `context` through today's agent APIs, because context reads/writes are authorized by the conversation's stored `clientId` and the service does not support admin impersonation. The Quarkus processor therefore does not mirror summaries into `context`.

### Consolidation Rules

The consolidator is idempotent and replay-safe:

- each promoted memory gets a stable natural key derived from type, scope, and normalized subject/facet
- each write carries a `source_hash` over the evidence pack so replays can no-op
- memories derived from compacted evidence also carry the compacted evidence base ID/hash so later recompaction can explain changed outputs
- contradictory memories archive or supersede prior rows instead of coexisting indefinitely
- confidence and freshness update in place by rewriting the active memory row for the same natural key
- low-confidence candidates never become durable memories; they may remain only as short-lived retrieval candidates

Concurrency is handled with optimistic compare-and-set on the existing memory `revision`/`version` field. On conflict, the consolidator reloads, replays the merge, and retries up to a small bound before deferring the job.

### Scope Rules

High-quality memory depends on not widening scope incorrectly:

- promote **user-scoped** durable memories first
- keep **conversation-scoped summaries** separate from durable user memories in the short-lived cognition cache
- do not promote cross-client or cross-agent durable memories until the conversation identity migration in [089](partial/089-single-agent-conversations.md) is more complete

This avoids polluting one app/agent's memory with assumptions learned from another scope while the underlying `clientId`/`agentId` storage migration is still partial.

### Quality Controls

The runtime supports two operating modes:

- **active** — writes durable and short-lived cognition memories
- **replay** — rebuilds memory state from a stored cursor or a time window

Each candidate and each retrieval-ready memory exposes:

- why it was included
- which evidence supported it
- which runtime produced it

### Evaluation and Benchmarking

Quality is measured explicitly before broad rollout. A replay harness feeds the same event window to multiple runtimes and records:

- extraction precision
- contradiction rate
- duplicate churn rate
- retrieval hit rate on evaluation prompts
- token cost
- prompt-cache hit rate and cached-token cost when a provider reports them
- latency per scope job

The harness is exposed as a Quarkus CLI subcommand (`mvn quarkus:dev` plus `-Dcognition.benchmark.scenario=...`, or a packaged uber-jar entry point) so scenarios can be run against a local memory-service instance using dev services.

The current [090](090-adaptive-knowledge-clustering.md) implementation is treated as one upstream evidence signal source, not the full cognition system. [091](partial/091-skill-extraction.md) is treated as a downstream consumer of verified procedural memories.

### API Surface

The processor surface is intentionally small at first.

- no new Memory Service cognition-specific API
- inspect durable outputs through existing memory APIs under cognition namespaces
- retrieve cognition-produced memories through the enhanced memory search contract defined by [100](100-enhanced-memory-search.md)
- operational status, rebuild triggers, and retrieval-debug output remain local Quarkus processor concerns, not Memory Service substrate APIs

The search request/response shape, pushdownable positive filter language, bounded result semantics, and safe retrieval attributes are owned by [100](100-enhanced-memory-search.md). This processor writes memory values compatible with that generic retrieval API and expresses retrieval as positive allow-lists, such as allowed `memoryKind`, `confidence`, and `freshness` values, rather than negative exclusion filters.

### Configuration

All configuration is exposed via Quarkus `@ConfigMapping` types under the `cognition.*` prefix.

```properties
# Identity / connectivity
cognition.worker.id=cognition-worker-1
cognition.runtime.id=quarkus-reference-v1
cognition.memory-service.grpc-target=memory-service:9090
cognition.memory-service.api-key=${COGNITION_API_KEY}

# Operating mode
cognition.mode=active                       # active | replay
cognition.profile=default                   # selects evidence/extractor/verifier registry filters

# Pipeline shape
cognition.evidence.base.max-tokens=6000
cognition.evidence.compaction.max-age=PT2H
cognition.evidence.delta.max-entries=12
cognition.evidence.delta.max-tokens=1500
cognition.evidence.episodic.max-memories=20
cognition.consolidation.max-revision-retries=3

# Dirty conversation debounce / batching
cognition.scheduler.debounce-delay=PT1M
cognition.scheduler.max-batch-age=PT5M
cognition.scheduler.max-batch-entries=24
cognition.scheduler.max-checkpoint-windows=1000
cognition.scheduler.max-concurrent-jobs=8

# Debug-only: persist assembled evidence packs to the cognition cache for inspection
cognition.debug.persist-evidence=false
cognition.debug.persist-evidence.ttl=PT1H

# Cache TTLs
cognition.cache.evidence-base.ttl=PT24H
cognition.cache.summary.ttl=PT2H
cognition.cache.candidate.ttl=PT15M
cognition.cache.bridge.ttl=PT45M
cognition.cache.topic.ttl=PT2H

# LangChain4j model routing (per stage)
quarkus.langchain4j.openai.memory.api-key=${OPENAI_API_KEY}
quarkus.langchain4j.openai.memory.chat-model.model-name=gpt-4o-mini
quarkus.langchain4j.openai.topic-summary.api-key=${OPENAI_API_KEY}
quarkus.langchain4j.openai.topic-summary.chat-model.model-name=gpt-4o-mini
```

Worker mode is the default. Replay processing is enabled by mode; the user-facing retrieval path remains memory search.

### Compose Integration

The repo `compose.yaml` already runs a separate cognition processor service against the main `memory-service` container. The Quarkus processor publishes a Docker image (`Dockerfile` in the new module) and replaces the existing entry by:

- consuming the gRPC admin event stream from the main `memory-service` container with the dedicated service principal `cognition-processor`
- using `COGNITION_WORKER_ID` for checkpoint identity
- writing cognition memories back through gRPC memory APIs

The compose service depends on `memory-service` plus its vector backend (`qdrant`/`pgvector`) and uses TCP-based readiness probes that match the dev images already in use.

## Design Decisions

### Standalone Quarkus Application, Not an Embedded Extension

`docs/memory-cognition.md` is directionally clear: cognition should evolve faster than the substrate. Shipping the processor as a separate Quarkus application keeps that separation strict, makes replay benchmarking straightforward, and avoids forcing memory-service deployments to take cognition release cadence.

### LangChain4j as the Model Layer

LangChain4j gives us declarative AiService interfaces, structured output, prompt caching, and a pluggable provider model that already aligns with how `chat-quarkus` integrates models. Reusing it avoids building a second model abstraction inside this repo and keeps the cognition stack consistent with the demo agent.

### Reuse Memory APIs Instead of a New Derived Store

This keeps governance, indexing, archive semantics, and encryption aligned with the rest of the system. Adding substrate extensions is deferred until the current memory primitives prove too weak.

### Reuse Enhanced Memory Search

The proposed cognition outputs are already stored as memory items. This processor depends on [100](100-enhanced-memory-search.md) so cognition-produced facts, preferences, procedures, summaries, bridge notes, and topic notes can be retrieved through the same governed surface as any other memory.

The tradeoff is that agent applications remain responsible for assembling the final LLM prompt from returned memory items and recent conversation entries. That is preferable for now because prompt assembly is application-specific, while retrieval of relevant memory products is a substrate responsibility.

### Use a Verifier Step

The extractor alone should never be trusted to create durable memory. Requiring explicit citations and a verifier pass is the simplest way to improve precision without giving up model-assisted reasoning.

### Use Existing Replay Surfaces in Phase 1

The gRPC admin event stream from [101](101-grpc-api-parity-for-cognition.md) is sufficient as long as cognition jobs are driven by conversation and entry activity plus manual or periodic rebuilds. A dedicated cognition replay feed should not be added until the generic event stream proves too coarse or until memory-lifecycle-triggered reprocessing becomes a measured bottleneck.

### Use Fixed Versioned Cognition Namespaces

The default episodic API validates namespaces against `EpisodicMaxDepth=5`. Using a shared versioned prefix plus kind segment, such as `["user", sub, "cognition.v1", "preferences"]`, keeps cognition storage within that limit and allows a single search under `["user", sub, "cognition.v1"]` to retrieve all cognition memory products.

### Service Principal With Custom Episodic Policy

The default episodic policy only allows `["user", <subject>, ...]` access for the authenticated subject. The Quarkus processor authenticates as a dedicated `cognition-processor` service principal and uses the on-behalf-of gRPC policy support from [101](101-grpc-api-parity-for-cognition.md) to write only under the configured cognition namespaces. This is a Phase 1 prerequisite, not optional later hardening.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Quarkus cognition processor
  Scenario: Durable preference is extracted from repeated evidence
    Given conversation "conv-1" contains turns showing user preference for "neovim"
    And the cognition processor is running in active mode
    When the processor replays admin events for "conv-1"
    Then a memory exists under namespace ["user","alice","cognition.v1","preferences"]
    And the memory value field "statement" contains "Neovim"
    And the memory value field "provenance.entry_ids[0]" is not null

  Scenario: Replay is idempotent
    Given the cognition processor already extracted memories for "conv-1"
    When the same admin event window is replayed again
    Then no duplicate active cognition memory rows are created

  Scenario: Debounced conversation window batches entry events
    Given conversation "conv-5" receives three entry events within the configured debounce delay
    When the debounce window becomes due
    Then the processor runs one scope job for "conv-5"
    And the evidence pack includes all three new entries

  Scenario: Checkpoint restores open debounce windows
    Given conversation "conv-6" has an open debounce window recorded in the processor checkpoint
    When the cognition processor restarts before the window becomes due
    Then it restores the open debounce window from the checkpoint
    And it processes "conv-6" after the configured debounce delay

  Scenario: Weak evidence is not promoted
    Given conversation "conv-2" contains one speculative assistant message without user confirmation
    When the processor runs
    Then no durable cognition memory is created

  Scenario: Memory search prefers relevant durable cognition memory over cache notes
    Given durable cognition memories exist for deployment troubleshooting
    And short-lived cognition cache memories exist for "conv-3"
    When memory search is called with query "deployment fix" across the cognition namespaces and positive filters for retrievable kinds and freshness states
    Then the response contains at least one item
    And the first item value field "kind" should equal "procedure"

  Scenario: Compacted evidence base keeps extraction cache-friendly
    Given conversation "conv-4" has more transcript entries than the configured delta limit
    And a compacted evidence base exists for "conv-4"
    When the processor handles one new entry for "conv-4"
    Then the durable extractor request includes the compacted evidence base before the new-entry delta
    And the durable extractor request does not include the full raw transcript

  Scenario: Cognition writes are scoped to the configured namespaces
    Given the cognition service principal is configured for namespaces under ["user", "*", "cognition.v1"]
    When the processor attempts to write outside those namespaces
    Then the episodic memory API rejects the write with 403
```

### Unit / Quarkus Integration Tests

- `DurableMemoryExtractor` AiService is exercised against a recorded transcript fixture and a stubbed model that returns canned structured output, asserting that all durable kinds round-trip through the candidate schema.
- `DurableMemoryVerifier` rejects candidates whose cited evidence does not appear in the bounded evidence pack.
- `EvidencePackBuilder` deduplicates repeated content, drops fenced code blocks during the prose normalization step, and never exceeds the configured token cap.
- `EvidenceCompactor` creates a cited `evidence-base:<conversation-id>` cache entry, advances the base cursor, and keeps later extraction requests to the configured delta bounds.
- `Consolidator` merges duplicates by stable natural key, supersedes contradicted memories, and produces no-op writes on identical `source_hash` replays.
- Cache-only `bridge` and `topic` notes are written under the cognition cache namespace with the configured TTLs and surface in memory search.
- The gRPC event consumer loads its checkpoint with `AdminCheckpointService.GetCheckpoint`, resumes `EventStreamService.SubscribeEvents` after the saved `lastEventCursor`, restores checkpoint-embedded debounce windows, persists progress with `AdminCheckpointService.PutCheckpoint`, coalesces bursts into singleton scope jobs, and does not lose events across reconnect.
- A Quarkus dev-services-backed integration test boots the full stack (memory-service container plus the processor) and verifies an end-to-end extraction-to-search flow against a `TestChatModel` that mimics the LangChain4j contract used in `chat-quarkus`.

## Tasks

- [ ] Create the `java/quarkus/cognition-processor-quarkus` Maven module with parent wiring, packaging, and Dockerfile.
- [ ] Define the `CognitionProcessor` contract and `ScopeJob` types.
- [ ] Wire the LangChain4j dependency and add named model configurations for durable extraction, verification, and topic summarization.
- [ ] Implement the gRPC event consumer with checkpointed replay against `EventStreamService.SubscribeEvents` and `AdminCheckpointService` from [101](101-grpc-api-parity-for-cognition.md).
- [ ] Implement checkpoint-backed dirty conversation debounce windows and the singleton-per-conversation scope-job queue running on virtual threads.
- [ ] Implement the evidence pack builder registry over transcript, `context`, episodic memory, and optional knowledge-cluster sources.
- [ ] Implement the compacted conversation evidence base with TTL-backed cache storage, source citations, base cursor tracking, and recompaction triggers.
- [ ] Implement the `DurableMemoryExtractor` AiService with strict structured output for fact, preference, procedure, problem_solution, and decision candidates.
- [ ] Implement the `DurableMemoryVerifier` AiService with batched citation checking and normalization.
- [ ] Implement the `TopicSummaryExtractor` AiService and TTL-backed topic-summary cache writes.
- [ ] Implement deterministic consolidation with stable natural keys, `source_hash`-based no-op replay, and supersede semantics.
- [ ] Implement cache-only `bridge` and `topic` heuristic extractors writing under `["user", <sub>, "cognition.v1", "cache"]`.
- [ ] Implement TTL-backed rolling summary and retrieval candidate cache entries.
- [ ] Use the enhanced memory search contract from [100](100-enhanced-memory-search.md) for retrieval examples and integration tests.
- [ ] Update the cognition memory policy so cognition memories expose safe filter attributes.
- [ ] Add the configurable `cognition.profile` selector for evidence/extractor/verifier registry filtering.
- [ ] Add the replay benchmark harness and scenario format.
- [ ] Add Cucumber-driven BDD coverage that drives the processor against a memory-service container with a stubbed LangChain4j model.
- [ ] Provide the dedicated `cognition-processor` service principal and a custom episodic policy that scopes on-behalf-of writes to the configured cognition namespaces.
- [ ] Update `compose.yaml` to run the Quarkus processor image as the cognition service.
- [ ] Update `docs/memory-cognition.md`'s "Relationship to Existing Enhancement Work" list to point at this enhancement.

## Files to Modify

| File | Change |
| --- | --- |
| `docs/enhancements/099-quarkus-cognition-processor.md` | This enhancement doc |
| `docs/memory-cognition.md` | Add a pointer to this enhancement under "Relationship to Existing Enhancement Work" |
| `internal/episodic/policy.go` and configured `attributes.rego` examples | Extract safe cognition attributes from cognition memory values/index payloads |
| `java/pom.xml` | Register the new Quarkus cognition module in the reactor |
| `java/quarkus/pom.xml` | Add the cognition processor module to the Quarkus reactor |
| `java/quarkus/cognition-processor-quarkus/pom.xml` | New module with Quarkus + LangChain4j + memory-service-contracts dependencies |
| `java/quarkus/cognition-processor-quarkus/Dockerfile` | Container image used by `compose.yaml` |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../runtime/*.java` | Processor implementation, scope job dispatch, scheduler |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../evidence/*.java` | Evidence loader registry, compactor, base-cursor tracking, and transcript/context/episodic/cluster loaders |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../extractor/*.java` | LangChain4j durable extractor and topic-summary AiServices, plus heuristic bridge/topic extractors |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../verifier/*.java` | LangChain4j verifier AiService and normalization helpers |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../consolidator/*.java` | Idempotent consolidation, supersede, and natural-key logic |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../retrieval/*.java` | Search payload shaping and retrieval metadata |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../cache/*.java` | Cognition cache namespace helpers and TTL writes |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../remote/*.java` | gRPC event, checkpoint, memory, conversation, and entry clients |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../remote/AdminCheckpointClient.java` | Client wrapper for gRPC checkpoint operations |
| `java/quarkus/cognition-processor-quarkus/src/main/java/.../config/CognitionConfig.java` | `@ConfigMapping` types for the `cognition.*` prefix |
| `java/quarkus/cognition-processor-quarkus/src/main/resources/application.properties` | Default config and named LangChain4j model bindings |
| `java/quarkus/cognition-processor-quarkus/src/main/resources/prompts/*.md` | Stable system/user prompt templates loaded by AiServices |
| `java/quarkus/cognition-processor-quarkus/src/test/java/.../*.java` | Unit and Quarkus integration tests, including a stubbed LangChain4j model and a cucumber runner |
| `compose.yaml` | Replace the existing cognition processor service with the Quarkus image |
| `java/quarkus/FACTS.md` | Record any module-specific gotchas discovered during implementation |

## Verification

```bash
# Compile the new module
./java/mvnw -f java/pom.xml -pl quarkus/cognition-processor-quarkus -am compile

# Run unit tests
./java/mvnw -f java/pom.xml -pl quarkus/cognition-processor-quarkus -am test > test.log 2>&1
# Search for failures using Grep tool on test.log

# Build the runnable jar / native dev image
./java/mvnw -f java/pom.xml -pl quarkus/cognition-processor-quarkus -am package -DskipTests

# Local end-to-end smoke against the dev memory-service
docker compose up -d memory-service qdrant
docker compose up cognition-processor
```

## Non-Goals

- replacing the existing conversation, search, or episodic-memory substrate APIs
- building a general-purpose graph memory system in the first iteration
- exposing raw cluster centroids or embedding-derived internals to non-admin callers
- promoting low-confidence or uncited candidate memories just to improve recall numbers
- sharing process space with the memory-service Go server
- adding cognition-specific status, rebuild, or retrieval-debug APIs to Memory Service

## Security Considerations

- derived memories must remain under the same effective user scope as their evidence
- durable writes must preserve provenance so incorrect memories can be audited and rebuilt
- the Quarkus processor requires a dedicated `cognition-processor` service principal and a tightly-scoped episodic-memory policy that only allows writes under the configured cognition namespaces; the built-in default policy is not sufficient
- cognition memory values and extracted attributes must not include internal `clientId` metadata, raw evidence dumps, provider prompts, or provider cache keys
- compacted evidence bases must be concise cited summaries, not raw transcript mirrors, and must use the same effective user scope as their source evidence
- LangChain4j prompt cache identifiers must be derived from stable, non-sensitive inputs such as runtime ID, stage, conversation ID, and evidence-base hash so cache keys do not leak user evidence into provider logs

## Deferred Evaluation

- Provider prompt-prefix caching must remain disabled by default until the benchmark harness shows a net benefit for a specific stage and provider. The benchmark must report cache hit rate, cache write/read token cost, and end-to-end latency before any stage's `prompt-cache.enabled` flag flips to `true`.

All other Phase 1 interface choices are intentionally decided in this document: use per-memory writes first, and do not add Quarkus dev-service automation until the processor module exists and local developer friction is measured.
