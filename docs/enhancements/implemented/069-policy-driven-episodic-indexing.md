---
status: implemented
---

# Enhancement 069: Application-Provided Index Content with Policy Guardrails

> **Status**: Implemented

## Summary

Move indexed text selection/redaction to the application by adding an explicit `index` object to memory writes. Keep policy extraction focused on `attributes` only, extend write authz policy input/output so policy can deny over-indexing with a concrete deny reason, and add LangGraph store hooks so application developers can control how index fields are selected and redacted.

## Motivation

Current behavior splits indexing responsibility between request hints and server-side extraction defaults:

- clients send `index_fields` / `index_disabled`
- server derives index text from raw `value`
- `attributes.rego` and `authz.rego` do not have enough context to enforce indexed field-count and payload-size limits

We want the same pattern as entries indexing: the caller provides explicitly indexable text (already redacted), while policy still enforces safety.

Goals:

- make the application explicitly choose and redact indexed content
- remove legacy indexing selector fields
- keep policy-owned derived attributes
- let authz policy reject write requests that would over-index, with a deny reason
- provide a LangGraph store API for custom index selection/redaction logic

## Design

### 1. Contract Changes

#### Agent API `PUT /v1/memories`

Replace write-time indexing selector flags with an explicit `index` payload:

| Field | Type | Required | Notes |
|---|---|---|---|
| `namespace` | `string[]` | yes | unchanged |
| `key` | `string` | yes | unchanged |
| `value` | `object` | yes | unchanged |
| `ttl_seconds` | `integer` | no | unchanged |
| `index` | `object<string,string>` | no | caller-provided, redacted text fragments to embed; `{}`/omitted means no indexing |

Remove from request:

- `attributes`
- `index_fields`
- `index_disabled`

OpenAPI schema shape:

```yaml
PutMemoryRequest:
  type: object
  required: [namespace, key, value]
  properties:
    namespace:
      type: array
      items: { type: string }
    key:
      type: string
      minLength: 1
      maxLength: 1024
    value:
      type: object
      additionalProperties: true
    ttl_seconds:
      type: integer
      minimum: 0
    index:
      type: object
      additionalProperties:
        type: string
```

#### gRPC `PutMemory`

Mirror the REST change:

- remove `attributes`
- remove `index_fields`
- remove `index_disabled`
- add `index` as a text map

```protobuf
message PutMemoryRequest {
  repeated string namespace = 1;
  string key = 2;
  google.protobuf.Struct value = 3;
  int32 ttl_seconds = 4;
  map<string, string> index = 5;
}
```

Pre-release stance applies: no backward compatibility shims are required.

### 2. Authz Policy: Write-Time Guardrails + Deny Reason

Change authz policy contract from boolean-only to structured decision.

Current query target:

- `data.memories.authz.allow` (bool)

New query target:

- `data.memories.authz.decision` (object)

New decision shape:

```json
{
  "allow": false,
  "reason": "too many indexed fields (max 8)"
}
```

`reason` is optional when `allow=true`.

Write input shape to `authz.rego`:

```json
{
  "operation": "write",
  "namespace": ["user", "alice", "notes"],
  "key": "py-tip",
  "value": {"text": "Use list comprehensions", "raw_notes": "PII..."},
  "index": {"text": "Use list comprehensions"},
  "context": {
    "user_id": "alice",
    "client_id": "chat-frontend",
    "jwt_claims": {"roles": ["user"]}
  }
}
```

Read/delete input keeps `operation`, `namespace`, `key`, `context`; `value` and `index` are omitted.

Example policy enforcing max `index` fields:

```rego
package memories.authz

import future.keywords.if

default decision = {"allow": false, "reason": "denied by policy"}

decision = {"allow": true} if {
  input.operation == "write"
  count(object.keys(input.index)) <= 8
  input.namespace[0] == "user"
  input.namespace[1] == input.context.user_id
}

decision = {"allow": false, "reason": "too many indexed fields (max 8)"} if {
  input.operation == "write"
  count(object.keys(input.index)) > 8
}
```

Server behavior on deny:

- REST: `403` with `{ "error": "access denied", "reason": <policy-reason?> }`
- gRPC: `PermissionDenied` with message set to policy reason when provided (fallback `"access denied"`)

### 3. Attributes Policy: Attributes-Only Extraction

Keep attributes extraction focused on filter attributes only.

Query target remains:

- `data.memories.attributes.attributes`

Input shape:

```json
{
  "namespace": ["user", "alice", "notes"],
  "key": "py-tip",
  "value": {"text": "Use list comprehensions"},
  "index": {"text": "Use list comprehensions"},
  "context": {
    "user_id": "alice",
    "client_id": "chat-frontend",
    "jwt_claims": {"roles": ["user"]}
  }
}
```

Output shape:

```rego
package memories.attributes

default attributes = {}

attributes = {"namespace": input.namespace[0], "sub": input.namespace[1]} {
  count(input.namespace) >= 2
}
```

### 4. Write and Indexing Flow

1. Bind request including caller-provided `index`.
2. Evaluate `authz.rego` with write input including `value` and `index`.
3. If denied, return `403/PermissionDenied` and include policy reason when present.
4. Evaluate `attributes.rego` to derive `policy_attributes`.
5. Persist:
   - encrypted `value`
   - plaintext `policy_attributes`
   - plaintext `indexed_content` (caller-redacted text map)
6. Indexer reads pending rows and embeds exactly persisted `indexed_content` entries.
7. Upsert vectors per `(memory_id, field_name)` as today.

`index` is the only source for episodic vector text. The server no longer derives string leaves from `value`.

### 5. Data Model and Schema Changes

#### PostgreSQL

`memories` table changes:

- drop column `attributes BYTEA`
- drop column `index_fields JSONB`
- drop column `index_disabled BOOLEAN`
- add column `indexed_content JSONB NOT NULL DEFAULT '{}'::jsonb`

Schema patch (in `internal/plugin/store/postgres/db/schema.sql`):

```sql
ALTER TABLE memories
  DROP COLUMN IF EXISTS attributes,
  DROP COLUMN IF EXISTS index_fields,
  DROP COLUMN IF EXISTS index_disabled,
  ADD COLUMN IF NOT EXISTS indexed_content JSONB NOT NULL DEFAULT '{}'::jsonb;
```

Go model/store updates:

- `internal/model/memory.go`: replace `Attributes`, `IndexFields`, `IndexDisabled` with `IndexedContent map[string]string`
- `internal/plugin/store/postgres/episodic_store.go`: write/read `indexed_content`
- `internal/registry/episodic/plugin.go`:
  - `PutMemoryRequest.Index map[string]string`
  - `PendingMemory.IndexedContent map[string]string`

#### MongoDB

`memories` document changes:

- remove `attributes`
- remove `index_fields`
- remove `index_disabled`
- add `indexed_content` subdocument (`{ "<field>": "<redacted text>" }`)

Migration behavior in `mongoEpisodicMigrator` (`internal/plugin/store/mongo/episodic_store.go`):

- ensure missing `indexed_content` is initialized to `{}` for active docs
- stop writing removed fields

#### Vector Storage

No schema changes to `memory_vectors`; it still stores:

- `memory_id`
- `field_name`
- `namespace`
- `policy_attributes`
- `embedding`

### 6. API Surface Simplification

- remove user-supplied `attributes` from write requests
- remove legacy index selector fields (`index_fields`, `index_disabled`)
- keep response `attributes` field, now sourced from policy-derived attributes

This removes server-side index-field discovery and makes client redaction explicit and auditable.

### 7. LangGraph Store Controls for Selection + Redaction

The LangGraph store adapters must convert LangGraph `put(..., index=...)` into the new REST `index` map payload and allow user-controlled selection/redaction.

Proposed adapter controls:

- `index_builder`: full override callback that returns the final `index` map.
- `index_redactor`: per-field callback used by the default builder to redact or drop indexed text.
- default builder behavior when no `index_builder` is provided:
  - `index is False` -> `{}`
  - `index is list[str]` -> include only those dotted paths when they resolve to strings in `value`
  - `index is None/True` -> include all string leaf fields from `value`
  - apply `index_redactor` to each candidate field (`None` return drops field)

Python shape:

```python
from typing import Any
from memory_service_langgraph import MemoryServiceStore

def redact(path: str, text: str, value: dict[str, Any]) -> str | None:
    if path.endswith("ssn"):
        return None
    if path == "summary":
        return text[:500]
    return text

store = MemoryServiceStore(
    base_url="http://localhost:8082",
    token="...",
    index_redactor=redact,
)
```

Advanced users can fully own construction of the `index` payload via `index_builder`.

## Security Considerations

- Attribute spoofing is reduced because write requests no longer accept caller `attributes`.
- Redaction boundary is explicit: only `index` content is considered indexable text.
- `authz.rego` can cap indexed field count, enforce field-name allowlists, and deny large payloads before persistence.
- No hard server-side index caps are added in handlers; limits are policy-driven only.
- Because `indexed_content` is caller-redacted and intentionally indexable, it is stored as plaintext JSON (matching the entries pattern).
- LangGraph redaction hooks must run before write calls so sensitive fields never enter the `index` payload.

## Testing

### Cucumber BDD Scenarios

```gherkin
Feature: Application-provided index content with policy guardrails

  Scenario: Write denied when indexed field count exceeds authz limit
    Given authz policy enforces a max of 8 indexed fields
    When I put a memory with 9 indexed fields
    Then the request fails with 403
    And the response reason is "too many indexed fields (max 8)"

  Scenario: Caller-provided index content is embedded exactly
    When I put a memory with index={"title":"safe title","summary":"safe summary"}
    Then exactly 2 vector rows are created for that memory
    And vector field names are "summary" and "title"

  Scenario: Omitted index payload skips vector upserts
    When I put a memory without index
    Then the memory is stored
    And no vector rows are created for that memory

  Scenario: LangGraph store redactor drops sensitive fields
    Given a LangGraph store with an index redactor that drops "profile.ssn"
    When I put a value containing profile.ssn and profile.summary
    Then the request index payload contains only "profile.summary"

  Scenario: attributes request field is rejected
    When I put a memory request containing attributes
    Then the request fails with 400
```

### Unit Tests

- policy engine:
  - authz decision decoding (`allow`, optional `reason`)
  - write input includes `value` and `index`
  - attribute extraction still returns plain map only
- store layer:
  - persist/load `indexed_content` for Postgres and Mongo
- indexer:
  - embeds only `indexed_content`
  - no fallback extraction from `value`
- API contract:
  - request binding for `index`
  - authz deny reason propagation in REST and gRPC error responses
  - absence/removal of `attributes`, `index_fields`, `index_disabled`
- Python LangGraph adapter:
  - default index builder behavior (`False`, list, `None/True`)
  - custom `index_redactor` behavior (mutate/drop)
  - custom `index_builder` override behavior

## Tasks

- [x] Update OpenAPI `PutMemoryRequest`: remove `attributes`, `index_fields`, `index_disabled`; add `index: object<string,string>`
- [x] Update protobuf `PutMemoryRequest`: remove legacy fields; add `map<string,string> index`
- [x] Regenerate generated API/proto clients and server bindings
- [x] Change authz policy evaluation from boolean to decision object (`allow`, optional `reason`)
- [x] Include `value` and `index` in write authz input payload
- [x] Keep attributes policy extraction as attributes-only
- [x] Persist `indexed_content` in Postgres and Mongo memories
- [x] Update indexer to embed persisted `indexed_content` entries only
- [x] Remove legacy server-side index field extraction logic
- [x] Update LangGraph adapters (sync + async) to send `index` payload instead of legacy fields
- [x] Add LangGraph store user controls for index field selection/redaction (`index_builder`, `index_redactor`)
- [x] Add BDD + unit coverage for authz deny reason and indexed-content indexing
- [x] Update docs (`memories.md`, `configuration.mdx`) with the new write/authz contract

## Files to Modify

| File | Change |
|---|---|
| `memory-service-contracts/src/main/resources/openapi.yml` | Update `PutMemoryRequest` (`index` map; remove legacy fields) |
| `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto` | Update `PutMemoryRequest` + regen stubs |
| `internal/episodic/policy.go` | Authz decision-object evaluation + deny reason plumbing; write input includes `value/index` |
| `internal/plugin/route/memories/memories.go` | REST binding changes for `index`; include authz reason in deny response |
| `internal/grpc/server.go` | gRPC binding changes for `index`; include authz reason in PermissionDenied |
| `internal/registry/episodic/plugin.go` | Replace `Attributes/IndexFields/IndexDisabled` request fields with `Index` map; update `PendingMemory` |
| `internal/service/episodic_indexer.go` | Embed `PendingMemory.IndexedContent` only; remove raw value extraction path |
| `internal/model/memory.go` | Replace legacy indexing columns with `IndexedContent` |
| `internal/plugin/store/postgres/db/schema.sql` | Postgres `memories` schema changes (`indexed_content`, drop old fields) |
| `internal/plugin/store/postgres/episodic_store.go` | Persist/load `indexed_content` and updated structs |
| `internal/plugin/store/mongo/episodic_store.go` | Persist/load `indexed_content` and migration/backfill behavior |
| `site/src/pages/docs/concepts/memories.md` | Document `index` request contract and authz deny reason behavior |
| `python/langgraph/memory_service_langgraph/store.py` | Send `index` payload in sync writes; add user controls (`index_builder`, `index_redactor`) |
| `python/langgraph/memory_service_langgraph/async_store.py` | Send `index` payload in async writes; add user controls (`index_builder`, `index_redactor`) |
| `python/langgraph/memory_service_langgraph/__init__.py` | Export LangGraph index control types/hooks |

## Verification

```bash
# Compile Go service
go build ./...

# Run Go tests (capture full output)
task test:go > test.log 2>&1
rg -n "ERROR|FAIL|panic|--- FAIL" test.log

# Verify site docs build (if docs changed)
cd site && npm run build
```

## Non-Goals

- Changing vector scoring/dedup logic.
- Reintroducing server-side automatic index-field extraction from raw `value`.
- Introducing backward compatibility shims for removed write request fields.

## Design Decisions

1. Indexed content is caller-provided and redacted (`index` map), not server-derived.
2. Attributes policy remains attributes-only; indexing selection is not done by `attributes.rego`.
3. Authz policy is upgraded to structured decision output so deny reason can be surfaced.
4. Deny responses expose only policy `reason` (no separate policy `code` field).
5. No hard server-side index caps are enforced; index limits are policy-only.
