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
- **Encrypted at rest** — values are AES-256-GCM encrypted; only metadata and derived attributes are stored in plaintext
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
    "attributes": {"topic": "python"},
    "ttl_seconds": 86400
  }'
```

Response:

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "namespace": ["user", "alice", "notes"],
  "key": "python_tip",
  "attributes": { "topic": "python" },
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
  "attributes": { "topic": "python" },
  "created_at": "2026-01-01T00:00:00Z",
  "expires_at": "2026-01-02T00:00:00Z"
}
```

The value is decrypted on read. Returns `404` if no active record exists for the key, `403` if the caller lacks access.

### Deleting a Memory

```bash
curl -X DELETE "http://localhost:8080/v1/memories?ns=user&ns=alice&ns=notes&key=python_tip" \
  -H "Authorization: Bearer <token>"
```

Returns `204 No Content`. The background indexer removes the corresponding vector entry on its next cycle.

## Searching Memories

`POST /v1/memories/search` supports two modes depending on whether a `query` string is provided.

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
      "attributes": { "topic": "python" },
      "score": 0.92,
      "created_at": "2026-01-01T00:00:00Z"
    }
  ]
}
```

`score` is `null` for attribute-only results and a cosine similarity value (0–1) for semantic results.

### Search Parameters

| Parameter          | Type       | Required | Description                                 |
| ------------------ | ---------- | -------- | ------------------------------------------- |
| `namespace_prefix` | `string[]` | yes      | Restricts results to this namespace subtree |
| `query`            | `string`   | no       | If set, enables vector similarity search    |
| `filter`           | `object`   | no       | Attribute filter expressions (see below)    |
| `limit`            | `integer`  | no       | Max results, default 10, max 100            |
| `offset`           | `integer`  | no       | Pagination offset (attribute-only mode)     |

### Attribute Filter Expressions

Filters are a flat JSON object where each key is an attribute field name. Three expression forms are supported:

| Form                             | Meaning                 | Example                              |
| -------------------------------- | ----------------------- | ------------------------------------ |
| Bare scalar                      | Equality                | `{"topic": "python"}`                |
| `{"in": [...]}`                  | Set membership          | `{"lang": {"in": ["python", "go"]}}` |
| `{"gt"/"gte"/"lt"/"lte": value}` | Numeric/timestamp range | `{"score": {"gte": 0.5}}`            |

All conditions in the object are ANDed.

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
      "attributes": { "topic": "python" }
    },
    {
      "id": "b2c3d4e5-...",
      "namespace": ["user", "alice", "notes"],
      "key": "python_tip",
      "kind": "update",
      "occurred_at": "2026-01-02T00:00:00Z",
      "value": { "text": "Alice prefers list comprehensions over map/filter." },
      "attributes": { "topic": "python" }
    },
    {
      "id": "c3d4e5f6-...",
      "namespace": ["user", "alice", "notes"],
      "key": "python_tip",
      "kind": "delete",
      "occurred_at": "2026-01-03T00:00:00Z",
      "value": null,
      "attributes": null
    }
  ],
  "after_cursor": "<opaque cursor>"
}
```

| Parameter          | Description                                                             |
| ------------------ | ----------------------------------------------------------------------- |
| `ns`               | Repeated per segment; filters to a namespace prefix                     |
| `kinds`            | Filter by event kind: `add`, `update`, `delete`, `expired`; default all |
| `after` / `before` | ISO 8601 timestamp bounds on `occurred_at`                              |
| `after_cursor`     | Opaque cursor for paginating through results                            |
| `limit`            | Max events per page; default 50, max 200                                |

The same OPA access control that governs memory reads applies here — callers only see events for namespaces they can access. `value` and `attributes` are `null` for `delete` and `expired` events.

## Memory Properties

| Property     | Description                                                              |
| ------------ | ------------------------------------------------------------------------ |
| `id`         | Unique UUID assigned on each write                                       |
| `namespace`  | Ordered list of string segments forming the address                      |
| `key`        | Unique key within the namespace                                          |
| `value`      | Arbitrary JSON object; encrypted at rest                                 |
| `attributes` | User-supplied JSON; encrypted at rest; used as filter input              |
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

Memory access is enforced by embedded **OPA/Rego policies** evaluated on every read, write, and search operation. The default built-in policy uses namespace-ownership rules:

- A user may access any namespace whose second segment matches their user ID (e.g. `["user", "alice", ...]`).
- Users with the `admin` role may access any namespace.

Custom policy directories can be configured for RBAC, ABAC, or shared-namespace patterns. OPA can also inject additional filter constraints into search queries — for example, transparently restricting non-admin searches to the caller's own subtree.

See the [Admin APIs](/docs/concepts/admin-apis/) for policy management endpoints.

## Encryption

Memory values and user-supplied attributes are **encrypted at rest** using AES-256-GCM via the service's existing key-management infrastructure. Only the namespace, key, policy-derived attributes, and expiry timestamp are stored in plaintext — these are required for efficient filtering and indexing.

Vector stores never receive encrypted data. They hold only embeddings and plaintext policy attributes derived by the OPA attribute-extraction policy.

## Vector Indexing

When a memory is written with embeddable string fields, the background indexer generates embeddings and upserts them to the configured vector store (PGVector or Qdrant). Indexing is **decoupled from the write path**: writes return immediately, and the indexer catches up asynchronously.

Control which fields are embedded via the `index_fields` parameter on `PUT`:

```json
{
  "namespace": ["user", "alice", "notes"],
  "key": "tip",
  "value": { "text": "...", "tags": ["python"] },
  "index_fields": ["text"]
}
```

Set `index_fields` to `false` to disable indexing entirely for a memory.

Admin-configurable indexing settings:

| Setting                               | Default | Description                       |
| ------------------------------------- | ------- | --------------------------------- |
| `memory.episodic.indexing.batch_size` | 100     | Items processed per indexer cycle |
| `memory.episodic.indexing.interval`   | 30 s    | Polling interval                  |
| `memory.episodic.namespace.max_depth` | 5       | Maximum namespace depth           |

## LangGraph Compatibility

The `memory-service-langgraph` Python package implements LangGraph's `BaseStore` interface by calling the Memory Service REST API. This lets any LangGraph agent use the Memory Service as a drop-in persistent store without changing agent code.

```python
from memory_service_langgraph import MemoryServiceStore

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

| Method   | Path                      | Purpose                                                 |
| -------- | ------------------------- | ------------------------------------------------------- |
| `PUT`    | `/v1/memories`            | Upsert a memory                                         |
| `GET`    | `/v1/memories`            | Get a single memory by namespace + key                  |
| `DELETE` | `/v1/memories`            | Delete a memory by namespace + key                      |
| `POST`   | `/v1/memories/search`     | Attribute filter and/or semantic search                 |
| `GET`    | `/v1/memories/namespaces` | List namespaces under a prefix                          |
| `GET`    | `/v1/memories/events`     | Paginated event timeline (add, update, delete, expired) |

## Next Steps

- Learn about [Indexing & Search](/docs/concepts/indexing-and-search/) for conversation-level semantic search
- Understand [Sharing & Access Control](/docs/concepts/sharing/) for conversation access control
- See [Admin APIs](/docs/concepts/admin-apis/) for policy management and index status endpoints
