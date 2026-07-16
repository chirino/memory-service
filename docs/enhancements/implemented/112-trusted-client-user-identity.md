---
status: implemented
---

# Enhancement 112: Trusted Client User Identity Assertion

> **Status**: Implemented.

## Summary

Allow explicitly trusted clients to select the effective user for normal user-scoped Memory Service operations by sending `X-User-ID` over REST or `x-user-id` in gRPC request metadata. Authentication remains based on OIDC and/or API keys, the trust decision is based on the authenticated client ID, and the protobuf request messages do not carry identity fields.

This enhancement implements [GitHub issue #401](https://github.com/chirino/memory-service/issues/401) and replaces the gRPC-only `RequestActor.on_behalf_of_user_id` mechanism described by [Enhancement 101](../101-grpc-api-parity-for-cognition.md).

## Motivation

Agent applications mediate operations for end users. Some deployments can forward and validate each user's OIDC token, but other deployments intentionally use a remotely hosted agent application with one service credential. Those clients still need user-scoped ownership, membership, namespace policy, search, and event behavior to be evaluated for the end user rather than for the agent application's service account.

The current APIs do not provide one consistent production mechanism:

- REST has no production user-assertion header.
- gRPC episodic memory requests expose `RequestActor.on_behalf_of_user_id`, but the field is request-schema-specific and is not consistently available across other gRPC services.
- API-key-only clients resolve a client ID but no user ID, so normal user APIs reject them as unauthenticated.
- The BDD raw bearer and `X-Client-ID` fixtures require the `auth_testfixtures` build tag and testing mode. They are deliberately not a production authentication mechanism.

Identity should be transport metadata because it applies to the call, not to the resource being created or read. REST and gRPC already carry credentials this way:

| Purpose | REST | gRPC metadata |
| --- | --- | --- |
| OIDC bearer token | `Authorization: Bearer <token>` | `authorization: Bearer <token>` |
| API key | `X-API-Key: <key>` | `x-api-key: <key>` |
| Asserted user | `X-User-ID: alice` | `x-user-id: alice` |

Adding the same metadata to every call is straightforward for generated clients and gRPC stubs through request filters, client interceptors, or a derived stub with per-call `CallOptions`. It avoids adding an identity wrapper or `user_id` field to every request message.

## Non-Goals

- Do not remove, broaden, or replace the existing `auth_testfixtures` support or its tests.
- Do not accept an untrusted user assertion, a raw bearer user name, or `X-Client-ID` as production authentication.
- Do not make `X-User-ID` a credential. A valid OIDC token or API key is still required.
- Do not grant admin, auditor, or indexer access merely because a client may assert a user.
- Do not apply an asserted user to admin or system APIs.
- Do not add `RequestIdentity`, `RequestActor`, `on_behalf_of_user_id`, or `user_id` fields to protobuf request messages.
- Do not introduce database schema changes. The effective user continues to use existing ownership, membership, policy, and event-routing fields.

## Design

### Configuration

Add one optional comma-separated allowlist:

| Environment variable | CLI flag | Config field | Default |
| --- | --- | --- | --- |
| `MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS` | `--trusted-user-id-clients` | `TrustedUserIDClients string` | Empty; user assertions are disabled. |

Entries are trimmed and compared as exact, case-sensitive client IDs. Wildcards are not supported. An empty allowlist disables the feature. The capability response reports whether the allowlist is non-empty, but never exposes its contents.

API key client IDs continue to be derived from the lower-cased environment-variable suffix. Underscores are preserved:

```bash
MEMORY_SERVICE_API_KEYS_COGNITION_PROCESSOR=replace-with-a-secret
MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS=cognition_processor
```

The client authenticates a user-scoped REST call as follows:

```http
X-API-Key: replace-with-a-secret
X-User-ID: alice
```

The equivalent gRPC metadata is:

```text
x-api-key: replace-with-a-secret
x-user-id: alice
```

OIDC clients use the signed `azp` claim, falling back to signed `client_id`, as already defined by [Enhancement 107](107-explicit-auth-credential-modes.md):

```bash
MEMORY_SERVICE_OIDC_ISSUER=https://idp.example.com/realms/example
MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=cognition-processor
MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES=memory-service
MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS=cognition-processor
```

A deployment may support OIDC callers and API-key callers at the same time. If their resolved client IDs differ, list both exact values:

```bash
MEMORY_SERVICE_API_KEYS_COGNITION_PROCESSOR=replace-with-a-secret
MEMORY_SERVICE_OIDC_ISSUER=https://idp.example.com/realms/example
MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=cognition-processor
MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES=memory-service
MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS=cognition_processor,cognition-processor
```

Trusting a client to assert a user is independent of role grants. For example, this configuration permits user assertions without granting administrative access:

```bash
MEMORY_SERVICE_API_KEYS_COGNITION_PROCESSOR=replace-with-a-secret
MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS=cognition_processor
MEMORY_SERVICE_ROLES_ADMIN_CLIENTS=
```

If the same client also requires an administrative API, it must be granted separately through existing role configuration:

```bash
MEMORY_SERVICE_ROLES_ADMIN_CLIENTS=cognition_processor
```

### Authentication and Assertion Pipeline

Authentication and user assertion are separate stages:

1. Resolve and validate the base credential exactly as today. OIDC validation, issuer/client/audience checks, API-key comparison, and invalid-credential failures happen before considering `X-User-ID`.
2. Store the authenticated user ID, authenticated client ID, OIDC scopes, and role provenance in the request identity.
3. At a normal user API boundary, inspect the asserted-user metadata. Apply it only when the authenticated client ID exactly matches the trusted-client allowlist.
4. Run `RequireUser`, OIDC scope gates, identity rate limiting, authorization, storage operations, and event subscription using the resulting effective user.

Admin and system API boundaries skip stage 3. The assertion is therefore ignored for those APIs and cannot alter the authenticated principal used for role checks, audit information, health, or capabilities.

The request identity should distinguish the authenticated and effective identities:

```go
type Identity struct {
    AuthenticatedUserID string
    UserID              string // Effective user for the current API boundary.
    ClientID            string

    UserRoles   map[string]bool
    ClientRoles map[string]bool
    Roles       map[string]bool // Effective role union.

    UserIDAsserted bool
    // Existing credential-kind and OIDC scope fields remain unchanged.
}
```

The exact internal representation may differ, but it must preserve role provenance. Authentication initially sets `UserID` to `AuthenticatedUserID`. A trusted assertion clones the identity before replacing `UserID`, so request-scoped changes cannot leak into another request.

For REST, add a user-assertion middleware after authentication and before `RequireUser`, OIDC scope checks, and identity rate limiting in each normal user wrapper. For gRPC, add unary and stream user-assertion interceptors after the authentication interceptor and before identity rate limiting. The gRPC interceptors use an explicit allowlist of normal user services/methods; admin and system methods are not assertion-enabled.

Streaming calls use the metadata supplied when the stream is opened. The effective user does not change for the lifetime of that stream.

### Behavior Matrix

| Base credential | Authenticated client trusted? | Assertion present? | Normal user API behavior |
| --- | --- | --- | --- |
| Valid API key, no authenticated user | Yes | Non-empty | Use the asserted user. |
| Valid API key, no authenticated user | Yes | Absent/empty | Behave as today: no user, so APIs requiring a user return unauthenticated. |
| Valid API key, no authenticated user | No | Any value | Ignore the assertion. Behave exactly as though it were absent, normally returning unauthenticated on user APIs. |
| Valid OIDC token | Yes | Different user | Use the asserted user and the delegated-role rules below. |
| Valid OIDC token | Yes | Same user | Preserve the authenticated user and its roles; no effective delegation occurred. |
| Valid OIDC token | No | Any value | Ignore the assertion and use the OIDC user. |
| Valid OIDC token plus valid API key | Yes, based on the resolved client identity | Non-empty | Use the asserted user. |
| Invalid or missing credential | Any | Any value | Reject using existing unauthenticated behavior. The assertion cannot authenticate the request. |

The required compatibility rule is: when a client is not trusted, the presence of `X-User-ID` or `x-user-id` has no effect on status, authorization, response body, or selected user compared with the same request without that metadata.

For a trusted client, trim surrounding whitespace. A blank value is absent. If multiple non-empty values are supplied, reject the request as `400 Bad Request` over REST or `INVALID_ARGUMENT` over gRPC rather than selecting an ambiguous identity. For an untrusted client, even duplicate or malformed assertion values are ignored so behavior remains identical to an absent header.

### Delegated Roles and OIDC Scopes

An asserted user selects data ownership and user policy context; it does not inherit privileges from a different authenticated user.

When a trusted OIDC client changes the effective user:

- retain roles derived from the authenticated client ID, including existing configured client-role lists;
- retain the validated OIDC token's client/audience result and existing OIDC scope gates;
- drop roles derived from the original authenticated user, including OIDC token roles/groups and configured user-role lists;
- do not evaluate configured user-role lists against the asserted user;
- preserve `AuthenticatedUserID` for security logging while setting `UserID` to the asserted value.

This prevents an administrative user token from delegating its administrative role to an arbitrary asserted user. Client roles remain because they describe the trusted agent application's own service privileges. If the assertion matches the authenticated user, retain the normal role union because no user identity changed.

API-key-only clients already have client-derived roles only, so applying an assertion changes the effective user without changing their roles.

### API Scope

The assertion applies uniformly to all normal user-scoped REST operations and their gRPC equivalents, including:

- conversations, entries, forks, sharing, and ownership transfers;
- user search and indexing operations;
- user episodic memories and namespace listing;
- user attachments;
- response recording and replay operations;
- authorized user event streams.

It does not apply to:

- admin conversation, memory, attachment, event, checkpoint, statistics, or maintenance APIs;
- health and capability system APIs;
- public attachment download-token routes, whose identity was fixed when the token was issued.

For authorized event streams, subscription membership and event routing use the effective user established when the stream opens. Admin event scope continues to use the authenticated admin identity and ignores asserted-user metadata.

### REST Contract

Document API-key authentication in `contracts/openapi/openapi.yml`:

```yaml
components:
  securitySchemes:
    ApiKeyAuth:
      type: apiKey
      in: header
      name: X-API-Key
```

Document `X-User-ID` in API-level prose rather than as an OpenAPI security scheme. It is identity context, not a credential, and modeling it as a security scheme causes some generators to pass the same configured authentication token to both bearer and assertion headers. The description must explain that it is optional with a valid bearer credential, required for API-key-only access to APIs that require a user, and honored only for trusted clients. Admin OpenAPI documentation should add the existing `ApiKeyAuth` credential where applicable and state that `X-User-ID` is ignored.

The server reads the header in middleware. Do not add it as a body/query field or thread it through generated operation request structs.

### gRPC Contract

All user services accept the lower-case `x-user-id` metadata key. gRPC metadata keys are case-insensitive on the wire but client code should use the canonical lower-case spelling.

Remove `RequestActor` and its five request fields from `contracts/protobuf/memory/v1/memory_service.proto`:

```protobuf
message PutMemoryRequest {
  // Existing fields omitted.
  reserved 7;
  reserved "actor";
}

message GetMemoryRequest {
  reserved 5;
  reserved "actor";
}

message UpdateMemoryRequest {
  reserved 5;
  reserved "actor";
}

message SearchMemoriesRequest {
  reserved 9;
  reserved "actor";
}

message ListMemoryNamespacesRequest {
  reserved 5;
  reserved "actor";
}
```

Delete the `RequestActor` message after its references are removed. Reserving each former field number and name prevents accidental wire-incompatible reuse. No replacement protobuf identity message is added.

Clients attach both credentials and identity through metadata. Java clients can derive an immutable stub per call, for example:

```java
Metadata metadata = new Metadata();
metadata.put(
    Metadata.Key.of("x-user-id", Metadata.ASCII_STRING_MARSHALLER),
    userId);

MemoriesServiceGrpc.MemoriesServiceBlockingStub requestStub =
    baseStub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
MemoryItem result = requestStub.getMemory(request);
```

A client interceptor may be preferable when the current user is already held in a request-scoped context. The shared base stub must not be mutated; derive a stub/interceptor for the call so concurrent users cannot leak metadata into one another.

### Capabilities

Extend the existing authentication capability summary without revealing trusted client IDs:

```json
{
  "auth": {
    "oidc_enabled": true,
    "api_key_enabled": true,
    "admin_justification_required": false,
    "user_id_assertion_enabled": true
  }
}
```

Add `bool user_id_assertion_enabled = 4;` to protobuf `CapabilitiesAuth` and the corresponding required boolean to the REST schema. The value is `true` when `MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS` contains at least one valid entry.

### Observability and Audit

When a trusted assertion changes the effective user, emit a structured security log containing the request ID, authenticated client ID, authenticated user ID when present, and effective user ID. Never log credentials. Record applied and ignored outcomes in a low-cardinality counter without user ID or client ID labels.

Ignored assertions should be debug-level events to avoid allowing an untrusted caller to flood normal logs. Authorization failures and invalid base credentials retain their current logging and metrics behavior.

Identity rate limiting must run after assertion processing so the effective user and authenticated client both contribute to the existing identity key. Source rate limiting remains before authentication.

### Compatibility and Rollout

This change is compatible with clients that do not use `RequestActor`: absent user metadata preserves current OIDC and API-key behavior. Existing `auth_testfixtures` builds, steps, tags, and scenarios remain unchanged.

Removing `RequestActor` is a pre-release gRPC contract cleanup but is source-breaking for clients that construct it. A new server treats the removed wire field as unknown; without `x-user-id`, an API-key-only call will no longer obtain an effective user. Downstream clients such as `cognition-processor-quarkus` must migrate to per-call `x-user-id` metadata as part of the coordinated rollout.

The deployment sequence should update the Memory Service and its remote gRPC clients together or deploy a client version that can select the metadata mechanism based on the server capability. The final server contract must not retain an ungated `RequestActor` fallback.

There is no persisted-data migration.

## Design Decisions

### Use metadata instead of request fields

Identity applies to the entire authenticated call. Metadata is already used for bearer tokens and API keys, works for unary and streaming gRPC APIs, and can cover all APIs without duplicating fields across request messages.

### Name the value `X-User-ID`

The value is the effective user ID, not a nested request object. The trust configuration and documentation carry the delegation warning. Names such as `RequestIdentity` or `on_behalf_of_user_id` would require schema changes and make the same call-level concern inconsistent across transports.

### Ignore untrusted assertions

Ignoring the header preserves compatibility for proxies or generic clients that attach it and avoids exposing the trust allowlist through a distinct authorization error. An untrusted API-key-only client still fails normal user APIs because it has no effective user, exactly as it does without the header.

### Support mixed OIDC and API-key deployments

Trust is evaluated after the existing credential resolver produces a client ID. The assertion mechanism therefore does not require a new authentication mode and can coexist with both credential families on one server.

### Apply assertions only to user API boundaries

Keeping the base authenticated identity intact and deriving an effective user only for normal user APIs prevents an assertion from changing admin role checks, audit identity, or system behavior. This is safer than globally overwriting the identity in the credential resolver.

## Security Considerations

- `X-User-ID` is not secret and must never be accepted without a valid base credential.
- The trusted-client allowlist is exact and disabled by default; no wildcard matching is allowed.
- OIDC trust uses only a validated signed `azp`/`client_id` claim. API-key trust uses the client ID associated with a successfully matched configured key.
- Proxies must strip inbound `X-User-ID` from public traffic unless the authenticated upstream client is intended to control it. Memory Service still enforces its own trust allowlist.
- User-role provenance must be discarded when an OIDC user is replaced. Otherwise an asserted user could inherit the original token user's admin/auditor/indexer privileges.
- Client-role grants remain separately configured and auditable. Assertion trust alone grants no administrative role.
- Logs and metrics must not include API keys, bearer tokens, or high-cardinality identity labels.
- Per-call client metadata must not be stored on mutable shared stubs or global request state.
- Admin and system handlers must never consult the asserted-user metadata.

## Testing

Keep all existing `auth_testfixtures` scenarios and runners unchanged. Add production-mode tests to the existing PostgreSQL API-key and Keycloak runners, which do not use the build tag.

Representative Cucumber scenarios:

```gherkin
Feature: Trusted client user identity assertion

  Scenario: Trusted API-key client performs REST operations as an asserted user
    Given API key client "cognition_processor" is trusted to assert user IDs
    And I authenticate with the configured API key for "cognition_processor"
    And I send header "X-User-ID" with value "alice"
    When I create a conversation
    Then the response status should be 201
    And the conversation owner should be "alice"

  Scenario: Untrusted API-key client assertion behaves as absent
    Given API key client "untrusted_processor" is not trusted to assert user IDs
    And I authenticate with the configured API key for "untrusted_processor"
    And I send header "X-User-ID" with value "alice"
    When I list conversations
    Then the response status should be 401
    And the response should equal the same request without "X-User-ID"

  Scenario: Untrusted OIDC client continues to use its token user
    Given I authenticate as OIDC user "bob" from client "frontend"
    And client "frontend" is not trusted to assert user IDs
    And I send header "X-User-ID" with value "alice"
    When I list conversations
    Then the operation should be authorized as user "bob"

  Scenario: Trusted OIDC delegation drops original user roles
    Given I authenticate as an OIDC admin user from trusted client "cognition-processor"
    And I send header "X-User-ID" with value "alice"
    When I call a normal user API
    Then the effective user should be "alice"
    And roles derived from the authenticated OIDC user should not be effective
    And roles configured for client "cognition-processor" should remain effective

  Scenario: Admin REST API ignores an asserted user
    Given I authenticate as an admin client
    And I send header "X-User-ID" with value "alice"
    When I call an admin operation
    Then the authenticated admin identity should be used

  Scenario: Trusted gRPC client uses request metadata
    Given I authenticate gRPC with the configured API key for "cognition_processor"
    And I attach gRPC metadata "x-user-id" with value "alice"
    When I call a user-scoped gRPC method
    Then the operation should be authorized as user "alice"

  Scenario: Invalid credentials cannot assert a user
    Given I send an invalid API key
    And I send header "X-User-ID" with value "alice"
    When I list conversations
    Then the response status should be 401
```

Add unit tests for:

- exact trusted-client matching, whitespace handling, and empty configuration;
- ignored untrusted assertions, including duplicate/malformed values;
- trusted duplicate-value validation and REST/gRPC error mapping;
- identity cloning and authenticated/effective user separation;
- role provenance for API-key, OIDC, and paired OIDC/API-key credentials;
- assertion interceptor placement before identity rate limiting;
- unary and streaming gRPC method classification;
- admin and system route/method exclusion;
- capability values with empty and non-empty allowlists;
- reserved protobuf field numbers/names and regenerated-client compilation.

## Tasks

- [x] Add `TrustedUserIDClients` configuration, environment variable, CLI flag, parsing, and validation.
- [x] Extend security identity resolution to preserve authenticated/effective user IDs and user/client role provenance.
- [x] Implement REST trusted-user assertion middleware and add it to every normal user wrapper before `RequireUser` and identity rate limiting.
- [x] Implement gRPC unary and stream trusted-user assertion interceptors for every normal user service.
- [x] Confirm admin/system REST routes and gRPC methods ignore asserted-user metadata.
- [x] Apply effective identity consistently to policy input, ownership, membership, search, attachments, recordings, rate limits, and authorized event streams.
- [x] Remove `RequestActor` handling from the gRPC memory server.
- [x] Remove and reserve the five protobuf `actor` fields, delete `RequestActor`, and regenerate Go, Java, and Python protobuf clients.
- [x] Document `X-API-Key` as an OpenAPI security scheme and `X-User-ID` in OpenAPI prose without adding request fields.
- [x] Add `user_id_assertion_enabled` to REST and gRPC capabilities and regenerate affected clients.
- [x] Add low-cardinality assertion metrics and structured security logging.
- [x] Add unit tests and production-mode REST/gRPC BDD scenarios without changing existing `auth_testfixtures` tests.
- [x] Update site configuration, deployment-security, REST, and gRPC client documentation with API-key and OIDC examples.
- [x] Update Enhancement 101 and `internal/FACTS.md` to remove the obsolete `RequestActor` behavior after implementation.
- [x] Validate the downstream `cognition-processor-quarkus` migration to per-request gRPC metadata.

## Files to Modify

| File or area | Change |
| --- | --- |
| `docs/enhancements/implemented/112-trusted-client-user-identity.md` | Keep the implemented design synchronized with runtime behavior and verification. |
| `internal/config/config.go` | Add `TrustedUserIDClients`. |
| `internal/cmd/serve/serve.go` | Register `--trusted-user-id-clients` and its environment source. |
| `internal/security/auth.go` | Preserve identity/role provenance and support effective request identity. |
| `internal/security/user_id_assertion.go` (new) | Parse trusted clients and implement REST/gRPC assertion middleware/interceptors. |
| `internal/security/*_test.go` | Test trust matching, ignored assertions, role provenance, errors, and interceptor scope/order. |
| `internal/security/rate_limit.go` | Ensure effective identity is used after assertion processing. |
| `internal/cmd/serve/wrapper_routes.go` | Install assertion middleware only on normal user REST wrappers. |
| `internal/cmd/serve/server.go` | Install unary/stream assertion interceptors in the correct order. |
| `internal/grpc/server.go` | Remove `RequestActor` policy overrides and consume effective context identity uniformly. |
| `internal/service/capabilities/summary.go` | Report whether user-ID assertion is enabled. |
| `contracts/openapi/openapi.yml` | Document API-key authentication, trusted `X-User-ID`, operation scope, and capability field. |
| `contracts/openapi/openapi-admin.yml` | Document existing API-key authentication while excluding user assertion from admin operations. |
| `contracts/protobuf/memory/v1/memory_service.proto` | Remove/reserve actor fields, delete `RequestActor`, document metadata, and add the capability field. |
| `internal/generated/api/`, `internal/generated/apiclient/` | Regenerate Go REST server/client contracts. |
| `internal/generated/pb/memory/v1/` | Regenerate Go protobuf and gRPC bindings. |
| `java/memory-service-contracts/` and generated Java client modules | Regenerate and compile OpenAPI/protobuf clients after contract changes. |
| `python/langchain/memory_service_langchain/grpc/memory/v1/` | Regenerate Python protobuf/gRPC bindings. |
| `frontends/chat-frontend/src/client/` | Regenerate frontend OpenAPI types when capabilities change. |
| `internal/bdd/cucumber_pg_apikey_test.go` | Include production API-key assertion scenarios. |
| `internal/bdd/cucumber_pg_keycloak_auth_test.go` | Include trusted/untrusted OIDC and mixed-mode scenarios. |
| `internal/bdd/testdata/features/*trusted-user-id*.feature` (new) | Cover REST and gRPC production behavior. |
| `internal/bdd/steps_auth.go`, `internal/bdd/steps_auth_modes.go`, `internal/bdd/steps_grpc.go` | Add production credential, trust configuration, and request-metadata steps as needed. |
| `site/src/pages/docs/configuration.mdx` | Document the allowlist and examples. |
| `site/src/pages/docs/deployment/security.mdx` | Document the trust boundary, proxy behavior, and role separation. |
| REST/gRPC client pages under `site/src/pages/docs/` | Show per-request REST headers and gRPC metadata. |
| `docs/enhancements/101-grpc-api-parity-for-cognition.md` | Mark `RequestActor` sections as superseded by this enhancement after implementation. |
| `internal/FACTS.md` | Replace the obsolete gRPC actor-policy fact after implementation. |

Generated-file paths may vary with the existing generators; all checked-in outputs changed by `go generate .`, Maven generation, frontend generation, and `task verify:python` must be committed.

## Verification

```bash
# Regenerate Go OpenAPI and protobuf outputs.
go generate .

# Regenerate frontend OpenAPI types.
cd frontends/chat-frontend
npm run generate
cd ../..

# Build Go implementation and generated clients.
go build ./...

# Run focused security, gRPC, and capabilities unit tests.
go test ./internal/security ./internal/grpc ./internal/service/capabilities > trusted-user-unit.log 2>&1
# Search trusted-user-unit.log for FAIL/error output.

# Run production-mode API-key and OIDC BDD suites without auth_testfixtures.
CGO_ENABLED=1 go test -race -tags='sqlite_fts5' ./internal/bdd \
  -run 'TestFeaturesPg(APIKeys|Keycloak|KeycloakAuthClients)$' -count=1 \
  > trusted-user-bdd.log 2>&1
# Search trusted-user-bdd.log for FAIL/error output.

# Run the full Go test task.
task test:go > test-go.log 2>&1
# Search test-go.log for FAIL/error output.

# Compile Java contracts and clients.
./java/mvnw -f java/pom.xml compile > java-compile.log 2>&1
# Search java-compile.log for BUILD FAILURE/error output.

# Regenerate and verify Python gRPC bindings/package.
task verify:python > verify-python.log 2>&1
# Search verify-python.log for FAIL/error output.

# Verify the frontend after capability generation.
cd frontends/chat-frontend
npm run lint
npm run build
```
