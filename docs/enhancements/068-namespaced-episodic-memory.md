---
status: partial
---

# Enhancement 068: Namespaced Episodic Memory

> **Status**: Partial. Core CRUD, encryption, OPA policy enforcement, TTL/eviction, OpenAPI/proto contract parity, generated REST model adoption, gRPC handlers/tests, vector indexing (Postgres + MongoDB + Qdrant), admin policy bundle endpoints, and manual index-trigger endpoint are implemented. Remaining: LangGraph integration tests (Phase 6).

## Summary

Add a namespaced episodic memory system to the memory-service enabling LLM agents to store,
retrieve, and semantically search persistent memories organized in a hierarchical namespace.
Access control is enforced via embedded OPA/Rego policies. Memory values are stored encrypted
in the primary datastore; vector stores hold only embeddings and plaintext filter attributes.
The system is compatible with the LangGraph `BaseStore` interface via a thin Python wrapper.
This enhancement also defines an event timeline endpoint to expose memory lifecycle changes
(`add`, `update`, `delete`) in namespace order.

## Motivation

LLM agents increasingly require persistent, cross-conversation memory — recalled facts, user
preferences, shared knowledge. The LangGraph ecosystem standardized on a `BaseStore` interface
for this purpose, but existing implementations push access control responsibility to the
application layer, requiring every agent app to re-implement its own authz.

The memory-service is well-positioned to provide:

- **Hierarchical namespace organization** — per-user, per-agent, per-session, or shared memories
  addressed by a path-like namespace tuple, similar to how conversations are owned by users.
- **Service-level access control** — OPA/Rego policies enforce who can read/write which
  namespaces, eliminating authz boilerplate from every agent app.
- **Encryption at rest** — memory values are AES-256-GCM encrypted via the existing DEK
  infrastructure; only plaintext attributes and embeddings leave the encrypted payload.
- **Semantic search** — combine namespace prefix + attribute filters + vector similarity,
  scoped to the caller's accessible namespaces.
- **TTL support** — memories expire automatically.
- **LangGraph compatibility** — a Python package implementing `BaseStore` over the REST API.

### Relationship to LangGraph Store

The LangGraph `BaseStore` interface defines five operations on a namespace tuple + key space:

| Operation | Purpose |
|---|---|
| `put(namespace, key, value)` | Write or delete an item |
| `get(namespace, key)` | Retrieve a single item |
| `search(namespace_prefix, query, filter)` | Filter/vector search |
| `delete(namespace, key)` | Remove an item |
| `list_namespaces(prefix, suffix, max_depth)` | Navigate the hierarchy |

This enhancement maps those directly onto memory-service REST endpoints (§3).

## Non-Goals

- Replacing the existing conversation/entry model (entries remain for ordered LLM message history).
- Providing a managed OPA sidecar — policies are loaded from local files.
- Re-encrypting existing entries or conversations.

## Design

### 1. Namespace Model

A namespace is an ordered list of non-empty string segments. Combined depth
(segment count) is bounded by an admin-configured limit (default: 10).

```
namespace: ["user", "alice", "memories"]
key:       "first_meeting"
```

When encoded for storage, each segment is **percent-encoded** (standard URL path encoding:
`url.PathEscape` in Go, `urllib.parse.quote(s, safe='')` in Python), then the encoded
segments are joined with `\x1e` (ASCII 30, the Record Separator). Percent-encoded output
only contains ASCII letters, digits, `-_.~`, and `%XX` sequences — never `\x1e` — so the
delimiter is always unambiguous and segment content is unrestricted. Decoding is the
reverse: split on `\x1e`, then percent-decode each part.

```
prefix: "user\x1ealice\x1ememories"
```

For SQL datastores this encoded prefix column is efficient to query with a `LIKE` condition.
Vector databases typically have no equivalent of SQL `LIKE` on payload fields, so each
backend may need to denormalize the namespace into searchable attributes to support prefix
queries efficiently.

---

### 2. Data Model

#### Primary Store — PostgreSQL

```sql
-- deleted_reason values (SMALLINT):
--   0 = updated  — superseded by a newer write; safe to hard-delete once indexed_at is synced
--   1 = deleted  — explicit DELETE; cleared to tombstone on eviction, kept for event history
--   2 = expired  — TTL elapsed; cleared to tombstone on eviction, kept for event history

-- Event log: each row is a write event for a (namespace, key).
-- The active value of a key is the single row where deleted_at IS NULL.
-- On update: a new row is inserted (kind='update') with created_at = T;
--            the previous active row is soft-deleted: deleted_at = T, deleted_reason = 0.
-- On delete: the active row is soft-deleted: deleted_at = NOW(), deleted_reason = 1.
-- On TTL expiry: the active row is soft-deleted: deleted_at = NOW(), deleted_reason = 2.
-- When a row is soft-deleted its indexed_at is reset to NULL so the indexer removes it from the vector store.
CREATE TABLE memories (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace         TEXT        NOT NULL,   -- RS-encoded, e.g. "user\x1ealice\x1ememories"
    key               TEXT        NOT NULL,
    value_encrypted   BYTEA,                  -- AES-256-GCM encrypted JSON value; NULL for tombstones
    attributes        BYTEA,                  -- AES-256-GCM encrypted user-supplied attributes; NULL for tombstones
    policy_attributes JSONB,                  -- plaintext attributes extracted by OPA policy
    kind              SMALLINT    NOT NULL DEFAULT 0,  -- 0=add, 1=update — set at write time, never changed
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at        TIMESTAMPTZ,            -- NULL = no TTL
    deleted_at        TIMESTAMPTZ,            -- NULL = active; non-NULL = superseded or key deleted
    deleted_reason    SMALLINT,               -- NULL=active, 0=updated, 1=deleted, 2=expired
    indexed_at        TIMESTAMPTZ             -- NULL = pending vector index sync
);

-- History / audit queries (all versions of a key, ordered)
CREATE INDEX memories_namespace_key_idx    ON memories (namespace, key, created_at DESC);
-- Active-record point lookups (GET, search)
CREATE INDEX memories_active_idx           ON memories (namespace, key) WHERE deleted_at IS NULL;
CREATE INDEX memories_expires_idx          ON memories (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX memories_indexed_at_idx       ON memories (indexed_at);
CREATE INDEX memories_policy_attrs_gin_idx ON memories USING GIN (policy_attributes);
-- Event timeline queries
CREATE INDEX memories_write_events_idx     ON memories (namespace, created_at, id) WHERE kind IN (0, 1);
CREATE INDEX memories_delete_events_idx    ON memories (namespace, deleted_at, id) WHERE deleted_reason IN (1, 2);

```

#### Primary Store — MongoDB

Equivalent collection: `memories`. Fields mirror the PostgreSQL schema: `kind` and `deleted_reason`
are stored as `int32` with the same numeric constants (kind: 0=add, 1=update; deleted_reason:
0=updated, 1=deleted, 2=expired). `value_encrypted` and `attributes` are stored as `BinData`
and set to `null` for tombstones.

Indexes:
- `{namespace: 1, key: 1, created_at: -1}` — history queries
- `{namespace: 1, key: 1, deleted_at: 1}` (sparse) — active-record lookups
- `{expires_at: 1}` (TTL index)
- `{indexed_at: 1}`
- `{namespace: 1, created_at: 1, _id: 1}, {partialFilterExpression: {kind: {$in: [0, 1]}}}` — write event queries
- `{namespace: 1, deleted_at: 1, _id: 1}, {partialFilterExpression: {deleted_reason: {$in: [1, 2]}}}` — delete event queries

No unique constraint on `(namespace, key)` — multiple historical rows per key are retained.
Access control is enforced entirely by OPA policies.

#### Vector Store — PGVector

```sql
CREATE TABLE memory_vectors (
    memory_id         UUID  NOT NULL,
    field_name        TEXT  NOT NULL,  -- embedded field, e.g. "text"
    namespace         TEXT  NOT NULL,  -- redundant copy for prefix filtering
    policy_attributes JSONB,           -- redundant copy of OPA-extracted attributes for filtering
    embedding         vector(N),       -- dimension from configured embedding model
    PRIMARY KEY (memory_id, field_name)
    -- no FK: primary DB and vector DB may be separate services
);

CREATE INDEX memory_vectors_ns_idx                ON memory_vectors (namespace);
CREATE INDEX memory_vectors_policy_attrs_gin_idx  ON memory_vectors USING GIN (policy_attributes);
```

#### Vector Store — Qdrant

Collection `memory_vectors`. Each point carries a payload with at minimum the `memory_id`,
`field_name`, encoded `namespace`, and any attributes needed for filtering. The exact
payload layout is left to the Qdrant backend implementation, which must denormalize the
namespace into attributes suitable for Qdrant's filter API.

One Qdrant point per embedded field per memory item. Multi-field items (e.g. `"text"` and
`"title"`) produce multiple points; search results are deduplicated and max-pooled by `memory_id`.

---

### 3. REST API

All Agent API endpoints are under `/v1/memories` in `openapi.yml`.

#### PUT /v1/memories — Upsert a Memory

Request body:

```json
{
  "namespace": ["user", "alice", "memories"],
  "key": "first_meeting",
  "value": {
    "text": "Alice mentioned she loves Python.",
    "text2": "Alice mentioned she loves Python.",
    "tags": ["python", "intro"]
  },
  "attributes": { "color": "blue" },
  "ttl_seconds": 86400,
  "index_fields": ["text", "text2"]
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `namespace` | `string[]` | yes | Depth ≤ configured limit |
| `key` | `string` | yes | Unique within namespace |
| `value` | `object` | yes | Arbitrary JSON; encrypted at rest |
| `attributes` | `object` | no | Arbitrary JSON; encrypted at rest; passed to attribute extraction policy |
| `ttl_seconds` | `integer` | no | Null = no expiry |
| `index_fields` | `string[] \| false` | no | Fields to embed; `false` disables indexing; default = all string leaf fields |

Response `200 OK`:

```json
{
  "id": "<uuid>",
  "namespace": ["user", "alice", "memories"],
  "key": "first_meeting",
  "attributes": { "color": "blue" },
  "created_at": "2026-01-01T00:00:00Z",
  "expires_at": "2026-01-02T00:00:00Z"
}
```

Value is omitted from the response (write-only confirmation).

#### GET /v1/memories — Get a Memory

```
GET /v1/memories?ns=user&ns=alice&ns=memories&key=first_meeting
```

The `ns` parameter is repeated once per namespace segment. The server joins them with `\x1e`
internally; clients always work with plain segment strings.

Response `200 OK`:

```json
{
  "id": "<uuid>",
  "namespace": ["user", "alice", "memories"],
  "key": "first_meeting",
  "value": { "text": "Alice mentioned she loves Python.", "tags": ["python", "intro"] },
  "attributes": { "color": "blue" },
  "created_at": "2026-01-01T00:00:00Z",
  "expires_at": null
}
```

Value is decrypted on read. Returns `404` if no active row exists for the key (`deleted_at IS NULL`). Returns `403` if inaccessible.

#### DELETE /v1/memories — Delete a Memory

```
DELETE /v1/memories?ns=user&ns=alice&ns=memories&key=first_meeting
```

Returns `204 No Content`. Sets `deleted_at = NOW()` and resets `indexed_at = NULL` on the active row; the background indexer then removes the corresponding vector entries.

#### POST /v1/memories/search — Filter and Semantic Search

Request body:

```json
{
  "namespace_prefix": ["user", "alice"],
  "query": "python programming",
  "filter": {
    "topic": "python"
  },
  "limit": 10,
  "offset": 0
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `namespace_prefix` | `string[]` | yes | Restricts search to this subtree |
| `query` | `string` | no | If present, triggers vector similarity search |
| `filter` | `object` | no | Attribute filter expressions (see §Attribute Filter Expressions) |
| `limit` | `integer` | no | Default 10, max 100 |
| `offset` | `integer` | no | For pagination (attribute-only mode only) |

OPA filter-injection policy appends additional constraints (see §4c) before execution.

**Without `query`**: attribute-filter-only query against primary store.

**With `query`**: embed the query string, then perform ANN search on the vector store filtered
by `namespace_prefix` + merged attribute filter. Retrieve full items (with decrypted values)
from primary store by the returned `memory_id` set. Results ranked by descending similarity
score; ties broken by `created_at DESC`.

Response `200 OK`:

```json
{
  "items": [
    {
      "id": "<uuid>",
      "namespace": ["user", "alice", "memories"],
      "key": "first_meeting",
      "value": { "text": "Alice mentioned she loves Python.", "tags": ["python", "intro"] },
      "attributes": { "color": "blue" },
      "score": 0.92,
      "created_at": "..."
    }
  ]
}
```

`score` is `null` for attribute-only results.

#### GET /v1/memories/namespaces — List Namespaces

```
GET /v1/memories/namespaces?prefix=user&prefix=alice&max_depth=3&suffix=memories
```

| Parameter | Notes |
|---|---|
| `prefix` | Repeated once per prefix segment; only namespaces under this prefix are returned |
| `suffix` | Only return namespaces ending with this suffix |
| `max_depth` | Truncate returned namespaces to this depth |

Response `200 OK`:

```json
{
  "namespaces": [
    ["user", "alice", "memories"],
    ["user", "alice", "tasks"]
  ]
}
```

#### GET /v1/memories/events — Memory Event Timeline

Returns an ordered stream of memory lifecycle events (`add`, `update`, `delete`, `expired`) for namespaces accessible to the caller. The OPA search filter injection policy applies exactly as for `POST /v1/memories/search`.

```
GET /v1/memories/events?ns=user&ns=alice&kinds=add&kinds=update&kinds=delete&limit=50
```

| Parameter | Type | Notes |
|---|---|---|
| `ns` | `string[]` (repeated) | Namespace prefix filter; OPA filter injection applies |
| `kinds` | `string[]` (repeated) | Event kinds to include: `add`, `update`, `delete`, `expired`; default all |
| `after` | ISO 8601 timestamp | Only return events with `occurred_at` after this time |
| `before` | ISO 8601 timestamp | Only return events with `occurred_at` before this time |
| `after_cursor` | string | Opaque pagination cursor (encodes `occurred_at + id`) |
| `limit` | integer | Max events per page; default 50, max 200 |

Response `200 OK`:

```json
{
  "events": [
    {
      "id": "<uuid>",
      "namespace": ["user", "alice", "memories"],
      "key": "first_meeting",
      "kind": "add",
      "occurred_at": "2026-01-01T00:00:00Z",
      "value": {"text": "Alice mentioned she loves Python."},
      "attributes": {"color": "blue"},
      "expires_at": null
    },
    {
      "id": "<uuid>",
      "namespace": ["user", "alice", "memories"],
      "key": "first_meeting",
      "kind": "update",
      "occurred_at": "2026-01-02T00:00:00Z",
      "value": {"text": "Alice loves Python and Go."},
      "attributes": {"color": "red"},
      "expires_at": null
    },
    {
      "id": "<uuid>",
      "namespace": ["user", "alice", "memories"],
      "key": "first_meeting",
      "kind": "delete",
      "occurred_at": "2026-01-03T00:00:00Z",
      "value": null,
      "attributes": null,
      "expires_at": null
    }
  ],
  "after_cursor": "<opaque cursor>"
}
```

- `occurred_at` = `created_at` for `add`/`update`; `deleted_at` for `delete`/`expired`
- `value` and `attributes` are decrypted and returned for `add`/`update` events; `null` for tombstoned events
- Events ordered by `occurred_at ASC, id ASC` for stable cursor pagination
- `add`/`update` events are sourced from the `kind` column; `delete`/`expired` events from `deleted_reason`

---

### 4. OPA/Rego Policy Integration

OPA is embedded in the Go service via `github.com/open-policy-agent/opa/v1`. Policies are loaded
from a configurable directory (default: `policies/memories/`) at startup with optional hot-reload.
If no policy directory is configured, the default built-in policy (namespace-ownership-based authz)
is used.

#### 4a. Access Control Policy

Evaluated on every read, write, or delete operation.

Input:

```json
{
  "operation": "write",
  "namespace": ["user", "alice", "memories"],
  "key": "first_meeting",
  "context": {
    "user_id": "alice",
    "client_id": "my-agent",
    "jwt_claims": { "sub": "alice", "roles": ["user"] }
  }
}
```

Expected output:

```json
{ "allow": true }
```

Default built-in policy (Rego):

```rego
package memories.authz

import future.keywords.in

default allow = false

# Users access their own namespace subtree
allow if {
    input.namespace[0] == "user"
    input.namespace[1] == input.context.user_id
}

# Admins access everything
allow if {
    "admin" in input.context.jwt_claims.roles
}
```

Custom policy example — shared agent namespace:

```rego
# Agents can access shared namespace (read only)
allow if {
    input.namespace[0] == "shared"
    input.operation == "read"
    input.context.client_id != ""
}
```

#### 4b. Attribute Extraction Policy

Called synchronously on every write. Extracts plaintext attributes from the memory value
for storage in the `attributes` column (used for filtering and vector payload).

Input:

```json
{
  "namespace": ["user", "alice", "memories"],
  "key": "first_meeting",
  "value": { "text": "Alice loves Python.", "tags": ["python"] },
  "attributes": { "color": "blue" }
}
```

Expected output (the attributes object directly, no envelope):

```json
{
  "namespace": "user",
  "sub": "alice"
}
```

Default policy: extracts `namespace` and `sub` from the namespace tuple.

#### 4c. Search Filter Injection Policy

Called before every search. Allows OPA to narrow the effective search scope based on
request context — e.g., non-admins are silently constrained to their own subtree.

Input:

```json
{
  "namespace_prefix": ["user"],
  "filter": { "topic": "python" },
  "context": {
    "user_id": "alice",
    "jwt_claims": { "roles": ["user"] }
  }
}
```

Expected output:

```json
{
  "namespace_prefix": ["user", "alice"],
  "attribute_filter": {
    "namespace": "user",
    "sub": "alice"
  }
}
```

The returned `namespace_prefix` replaces (or further constrains) the requested prefix.
`attribute_filter` is merged with the caller-provided `filter`.

---

### Attribute Filter Expressions

Filters are a flat JSON object where each key is a `policy_attributes` field name and the
value is a match expression. Three forms are supported — chosen to translate directly to
both SQL JSONB operators and vector-store payload filters (e.g. Qdrant's `match`/`range`):

| Form | Meaning | Example |
|---|---|---|
| bare scalar | equality | `"topic": "python"` |
| `{"in": [...]}` | set membership | `"lang": {"in": ["python", "go"]}` |
| `{"gt"|"gte"|"lt"|"lte": value}` | numeric / timestamp range | `"score": {"gte": 0.5}` |

All conditions in the object are ANDed. Examples:

```json
{ "topic": "python" }
```

```json
{ "lang": {"in": ["python", "go"]}, "has_tags": true }
```

```json
{ "created_year": {"gte": 2024, "lt": 2026} }
```

The same filter object is used in both `POST /v1/memories/search` request bodies and as
the `attribute_filter` output of the search filter injection policy.

---

### 5. Encryption

Memory values (`value_encrypted`) use AES-256-GCM via the existing MSEH/DEK provider
infrastructure from [Enhancement 066](066-db-stored-dek-table.md). No new encryption
primitives are introduced.

- **Encrypted**: `value_encrypted` (the full JSON value), `attributes` (user-supplied attributes; decrypted on read)
- **Plaintext**: `namespace`, `key`, `policy_attributes`, `expires_at` (required for filtering and indexing; `policy_attributes` is internal and never returned to clients)
- **Vector store**: never receives encrypted data — only embeddings and plaintext `policy_attributes`

---

### 6. Batch Vector Indexing

Primary store and vector store may be separate services (e.g., PostgreSQL + Qdrant). Vector
indexing is decoupled from the write path via the `indexed_at` column (`NULL` = pending sync).

A background goroutine polls for rows where `indexed_at IS NULL` at a configurable interval
(default: 30s) and processes each in one of two ways:

- **`deleted_at IS NULL`** (active row): generate embedding, upsert to vector store, set `indexed_at = NOW()`.
- **`deleted_at IS NOT NULL`** (soft-deleted row): remove the corresponding vector entry by `memory_id`,
  set `indexed_at = NOW()`.

The soft-delete path is triggered on both key deletion and update. On update, the write handler
resets `indexed_at = NULL` on the previous active row at the same time it inserts the new row
(same transaction, same timestamp), so both the old removal and the new embedding are processed
in the next indexer cycle.

Embedding model: same as used for entry embeddings, configured via
`MEMORY_SERVICE_EMBEDDING_MODEL`.

Admin-configurable settings:

| Setting | Default | Notes |
|---|---|---|
| `memory.episodic.indexing.batch_size` | 100 | Items processed per indexer cycle |
| `memory.episodic.indexing.interval` | 30s | Polling interval |
| `memory.episodic.namespace.max_depth` | 5 | Maximum allowed namespace depth |

---

### 7. TTL Cleanup

TTL cleanup runs as a background goroutine on a configurable interval (default: 60s) in two passes:

**Expiry pass** — soft-deletes memories whose TTL has elapsed:

```sql
UPDATE memories
SET deleted_at = NOW(), indexed_at = NULL
WHERE expires_at <= NOW() AND deleted_at IS NULL
```

Resetting `indexed_at = NULL` causes the batch indexer (§6) to remove the corresponding
vector entries on its next cycle. No direct vector store interaction occurs here.

**Eviction pass** — processes soft-deleted rows based on `deleted_reason`:

- `deleted_reason = 0` (updated): hard-delete immediately — the update event is captured by the
  new row's `kind = 1`. The indexer already handled vector cleanup via `indexed_at = NULL`.
- `deleted_reason IN (1, 2)` (deleted or expired): **tombstone** — clear `value_encrypted` and
  `attributes` (bulk-zeroed in one UPDATE), but keep the row. The row now serves solely as an
  event record for `GET /v1/memories/events`. A separate tombstone-retention pass hard-deletes
  tombstones older than the configured retention period.

This ensures the vector store is never left with orphaned entries and delete/expire events
remain queryable until the tombstone retention period expires.

Admin-configurable settings for TTL cleanup:

| Setting | Default | Notes |
|---|---|---|
| `memory.episodic.ttl.interval` | 60s | Polling interval for expiry + eviction passes |
| `memory.episodic.ttl.eviction_batch_size` | 100 | Max rows processed per eviction pass |
| `memory.episodic.ttl.tombstone_retention` | 90d | How long delete/expired tombstones are kept |

---

### 8. Admin API

New endpoints in `openapi-admin.yml`:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/admin/v1/memories/policies` | Download current active policy bundle |
| `PUT` | `/admin/v1/memories/policies` | Upload and hot-reload policy bundle |
| `DELETE` | `/admin/v1/memories/{id}` | Force-delete any memory by UUID |
| `GET` | `/admin/v1/memories/index/status` | Indexing queue depth (pending) |
| `POST` | `/admin/v1/memories/index/trigger` | Manually trigger one indexing cycle |
| `PUT` | `/admin/memories/settings` | Update namespace depth limit, indexing interval |

---

### 9. LangGraph Compatibility Layer

A Python package `memory-service-langgraph` in `python/langgraph/` implements `BaseStore`
by calling the memory-service Agent API. Namespace tuples are converted to dot-encoded
query parameters.

| LangGraph `BaseStore` method | Memory Service REST call |
|---|---|
| `put(ns, key, value, index)` | `PUT /v1/memories` |
| `get(ns, key)` | `GET /v1/memories?namespace=...&key=...` |
| `delete(ns, key)` | `DELETE /v1/memories?namespace=...&key=...` |
| `search(ns_prefix, query, filter, limit)` | `POST /v1/memories/search` |
| `list_namespaces(prefix, suffix, max_depth)` | `GET /v1/memories/namespaces` |

Authentication uses the same JWT/API key mechanism as all other Agent API clients,
configured via standard environment variables (`MEMORY_SERVICE_URL`, `MEMORY_SERVICE_TOKEN`).

---

## Testing

### Cucumber BDD Scenarios

```gherkin
Feature: Namespaced Episodic Memory

  Background:
    Given a memory service is running
    And user "alice" is authenticated

  Scenario: Write and read a memory
    When alice puts namespace ["user","alice","notes"], key "py_tip", value {"text":"Use list comprehensions"}
    Then getting namespace ["user","alice","notes"], key "py_tip" returns value {"text":"Use list comprehensions"}

  Scenario: TTL expiry
    When alice puts namespace ["user","alice","tmp"], key "ephemeral", value {"x":1} with ttl_seconds 1
    And 2 seconds pass
    Then getting namespace ["user","alice","tmp"], key "ephemeral" returns 404

  Scenario: Namespace prefix search
    When alice puts namespace ["user","alice","a"], key "k1", value {"text":"cats"}
    And alice puts namespace ["user","alice","b"], key "k2", value {"text":"dogs"}
    And alice puts namespace ["user","bob","c"],   key "k3", value {"text":"fish"}
    Then searching prefix ["user","alice"] returns keys ["k1","k2"] but not "k3"

  Scenario: Attribute filtering
    When alice puts namespace ["user","alice","mem"], key "m1", value {"text":"Python is great"} with extracted attributes {"lang":"python"}
    And alice puts namespace ["user","alice","mem"], key "m2", value {"text":"Go is fast"} with extracted attributes {"lang":"go"}
    Then searching prefix ["user","alice"] filter {"lang":"python"} returns only "m1"

  Scenario: OPA blocks cross-user access
    Given user "bob" is authenticated
    Then bob getting namespace ["user","alice","notes"], key "py_tip" returns 403

  Scenario: Namespace boundary matching
    When alice puts namespace ["user","aliced","notes"], key "trap", value {"text":"trap"}
    Then searching prefix ["user","alice"] does not return key "trap"

  Scenario: Semantic search
    When alice puts namespace ["user","alice","facts"], key "f1", value {"text":"Python uses indentation for blocks"}
    And the vector index has been synchronized
    Then searching prefix ["user","alice"] with query "whitespace-sensitive syntax" returns "f1" in top results

  Scenario: Delete removes from vector index
    When alice puts namespace ["user","alice","mem"], key "to_delete", value {"text":"delete me"}
    And alice deletes namespace ["user","alice","mem"], key "to_delete"
    Then getting namespace ["user","alice","mem"], key "to_delete" returns 404
    And the vector index has no entry for the deleted memory

  Scenario: List namespaces
    When alice puts namespace ["user","alice","mem"], key "k1", value {}
    And alice puts namespace ["user","alice","tasks"], key "k2", value {}
    Then listing namespaces with prefix ["user","alice"] returns [["user","alice","mem"],["user","alice","tasks"]]
```

### Unit Tests (Go)

- `internal/episodic/namespace_test.go`
  - `TestEncodeNamespace` — percent-encode + RS-join, empty segment rejection
  - `TestDecodeNamespace` — round-trip: split on RS, percent-decode each segment
  - `TestNamespaceDepthLimit` — depth limit enforcement
- `internal/episodic/ttl_test.go` — expired items deleted on next cleanup cycle
- `internal/episodic/indexer_test.go`
  - `TestIndexerEmbedsActiveRow` — active row (`deleted_at IS NULL`) is embedded and upserted
  - `TestIndexerSetsIndexedAt` — `indexed_at` is set after successful vector upsert
  - `TestIndexerRemovesSoftDeleted` — soft-deleted row (`deleted_at IS NOT NULL`) removes vector entry
  - `TestIndexerUpdateCycle` — update resets old row's `indexed_at`; indexer removes old vector entry and upserts new one
- `internal/episodic/policy_test.go`
  - `TestDefaultAuthzPolicyOwnerNamespace` — user allowed to access `user/<user_id>/...`
  - `TestDefaultAuthzPolicyCrossUser` — user blocked from `users/<other_id>/...`
  - `TestAttributeExtractionDefault` — returns empty attributes
  - `TestFilterInjectionNarrowsPrefix`

---

## Tasks

### Phase 1 — Core CRUD (no OPA, no vector search)

- [x] Add `memories`, `memory_vectors` PostgreSQL migration
- [x] Add equivalent MongoDB collections and indexes
- [x] Define Go model structs (`Memory`)
- [x] Implement `EpisodicStore` interface (primary store) with PostgreSQL backend
- [x] Implement namespace encoding/decoding helpers and depth validation
- [x] Implement `PUT /v1/memories` handler (upsert + DEK encryption)
- [x] Implement `GET /v1/memories` handler (DEK decryption)
- [x] Implement `DELETE /v1/memories` handler
- [x] Implement `POST /v1/memories/search` (attribute-filter-only, no vector)
- [x] Implement `GET /v1/memories/namespaces`
- [x] Add namespace depth limit config option
- [x] Wire handlers into Go router

### Phase 2 — Encryption

- [x] Integrate existing DEK provider for `value_encrypted` (AES-256-GCM)

### Phase 3 — OPA Access Control

- [x] Add `github.com/open-policy-agent/opa` dependency (v1.14.0; import path `rego` not `/v1/rego`)
- [x] Implement policy loader with configurable directory + hot-reload
- [x] Implement built-in default authz policy (namespace-ownership-based)
- [x] Wire authz policy evaluation into `PUT`, `GET`, `DELETE`, `search` handlers
- [x] Implement attribute extraction policy evaluation (called on `PUT`)
- [x] Implement search filter injection policy evaluation (called on `search`)
- [x] Add admin endpoints for policy bundle upload/download

### Phase 4 — TTL Cleanup

- [x] Background goroutine: soft-delete memories where `expires_at <= NOW()`; eviction pass hard-deletes confirmed rows
- [x] Configurable cleanup interval

### Phase 5 — Vector Indexing

- [x] PGVector: `memory_vectors` table + background indexer (Postgres primary store handles upsert via pgvector-go)
- [x] MongoDB: `memory_vectors` collection + in-memory cosine scoring for ANN (no Atlas required)
- [x] Qdrant: collection creation with ancestor-set payload fields
- [x] Background indexer goroutine (poll `indexed_at IS NULL`, embed, upsert)
- [x] Integrate `query` field in `POST /v1/memories/search` (ANN + attribute filter, falls back to attribute-only)
- [x] Qdrant prefix matching via `namespace_ancestors` equality filter
- [x] Multi-field embedding and max-pool deduplication of search results
- [x] Admin endpoint: `GET /admin/v1/memories/index/status`
- [x] Admin endpoint: `POST /admin/v1/memories/index/trigger` (manual cycle trigger)

### Phase 6 — LangGraph Python Client

- [x] Create `python/langgraph/` package skeleton (`memory-service-langgraph`)
- [x] Implement sync `MemoryServiceStore(BaseStore)` wrapping the REST API
- [x] Implement async `AsyncMemoryServiceStore(BaseStore)` variant
- [ ] Add integration tests against a running memory-service

### Phase 7 — MongoDB Backend

- [x] Implement `EpisodicStore` interface for MongoDB primary store (with in-memory vector search)

### Phase 8 — Contract and API Parity

- [x] Add episodic REST endpoints and schemas to `memory-service-contracts/src/main/resources/openapi.yml`
- [x] Regenerate clients/stubs and switch memories REST route to generated OpenAPI models
- [x] Add episodic RPCs/messages to `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`
- [x] Implement episodic gRPC handlers and register the new gRPC service
- [x] Add Cucumber feature coverage for episodic memory on both REST and gRPC interfaces

### Phase 9 — Event Timeline

- [ ] Add `kind SMALLINT` and `deleted_reason SMALLINT` columns to `memories` PostgreSQL migration
- [ ] Add equivalent fields to MongoDB `memories` collection
- [ ] Add event-timeline indexes (`memories_write_events_idx`, `memories_delete_events_idx`)
- [ ] Update `PUT /v1/memories` handler to set `kind` (0=add, 1=update) at write time
- [ ] Update `DELETE /v1/memories` handler to set `deleted_reason = 1` at soft-delete time
- [ ] Update TTL expiry pass to set `deleted_reason = 2`; update pass to set `deleted_reason = 0`
- [ ] Update eviction pass: hard-delete `deleted_reason = 0` rows; tombstone `deleted_reason IN (1,2)` rows
- [ ] Add tombstone-retention pass (hard-delete tombstones older than `tombstone_retention`)
- [ ] Add `tombstone_retention` config option
- [ ] Implement `GET /v1/memories/events` handler (cursor-paginated, OPA filter injection)
- [ ] Add `GET /v1/memories/events` to `openapi.yml` and regenerate clients
- [ ] Add Cucumber scenario coverage for event timeline (add, update, delete, expired kinds)

---

## Files to Modify

| File | Change |
|---|---|
| `internal/model/memory.go` | **new** — `Memory` struct |
| `internal/episodic/` | **new directory** — namespace helpers, indexer, TTL cleaner, OPA client |
| `internal/api/memories.go` | **new** — REST handlers for `/v1/memories*` |
| `internal/plugin/store/postgres/db/schema.sql` | Add `memories`, `memory_vectors` tables |
| `internal/plugin/store/postgres/memory_store.go` | **new** — PostgreSQL `MemoryStore` implementation |
| `internal/plugin/store/mongo/mongo.go` | Add `memories` collection and indexes |
| `internal/plugin/store/mongo/memory_store.go` | **new** — MongoDB `MemoryStore` implementation |
| `internal/plugin/vectorstore/pgvector/memory_vectors.go` | **new** — PGVector memory vector upsert/delete/search |
| `internal/plugin/vectorstore/qdrant/memory_vectors.go` | **new** — Qdrant memory vector upsert/delete/search with ancestor-set |
| `internal/config/config.go` | Add episodic memory settings (depth limit, indexing interval, policy dir) |
| `internal/cmd/serve/serve.go` | Wire episodic handlers, indexer, TTL cleaner |
| `memory-service-contracts/src/main/resources/openapi.yml` | Add `/v1/memories`, `/v1/memories/search`, `/v1/memories/namespaces`, `/v1/memories/events` |
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Add `/admin/memories/*` endpoints |
| `python/langgraph/` | **new** — LangGraph `BaseStore` Python package |

---

## Verification

```bash
# Build
go build ./...

# Unit tests
go test ./internal/episodic/... -count=1

# BDD integration tests (Postgres)
go test ./internal/bdd -run TestFeaturesPg -count=1

# BDD integration tests (Postgres + Encryption)
go test ./internal/bdd -run TestFeaturesPgEncrypted -count=1

# Python LangGraph client tests
cd python/langgraph && python -m pytest
```

---

## Design Decisions

**Why OPA/Rego rather than a bespoke policy DSL?**
OPA is a mature, well-documented policy engine with a Go-native embedding API. It supports
the full range of access control patterns (RBAC, ABAC, context-sensitive filtering) without
requiring a custom DSL. The three-policy structure (authz, attribute extraction, filter injection)
covers the common cases while remaining extensible.

**Why store `policy_attributes` in plaintext but encrypt `attributes`?**
Attribute-based pre-filtering must happen at the database level for performance. Decrypting
every memory to evaluate filter criteria at query time would be O(N) decryptions per search.
`policy_attributes` are derived by the OPA extraction policy specifically to be safe to store
plaintext and used for server-side filtering. User-supplied `attributes` are treated as opaque
content data — encrypted at rest alongside the value and decrypted only on read.

**Why decouple vector indexing from the write path?**
Primary DB and vector DB are often separate services with different latency/availability
characteristics. Synchronous indexing on every write would make PUT latency depend on the
vector DB. The `indexed_at` column tolerates temporary divergence and self-heals.


**Why percent-encoding + `\x1e` as the namespace delimiter?**
Percent-encoding each segment before joining ensures the output alphabet (ASCII letters,
digits, `-_.~`, `%XX`) never contains `\x1e`, making the delimiter unambiguous with no
character restrictions on segment content. `\x1e` (ASCII 30, Record Separator) is chosen
as the delimiter because it is outside the percent-encoding output alphabet by construction
and is a standard ASCII field separator. Standard library support exists in every language
(`url.PathEscape`/`url.PathUnescape` in Go; `urllib.parse.quote`/`unquote` in Python).

**Why not use LangGraph's built-in PostgresStore?**
LangGraph's PostgresStore pushes all access control to the application layer, has the
prefix-matching boundary bug documented above, and does not integrate with the memory-service's
existing encryption, embedding, or multi-backend infrastructure.

---

## Security Considerations

- **Namespace segment validation**: reject empty segments and enforce the configured max depth; reject keys exceeding 1 KB. No character restrictions are needed — percent-encoding handles all content safely.
- **Attribute plaintext exposure**: the attribute extraction OPA policy must not expose PII
  or sensitive fields. Document this as a policy authoring responsibility.
- **Vector store isolation**: the vector store is treated as an untrusted index. Even if
  compromised, it contains only embeddings and plaintext attributes — no encrypted values.
- **OPA hot-reload**: policy hot-reload writes are admin-only and authenticated.

---

## Open Questions

1. **Default index fields**: should the default be all string leaf fields, only a field named
   `"text"`, or none (opt-in)? LangGraph defaults to all fields; opt-in is safer for sensitive data.

2. **OPA policy bootstrap**: what default policy should ship with the service for users who have not configured a custom policy directory? A permissive "allow all" policy risks open access; a restrictive "deny all" requires every deployment to configure policies before memories work.

3. **Python package distribution**: pip-installable from PyPI, or local-only like the existing
   Python checkpoint package?

4. **Wildcard namespace support in `list_namespaces`**: LangGraph supports `*` wildcards in
   namespace prefix/suffix filters. Should these be supported in Phase 1, or deferred?
