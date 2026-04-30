---
layout: ../../../layouts/DocsLayout.astro
title: Memories
description: Understanding namespaced episodic memory for LLM agents in Memory Service.
---

Memories are a persistent, namespaced key-value store that lets LLM agents save and recall information across conversations. Unlike conversation entries — which record the chronological exchange between users and models — memories hold arbitrary facts, preferences, and context that agents want to retain long-term.

## What is a Memory?

A memory in Memory Service is:

- A **key-value item** identified by a namespace tuple and a string key
- Stored in a **hierarchical namespace** that organizes memories by user, agent, or session
- **Encrypted at rest** — values are AES-256-GCM encrypted; metadata, derived attributes, and caller-provided index text are stored in plaintext
- Optionally **indexed for semantic search** via vector embeddings
- Subject to **OPA/Rego access control** enforced at the service level
- Compatible with the **LangGraph `BaseStore` interface** via a Python client library

## Namespace Model

A namespace is an ordered list of non-empty string segments that forms a path-like address. Namespaces let you organize memories into hierarchies — per-user, per-agent, per-session, or shared.

```
namespace: ["user", "alice", "notes"]
key:       "python_tip"
```

Common patterns:

| Pattern             | Example namespace             | Use case                       |
| ------------------- | ----------------------------- | ------------------------------ |
| Per-user            | `["user", "alice"]`           | Personal preferences and facts |
| Per-user + category | `["user", "alice", "notes"]`  | Categorized user memories      |
| Per-agent           | `["agent", "support-bot"]`    | Agent-global knowledge         |
| Session-scoped      | `["session", "<session-id>"]` | Short-lived context            |
| Shared              | `["shared", "product-faqs"]`  | Knowledge shared across agents |

The maximum namespace depth is admin-configurable (default: 5 segments).

## Memory Lifecycle

### Writing a Memory

```bash
curl -X PUT http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "namespace": ["user", "alice", "notes"],
    "key": "python_tip",
    "value": {
      "text": "Alice prefers list comprehensions over map/filter."
    },
    "index": {"text": "Alice prefers list comprehensions over map/filter."},
    "ttl_seconds": 86400
  }'
```

Response:

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "namespace": ["user", "alice", "notes"],
  "key": "python_tip",
  "attributes": { "namespace": "user", "sub": "alice" },
  "created_at": "2026-01-01T00:00:00Z",
  "expires_at": "2026-01-02T00:00:00Z"
}
```

The `value` is not echoed back in the response — only the write confirmation is returned.

Calling `PUT` with an existing `(namespace, key)` pair upserts the memory, replacing the previous value.

### Reading a Memory

Use repeated `ns` query parameters — one per namespace segment:

```bash
curl "http://localhost:8080/v1/memories?ns=user&ns=alice&ns=notes&key=python_tip" \
  -H "Authorization: Bearer <token>"
```

Response:

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "namespace": ["user", "alice", "notes"],
  "key": "python_tip",
  "value": {
    "text": "Alice prefers list comprehensions over map/filter."
  },
  "attributes": { "namespace": "user", "sub": "alice" },
  "created_at": "2026-01-01T00:00:00Z",
  "expires_at": "2026-01-02T00:00:00Z"
}
```

The value is decrypted on read. Returns `404` if no matching record exists for the requested archive mode, `403` if the caller lacks access.

Use the optional `archived` query parameter to control which version is readable:

| Value     | Meaning                        |
| --------- | ------------------------------ |
| `exclude` | Active memories only (default) |
| `include` | Active and archived memories   |
| `only`    | Archived memories only         |

### Archiving a Memory

```bash
curl -X PATCH http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "namespace": ["user", "alice", "notes"],
    "key": "python_tip",
    "archived": true
  }'
```

Returns `204 No Content`. Archiving is recorded as a memory update, and semantic search respects the memory's archive state via vector-store metadata plus datastore post-filtering.

## Searching Memories

`POST /v1/memories/search` supports two modes depending on whether a `query` string is provided. Both modes honor the same `archived` selector used by direct reads: `exclude` (default), `include`, or `only`.

### Attribute-Filter Search

Without a `query`, the service applies an attribute filter against the primary store:

```bash
curl -X POST http://localhost:8080/v1/memories/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "namespace_prefix": ["user", "alice"],
    "filter": {"topic": "python"},
    "limit": 10
  }'
```

### Semantic Search

With a `query`, the service embeds the query text and performs an approximate nearest-neighbor search in the vector store, then fetches and decrypts the matching memories from the primary store:

```bash
curl -X POST http://localhost:8080/v1/memories/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "namespace_prefix": ["user", "alice"],
    "query": "whitespace-sensitive syntax",
    "limit": 5
  }'
```

Response:

```json
{
  "items": [
    {
      "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "namespace": ["user", "alice", "notes"],
      "key": "python_tip",
      "value": { "text": "Alice prefers list comprehensions over map/filter." },
      "attributes": { "namespace": "user", "sub": "alice" },
      "score": 0.92,
      "created_at": "2026-01-01T00:00:00Z"
    }
  ]
}
```

`score` is `null` for attribute-only results and a cosine similarity value (0–1) for semantic results. Semantic search pre-filters by archive state in the vector store and then re-checks the hydrated memory rows before returning results.

### Search Parameters

| Parameter          | Type       | Required | Description                                 |
| ------------------ | ---------- | -------- | ------------------------------------------- |
| `namespace_prefix` | `string[]` | yes      | Restricts results to this namespace subtree |
| `query`            | `string`   | no       | If set, enables vector similarity search    |
| `filter`           | `object`   | no       | Attribute filter expressions (see below)    |
| `archived`         | `string`   | no       | `exclude` (default), `include`, or `only`   |
| `limit`            | `integer`  | no       | Max results, default 10, max 100            |

### Attribute Filter Expressions

Filters are a flat JSON object where each key is an attribute field name. Search returns a bounded top-k result set; it is not pageable, and request fields such as `offset`, `order`, or `after_cursor` are rejected.

The filter language uses positive, pushdownable predicates only:

| Form                              | Meaning                 | Example                               |
| --------------------------------- | ----------------------- | ------------------------------------- |
| Bare scalar or `{"$eq": value}`   | Equality                | `{"topic": "python"}`                 |
| Array or `{"$in": [...]}`         | Set membership          | `{"lang": {"$in": ["python", "go"]}}` |
| `{"$gte"/"$lte": value}`          | Numeric/timestamp range | `{"score": {"$gte": 0.5}}`            |
| `{"$exists": true}`               | Present non-null value  | `{"sourceHash": {"$exists": true}}`   |

All conditions in the object are ANDed. `$ne`, `$nin`, `$exists: false`, old unprefixed operators such as `{"in": [...]}`, and arbitrary datastore query operators are rejected.

## Listing Namespaces

Navigate the namespace hierarchy to discover what subtrees exist:

```bash
curl "http://localhost:8080/v1/memories/namespaces?prefix=user&prefix=alice&max_depth=3" \
  -H "Authorization: Bearer <token>"
```

Response:

```json
{
  "namespaces": [
    ["user", "alice", "notes"],
    ["user", "alice", "tasks"]
  ]
}
```

| Parameter   | Description                                                          |
| ----------- | -------------------------------------------------------------------- |
| `prefix`    | Repeated per segment; only namespaces under this prefix are returned |
| `suffix`    | Only return namespaces ending with this suffix                       |
| `archived`  | `exclude` (default), `include`, or `only`                            |
| `max_depth` | Truncate returned namespaces to this depth                           |

## Memory Event Timeline

`GET /v1/memories/events` returns a paginated, time-ordered stream of memory lifecycle events — useful for syncing external systems, auditing changes, or replaying history.

```bash
curl "http://localhost:8080/v1/memories/events?ns=user&ns=alice&limit=50" \
  -H "Authorization: Bearer <token>"
```

```json
{
  "events": [
    {
      "id": "a1b2c3d4-...",
      "namespace": ["user", "alice", "notes"],
      "key": "python_tip",
      "kind": "add",
      "occurred_at": "2026-01-01T00:00:00Z",
      "value": { "text": "Alice prefers list comprehensions." },
      "attributes": { "namespace": "user", "sub": "alice" }
    },
    {
      "id": "b2c3d4e5-...",
      "namespace": ["user", "alice", "notes"],
      "key": "python_tip",
      "kind": "update",
      "occurred_at": "2026-01-02T00:00:00Z",
      "value": { "text": "Alice prefers list comprehensions over map/filter." },
      "attributes": { "namespace": "user", "sub": "alice" }
    },
    {
      "id": "c3d4e5f6-...",
      "namespace": ["user", "alice", "notes"],
      "key": "python_tip",
      "kind": "update",
      "occurred_at": "2026-01-03T00:00:00Z",
      "value": { "text": "Alice prefers list comprehensions over map/filter." },
      "attributes": { "namespace": "user", "sub": "alice" }
    }
  ],
  "after_cursor": "<opaque cursor>"
}
```

| Parameter          | Description                                                   |
| ------------------ | ------------------------------------------------------------- |
| `ns`               | Repeated per segment; filters to a namespace prefix           |
| `kinds`            | Filter by event kind: `add`, `update`, `expired`; default all |
| `after` / `before` | ISO 8601 timestamp bounds on `occurred_at`                    |
| `after_cursor`     | Opaque cursor for paginating through results                  |
| `limit`            | Max events per page; default 50, max 200                      |

The same OPA access control that governs memory reads applies here — callers only see events for namespaces they can access. `value` and `attributes` are `null` for `expired` events; archive operations appear as `update` events.

## Memory Properties

| Property     | Description                                                              |
| ------------ | ------------------------------------------------------------------------ |
| `id`         | Unique UUID assigned on each write                                       |
| `namespace`  | Ordered list of string segments forming the address                      |
| `key`        | Unique key within the namespace                                          |
| `value`      | Arbitrary JSON object; encrypted at rest                                 |
| `attributes` | Policy-derived plaintext attributes used for filtering/search scoping    |
| `created_at` | Timestamp of this version                                                |
| `expires_at` | TTL expiry timestamp, or `null` for no expiry                            |
| `score`      | Cosine similarity score (search results only; `null` for attribute-only) |

## TTL and Expiry

Set `ttl_seconds` on a `PUT` request to make a memory expire automatically:

```json
{
  "namespace": ["session", "abc123"],
  "key": "context",
  "value": { "summary": "User asked about billing." },
  "ttl_seconds": 3600
}
```

A background goroutine expires memories on a configurable interval (default: 60 s). The vector indexer removes the corresponding vector entries on its next cycle.

## Access Control

Memory access is enforced by embedded **OPA/Rego policies** evaluated on every memory API call. The service loads policy files from:

- `--policy-dir`
- `MEMORY_SERVICE_POLICY_DIR`

Expected files in that directory:

- `authz.rego` - read/write/delete authorization
- `attributes.rego` - plaintext policy attributes extraction
- `filter.rego` - search/list namespace+filter injection

If no directory is set, or if a file is missing, the service falls back to the built-in default for that file.

### Rego Policy Input Variables

Each policy is evaluated with an `input` object. Available fields differ by policy type.

#### `authz.rego` (`data.memories.authz.decision`)

| `input` field        | Type                    | Description                                                 |
| -------------------- | ----------------------- | ----------------------------------------------------------- |
| `operation`          | `string`                | Operation being authorized: `write`, `read`, or `delete`    |
| `namespace`          | `string[]`              | Full namespace segments from the request                    |
| `key`                | `string`                | Memory key from the request                                 |
| `value`              | `object`                | Present for `write`; full memory value payload              |
| `index`              | `object<string,string>` | Present for `write`; caller-provided redacted index payload |
| `context.user_id`    | `string`                | Authenticated subject/user ID                               |
| `context.client_id`  | `string`                | Authenticated client ID (API key/OIDC client), when present |
| `context.jwt_claims` | `object`                | Raw JWT claims map (for example `roles`)                    |

#### `attributes.rego` (`data.memories.attributes.attributes`)

| `input` field        | Type                    | Description                                                 |
| -------------------- | ----------------------- | ----------------------------------------------------------- |
| `namespace`          | `string[]`              | Full namespace segments from the write request              |
| `key`                | `string`                | Memory key from the write request                           |
| `value`              | `object`                | Memory value JSON body                                      |
| `index`              | `object<string,string>` | Caller-provided redacted index payload                      |
| `context.user_id`    | `string`                | Authenticated subject/user ID                               |
| `context.client_id`  | `string`                | Authenticated client ID (API key/OIDC client), when present |
| `context.jwt_claims` | `object`                | Raw JWT claims map (for example `roles`)                    |

Typical `value` and `index` payloads passed to `attributes.rego`:

```json
{
  "value": {
    "text": "Alice prefers list comprehensions over map/filter.",
    "topic": "python",
    "confidence": 0.92,
    "source": {
      "type": "chat",
      "conversationId": "8fa3deec-4a45-42a5-a36d-6076b20a2c8d"
    },
    "tags": ["style", "python", "preferences"]
  },
  "index": {
    "text": "Alice prefers list comprehensions over map/filter.",
    "topic": "python"
  }
}
```

#### `filter.rego` (`data.memories.filter`)

| `input` field        | Type       | Description                                                 |
| -------------------- | ---------- | ----------------------------------------------------------- |
| `namespace_prefix`   | `string[]` | Requested namespace prefix for search/list                  |
| `filter`             | `object`   | Caller-supplied attribute filter (may be empty)             |
| `context.user_id`    | `string`   | Authenticated subject/user ID                               |
| `context.client_id`  | `string`   | Authenticated client ID (API key/OIDC client), when present |
| `context.jwt_claims` | `object`   | Raw JWT claims map (for example `roles`)                    |

The `filter.rego` result may return:

- `namespace_prefix` (`string[]`) - effective prefix to enforce
- `attribute_filter` (`object`) - merged into the caller filter before datastore query

### Default Built-In Policy (Repo Default)

The default policy bundle shipped in this repository is:

```rego
package memories.authz

default decision = {"allow": false, "reason": "access denied"}

decision = {"allow": true} if {
  input.namespace[0] == "user"
  input.namespace[1] == input.context.user_id
}
```

```rego
package memories.attributes

default attributes = {}

attributes = {"namespace": input.namespace[0], "sub": input.namespace[1]} if {
  count(input.namespace) >= 2
}
```

```rego
package memories.filter

is_admin if {
  "admin" in input.context.jwt_claims.roles
}

namespace_prefix := input.namespace_prefix if { is_admin }
namespace_prefix := user_prefix if {
  not is_admin
  not starts_with(input.namespace_prefix, user_prefix)
}

attribute_filter := {} if { is_admin }
attribute_filter := {"namespace": "user", "sub": input.context.user_id} if { not is_admin }
```

What this means in practice:

- `authz.rego`: direct `PUT`/`GET`/`DELETE` is allowed only under `["user", <caller_user_id>, ...]`; deny responses can carry a `reason`.
- `attributes.rego`: each memory gets plaintext policy attributes `namespace` and `sub` for policy-aware filtering.
- `filter.rego`: non-admin search/list calls are constrained to the caller's own `["user", <caller_user_id>]` subtree; admin callers keep the requested prefix and no forced attribute filter.

Important: with the default bundle, `admin` role affects search/list filtering, but does not bypass `authz.rego` for direct read/write/delete. If you want admin bypass there, add it in `authz.rego`.

See the [Admin APIs](/docs/concepts/admin-apis/) for policy management endpoints.

## Encryption

Memory values are **encrypted at rest** using AES-256-GCM via the service's existing key-management infrastructure. The namespace, key, policy-derived attributes, caller-provided `index` payload (stored as `indexed_content`), and expiry timestamp are stored in plaintext for filtering and indexing.

Vector stores never receive encrypted data. They hold only embeddings and plaintext policy attributes derived by the OPA attribute-extraction policy.

## Vector Indexing

When a memory is written with an `index` payload, the background indexer embeds those field values and upserts them to the configured vector store (PGVector or Qdrant). Indexing is **decoupled from the write path**: writes return immediately, and the indexer catches up asynchronously.

Control which fields are embedded by sending a redacted `index` map on `PUT`:

```json
{
  "namespace": ["user", "alice", "notes"],
  "key": "tip",
  "value": { "text": "...", "tags": ["python"] },
  "index": { "text": "..." }
}
```

Set `"index": {}` (or omit `index`) to disable vector indexing for that memory version.

Admin-configurable indexing settings:

| Setting                               | Default | Description                       |
| ------------------------------------- | ------- | --------------------------------- |
| `memory.episodic.indexing.batch_size` | 100     | Items processed per indexer cycle |
| `memory.episodic.indexing.interval`   | 30 s    | Polling interval                  |
| `memory.episodic.namespace.max_depth` | 5       | Maximum namespace depth           |

## LangGraph Compatibility

The `memory-service-langchain` Python package implements LangGraph's `BaseStore` interface by calling the Memory Service REST API. This lets any LangGraph agent use the Memory Service as a drop-in persistent store without changing agent code.

```python
from memory_service_langchain.langgraph import MemoryServiceStore

store = MemoryServiceStore(
    url="http://localhost:8080",
    token="<your-token>"
)

# Standard LangGraph BaseStore interface
store.put(("user", "alice", "notes"), "python_tip", {"text": "Use list comprehensions."})
item = store.get(("user", "alice", "notes"), "python_tip")
results = store.search(("user", "alice"), query="python syntax", limit=5)
```

An async variant (`AsyncMemoryServiceStore`) is also available for use in async LangGraph workflows.

## API Operations

| Method  | Path                      | Purpose                                                 |
| ------- | ------------------------- | ------------------------------------------------------- |
| `PUT`   | `/v1/memories`            | Upsert a memory                                         |
| `GET`   | `/v1/memories`            | Get a single memory by namespace + key and archive mode |
| `PATCH` | `/v1/memories`            | Archive or unarchive a memory                           |
| `POST`  | `/v1/memories/search`     | Attribute filter and/or semantic search                 |
| `GET`   | `/v1/memories/namespaces` | List namespaces under a prefix and archive mode         |
| `GET`   | `/v1/memories/events`     | Paginated event timeline (add, update, expired)         |

## Next Steps

- Learn about [Indexing & Search](/docs/concepts/indexing-and-search/) for conversation-level semantic search
- Understand [Sharing & Access Control](/docs/concepts/sharing/) for conversation access control
- See [Admin APIs](/docs/concepts/admin-apis/) for policy management and index status endpoints
