---
status: implemented
---

# Enhancement 092: Client Capabilities Endpoint

> **Status**: Implemented. `GET /v1/capabilities` and gRPC `SystemService/GetCapabilities` now return a secret-free, config-derived capability summary for authenticated agent/app clients, admins, and auditors.

## Summary

Add user-surface capability endpoints at `GET /v1/capabilities` and gRPC `SystemService/GetCapabilities` that let authenticated clients discover the server's configured backend choices and feature flags without probing multiple APIs. The endpoints are config-derived, require either a resolved client ID or an authenticated admin/auditor role, and intentionally omit secrets and concrete infrastructure values.

## Motivation

Client apps need a stable way to answer questions such as:

- is outbox-backed replay enabled?
- is semantic search enabled?
- what backend family is this server configured to use?

Before this enhancement, clients had to infer capabilities indirectly from configuration docs, feature failures, or deployment-specific knowledge. That created several problems:

1. **Feature probing leaks complexity**: clients had to guess whether `after` replay, semantic search, or specific backend assumptions would work.
2. **No single source of truth**: the active tech stack (`store`, `vector`, `cache`, `event_bus`, `embedder`) was visible to operators, but not available through a supported client contract.
3. **User auth was too broad**: a normal bearer-authenticated user should not automatically be treated as an agent/app capability consumer. The endpoint needed either real client context or elevated admin/auditor access.

## Design

### Endpoint

Add:

```http
GET /v1/capabilities
```

and:

```text
SystemService/GetCapabilities
```

Behavior:

- requires normal REST authentication
- requires normal gRPC authentication for the RPC path
- requires either a resolved client context or an authenticated admin/auditor role
- returns `401` for missing/invalid auth
- returns `403` when auth succeeded but the caller has neither client context nor admin/auditor role
- returns `200` with a structured JSON summary otherwise
- gRPC mirrors that contract with `UNAUTHENTICATED`, `PERMISSION_DENIED`, and `OK`

### Response Shape

The endpoint returns:

```json
{
  "version": "1.0.0",
  "tech": {
    "store": "postgres",
    "attachments": "postgres",
    "cache": "infinispan",
    "vector": "pgvector",
    "event_bus": "postgres",
    "embedder": "local"
  },
  "features": {
    "outbox_enabled": true,
    "semantic_search_enabled": true,
    "fulltext_search_enabled": true,
    "cors_enabled": false,
    "management_listener_enabled": false,
    "private_source_urls_enabled": false,
    "s3_direct_download_enabled": false
  },
  "auth": {
    "oidc_enabled": true,
    "api_key_enabled": true,
    "admin_justification_required": false
  },
  "security": {
    "encryption_enabled": true,
    "db_encryption_enabled": true,
    "attachment_encryption_enabled": true
  }
}
```

Rules:

- `version` comes from Go build info when available, otherwise `"dev"`
- `tech.vector` normalizes to `"none"` when semantic search is disabled or no vector backend is configured
- `tech.attachments` resolves the Java-parity `"db"` alias to the effective backend (`postgres`, `mongo`, or `fs` for SQLite)
- values are derived from startup config only; the endpoint does not probe dependency health

### Summary Builder

A small pure helper in `internal/service/capabilities` owns the mapping from `*config.Config` to the public JSON shape.

This keeps:

- Gin/HTTP handling separate from capability mapping
- unit tests focused on normalization rules
- future additions to the capability surface in one place

### Client Context Enforcement

The endpoint intentionally does **not** rely on `ClientIDMiddleware()`.

Instead it uses the client ID resolved by the normal auth middleware when present, while also allowing admin/auditor callers:

- API-key-backed auth in normal mode
- testing-mode `X-Client-ID` fallback in BDD/site tests

This prevents production callers from satisfying the capability contract by sending a raw `X-Client-ID` header without a real resolved client identity.

## Testing

### BDD Scenarios

```gherkin
Feature: Client capabilities REST API
  Scenario: Authenticated client with resolved client context can fetch capabilities
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    When I call GET "/v1/capabilities"
    Then the response status should be 200

  Scenario: Authenticated admin without client context can fetch capabilities
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/capabilities"
    Then the response status should be 200

  Scenario: Authenticated auditor without client context can fetch capabilities
    Given I am authenticated as auditor user "alice"
    When I call GET "/v1/capabilities"
    Then the response status should be 200

  Scenario: User-only auth without client context is rejected
    Given I am authenticated as user "bob"
    When I call GET "/v1/capabilities"
    Then the response status should be 403

  Scenario: Missing authentication is rejected
    Given I am not authenticated
    When I call GET "/v1/capabilities"
    Then the response status should be 401
```

```gherkin
Feature: Client capabilities gRPC API
  Scenario: Authenticated client with resolved client context can fetch capabilities
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should not have an error

  Scenario: Authenticated admin without client context can fetch capabilities
    Given I am authenticated as admin user "alice"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should not have an error

  Scenario: Authenticated auditor without client context can fetch capabilities
    Given I am authenticated as auditor user "alice"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should not have an error

  Scenario: User-only auth without client context is rejected
    Given I am authenticated as user "bob"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Missing authentication is rejected
    Given I am not authenticated
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should have status "UNAUTHENTICATED"
```

### Unit Tests

- capability summary mapping for a representative configured stack
- vector normalization to `"none"`
- attachment backend alias normalization
- encryption flag mapping
- build-info fallback to `"dev"`

## Tasks

- [x] Add `GET /v1/capabilities` to `contracts/openapi/openapi.yml`
- [x] Add `SystemService/GetCapabilities` to `contracts/protobuf/memory/v1/memory_service.proto`
- [x] Add a pure capability summary builder in `internal/service/capabilities`
- [x] Add an HTTP handler that enforces client-or-admin/auditor access
- [x] Add a gRPC handler that enforces client-or-admin/auditor access
- [x] Wire the endpoint through the main generated REST API wrapper
- [x] Wire the RPC through `SystemService`
- [x] Regenerate Go and TypeScript OpenAPI bindings
- [x] Regenerate Go protobuf bindings
- [x] Add unit coverage for summary normalization and version fallback
- [x] Add BDD coverage for `200`, `403`, and `401` behavior
- [x] Add gRPC BDD coverage for `OK`, `PERMISSION_DENIED`, and `UNAUTHENTICATED`
- [x] Add a docs note describing the endpoint for authenticated callers
- [x] Record the capabilities auth rule in `internal/FACTS.md`

## Files to Modify

| File | Change |
| --- | --- |
| `contracts/openapi/openapi.yml` | Add the new user REST operation and response schemas |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add the gRPC `SystemService/GetCapabilities` RPC and response messages |
| `internal/service/capabilities/summary.go` | Add the pure config-to-public-summary mapper |
| `internal/service/capabilities/summary_test.go` | Add unit tests for mapping and normalization rules |
| `internal/plugin/route/capabilities/capabilities.go` | Add the HTTP handler and client-or-admin/auditor enforcement |
| `internal/cmd/serve/wrapper_routes.go` | Register the endpoint and delegate to the new handler |
| `internal/grpc/server.go` | Add `SystemService/GetCapabilities` and protobuf mapping |
| `internal/cmd/serve/server.go` | Pass config into `SystemServer` for gRPC capability reporting |
| `internal/plugin/route/memories/wrapper_adapter.go` | Keep the generated API adapter interface complete after adding the new operation |
| `internal/bdd/steps_auth.go` | Add a step for unauthenticated REST requests |
| `internal/bdd/testdata/features/capabilities-rest.feature` | Add REST behavior coverage |
| `internal/bdd/testdata/features-grpc/capabilities-grpc.feature` | Add gRPC behavior coverage |
| `frontends/chat-frontend/src/client/*` | Refresh generated TypeScript client bindings |
| `site/src/pages/docs/configuration.mdx` | Document capability discovery for authenticated clients |
| `internal/FACTS.md` | Record the capabilities auth rule |

## Verification

```bash
# Regenerate contracts
go generate ./...

# Compile Go
go build ./...

# Unit tests
go test ./internal/service/capabilities -count=1

# REST BDD coverage
go test ./internal/bdd -run '^TestFeatures/capabilities-rest$' -count=1
go test -tags 'sqlite_fts5' ./internal/bdd -run '^TestFeaturesSQLite/capabilities-rest$' -count=1
go test ./internal/bdd -run '^TestFeaturesMongo/capabilities-rest$' -count=1

# gRPC BDD coverage
go test ./internal/bdd -run '^TestFeatures/capabilities-grpc$' -count=1

# Generated clients and docs
cd frontends/chat-frontend && npm run generate && npm run build
cd /Users/chirino/sandbox/memory-service/site && npm run build

# Java OpenAPI consumers
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus,spring/memory-service-rest-spring compile -am
```

## Design Decisions

### Why make this a user-surface endpoint instead of an admin endpoint?

Because the target audience is primarily authenticated agent/app clients that need runtime capability discovery, but admin and auditor operators also need the same secret-free configuration summary. The endpoint is still protected by auth and does not expose sensitive values.

### Why expose backend product names at all?

Because the use case was explicitly broader than simple boolean capability flags. Clients may need to understand configured tech choices such as `store`, `vector`, `cache`, `event_bus`, and `embedder` without inspecting deployment manifests.

### Why keep it config-derived instead of probing live capability?

Because this endpoint is meant to describe configured intent, not momentary operational health. Health and dependency availability remain the concern of `/health`, metrics, and runtime error handling.

## Security Considerations

- The endpoint omits sensitive values such as URLs, passwords, API keys, bucket names, paths, issuer URLs, and configured role/user/client lists.
- The endpoint requires either a resolved client context or an admin/auditor role, so ordinary bearer-authenticated user requests are not enough.
- The endpoint does not expose live health or dependency connectivity state.

## Non-Goals

- exposing secrets or concrete infrastructure addresses
- converting the endpoint into a live health probe
- broadening client-context or elevated-role enforcement rules for unrelated endpoints
