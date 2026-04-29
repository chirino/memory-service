---
status: implemented
---

# Enhancement 097: Admin Client Checkpoints

> **Status**: Implemented.

## Summary

Add a small admin-only checkpoint API that lets external processors persist and retrieve encrypted, processor-defined JSON progress state by stable admin `clientId`. The API is intentionally generic so external processors, indexers, or future background clients can resume from the last acknowledged cursor without adding processor-specific database tables.

## Motivation

Remote processors often consume durable streams or batch work outside the main memory-service process. They need a reliable place to save restart state after successful processing so they do not lose progress or reprocess large event ranges after a crash.

Keeping checkpoint persistence inside the admin API gives processor deployments a single authenticated control plane. Processors should use dedicated admin credentials and use that credential's `clientId` as the checkpoint key. The service validates ownership, checkpoint key, content type, and JSON shape, and encrypts the payload at rest, while the processor owns the payload contract.

## Design

### REST API

The admin API exposes two endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/v1/admin/checkpoints/{clientId}` | Return the latest checkpoint for one admin client. |
| `PUT` | `/v1/admin/checkpoints/{clientId}` | Create or replace the checkpoint for one admin client. |

Both endpoints require the admin role. Auditors and regular users cannot read or write checkpoints. Callers should pass the processor credential's stable `clientId` as the path parameter. When the request has an authenticated client ID, it must match the path `clientId`. Admin calls without a client ID can manage any checkpoint.

Example write:

```http
PUT /v1/admin/checkpoints/checkpoint-processor
Content-Type: application/json

{
  "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
  "value": {
    "cursor": "cursor-123",
    "processed": 3,
    "recentConversationIds": ["conv-a", "conv-b"]
  }
}
```

Example response:

```json
{
  "clientId": "checkpoint-processor",
  "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
  "value": {
    "cursor": "cursor-123",
    "processed": 3,
    "recentConversationIds": ["conv-a", "conv-b"]
  },
  "updatedAt": "2026-04-29T12:00:00Z"
}
```

### Data Model

Each store implements `AdminCheckpointStore`:

```go
type ClientCheckpoint struct {
    ClientID    string          `json:"clientId"`
    ContentType string          `json:"contentType"`
    Value       json.RawMessage `json:"value"`
    UpdatedAt   time.Time       `json:"updatedAt"`
}

type AdminCheckpointStore interface {
    AdminGetCheckpoint(ctx context.Context, clientID string) (*ClientCheckpoint, error)
    AdminPutCheckpoint(ctx context.Context, checkpoint ClientCheckpoint) (*ClientCheckpoint, error)
}
```

PostgreSQL and SQLite store checkpoints in `admin_checkpoints` with `client_id` as the primary key. MongoDB stores checkpoints in the `admin_checkpoints` collection with a unique `client_id` index.

The `value` field is encrypted before it is written to the store and decrypted before it is returned by the API. When database encryption is disabled, the same code path stores the JSON bytes as opaque `BYTEA`/`BLOB`/binary data rather than as queryable JSON.

### Validation

The checkpoint `clientId` is trimmed and must be non-empty. `contentType` is trimmed and must be non-empty. `value` must be valid JSON, but the service does not validate processor-specific fields inside the payload. If the authenticated admin client ID does not match the path `clientId`, reads and writes return `404` so callers cannot distinguish it from an unknown checkpoint.

## Testing

BDD coverage should exercise:

```gherkin
Feature: Admin checkpoint REST API

  Scenario: Admin can create a checkpoint with typed generic JSON data
    When I call PUT "/v1/admin/checkpoints/checkpoint-processor" with body:
      """
      {
        "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
        "value": {
          "cursor": "cursor-123",
          "processed": 3
        }
      }
      """
    Then the response status should be 200
    And the response body field "clientId" should be "checkpoint-processor"
    And the response body field "value.cursor" should be "cursor-123"

  Scenario: Admin can read a previously stored checkpoint
    Given I call PUT "/v1/admin/checkpoints/checkpoint-processor" with body:
      """
      {
        "contentType": "application/example+json",
        "value": { "cursor": "cursor-456" }
      }
      """
    When I call GET "/v1/admin/checkpoints/checkpoint-processor"
    Then the response status should be 200
    And the response body field "value.cursor" should be "cursor-456"

  Scenario: Non-admin callers cannot read or write checkpoints
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/checkpoints/checkpoint-processor"
    Then the response status should be 403

  Scenario: Admin client IDs scope checkpoint ownership
    Given I am authenticated as admin user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    And set "currentClientId" to the current client ID
    When I call PUT "/v1/admin/checkpoints/${currentClientId}" with body:
      """
      {
        "contentType": "application/example+json",
        "value": { "cursor": "client-a" }
      }
      """
    Then the response status should be 200
    Given I am authenticated as admin user "alice"
    And I am authenticated as agent with API key "test-agent-key-b"
    When I call GET "/v1/admin/checkpoints/${currentClientId}"
    Then the response status should be 404
```

## Tasks

- [x] Add OpenAPI admin contract for checkpoint get/put operations.
- [x] Regenerate Go admin OpenAPI bindings.
- [x] Add admin route handlers and wrapper route registration.
- [x] Add generic checkpoint store interface.
- [x] Encrypt checkpoint values at rest.
- [x] Bind checkpoints to the authenticated admin client ID when present.
- [x] Implement checkpoint storage for PostgreSQL, SQLite, and MongoDB.
- [x] Forward checkpoint operations through the metrics store wrapper.
- [x] Add BDD feature coverage for create, read, replace, validation, and authorization behavior.

## Files to Modify

| File | Changes |
| --- | --- |
| `contracts/openapi/openapi-admin.yml` | Add checkpoint endpoints and schemas. |
| `internal/generated/admin/admin.gen.go` | Regenerate admin server bindings. |
| `internal/plugin/route/admin/checkpoints.go` | Add checkpoint get/put handlers. |
| `internal/plugin/route/admin/admin.go` | Mount checkpoint routes for direct admin route registration. |
| `internal/cmd/serve/wrapper_routes.go` | Register generated-wrapper checkpoint routes. |
| `internal/registry/store/plugin.go` | Add `ClientCheckpoint` and `AdminCheckpointStore`. |
| `internal/plugin/store/postgres/checkpoints.go` | Implement PostgreSQL checkpoint operations. |
| `internal/plugin/store/postgres/db/schema.sql` | Add `admin_checkpoints` table with encrypted value bytes keyed by `client_id`. |
| `internal/plugin/store/sqlite/checkpoints.go` | Implement SQLite checkpoint operations. |
| `internal/plugin/store/sqlite/db/schema.sql` | Add `admin_checkpoints` table with encrypted value bytes keyed by `client_id`. |
| `internal/plugin/store/mongo/checkpoints.go` | Implement MongoDB checkpoint operations. |
| `internal/plugin/store/mongo/mongo.go` | Add unique checkpoint key index. |
| `internal/plugin/store/metrics/metrics.go` | Forward checkpoint operations through the metrics wrapper. |
| `internal/bdd/testdata/features/admin-checkpoints-rest.feature` | Add REST behavior coverage. |
| `internal/bdd/feature_selection.go` | Run checkpoint feature serially. |

## Verification

```bash
# Regenerate admin OpenAPI bindings
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=internal/generated/admin/cfg.yaml contracts/openapi/openapi-admin.yml

# Compile affected Go packages
go test ./internal/registry/store ./internal/plugin/route/admin ./internal/plugin/store/sqlite ./internal/plugin/store/postgres ./internal/plugin/store/mongo ./internal/plugin/store/metrics ./internal/cmd/serve -run '^$'

# Run serial SQLite BDD coverage, including admin-checkpoints-rest.feature
go test -tags sqlite_fts5 ./internal/bdd -run TestFeaturesSQLiteSerial -count=1
```

## Non-Goals

This enhancement does not define a processor-specific checkpoint payload schema, retention policy, or checkpoint history. A checkpoint is a replace-in-place document keyed by the processor's admin `clientId`.

## Security Considerations

Checkpoints can contain durable event cursors and processor state, so the API is admin-only rather than auditor-readable. The payload is encrypted at rest using the existing database encryption service. Processors should use dedicated admin credentials with a stable `clientId` and pass that `clientId` as the checkpoint key; when they do, other admin clients receive `404` for that key. Processors should still avoid storing long-lived secrets in checkpoint payloads.
