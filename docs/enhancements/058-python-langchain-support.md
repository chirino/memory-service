status: in-progress
---

# Enhancement 058: Python LangGraph/LangChain Support

> **Status**: In Progress.

## Summary

Add a Python client SDK and LangGraph/LangChain integration for the memory service, with accompanying user documentation. This enables Python-based AI agent applications to use the memory service for conversation history and agent state, matching the existing Quarkus/LangChain4j and Spring AI integrations.

**Primary target: LangGraph.** As of **February 22, 2026**, LangChain positions LangGraph as the foundation for durable agent workflows. `RunnableWithMessageHistory` still exists and remains useful for simple/legacy chains, but new durable agent work should target LangGraph interfaces first.

The implemented integration currently provides:

1. **`MemoryServiceCheckpointSaver`** (`BaseCheckpointSaver`): LangGraph short-term memory persisted to the `MEMORY` channel.
2. **`MemoryServiceHistoryMiddleware`** (`AgentMiddleware`): records `HISTORY` entries and streams response chunks/events via gRPC to `ResponseRecorderService` (Quarkus-interceptor equivalent behavior).
3. **`MemoryServiceProxy`** + **`to_fastapi_response(...)`**: proxy-style API surface for conversation/search/fork/share endpoints, analogous to `MemoryServiceProxy.java`.
4. **Request-context helpers**: `install_fastapi_authorization_middleware(...)` + `memory_service_scope(...)` to bind bearer token, conversation, and optional fork metadata.
5. **`MemoryServiceResponseResumer`**: checkpoint 05 helper for resume/check/cancel flow.

`MemoryServiceStore` (`BaseStore`) remains a follow-up/experimental area and is not part of the current GA tutorial checkpoints.

### Implementation Snapshot (2026-02-23)

Implemented in repo now:

1. Python tutorial/checkpoint flow (`01`-`07`) aligned with docs and `site-tests`.
2. Step 1 checkpoint follows LangChain quickstart style and exposes HTTP via FastAPI without importing memory-service modules.
3. Memory-service integration code lives in a single installable package path:
   - `python/langchain` (`memory-service-langchain`, primary integration used by checkpoints and examples)
4. Checkpoints `02`-`07` consume `memory-service-langchain` via local pip-style dependency sources (`tool.uv.sources`) so imports match published-package usage.
5. `python/pom.xml` now runs Python packaging/stub generation in Docker so CI/developers can use Maven + Docker without requiring host Python tooling for package build/verification.

Terminology mapping used in older sections of this document:

| Earlier design name | Implemented name |
|---|---|
| `HistoryRecorder` | `MemoryServiceHistoryMiddleware` |
| `MemoryServiceRouter` | `MemoryServiceProxy` (+ `to_fastapi_response`) |
| "langgraph package is primary" | `python/langchain` package is primary for checkpoint integrations |

## Motivation

The memory service currently supports two framework integrations:

| Framework | Language | Memory Interface | Content Type Tag |
|-----------|----------|------------------|------------------|
| Quarkus + LangChain4j | Java | `ChatMemoryStore` | `LC4J` |
| Spring Boot + Spring AI | Java | `ChatMemoryRepository` | `SpringAI` |

Python is the dominant language for AI/ML development. Without Python support, the memory service is limited to Java ecosystems.

### What Developers Need

1. **REST client**: A typed Python client generated from the OpenAPI spec.
2. **gRPC client**: Python gRPC stubs generated from the proto for live response recording via `ResponseRecorderService`.
3. **LangGraph checkpointer**: A `BaseCheckpointSaver` implementation that persists graph state to the memory service (`MEMORY` channel), replacing the legacy `BaseChatMessageHistory` pattern.
4. **LangGraph store (experimental)**: A `BaseStore` compatibility layer for cross-thread long-term memory until a native `/v1/store` API exists.
5. **History recording**: A recorder that captures user messages and AI streaming responses to the `HISTORY` channel (with live gRPC streaming for resumption support).
6. **API proxy**: A FastAPI router that forwards memory service endpoints to frontend clients with proper authentication pass-through.
7. **LangChain compatibility**: A `BaseChatMessageHistory` implementation for users on the legacy `RunnableWithMessageHistory` pattern.
8. **Documentation**: Getting-started guide and progressive feature tutorials (matching the Spring/Quarkus doc pattern).

### Assumptions Validation Snapshot (2026-02-22)

| Assumption | Status | Notes |
|---|---|---|
| LangGraph is the strategic foundation for durable agents | Confirmed | LangChain documentation and package metadata position LangGraph as the durable/runtime layer. |
| `RunnableWithMessageHistory` should be treated as legacy compatibility | Confirmed (positioning) | Keep support for compatibility, but do not make it the primary path for new agent builds. |
| `BaseCheckpointSaver` methods in active use include `put_writes(..., task_path)` and `delete_thread()` | Confirmed | Design updated to include both methods. |
| `BaseStore` implementation centers on `batch()` / `abatch()` | Confirmed | Higher-level store methods delegate to batch operations. |
| Memory-service search endpoint is `/v1/search` | **Incorrect** | Current contract is `/v1/conversations/search`. |
| gRPC `RecordRequest` field name is `conversationId` | **Incorrect** | Proto field is `conversation_id` (snake_case in Python stubs). |
| LangGraph 1.x supports Python < 3.10 | **Incorrect** | LangGraph 1.x requires Python `>=3.10`; interpreter pinning is required. |

## Design

### Python Environment and Reproducibility

Because this is the first Python surface in this repo, standardize on one workflow from day 1:

- **Primary local/CI workflow**: `uv` with checked-in lockfiles.
- **Docker usage**: for integration dependencies (memory-service, Postgres, Redis, etc.) and optional full-stack CI jobs, not as the primary day-to-day Python package manager.

#### Recommended setup (2026)

1. Pin interpreter with `.python-version` (`3.11.x` recommended; minimum `3.10` because current LangGraph 1.x requires `>=3.10`).
2. Use `pyproject.toml` + `uv.lock` for each Python package.
3. Use `uv sync --frozen` in CI to guarantee reproducible installs from lock.
4. Use dependency groups (`dev`, `test`, `docs`) to keep runtime installs minimal.
5. Run unit tests directly on host (`uv run pytest ...`) and run integration tests against Dockerized services.

Example commands:

```bash
# one-time
uv python install 3.11
uv sync

# day-to-day
./mvnw -pl python verify

# integration (dockerized dependencies)
docker compose -f python/examples/chat-langchain/docker-compose.yml up -d
./mvnw -pl python verify
```


This gives faster local iteration than full Dockerized Python while preserving deterministic execution via lockfiles.

### Module Structure

```text
python/
├── langchain/                          # Implemented: primary integration package
│   ├── pyproject.toml
│   └── memory_service_langchain/
│       ├── __init__.py
│       ├── checkpoint_saver.py
│       ├── history_middleware.py
│       ├── response_recorder.py
│       ├── response_resumer.py
│       ├── proxy.py
│       ├── request_context.py
│       ├── chat_helpers.py
│       └── grpc/                       # Generated gRPC stubs
├── pom.xml                             # Dockerized Python build/stub/package verify hooks
└── examples/
    ├── doc-checkpoints/                # Implemented tutorial checkpoints used by site-tests
    └── chat-python/                    # Full Python chat example mirroring chat-quarkus APIs
```

### REST Client Generation

Use `openapi-python-client` which produces idiomatic Python with `httpx` and Pydantic models:

```bash
openapi-python-client generate \
    --path memory-service-contracts/src/main/resources/openapi.yml \
    --output-path python/memory-service-client
```

Alternatively, use `openapi-generator-cli` with the `python` generator for broader ecosystem support:

```bash
openapi-generator-cli generate \
    -i memory-service-contracts/src/main/resources/openapi.yml \
    -g python \
    -o python/memory-service-client \
    --package-name memory_service_client \
    --additional-properties=library=urllib3,projectName=memory-service-client
```

### gRPC Client Generation

Generate Python gRPC stubs from the proto definition for use by the `HistoryRecorder`:

```bash
python -m grpc_tools.protoc \
    -I memory-service-contracts/src/main/resources \
    --python_out=python/langchain/memory_service_langchain/grpc \
    --grpc_python_out=python/langchain/memory_service_langchain/grpc \
    memory/v1/memory_service.proto
```

The generated stubs expose `ResponseRecorderServiceStub` with:
- `Record(iter[RecordRequest]) -> RecordResponse` — client streaming; send `conversation_id` (UUID bytes) first, then content chunks
- `Replay(ReplayRequest) -> iter[ReplayResponse]` — server streaming; replay recorded tokens
- `CheckRecordings(CheckRecordingsRequest) -> CheckRecordingsResponse` — check in-progress conversations

### Maven Integration

Use `python/pom.xml` to run Python build tasks in Docker (via `ghcr.io/astral-sh/uv:python3.11-bookworm`):

```xml
<plugin>
    <groupId>org.codehaus.mojo</groupId>
    <artifactId>exec-maven-plugin</artifactId>
    <executions>
        <execution>
            <id>generate-python-grpc-stubs</id>
            <phase>generate-sources</phase>
            <goals><goal>exec</goal></goals>
            <configuration>
                <executable>bash</executable>
                <arguments>
                    <argument>-lc</argument>
                    <argument>docker run --rm -v "${python.repo.root}:/workspace" -w /workspace/python ${python.docker.image} ./scripts/generate-grpc-stubs.sh</argument>
                </arguments>
            </configuration>
        </execution>
        <execution>
            <id>build-langchain-wheel</id>
            <phase>package</phase>
            <goals><goal>exec</goal></goals>
            <configuration>
                <executable>bash</executable>
                <arguments>
                    <argument>-lc</argument>
                    <argument>docker run --rm -v "${python.repo.root}:/workspace" -w /workspace/python/langchain ${python.docker.image} uv build</argument>
                </arguments>
            </configuration>
        </execution>
        <execution>
            <id>verify-python-package-install</id>
            <phase>verify</phase>
            <goals><goal>exec</goal></goals>
            <configuration>
                <executable>bash</executable>
                <arguments>
                    <argument>-lc</argument>
                    <argument>docker run --rm -v "${python.repo.root}:/workspace" -w /workspace/python ${python.docker.image} bash -lc "uv venv /tmp/memory-service-python &amp;&amp; uv pip install --python /tmp/memory-service-python/bin/python ./langchain &amp;&amp; /tmp/memory-service-python/bin/python -c 'import memory_service_langchain'"</argument>
                </arguments>
            </configuration>
        </execution>
    </executions>
</plugin>
```

### Low-level REST Client

Current implementation intentionally keeps REST access lightweight and embedded in integration classes:

- `MemoryServiceCheckpointSaver` performs checkpoint/entry reads+writes.
- `MemoryServiceHistoryMiddleware` performs history appends.
- `MemoryServiceProxy` performs endpoint pass-through.
- `request_context.memory_service_request(...)` provides shared auth-aware HTTP execution.

The design choice is to avoid a large generated client surface until endpoint stability warrants it.

---

### Primary: LangGraph `BaseCheckpointSaver`

LangGraph persists graph state as **checkpoints** — snapshots of all channels (messages, tool state, agent decisions, etc.) taken after each node execution. The `BaseCheckpointSaver` interface replaces the legacy `BaseChatMessageHistory` / `RunnableWithMessageHistory` pattern for LangGraph agents.

**Mapping to the memory service:**

| LangGraph concept | Memory service concept |
|-------------------|----------------------|
| `thread_id` | `conversation_id` |
| Checkpoint (full graph state) | `MEMORY` channel entry, content type `"LangGraph/checkpoint"` |
| Pending writes (in-flight node output) | `MEMORY` channel entry, content type `"LangGraph/writes"` |
| `checkpoint_id` | entry `id` from the memory service |

Each `put()` call appends a new entry to the `MEMORY` channel. The memory service's entry consolidation model naturally handles the rolling checkpoint history. `get_tuple()` with no `checkpoint_id` returns the latest entry; with a `checkpoint_id` it returns that specific entry by ID.

`BaseCheckpointSaver` interface coverage in implemented class:
- `put(...)`
- `put_writes(..., task_path="")`
- `get_tuple(...)`
- `list(...)`
- `delete_thread(...)`
- async variants used by LangGraph async runtime

Pseudocode:

```text
put(config, checkpoint, metadata):
  thread_id <- config.configurable.thread_id
  parent_id <- config.configurable.checkpoint_id (if any)
  append MEMORY entry with contentType=LangGraph/checkpoint
    content={checkpoint, metadata, parent_checkpoint_id=parent_id}
  return config + new checkpoint_id (entry.id)

get_tuple(config):
  if config has checkpoint_id:
    read that entry
  else:
    list MEMORY entries, filter checkpoint entries, pick latest
  if none: return null
  load pending writes for checkpoint_id (from LangGraph/writes entries)
  map to CheckpointTuple and return
```

#### Usage with LangGraph

`graph = builder.compile(checkpointer=MemoryServiceCheckpointSaver(...))`

`graph.invoke(...)` / `graph.ainvoke(...)` must include `configurable.thread_id` set to the conversation id.

---

### LangGraph `BaseStore` (long-term cross-thread memory)

`BaseStore` stores facts and preferences that persist **across** conversations. It is attractive for personalization, but the current memory service APIs are conversation-first and do not provide native key/value namespace semantics.

`BaseStore` implementation status in this enhancement:

- **GA scope**: do **not** require `MemoryServiceStore`.
- **Experimental scope**: ship an opt-in adapter (`enable_store_adapter=True`) for teams that accept trade-offs.
- **Follow-up scope**: build `/v1/store` for production-correct semantics.

**Experimental mapping (current API constraints):**

| `BaseStore` operation | Temporary mapping | Status |
|---|---|---|
| `put(namespace, key, value)` | append entry to a namespace conversation | Works |
| `get(namespace, key)` | scan latest matching key in namespace conversation | Works, O(n) |
| `delete(namespace, key)` | append tombstone entry | Works, O(n) |
| `list_namespaces()` | list conversations with `store:` prefix via `/v1/conversations?query=store:` | Works, paginated |
| `search(namespace_prefix, query)` | best-effort via `/v1/conversations/search` + client-side namespace filtering | Approximate only |

Because `/v1/conversations/search` does not accept namespace filters, `SearchOp` cannot be fully correct under concurrency and scale. This is the key reason `MemoryServiceStore` is experimental until `/v1/store` exists.

Pseudocode for experimental adapter:

```text
batch(ops):
  for op in ops:
    if PutOp: append namespace conversation entry
    if GetOp: scan namespace entries and pick latest non-tombstone by key
    if DeleteOp: append tombstone
    if SearchOp: call /v1/conversations/search, then namespace-filter client-side
    if ListNamespacesOp: query conversations by prefix
```

---

### History Recording: `MemoryServiceHistoryMiddleware`

`MemoryServiceHistoryMiddleware` is the Python equivalent of Quarkus `ConversationInterceptor` + stream adapter behavior. It captures USER/AI history entries and streams model output to `ResponseRecorderService` via gRPC.

**Role separation from the checkpointer:**
- `MemoryServiceCheckpointSaver` persists full graph state (`MEMORY` channel) — for agent resumption. LangGraph owns this lifecycle.
- `MemoryServiceHistoryMiddleware` records human-readable turns (`HISTORY` channel) — for frontend replay and search.

**Streaming architecture (dual-path, matching the Java design):**
1. **Live recording via gRPC** (`ResponseRecorderService.Record`): Tokens are streamed to the memory service in real time, enabling response resumption if a client disconnects mid-stream.
2. **Final storage via REST**: When the stream completes, the accumulated text is stored as a `HISTORY` entry via REST.

Pseudocode:

```text
wrap_model_call(request):
  conversation_id <- request context
  extract latest USER text and append HISTORY USER entry
  create gRPC recorder callback for model tokens/events
  call model handler
  collect final AI text (or buffered token text on failure)
  append HISTORY AI entry
  complete recorder
```

---

### API Proxy: `MemoryServiceProxy`

`MemoryServiceProxy` is the Python equivalent of `MemoryServiceProxy.java` in the Quarkus extension. It provides explicit helper methods for conversations, entries, forks, sharing, transfers, and search, returning raw upstream responses that route handlers convert with `to_fastapi_response(...)`.

**Authentication model:**
- Incoming requests carry the **user's bearer token** in the `Authorization` header.
- The proxy forwards this token to the memory service, which enforces per-user access control.
- When no user token is present, the **agent's API key** is used as a fallback.

This lets a browser SPA call the agent app API for history/search/forks without exposing the agent API key.

Pseudocode:

```text
list_conversations(request):
  return to_fastapi_response(
    proxy.list_conversations(mode, after_cursor, limit, query)
  )
```

---

### Legacy: LangChain `BaseChatMessageHistory`

Legacy `BaseChatMessageHistory` is not part of the current tutorial checkpoint GA path. If added later, it should be explicitly marked compatibility-only and separate from LangGraph-first guidance.

---

### Example Chat Application

Reference implementation is split across tutorial checkpoints:
- `01-basic-agent`: plain LangChain quickstart-style HTTP agent.
- `02-with-memory`: add `MemoryServiceCheckpointSaver`.
- `03-with-history`: add `MemoryServiceHistoryMiddleware` + proxy endpoints.
- `04`-`07`: forking, resumption, sharing, and search.

This keeps examples incremental and directly testable via `site-tests`.

### Documentation Plan (Quarkus-Parity + Testable Checkpoints)

Follow the same incremental tutorial shape as Quarkus, with Python checkpoints and executable `site-tests` scenarios:

1. **Index** (`site/src/pages/docs/python/index.mdx`)
   - Prerequisites, tutorial sequence, links.
2. **Getting Started** (`site/src/pages/docs/python/getting-started.mdx`)
   - `01-basic-agent` and `02-with-memory`.
3. **Conversation History** (`site/src/pages/docs/python/conversation-history.mdx`)
   - `03-with-history` + conversation/entries/list proxy endpoints.
4. **Indexing and Search** (`site/src/pages/docs/python/indexing-and-search.mdx`)
   - `07-with-search` + `/v1/conversations/search`.
5. **Conversation Forking** (`site/src/pages/docs/python/conversation-forking.mdx`)
   - `04-conversation-forking`.
6. **Response Resumption** (`site/src/pages/docs/python/response-resumption.mdx`)
   - `05-response-resumption` with recorder replay/check/cancel APIs.
7. **Conversation Sharing** (`site/src/pages/docs/python/sharing.mdx`)
   - `06-sharing` memberships + ownership transfers.
8. **Python Dev Setup** (`site/src/pages/docs/python/dev-setup.mdx`)
   - `uv` workflow, Docker dependency services, CI parity.

Each guide must include `<TestScenario>` + `<CurlTest>` blocks tied to checkpoint paths under `python/examples/doc-checkpoints/*` so `./site-tests` can execute the same commands shown in docs.

## Testing

### Unit Tests

Target unit-test coverage (concise):

- `MemoryServiceCheckpointSaver`:
  - writes checkpoints to `MEMORY` with expected content type
  - returns `checkpoint_id` in updated config
  - resolves latest/specific checkpoints correctly
  - handles `put_writes` + pending writes reconstruction
- `MemoryServiceHistoryMiddleware`:
  - appends USER and AI entries
  - forwards tokens/events to gRPC recorder when enabled
  - tolerates gRPC failures without breaking model response
- `MemoryServiceProxy`:
  - forwards auth/query/body/path correctly
  - maps upstream responses via `to_fastapi_response`
- `MemoryServiceResponseResumer`:
  - `stream`, `resume`, `resume-check`, and `cancel` control paths
  - in-memory state transitions for active/completed/cancelled

### Integration Tests

Run integration tests against dockerized memory-service dependencies.

```bash
uv run pytest python/langchain/tests -m integration
```

### Documentation Tests (`site-tests`)

Python tutorial checkpoints must be executable through the same docs-testing pipeline used by Spring/Quarkus:

- Add `<TestScenario checkpoint="python/examples/doc-checkpoints/...">` blocks to Python docs.
- Add matching OpenAI fixtures under `site-tests/openai-mock/fixtures/python/<checkpoint>/`.
- Ensure `site-tests` can build/start Python checkpoints.

```bash
./mvnw test -pl site-tests -Psite-tests > site-tests.log 2>&1
# Then inspect failures in site-tests.log

# Python-only docs loop
./mvnw -Psite-tests -pl site-tests -Dcucumber.filter.tags=@python surefire:test
```

## Files to Create

| File | Purpose |
|------|---------|
| `python/pom.xml` | **New**: Dockerized Maven build hooks for Python stubs/packages |
| `python/scripts/generate-grpc-stubs.sh` | **New**: Proto-to-Python gRPC generation script |
| `python/langchain/pyproject.toml` | **New**: Primary Python integration package metadata |
| `python/langchain/memory_service_langchain/__init__.py` | **New**: Public package exports |
| `python/langchain/memory_service_langchain/checkpoint_saver.py` | **New**: LangGraph `BaseCheckpointSaver` integration |
| `python/langchain/memory_service_langchain/history_middleware.py` | **New**: History + streaming response recording middleware |
| `python/langchain/memory_service_langchain/proxy.py` | **New**: API proxy helper (`MemoryServiceProxy`) |
| `python/langchain/memory_service_langchain/request_context.py` | **New**: FastAPI auth/conversation/fork context binding |
| `python/langchain/memory_service_langchain/response_recorder.py` | **New**: gRPC recorder implementation |
| `python/langchain/memory_service_langchain/response_resumer.py` | **New**: Response resumption helper used by checkpoint 05 |
| `python/langchain/memory_service_langchain/grpc/` | **New**: Generated gRPC stubs |
| `python/examples/chat-python/` | **New**: Full Python chat example mirroring `chat-quarkus` API surface |
| `python/examples/doc-checkpoints/01-basic-agent/` | **New**: Tutorial checkpoint 1 |
| `python/examples/doc-checkpoints/02-with-memory/` | **New**: Tutorial checkpoint 2 |
| `python/examples/doc-checkpoints/03-with-history/` | **New**: Tutorial checkpoint 3 |
| `python/examples/doc-checkpoints/04-conversation-forking/` | **New**: Tutorial checkpoint 4 |
| `python/examples/doc-checkpoints/05-response-resumption/` | **New**: Tutorial checkpoint 5 |
| `python/examples/doc-checkpoints/06-sharing/` | **New**: Tutorial checkpoint 6 |
| `python/examples/doc-checkpoints/07-with-search/` | **New**: Tutorial checkpoint 7 |
| `site/src/pages/docs/python/index.mdx` | **New**: Python tutorial index |
| `site/src/pages/docs/python/getting-started.mdx` | **New**: Getting started guide |
| `site/src/pages/docs/python/conversation-history.mdx` | **New**: History guide |
| `site/src/pages/docs/python/indexing-and-search.mdx` | **New**: Search guide |
| `site/src/pages/docs/python/conversation-forking.mdx` | **New**: Forking guide |
| `site/src/pages/docs/python/response-resumption.mdx` | **New**: Resumption guide |
| `site/src/pages/docs/python/sharing.mdx` | **New**: Sharing guide |
| `site/src/pages/docs/python/dev-setup.mdx` | **New**: Python environment/setup guide |
| `site-tests/src/test/java/io/github/chirino/memoryservice/docstest/steps/CheckpointSteps.java` | **Update**: Add Python checkpoint build/start logic (`uv sync`, `uv run`) |
| `site-tests/openai-mock/fixtures/python/*` | **New**: WireMock fixtures per Python checkpoint |

## Design Decisions

1. **LangGraph primary, LangChain compatibility kept**: `BaseCheckpointSaver` is the primary persistence interface; `BaseChatMessageHistory` remains for legacy `RunnableWithMessageHistory` users.
2. **Reproducible Python by default**: use `uv` + checked-in lockfiles for local and CI reproducibility; use Docker for integration dependencies and optional full-stack jobs.
3. **Store adapter is experimental by default**: `MemoryServiceStore` is opt-in until a native `/v1/store` API exists.
4. **Checkpoints as opaque blobs in MEMORY channel**: checkpoint payloads are stored verbatim in `MEMORY` (`LangGraph/checkpoint`), preserving LangGraph internals without server coupling.
5. **Align with current LangGraph interfaces**: include `put_writes(..., task_path="")` and `delete_thread()` in checkpointer implementations.
6. **Align with current memory-service contracts**: use `/v1/conversations/search` (not `/v1/search`) and proto fields like `conversation_id`.
7. **gRPC live recording is optional and non-blocking**: stream to caller immediately, while recording in parallel; gRPC failure must not break response streaming.
8. **Proxy stays thin**: `MemoryServiceProxy` forwards path/query/body/auth with minimal transformation.
9. **Prefer async-native HTTP for production**: avoid sync calls inside async code paths.

## Native Store API Decision

**Recommendation:** Yes, avoid making a heavy workaround the default for missing native Store semantics.

- Ship `MemoryServiceCheckpointSaver`, `MemoryServiceHistoryMiddleware`, `MemoryServiceProxy`, and request-context helpers as GA.
- Keep `MemoryServiceStore` behind an explicit feature flag with "experimental" labeling.
- Prioritize a separate enhancement for `/v1/store` and move `MemoryServiceStore` to GA only after that API exists.

## Future Enhancement: Native Store API

The experimental `MemoryServiceStore` maps LangGraph's `BaseStore` onto existing conversations/entries APIs. It is useful for prototypes, but still has structural gaps that a first-class `/v1/store` API should solve.

### Current Workarounds and Their Costs

| `BaseStore` operation | Workaround in current implementation | Cost |
|---|---|---|
| `put(namespace, key, value)` | Create one "store conversation" per namespace; append a new entry | Append-only updates, no true upsert |
| `get(namespace, key)` | List all entries for the namespace conversation, scan for matching key in Python | O(n) full scan; degrades as the namespace grows |
| `delete(namespace, key)` | Append a tombstone entry (`"deleted": true`) | Tombstones accumulate indefinitely; scans get slower over time |
| `search(namespace_prefix, query=...)` | Call `/v1/conversations/search` then client-filter by namespace conversation IDs | Namespace filtering is approximate and expensive |
| `list_namespaces()` | Query `/v1/conversations?query=store:` and parse titles/metadata | O(n) over accessible conversations; depends on naming conventions |

The root cause is a model mismatch: the memory service is **conversation-centric** (append-only entries, soft deletes, content-type consolidation), while `BaseStore` needs **key-value semantics** (upsert by key, hard delete, namespace-addressable lookups). Mapping one onto the other is inherently awkward.

### Proposed: `/v1/store` API

A new top-level resource alongside `/v1/conversations`, backed by a dedicated `store_items` table with columns `(owner_id, namespace text[], key text, value jsonb, embedding_vector, created_at, updated_at)`.

#### Endpoints

```
PUT    /v1/store/{namespace}/items/{key}    # Upsert a value (create or replace)
GET    /v1/store/{namespace}/items/{key}    # Get by key
DELETE /v1/store/{namespace}/items/{key}    # Hard delete
POST   /v1/store/search                     # Semantic + filtered search
GET    /v1/store/namespaces                 # List namespaces
```

`{namespace}` in the URL is a `/`-separated path (e.g. `user_123/memories`), mapping to the `text[]` column.

#### Upsert

```http
PUT /v1/store/user_123/memories/pref_theme
Content-Type: application/json

{"value": "dark mode"}
```

Response `201 Created` or `200 OK` (replaced). No append — always overwrites the previous value for that key.

#### Get

```http
GET /v1/store/user_123/memories/pref_theme
```

```json
{
  "namespace": ["user_123", "memories"],
  "key": "pref_theme",
  "value": {"value": "dark mode"},
  "createdAt": "2025-01-01T00:00:00Z",
  "updatedAt": "2025-01-15T12:00:00Z"
}
```

#### Search

```http
POST /v1/store/search
Content-Type: application/json

{
  "namespacePrefix": ["user_123"],
  "query": "user interface preferences",
  "filter": {"category": "ui"},
  "limit": 10,
  "offset": 0
}
```

Returns items ranked by semantic similarity to `query` within the namespace prefix. When `query` is omitted, returns items matching `filter` only (key-value scan). This maps directly to `BaseStore.search()`.

#### List Namespaces

```http
GET /v1/store/namespaces?prefix=user_123&maxDepth=2&limit=50
```

```json
{
  "namespaces": [
    ["user_123", "memories"],
    ["user_123", "preferences"],
    ["user_123", "facts"]
  ]
}
```

Supports `prefix`, `suffix`, `maxDepth`, `limit`, and `offset` query parameters — a direct mapping to `BaseStore.list_namespaces()`.

#### Access Control

Store items are scoped to the authenticated user (or organization, once Enhancement 060 lands). An agent using its API key sees items it created; a user using their bearer token sees items scoped to their identity. No sharing model initially — store items are private to their owner.

#### Search Integration

Store items are indexed in the vector store similarly to conversation entries. `/v1/store/search` should support namespace-prefix filtering natively, unlike today's `/v1/conversations/search`.

### Impact on `MemoryServiceStore` Implementation

With this API, the Python `MemoryServiceStore.batch()` implementation simplifies to direct HTTP calls with no workarounds:

- `PutOp` → `PUT /v1/store/{namespace}/{key}`
- `GetOp` → `GET /v1/store/{namespace}/{key}`
- `DeleteOp` → `DELETE /v1/store/{namespace}/{key}`
- `SearchOp` → `POST /v1/store/search`
- `ListNamespacesOp` → `GET /v1/store/namespaces`

This enhancement should be filed as a separate issue and treated as the graduation path from experimental to GA for `MemoryServiceStore`.

---

## Implementation Order (Tutorial-Aligned + `site-tests`)

### Progress Snapshot (2026-02-23)

- Completed: Phases 0-7 (Python checkpoints `01`-`07`, Python docs pages, fixtures, and `site-tests` Python tagging/filtering support).
- Pending: Phases 8-9 (`MemoryServiceStore` experimental adapter and native `/v1/store` API).

Each phase must ship three things together:

1. A runnable checkpoint under `python/examples/doc-checkpoints/*`.
2. A matching docs page section with `<TestScenario checkpoint="...">` + `<CurlTest>`.
3. Passing `site-tests` coverage for that checkpoint (including WireMock fixtures).

### Phase 0: Foundation (`site-tests` + Python tooling)

1. Create Python workspace (`pyproject.toml`, `.python-version`, `uv.lock`).
2. Extend `site-tests/src/test/java/io/github/chirino/memoryservice/docstest/steps/CheckpointSteps.java` to build/start Python checkpoints (use `uv sync --frozen` and `.venv/bin/python -m uvicorn ...` conventions).
3. Create fixture roots under `site-tests/openai-mock/fixtures/python/`.
4. Add CI gate to run docs tests: `./mvnw test -pl site-tests -Psite-tests`.

Exit gate: a minimal Python checkpoint can be started by `site-tests` and one `<CurlTest>` passes.

### Phase 1: Getting Started (`01-basic-agent`, `02-with-memory`)

1. Implement basic FastAPI/LangGraph chat checkpoint (`01-basic-agent`) without memory persistence.
2. Add memory-service integration checkpoint (`02-with-memory`) using `MemoryServiceCheckpointSaver`.
3. Write `site/src/pages/docs/python/getting-started.mdx` with test scenarios for both checkpoints.
4. Add fixtures under `site-tests/openai-mock/fixtures/python/01-basic-agent/` and `.../02-with-memory/`.

Exit gate: tutorial shows memory/no-memory behavior and `site-tests` validates both flows.

### Phase 2: Conversation History (`03-with-history`)

1. Implement `MemoryServiceHistoryMiddleware` for user + AI history recording.
2. Expose conversation and entry listing via `MemoryServiceProxy`.
3. Write `site/src/pages/docs/python/conversation-history.mdx` with tested curl examples.
4. Add `03-with-history` fixtures.

Exit gate: messages appear in history APIs and checkpoint passes docs tests.

### Phase 3: Indexing and Search (`07-with-search`)

1. Add indexed-content support in recorder flow (including redaction hook option).
2. Expose `/v1/conversations/search` in Python proxy tutorial flow.
3. Write `site/src/pages/docs/python/indexing-and-search.mdx` with tested search examples.
4. Add `07-with-search` fixtures.

Exit gate: indexed queries return expected results in `site-tests`.

### Phase 4: Conversation Forking (`04-conversation-forking`)

1. Add fork metadata handling in chat/history flow.
2. Expose forks endpoint usage pattern in Python tutorial.
3. Write `site/src/pages/docs/python/conversation-forking.mdx`.
4. Add `04-conversation-forking` fixtures.
5. Keep `/chat/{conversation_id}` text/plain and pass fork metadata through `forkedAtConversationId`/`forkedAtEntryId` query params, bound into `memory_service_scope(...)`.

Exit gate: fork creation/listing is verified by tutorial tests.

### Phase 5: Response Resumption (`05-response-resumption`)

1. Finalize gRPC recorder integration (`Record`, `Replay`, `CheckRecordings`, cancel path).
2. Add resume-check/resume/cancel API surface in tutorial app.
3. Write `site/src/pages/docs/python/response-resumption.mdx` with tested flow.
4. Add `05-response-resumption` fixtures.

Exit gate: interrupted stream can be detected/resumed/cancelled via tested commands.

### Phase 6: Sharing (`06-sharing`)

1. Expose memberships and ownership transfer flows through Python router.
2. Write `site/src/pages/docs/python/sharing.mdx` with multi-user token examples.
3. Add `06-sharing` fixtures.

Exit gate: sharing and transfer lifecycle passes `site-tests`.

### Phase 7: Index + Dev Setup Pages

1. Publish `site/src/pages/docs/python/index.mdx` linking all phases.
2. Publish `site/src/pages/docs/python/dev-setup.mdx` (`uv` + Docker dependency services + CI commands).
3. Ensure all docs examples remain wrapped in `TestScenario`/`CurlTest` where intended.

Exit gate: Python docs navigation is complete and docs tests remain green.

### Phase 8: Experimental Store Adapter (`MemoryServiceStore`)

1. Implement `MemoryServiceStore` behind explicit feature flag.
2. Document limitations and non-goals in docs and enhancement.
3. Add focused unit/integration tests (not required for core tutorial parity).

Exit gate: adapter is available for experiments but not required by tutorial happy path.

### Phase 9: Native Store API (`/v1/store`)

1. Implement server-side `/v1/store`.
2. Migrate Python store adapter to native endpoints.
3. Add dedicated docs + `site-tests` scenarios for native store behavior.

Exit gate: promote store support from experimental to GA.
