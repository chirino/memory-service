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
  "kind": "default/v1",
  "created_at": "2026-01-01T00:00:00Z",
  "expires_at": "2026-01-02T00:00:00Z",
  "revision": 1
}
```

The `value` is not echoed back in the response — only the write confirmation is returned.

Calling `PUT` with an existing `(namespace, key)` pair upserts the memory, replacing the previous value.

#### Idempotent Writes and Preconditions

By default, omitting `revision_id` creates a new revision with a server-generated UUID and returns `200 OK`. To make writes retry-safe across network retries, clients can supply a caller-generated UUID `revision_id`.

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
    "revision_id": "6f1c1d0a-0000-4000-8000-000000000001",
    "revision": 1
  }'
```

- **First write with `revision_id`**: Returns `201 Created` with the new memory record.
- **Exact retry**: Retrying with the identical `revision_id` and content (`namespace`, `key`, `value`, `index`, `ttl_seconds`, and kind) returns `200 OK` with the stored revision record (`id`, `createdAt`, `expiresAt`) unchanged. Exact retries produce no new database rows, archive rows, event stream events, or outbox records.
- **Mismatched retry**: Retrying the same `revision_id` with different content, key, or namespace returns `409 Conflict` with code `memory_revision_already_exists`. To prevent existence disclosure across tenants, the response body contains only the error `code` and the caller's `revision_id`.
- **Create-only writes (`revision: 1`)**: Supplying `"revision": 1` (requires `revision_id`) ensures the memory is only created if no active revision currently exists for `(namespace, key)`. If an active memory exists, the write fails with `409 Conflict` (`code`: `revision_already_exists`).
- **Active revision precondition (`expected_revision_id`)**: Supplying `expected_revision_id` (requires `revision_id`) checks that the current active memory revision has the specified UUID before archiving and replacing it. If the active revision ID does not match, the write fails with `409 Conflict` (`code`: `expected_active_revision_id_mismatch`).
- **Numeric revision check (`expected_revision`)**: Numeric compare-and-swap can be combined on `PUT` or `PATCH`. If the active memory's numeric revision does not match, the request fails with `409 Conflict` (`code`: `memory revision conflict`).

##### Idempotency Window and Revision Caveat

- **Idempotency Window**: Exact-retry idempotency is guaranteed for as long as the revision record is retained. In high-churn environments, superseded revisions are pruned after an administrator-configurable retention window. Retries of pruned or archived-and-swept revisions return `404 Not Found`. Note that superseded-revision eviction policies and deleted tombstone retention are configured and operated independently.
- **Revision Number Across Retries**: The response `revision` field reflects the row's state at retrieval. If the memory was subsequently superseded or migrated between attempts, the returned `revision` integer may differ from the initial attempt; clients should not compare the numeric `revision` field across retries.

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
  "kind": "default/v1",
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

## Memory Kind Versions

Every memory row carries a **canonical kind name** — a string like `default/v1` or `customer-profile/v2` — that identifies which projection program was used to compute its plaintext attributes. Different memories can use different kinds in the same datastore, and migrations can move memories to a new kind online without stopping writes.

### Kind Names

A kind name combines a family and a version, separated by exactly one `/`:

```
customer-profile/v2
```

Both segments are lowercase DNS-label-like values: 1–63 characters, starting with a letter, containing only letters, digits, and hyphens. The name is the resource's unique identifier and is stored directly on the memory row.

### The Built-In Default Kind

The built-in kind `default/v1` is always registered from manifest and Rego source embedded in the Memory Service binary. Its immutable projection derives the compatibility `namespace` and `sub` attributes. Memories written without an explicit `kind` field always resolve to `default/v1`. The manifest and the built-in `authz.rego`, `projection.rego`, and `filter.rego` sources live together under `internal/episodic/default-v1/`; authz/filter remain global across kinds.

Its declared attributes are:

```json
{
  "namespace": "string",
  "sub": "string"
}
```

Its attribute-projection program is:

```rego
package memories.attributes

default attributes = {}

attributes = {"namespace": input.namespace[0], "sub": input.namespace[1]} if {
  count(input.namespace) >= 2
}
```

This Rego belongs to the immutable `default/v1` kind. It is separate from the global `authz.rego` and `filter.rego` programs described under [Access Control](#access-control).

### Selecting a Kind at Write Time

```bash
curl -X PUT http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "namespace": ["user", "alice", "events"],
    "key": "signup",
    "value": {"observedAt": "2026-01-01T00:00:00Z", "channel": "web"},
    "kind": "event-tracking/v2"
  }'
```

`kind` accepts:

| Value                 | Behavior                                  |
| --------------------- | ----------------------------------------- |
| `"event-tracking/v2"` | Use that exact writable version           |
| `"event-tracking"`    | Rejected; writes require an exact version |
| omitted               | Use the fixed built-in `default/v1`       |

The exact resolved name is returned in the write response as `kind`.

### Searching by Kind

Attribute-only `POST /v1/memories/search` accepts an optional `kind` selector:

```json
{
  "namespace_prefix": ["user", "alice"],
  "kind": "event-tracking/v2",
  "filter": { "channel": { "$eq": "web" } },
  "limit": 10
}
```

| Selector              | Matches                     |
| --------------------- | --------------------------- |
| `"event-tracking/v2"` | Only that exact version     |
| `"event-tracking"`    | All versions in that family |
| omitted               | All schemas                 |

A field absent from a given schema version does not match a condition on that field, so the same filter can safely be used across a family search.

### Attribute Sort

Attribute-only searches (no `query` or `queries`) support a single typed sort:

```json
{
  "namespace_prefix": ["user", "alice", "events"],
  "kind": "event-tracking/v2",
  "sort": { "field": "observedAt", "direction": "desc" },
  "limit": 20
}
```

Rules:

- `field` must be a top-level attribute name declared in the selected schema(s).
- `direction` is `"asc"` or `"desc"`.
- Memories with the field absent sort last in both directions.
- Ties break on `created_at DESC, id DESC`.
- String sorting uses binary/locale-independent collation (`COLLATE "C"` on PostgreSQL, `COLLATE BINARY` on SQLite).
- Sort is rejected when `query` or `queries` is present — semantic results are always ordered by similarity score.

## Searching Memories

`POST /v1/memories/search` supports attribute-filter search, single-query semantic search, and multi-query semantic search. All search modes honor the same `archived` selector used by direct reads: `exclude` (default), `include`, or `only`.

### Attribute-Filter Search

Without a `query`, the service applies an attribute filter against the primary store:

```bash
curl -X POST http://localhost:8080/v1/memories/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "namespace_prefix": ["user", "alice"],
    "filter": {"sub": {"$eq": "alice"}},
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

For prompts with multiple retrieval intents, send `queries` instead of `query`. Each query item has a required `text` and an optional `purpose`; the response uses the purpose as query attribution and falls back to the text when purpose is omitted.

```bash
curl -X POST http://localhost:8080/v1/memories/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "namespace_prefix": ["user", "alice", "cognition.v1"],
    "queries": [
      {"text": "release plan", "purpose": "release"},
      {"text": "Docker image build failure", "purpose": "docker"},
      {"text": "Python packages excluded from release", "purpose": "python-scope"}
    ],
    "per_query_limit": 5,
    "limit": 12
  }'
```

Multi-query search embeds all query texts in one batch, runs vector search independently for each query, deduplicates memory IDs, and merges rankings with Reciprocal Rank Fusion. Returned items include `matchedQueries` so callers can see which query purposes matched each memory:

```json
{
  "items": [
    {
      "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "namespace": ["user", "alice", "cognition.v1", "procedures"],
      "key": "procedure:deployment-debugging",
      "value": { "text": "Check CI image build logs before rerunning release." },
      "score": 0.0325,
      "matchedQueries": ["release", "docker"]
    }
  ]
}
```

`query` and `queries` are mutually exclusive. `queries` must be non-empty when present, and every `queries[].text` must be non-blank.

`score` is `null` for attribute-only results, a cosine similarity value (0–1) for single-query semantic results, and an RRF score for multi-query semantic results. Semantic search pre-filters by archive state in the vector store and then re-checks the hydrated memory rows before returning results.

### Search Parameters

| Parameter          | Type       | Required | Description                                                                                       |
| ------------------ | ---------- | -------- | ------------------------------------------------------------------------------------------------- |
| `namespace_prefix` | `string[]` | yes      | Restricts results to this namespace subtree                                                       |
| `query`            | `string`   | no       | Single semantic search string. Mutually exclusive with `queries`                                  |
| `queries`          | `object[]` | no       | Multi-query semantic search strings with required `text` and optional `purpose`                   |
| `per_query_limit`  | `integer`  | no       | Per-query vector search budget for multi-query search, default `limit`, max 100                   |
| `filter`           | `object`   | no       | Attribute filter expressions (see below)                                                          |
| `kind`             | `string`   | no       | Schema selector: exact name, family name, or omit for all schemas                                 |
| `sort`             | `object`   | no       | One-field typed sort: `{"field":"<attr>","direction":"asc\|desc"}`. Rejected with semantic search |
| `archived`         | `string`   | no       | `exclude` (default), `include`, or `only`                                                         |
| `limit`            | `integer`  | no       | Max results, default 10, max 100                                                                  |

### Attribute Filter Expressions

Filters are a flat JSON object where each key is an attribute field name. Search returns a bounded top-k result set; it is not pageable, and request fields such as `offset`, `order`, or `after_cursor` are rejected.

The filter language uses positive, pushdownable predicates only:

| Form                            | Meaning                 | Example                               |
| ------------------------------- | ----------------------- | ------------------------------------- |
| Bare scalar or `{"$eq": value}` | Equality                | `{"topic": "python"}`                 |
| Array or `{"$in": [...]}`       | Set membership          | `{"lang": {"$in": ["python", "go"]}}` |
| `{"$gte"/"$lte": value}`        | Numeric/timestamp range | `{"score": {"$gte": 0.5}}`            |
| `{"$exists": true}`             | Present non-null value  | `{"sourceHash": {"$exists": true}}`   |

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
| `limit`            | Max events per page; default 50, server-configurable maximum  |

The same OPA access control that governs memory reads applies here — callers only see events for namespaces they can access. `value` and `attributes` are `null` for `expired` events; archive operations appear as `update` events.

## Memory Properties

| Property     | Description                                                              |
| ------------ | ------------------------------------------------------------------------ |
| `id`         | Unique UUID assigned on each write                                       |
| `namespace`  | Ordered list of string segments forming the address                      |
| `key`        | Unique key within the namespace                                          |
| `value`      | Arbitrary JSON object; encrypted at rest                                 |
| `attributes` | Policy-derived plaintext attributes used for filtering/search scoping    |
| `kind`       | Canonical schema name used for this row (e.g. `"default/v1"`)            |
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

Memory access is enforced by embedded **OPA/Rego policies** evaluated on every memory API call. The service loads policy definitions from the comma-separated `--policy-import-path` or `MEMORY_SERVICE_POLICY_IMPORT_PATH` setting.

A configured directory may contain this optional pair of global policy overrides:

- `authz.rego` — read/write/delete authorization
- `filter.rego` — search/list namespace and filter injection

The two global files are optional as a pair. When neither is present, the service uses its built-in authorization and scoping programs. Each configured directory contributes only root-level global files. You can also list either global file explicitly. The complete import path must resolve at most one file of each name and must provide both files or neither file. Other Rego files are available as assets for manifest-based policy types and are not loaded as global programs.

Each configured directory is searched recursively for `.yaml` and `.yml` files. Explicit file entries are examined regardless of their extension. Documents without `kind: memory-kind` are ignored. Matching documents are decoded strictly and may reference a projection file relative to their own directory. Imports insert only absent versions. Identical versions are logged, and same-name differences are logged without overwriting the database. The built-in `default/v1` kind handles namespace/sub projection for memories written without an explicit kind.

If no path is set, the service uses its built-in authorization and scoping programs and imports no deployment-provided memory kinds. The distributed container image copies the policy tree to `/etc/memory-service/policies/`, including the cognition bundle under `cognition/`, and sets the parent directory as its default import path.

### Policy import path configuration

The policy import path accepts a comma-separated list of files and directories. You can also repeat `--policy-import-path`; each occurrence appends to the list.

```text
/etc/memory-service/policies/,/opt/acme/customer-profile.yaml
```

A directory can contain global Rego overrides and any number of manifest-based policy bundles:

```text
<policy-directory>/
├── authz.rego                 # optional global override
├── filter.rego                # optional global override
└── cognition/
    ├── cognition.yaml         # discovered recursively
    └── projection.rego
```

The global overrides use filename-based discovery:

- `authz.rego` and `filter.rego` are exact, case-sensitive filenames. They can be located directly in a configured directory or listed as explicit file entries.
- The import path must provide both files or neither file. It cannot resolve more than one file of either name.
- `authz.rego` must expose `data.memories.authz.decision`; `filter.rego` must expose `data.memories.filter`.
- Global Rego discovery is not recursive. Other `.rego` files are not loaded as global programs.

Manifest-based policy documents use discriminator-based discovery. Every `.yaml` and `.yml` file below a configured directory is examined recursively. Files with other extensions in that directory are ignored. An explicit file entry is examined regardless of its extension. Documents without `kind: memory-kind`, including documents for future policy types, are ignored by the memory-kind importer.

A memory-kind manifest can embed its projection in `projectionRego` or reference a Rego file relative to the manifest with `projectionRegoFile`:

```yaml
kind: memory-kind
name: customer-profile/v1
attributes:
  customerId: string
  updatedAt: timestamp
projectionRegoFile: projection.rego
writable: true
```

The referenced filename is arbitrary; `projection.rego` is the recommended convention. Relative references cannot escape the manifest's directory. The referenced program must declare `package memories.attributes` and define an `attributes` rule, producing `data.memories.attributes.attributes`. A `.rego` file that is neither a root-level global override nor referenced by a manifest is ignored.

### Rego Policy Input Variables

Each policy is evaluated with an `input` object. Available fields differ by policy type.

#### `authz.rego` (`data.memories.authz.decision`)

| `input` field        | Type                    | Description                                                 |
| -------------------- | ----------------------- | ----------------------------------------------------------- |
| `operation`          | `string`                | Operation being authorized: `write`, `read`, or `delete`    |
| `namespace`          | `string[]`              | Full namespace segments from the request                    |
| `key`                | `string`                | Memory key from the request                                 |
| `kind`               | `string`                | Exact resolved kind for writes, or the selected row's kind  |
| `value`              | `object`                | Present for `write`; full memory value payload              |
| `index`              | `object<string,string>` | Present for `write`; caller-provided redacted index payload |
| `context.user_id`    | `string`                | Authenticated subject/user ID                               |
| `context.client_id`  | `string`                | Authenticated client ID (API key/OIDC client), when present |
| `context.jwt_claims` | `object`                | Raw JWT claims map (for example `roles`)                    |

#### `filter.rego` (`data.memories.filter`)

| `input` field        | Type       | Description                                                 |
| -------------------- | ---------- | ----------------------------------------------------------- |
| `namespace_prefix`   | `string[]` | Requested namespace prefix for search/list                  |
| `filter`             | `object`   | Caller-supplied attribute filter (may be empty)             |
| `kind`               | `string`   | Caller-supplied exact/family kind selector (may be empty)   |
| `context.user_id`    | `string`   | Authenticated subject/user ID                               |
| `context.client_id`  | `string`   | Authenticated client ID (API key/OIDC client), when present |
| `context.jwt_claims` | `object`   | Raw JWT claims map (for example `roles`)                    |

The `filter.rego` result may return:

- `namespace_prefix` (`string[]`) — effective prefix to enforce
- `attribute_filter` (`object`) — merged into the caller filter before datastore query
- `kind` (`string`) — optional exact/family selector that can only narrow the caller's selector

The result must be an object and any returned fields must have these exact types. Malformed output fails the request closed.

### Default Built-In Policy (Repo Default)

The default global authorization and search-scoping programs are shown below. They are independent of the `default/v1` attribute-projection Rego above and apply across all memory kinds.

```rego
package memories.authz

default decision = {"allow": false, "reason": "access denied"}

decision = {"allow": true} if {
  input.namespace[0] == "user"
  input.namespace[1] == input.context.user_id
}
```

```rego
package memories.filter

namespace_prefix := input.namespace_prefix if {
  starts_with(input.namespace_prefix, user_prefix)
}
namespace_prefix := user_prefix if {
  not starts_with(input.namespace_prefix, user_prefix)
}

user_prefix := ["user", input.context.user_id]

starts_with(ns, prefix) if {
  count(prefix) == 0
}
starts_with(ns, prefix) if {
  count(ns) >= count(prefix)
  not mismatch(ns, prefix)
}
mismatch(ns, prefix) if {
  some i
  i < count(prefix)
  ns[i] != prefix[i]
}

# Authorization scoping is entirely expressed by namespace_prefix. Custom kinds
# are not required to project security attributes.
attribute_filter := {}
```

What this means in practice:

- `authz.rego`: direct `PUT`/`GET`/`DELETE` is allowed only under `["user", <caller_user_id>, ...]`; deny responses can carry a `reason`.
- `filter.rego`: every public search/list call is constrained to the authenticated caller's own `["user", <caller_user_id>]` subtree, including calls made by principals that also have an admin role.

Administrative memory exploration uses the separate `/admin/v1/...` endpoints described below. Those endpoints bypass the user-facing `filter.rego`, except that admin search with `as_user_id` deliberately evaluates it as the target user with no administrative roles.

See the [Admin APIs](/docs/concepts/admin-apis/) for schema version, migration, and index management endpoints.

## Admin Memory Exploration

Admin and auditor users can inspect episodic memories through a dedicated admin surface. Admin users can also write or archive namespace-scoped memories through this surface. These endpoints are separate from `/v1/memories`; they do not rely on user-facing OPA policy injection unless an admin explicitly searches as a target user.

| Method  | Endpoint                      | Role             | Purpose                                                                                              |
| ------- | ----------------------------- | ---------------- | ---------------------------------------------------------------------------------------------------- |
| `GET`   | `/admin/v1/memories`          | admin or auditor | List latest memory rows across users with filters and cursor pagination                              |
| `PUT`   | `/admin/v1/memories`          | admin            | Upsert a memory in any namespace                                                                     |
| `PATCH` | `/admin/v1/memories`          | admin            | Archive an active memory by namespace and key                                                        |
| `GET`   | `/admin/v1/memories/{id}`     | admin or auditor | Read a retained memory row by UUID without incrementing usage counters                               |
| `POST`  | `/admin/v1/memories/search`   | admin or auditor | Search across users by namespace prefix, safe attributes, and optional semantic query or query batch |
| `GET`   | `/admin/v1/memory-namespaces` | admin or auditor | Browse memory namespace trees across users                                                           |

Admin list and namespace query parameters use camelCase query names, for example:

```bash
curl "http://localhost:8080/admin/v1/memories?namespacePrefix=user&namespacePrefix=alice&includeUsage=true" \
  -H "Authorization: Bearer <admin-or-auditor-token>" \
  -H "X-Justification: support investigation"
```

Admin search reuses the public memory search JSON shape, including single-query and multi-query semantic search, and adds `as_user_id`:

```bash
curl -X POST http://localhost:8080/admin/v1/memories/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <admin-or-auditor-token>" \
  -H "X-Justification: support investigation" \
  -d '{
    "namespace_prefix": ["user"],
    "as_user_id": "alice",
    "kind": "preference",
    "filter": {"category": {"$in": ["preference", "procedure"]}},
    "limit": 25
  }'
```

When `as_user_id` is omitted, admin search is admin-wide and caller filters narrow that result set. When `as_user_id` is set, the server applies the same memory search policy that the target user would receive from public `POST /v1/memories/search`.

Admin writes use the same memory body shape as public `PUT /v1/memories` and support `revision_id`, `expected_revision_id`, `revision: 1`, and `expected_revision` for idempotent writes and compare-and-swap preconditions. Admin archive uses repeated `ns` query parameters plus `key`, with `{"archived": true}` in the request body:

```bash
curl -X PUT "http://localhost:8080/admin/v1/memories?justification=cognition%20processor%20write" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <admin-token>" \
  -d '{
    "namespace": ["user", "alice", "cognition.v1", "facts"],
    "key": "preference-theme",
    "value": {"content": "Alice prefers light theme"},
    "index": {"content": "Alice prefers light theme"}
  }'
```

```bash
curl -X PATCH "http://localhost:8080/admin/v1/memories?ns=user&ns=alice&ns=cognition.v1&ns=facts&key=preference-theme&justification=cognition%20cleanup" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <admin-token>" \
  -d '{
    "archived": true,
    "expected_revision": 2
  }'
```

Admin memory writes are authorized by admin role, OIDC scope when configured, and optional justification enforcement. They intentionally bypass user-facing memory OPA authorization because they are administrative namespace operations. Attribute projection still uses the selected immutable `MemoryKindVersion`, with only persisted namespace, key, value, and index inputs, so agent and admin writes produce the same replayable metadata. Admin write requests do not carry `on_behalf_of_user_id`.

Operational memory admin routes (separate from the CRUD surface above):

| Method   | Endpoint                         | Role  |
| -------- | -------------------------------- | ----- |
| `GET`    | `/admin/v1/memory-index/status`  | admin |
| `POST`   | `/admin/v1/memory-index/trigger` | admin |
| `GET`    | `/admin/v1/memory-usage`         | admin |
| `GET`    | `/admin/v1/memory-usage/top`     | admin |
| `DELETE` | `/admin/v1/memories/{id}`        | admin |

If admin justification enforcement is enabled, admin memory routes accept `X-Justification` or `?justification=...`; gRPC admin memory requests carry a `justification` field.

## Encryption

Memory values are **encrypted at rest** using AES-256-GCM via the service's existing key-management infrastructure. The namespace, key, policy-derived attributes, caller-provided `index` payload (stored as `indexed_content`), and expiry timestamp are stored in plaintext for filtering and indexing.

Vector stores never receive encrypted data. They hold only embeddings and plaintext attributes derived by the memory's immutable kind projection.

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
- See [Admin APIs](/docs/concepts/admin-apis/) for schema version, migration, and index management endpoints
