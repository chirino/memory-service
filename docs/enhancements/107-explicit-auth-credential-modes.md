---
status: proposed
---

# Enhancement 107: OIDC Client and API Key Authentication

> **Status**: Proposed.

## Summary

Make memory-service authentication resolve both user identity and client identity consistently for OIDC callers and trusted agent-app callers that provide an API key. This fixes [GitHub issue #181](https://github.com/chirino/memory-service/issues/181) by allowing a configured API key to authenticate client-only admin processors with one header while preserving strict Keycloak/OIDC validation and OIDC client allowlisting. Raw bearer user assertions remain test-fixture behavior only.

## Motivation

The Go service currently requires `Authorization: Bearer ...` before HTTP auth middleware calls `TokenResolver.Resolve`, but client identity for API keys is resolved from `X-API-Key`. Agent apps that carry a real user token can send both a bearer token and an API key:

```http
Authorization: Bearer eyJ...
X-API-Key: agent-api-key-1
```

That model should resolve two identities:

- `Authorization: Bearer <user-token>` supplies user identity.
- `X-API-Key: <api-key>` supplies client identity and client-role context.

The current middleware ordering makes that model brittle:

- `X-API-Key` cannot be evaluated unless an `Authorization` header is present.
- client identity is not consistently available for OIDC bearer-token calls unless the token's client claim is extracted.
- With OIDC configured, non-JWT bearer values can still fall through to raw bearer-user behavior instead of being rejected as unauthenticated.

Authentication should be derived from the deployment configuration: OIDC is enabled when `OIDCIssuer` is configured, configured API keys can authenticate client-only admin processors or supplement a bearer user/JWT when `MEMORY_SERVICE_API_KEYS_<CLIENT_ID>` values exist, and OIDC client identity is accepted only when the token's client claim is in the configured allowlist. No-OIDC API-key-only deployments are in scope because `deploy/fly` already documents that production shape, and mixed OIDC/API-key deployments are in scope because async processors may need admin API access without a user token while frontend or agent-app callers still use OIDC user identity.

The two-header API-key model must remain supported for clients that send a real user bearer token plus an API key:

```http
Authorization: Bearer eyJ...
X-API-Key: agent-api-key-1
```

In that model, `Authorization` supplies the user principal and `X-API-Key` supplies the client principal and any client roles. In production, `Authorization` must be a validated user credential, such as an OIDC JWT. `Authorization: Bearer <user-id>` is not a production user-auth mechanism, even when paired with a valid API key; keep that raw bearer shape restricted to binaries built with the `auth_testfixtures` tag and running in `ModeTesting`.

## Design

### Configuration Model

Do not add an auth mode flag or per-credential compatibility toggles. Add one OIDC client allowlist and otherwise use the existing configuration as the source of truth:

| Config | Value shape | Required | Meaning |
| --- | --- | --- | --- |
| `MEMORY_SERVICE_OIDC_ISSUER` / `cfg.OIDCIssuer` | Single issuer URL, for example `https://idp.example.com/realms/memory-service`. | Required for OIDC deployments. | Enables OIDC/JWT bearer validation. Token `iss` must match this issuer. |
| `MEMORY_SERVICE_OIDC_DISCOVERY_URL` / `cfg.OIDCDiscoveryURL` | Single URL. Optional internal discovery URL when the issuer URL is not directly reachable from memory-service. | Optional. | Used only for OIDC discovery and JWKS fetches; token issuer validation still uses `OIDCIssuer`. |
| `MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS` / `cfg.OIDCAllowedClients` | Comma-separated OIDC client IDs, for example `memory-service-client,frontend,developer-frontend`. | Optional if `OIDCAllowedAudiences` is set; otherwise required when `OIDCIssuer` is set. | When non-empty, allows only tokens whose signed `azp` or `client_id` claim matches one listed client ID. Empty means no client allowlist check. |
| `MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES` / `cfg.OIDCAllowedAudiences` | Comma-separated audience values, for example `memory-service,memory-service-api`. | Optional if `OIDCAllowedClients` is set; otherwise required when `OIDCIssuer` is set. | When non-empty, token `aud` must contain at least one listed value. Empty means no audience check. |
| `MEMORY_SERVICE_API_KEYS_<CLIENT_ID>` / `cfg.APIKeys` | One env var per client ID. Value is one API key or comma-separated API keys. Example: `MEMORY_SERVICE_API_KEYS_AGENT=key-1,key-2`. | Optional. | Maps each configured API key value to the client ID from the env var suffix. Used as service-principal auth for admin/operational APIs, or as paired client identity alongside a bearer user/JWT. |
| `MEMORY_SERVICE_ROLES_ADMIN_OIDC_ROLE` / `cfg.AdminOIDCRole` | Single token role/group value. Empty keeps the current effective default of `admin`. | Optional. | Grants admin role to resolved OIDC users when the signed token contains this value. Admin implies auditor and indexer. |
| `MEMORY_SERVICE_ROLES_AUDITOR_OIDC_ROLE` / `cfg.AuditorOIDCRole` | Single token role/group value. Empty keeps the current effective default of `auditor`. | Optional. | Grants auditor role to resolved OIDC users when the signed token contains this value. |
| `MEMORY_SERVICE_ROLES_INDEXER_OIDC_ROLE` / `cfg.IndexerOIDCRole` | Single token role/group value. Empty disables token-claim indexer grants. | Optional. | Grants indexer role to resolved OIDC users when the signed token contains this value. |
| `MEMORY_SERVICE_ROLES_ADMIN_OIDC_SCOPE` / `cfg.AdminOIDCScope` | Single token scope value, for example `memory-service:admin`. | Optional. | When set, admin actions using an OIDC token require this signed `scope` value in addition to a normal admin role grant. |
| `MEMORY_SERVICE_ROLES_AUDITOR_OIDC_SCOPE` / `cfg.AuditorOIDCScope` | Single token scope value, for example `memory-service:auditor`. | Optional. | When set, auditor actions using an OIDC token require this signed `scope` value in addition to a normal auditor role grant. |
| `MEMORY_SERVICE_ROLES_INDEXER_OIDC_SCOPE` / `cfg.IndexerOIDCScope` | Single token scope value, for example `memory-service:indexer`. | Optional. | When set, indexer actions using an OIDC token require this signed `scope` value in addition to a normal indexer role grant. |
| `MEMORY_SERVICE_ROLES_ADMIN_USERS` / `cfg.AdminUsers` | Comma-separated user IDs. Existing wildcard suffix matching such as `alice-*` is supported by the current role matcher. | Optional. | Grants admin role to resolved users. Admin implies auditor and indexer. |
| `MEMORY_SERVICE_ROLES_AUDITOR_USERS` / `cfg.AuditorUsers` | Comma-separated user IDs. | Optional. | Grants auditor role to resolved users. |
| `MEMORY_SERVICE_ROLES_INDEXER_USERS` / `cfg.IndexerUsers` | Comma-separated user IDs. | Optional. | Grants indexer role to resolved users. |
| `MEMORY_SERVICE_ROLES_ADMIN_CLIENTS` / `cfg.AdminClients` | Comma-separated client IDs. | Optional. | Grants admin role to resolved clients from API-key env suffixes or OIDC `azp` / `client_id` claims. Admin implies auditor and indexer. |
| `MEMORY_SERVICE_ROLES_AUDITOR_CLIENTS` / `cfg.AuditorClients` | Comma-separated client IDs. | Optional. | Grants auditor role to resolved clients from API-key env suffixes or OIDC `azp` / `client_id` claims. |
| `MEMORY_SERVICE_ROLES_INDEXER_CLIENTS` / `cfg.IndexerClients` | Comma-separated client IDs. | Optional. | Grants indexer role to resolved clients from API-key env suffixes or OIDC `azp` / `client_id` claims. |

Add these fields to `internal/config.Config`:

```go
// OIDC
OIDCAllowedClients   string
OIDCAllowedAudiences string
AdminOIDCScope       string
AuditorOIDCScope     string
IndexerOIDCScope     string
```

Add these serve flags:

```go
&cli.StringFlag{
    Name:        "oidc-allowed-clients",
    Category:    "Authorization:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS"),
    Destination: &cfg.OIDCAllowedClients,
    Usage:       "Comma-separated OIDC client IDs allowed to call memory-service",
}

&cli.StringFlag{
    Name:        "oidc-allowed-audiences",
    Category:    "Authorization:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES"),
    Destination: &cfg.OIDCAllowedAudiences,
    Usage:       "Optional comma-separated OIDC audiences accepted by memory-service; empty disables audience checks",
}

&cli.StringFlag{
    Name:        "roles-admin-oidc-scope",
    Category:    "Authorization:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_ADMIN_OIDC_SCOPE"),
    Destination: &cfg.AdminOIDCScope,
    Usage:       "Optional OIDC scope required to exercise admin permissions",
}

&cli.StringFlag{
    Name:        "roles-auditor-oidc-scope",
    Category:    "Authorization:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_AUDITOR_OIDC_SCOPE"),
    Destination: &cfg.AuditorOIDCScope,
    Usage:       "Optional OIDC scope required to exercise auditor permissions",
}

&cli.StringFlag{
    Name:        "roles-indexer-oidc-scope",
    Category:    "Authorization:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_INDEXER_OIDC_SCOPE"),
    Destination: &cfg.IndexerOIDCScope,
    Usage:       "Optional OIDC scope required to exercise indexer permissions",
}
```

API keys continue to be declared as:

```bash
MEMORY_SERVICE_API_KEYS_AGENT=agent-api-key-1
MEMORY_SERVICE_API_KEYS_ADMIN_AGENT=admin-agent-api-key
```

`loadAPIKeysFromEnv` continues to lower-case the client ID suffix and map key values to client IDs.

Client role lists use those resolved client IDs:

```bash
MEMORY_SERVICE_API_KEYS_AGENT=agent-api-key-1
MEMORY_SERVICE_API_KEYS_ADMIN_AGENT=admin-agent-api-key
MEMORY_SERVICE_ROLES_ADMIN_CLIENTS=agent,admin-agent
MEMORY_SERVICE_ROLES_AUDITOR_CLIENTS=frontend,developer-frontend
```

In this example:

- API key `agent-api-key-1` resolves to client ID `agent`.
- API key `admin-agent-api-key` resolves to client ID `admin-agent`.
- OIDC tokens from clients whose signed `azp` or `client_id` is `frontend` or `developer-frontend` match the auditor client list.

The test Keycloak realm can configure:

```bash
MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=memory-service-client,frontend,developer-frontend
MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES=memory-service
```

The only new behavior is resolver semantics:

| Config state | Accepted credential families |
| --- | --- |
| `OIDCIssuer` set and API keys configured | OIDC JWTs that pass configured client/audience checks, optionally paired with API-key client identity, plus `X-API-Key`-only service-principal requests for admin/operational APIs. Non-JWT bearer values are still rejected while OIDC is enabled. |
| `OIDCIssuer` set and no API keys configured | OIDC JWTs that pass configured client/audience checks only. |
| API keys configured and `OIDCIssuer` empty | API-key-only service-principal requests using `X-API-Key` or `Authorization: Bearer <api-key>`. No production raw bearer user assertions are accepted. |
| neither configured and `Mode == testing` | existing raw bearer test fixture behavior only when the binary is built with the test-only `auth_testfixtures` build tag. |
| neither configured in production | startup error. |

If `OIDCIssuer` is configured but provider discovery fails, startup should fail instead of silently falling back to API-key/raw-bearer behavior. If `OIDCIssuer` is configured and both `OIDCAllowedClients` and `OIDCAllowedAudiences` are empty, startup should fail with a clear message. That makes Keycloak deployments fail closed while allowing either client allowlisting, audience enforcement, or both. When both are configured, both checks must pass.

### OIDC Client Identity

Resolve the OIDC client ID from signed token claims, in this order:

1. `azp` (Keycloak's authorized party claim for access tokens)
2. `client_id` (common for client-credentials tokens)

Use signed token claims for authorization decisions:

- `azp` or `client_id` identifies the calling OIDC client and is checked against `OIDCAllowedClients` when that setting is non-empty.
- `aud` identifies intended token recipients and is checked against `OIDCAllowedAudiences` when that setting is non-empty.

Audience-only configuration is valid:

```bash
MEMORY_SERVICE_OIDC_ISSUER=https://idp.example.com/realms/memory-service
MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES=memory-service-api
```

Use this when the identity provider is configured so only trusted clients can obtain tokens with the memory-service audience. If the identity provider allows broad audience issuance, also configure `OIDCAllowedClients` to restrict which clients may call memory-service.

Common deployment options:

| Option | Config | Pros | Cons |
| --- | --- | --- | --- |
| Allowed clients only | `MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=frontend,agent-app` | Simple to configure; works with ordinary Keycloak access tokens; no token exchange required for services that already receive user tokens from allowed clients. | Does not prove the token was minted specifically for memory-service; any accepted client token from the realm may be usable unless endpoint/user authorization blocks it. |
| Allowed audiences only | `MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES=memory-service-api` | Strong resource-server boundary; proves the token was minted for memory-service or an accepted API audience; works well with service-to-service delegation. | Callers may need IdP/client configuration changes or token exchange/on-behalf-of flow to obtain a token with the memory-service audience. |
| Allowed clients and audiences | Configure both settings. | Strongest boundary: caller must be an allowed client and present a token intended for memory-service. | Most operational setup; services commonly need token exchange unless the original token already includes the memory-service audience. |

For direct frontend or agent-app calls, allowed-client checking may be sufficient and simpler. For delegated service-to-service calls, audience checking is usually the cleaner model, but it often requires exchanging the incoming user token for a token whose `aud` includes memory-service.

Do not use Keycloak client attributes directly. Client attributes are Keycloak administration metadata and are not automatically available to memory-service during bearer-token validation. Reading them directly would require Keycloak admin/introspection calls during authentication, admin credentials, caching, and provider-specific logic. It would also make authorization depend on mutable server-side metadata that is not bound to the signed token presented by the caller.

Client attributes are only appropriate if Keycloak maps them into a signed token claim through a protocol mapper. At that point memory-service is validating a token claim, not querying Keycloak attributes directly. Even then, prefer the standard `azp`, `client_id`, and optional `aud` checks for the first implementation, and reserve custom claims for later policy work.

### OIDC Scopes

Scopes are useful for controlling what an allowed OIDC client can do, but they should be treated as signed token claims and used as a delegation gate for the existing memory-service roles. Do not add a separate endpoint permission matrix in this enhancement. Scope gates are optional: if `MEMORY_SERVICE_ROLES_ADMIN_OIDC_SCOPE`, `MEMORY_SERVICE_ROLES_AUDITOR_OIDC_SCOPE`, and `MEMORY_SERVICE_ROLES_INDEXER_OIDC_SCOPE` are unset, scopes do not restrict access by themselves and memory-service continues to rely on OIDC role grants, user role allowlists, client role allowlists, and normal endpoint authorization.

The current resolver already extracts role grant values from:

- `roles`
- `groups`
- whitespace-separated `scope`
- Keycloak `realm_access.roles`

This enhancement should keep that role-grant behavior for `MEMORY_SERVICE_ROLES_*_OIDC_ROLE`. Add separate optional scope-gate extraction from the signed whitespace-separated `scope` claim:

```bash
MEMORY_SERVICE_ROLES_ADMIN_OIDC_SCOPE=memory-service:admin
MEMORY_SERVICE_ROLES_AUDITOR_OIDC_SCOPE=memory-service:auditor
MEMORY_SERVICE_ROLES_INDEXER_OIDC_SCOPE=memory-service:indexer
```

When scope gates are configured:

- `OIDCAllowedClients`, when configured, answers "which OIDC clients may call memory-service at all?"
- `OIDCAllowedAudiences`, when configured, answers "was this token minted for memory-service?"
- `MEMORY_SERVICE_ROLES_*_OIDC_SCOPE` answers "which coarse memory-service roles may this token exercise on behalf of an authorized user or client?"
- normal conversation and memory authorization still uses the resolved user ID and conversation membership

Configured scopes cap delegated capability; they do not grant memory-service roles by themselves. For an auditor-protected action, both gates must pass:

1. The token must carry the configured auditor scope, such as `memory-service:auditor`.
2. The resolved user or client must still satisfy the auditor role through `MEMORY_SERVICE_ROLES_AUDITOR_OIDC_ROLE`, `MEMORY_SERVICE_ROLES_AUDITOR_USERS`, `MEMORY_SERVICE_ROLES_AUDITOR_CLIENTS`, or another existing trusted role source.

The same two-gate rule applies to admin and indexer actions when their scope mappings are configured. A scoped token does not let the caller bypass conversation membership rules on ordinary user endpoints. If no scope mapping is configured for a role, this delegation cap is absent for that role and existing role/user/client configuration continues to decide access.

A future enhancement can add endpoint-level read/write scopes such as `memory-service:conversations:read` or `memory-service:entries:write` if product requirements call for OAuth-style delegated permissions on every agent endpoint. That is intentionally out of scope here because issue #181 only needs reliable client identity, client allowlisting, and existing role mapping.

### Credential Resolution

Replace the current boolean `apiKeyAuth` inference with a typed credential resolution result:

```go
type CredentialKind string

const (
    CredentialOIDC                CredentialKind = "oidc"
    CredentialAPIKey              CredentialKind = "api-key"
    CredentialOIDCAPIKey          CredentialKind = "oidc-api-key"
    CredentialBearerAPIKey        CredentialKind = "bearer-api-key"
    CredentialRawBearerUserAPIKey CredentialKind = "raw-bearer-user-api-key"
    CredentialTesting             CredentialKind = "testing-bearer-user"
)

type Identity struct {
    UserID         string
    ClientID       string
    Roles          map[string]bool
    IsAdmin        bool
    CredentialKind CredentialKind
}
```

Resolution rules:

1. `Authorization: Bearer <jwt>` is validated as OIDC when `OIDCIssuer` is configured. The token's OIDC client ID must be present in `OIDCAllowedClients`; otherwise return `401` or `403`. If `OIDCAllowedAudiences` is non-empty, the token's `aud` claim must contain at least one configured audience.
2. When `OIDCIssuer` is configured, a non-JWT bearer value is rejected. It must not fall through to raw bearer-user behavior even if a valid `X-API-Key` is also present.
3. When `X-API-Key` is present with a valid OIDC JWT and matches a configured API key, the JWT supplies `UserID`, the API key supplies `ClientID`, and role resolution unions OIDC/user roles with configured client roles.
4. When `X-API-Key` matches a configured API key and no `Authorization` bearer token is present, an API-key-only request authenticates a client service principal: `ClientID` is set, `UserID` is empty, and configured client roles apply. This is allowed whether OIDC is configured or not, because OIDC configuration controls bearer-token validation and does not disable configured API-key service principals. This supports admin/processors and other client-role-protected APIs that do not require user context.
5. When `OIDCIssuer` is empty, `Authorization: Bearer <api-key>` also authenticates the same client service principal for compatibility with existing Fly/no-OIDC deployments and issue #181. Prefer `X-API-Key` in new clients because it keeps API keys separate from user bearer tokens.
6. `Authorization: Bearer <user-id>` is accepted only when both conditions are true: `cfg.Mode == config.ModeTesting` and the binary was built with a test-only build tag such as `auth_testfixtures`. This keeps raw user assertions out of production binaries, not just production configuration. If a valid `X-API-Key` or testing-only `X-Client-ID` is also present under that tag, the resolver may include that client identity for fixture coverage, but this must use `CredentialRawBearerUserAPIKey` or `CredentialTesting`.
7. Raw bearer user values are rejected in all production modes, with or without a valid `X-API-Key`.

### Security Code Organization

Refactor the security package so the auth flow can be audited in one pass:

```text
internal/security/
  identity.go                    # Identity, CredentialKind, role constants
  credentials.go                 # RequestCredentials and header/metadata names
  resolver.go                    # credential state machine and OIDC/API-key resolution
  roles.go                       # user/client/OIDC role mapping and scope gates
  raw_bearer_disabled.go         # //go:build !auth_testfixtures
  raw_bearer_testfixtures.go     # //go:build auth_testfixtures
  http_middleware.go             # Gin credential extraction and context attachment only
  grpc_middleware.go             # gRPC metadata extraction and context attachment only
  guards.go                      # RequireAuthenticated, RequireUser, RequireAdminRole, RequireAuditorRole
  logging.go                     # admin audit and justification enforcement
```

The resolver should accept one transport-neutral input shape:

```go
type RequestCredentials struct {
    BearerToken    string
    APIKey         string
    ClientIDHeader string
    Transport      string
}
```

HTTP and gRPC middleware should only extract headers/metadata into `RequestCredentials`, call the resolver, and attach the resulting identity to context. They should not contain credential policy, role calculation, or endpoint authorization decisions.

Endpoint authorization should use explicit guard middleware/helpers:

- `RequireAuthenticated`: accepts either user or client identity.
- `RequireUser`: requires non-empty `UserID` for normal user/agent APIs.
- `RequireAdminRole`: requires final admin role.
- `RequireAuditorRole`: requires final auditor/admin role.

This makes audit review mechanical: inspect `resolver.go` for accepted credential shapes, `roles.go` for role derivation, `guards.go` for endpoint gates, and route registration for the intended guard. User-facing route groups should have `RequireUser`; admin/operational route groups may intentionally use role guards that accept client-only identities.

### HTTP and gRPC Middleware

HTTP `AuthMiddleware` should extract `X-API-Key`, `Authorization`, and the testing-only `X-Client-ID` header into `RequestCredentials{Transport: "http"}`, then let the resolver decide whether an accepted credential is present and whether a paired API key supplies client identity. A request without an accepted credential remains unauthenticated.

`Authorization` is optional when `X-API-Key` alone can authenticate a client-only service principal. If `Authorization` is present, it must use the `Bearer` scheme; non-Bearer authorization values should be rejected instead of ignored.

The CORS middleware must include `X-API-Key` in `Access-Control-Allow-Headers` so browser-based agent apps and admin tools can send the canonical API-key header. Keep `Authorization` for OIDC JWTs and no-OIDC bearer API-key compatibility, and keep `X-Client-ID` limited to existing testing/dev compatibility behavior.

Raw bearer user fixture support should be split behind a compile-time build tag:

- production/default builds include `raw_bearer_disabled.go` with `//go:build !auth_testfixtures`; it rejects raw bearer user values even if `cfg.Mode == config.ModeTesting`
- fixture builds include `raw_bearer_testfixtures.go` with `//go:build auth_testfixtures`; it enables the `CredentialRawBearerUserAPIKey` / `CredentialTesting` branch only when `cfg.Mode == config.ModeTesting`

Do not compile `auth_testfixtures` into release images, dev servers, or production-mode API-key BDD runners. Existing relaxed BDD suites that still rely on raw bearer fixture users should run with `-tags auth_testfixtures` until they are migrated to OIDC or real API-key credentials.

gRPC interceptors should extract the same `RequestCredentials` shape with `Transport: "grpc"` using incoming metadata:

- `authorization`
- `x-api-key`
- `x-client-id` only in testing/dev compatibility mode

Missing credentials should return the existing unauthenticated behavior:

- HTTP: `401` with a concise error such as `missing credentials`
- gRPC: no identity in context, causing existing service methods to return `Unauthenticated`

The middleware may resolve a client-only identity, but normal user/agent APIs must still require a user principal before executing user-scoped behavior. Current gRPC methods already follow this pattern, for example `ConversationsServer.ListConversations` rejects empty `getUserID(ctx)` with `Unauthenticated`, while admin helpers such as `AdminConversationsServer.requireReadAccess` accept either a user or client identity and then check admin/auditor roles. REST wrapper-native agent endpoints should get the same explicit user-principal guard before handlers that currently derive ownership or membership from `security.GetUserID(c)`.

### Role Resolution

Role mapping stays source-specific but uses the resolved credential kind. Configured OIDC scopes are evaluated as an additional role gate, not as a standalone role grant:

| Source | Effect |
| --- | --- |
| `MEMORY_SERVICE_ROLES_ADMIN_USERS` / `AUDITOR_USERS` / `INDEXER_USERS` | Grants the role to a resolved non-empty `UserID`. |
| `MEMORY_SERVICE_ROLES_ADMIN_CLIENTS` / `AUDITOR_CLIENTS` / `INDEXER_CLIENTS` | Grants the role to a resolved non-empty `ClientID` from API key or OIDC client claim. |
| OIDC token values from `roles`, `groups`, `scope`, and `realm_access.roles` matched by `MEMORY_SERVICE_ROLES_*_OIDC_ROLE` | Grants the corresponding base role for OIDC tokens. Optional `MEMORY_SERVICE_ROLES_*_OIDC_SCOPE` values can then cap whether that role may be exercised. |

Admin continues to imply auditor and indexer.

`TokenResolver.Resolve` should apply scope gates before storing final roles in `Identity.Roles`. Existing HTTP helpers such as `RequireAdminRole`, `RequireAuditorRole`, `HasRole`, and gRPC service methods can continue checking the final role map.

Role calculation order:

1. Resolve base role grants from OIDC role claims, configured user allowlists, and configured client allowlists.
2. Apply admin implication to the base grants, so a base admin grant also grants auditor and indexer.
3. If the credential includes an OIDC token (`CredentialOIDC` or `CredentialOIDCAPIKey`) and a `MEMORY_SERVICE_ROLES_<ROLE>_OIDC_SCOPE` value is configured, keep that role only when the signed token `scope` claim contains the configured value.
4. Store only the gated final roles in `Identity.Roles` and derive `Identity.IsAdmin` from the final admin role.

This keeps the two-gate behavior centralized in the resolver instead of duplicating scope checks across every protected endpoint.

### Endpoint Applicability

Normal non-admin access always requires a user identity. Conversation ownership, sharing, listing, user-scoped event streams, and memory access are user-scoped, so a client-only API-key identity must receive `401`/`Unauthenticated` or an equivalent existing auth failure on those endpoints.

Admin and operational APIs that are already role-gated rather than user-scoped may continue to run without user context. This is why API-key-only admin clients are valid: the resolver supplies a `ClientID`, configured client roles grant `admin`/`auditor`/`indexer`, and handlers authorize by role. Current examples in the codebase include:

- REST admin routes under `/v1/admin`, which mount `auth` plus `RequireAuditorRole`/`RequireAdminRole`.
- gRPC admin conversation APIs, where `requireGRPCIdentity` accepts an identity with either `UserID` or `ClientID` and then checks admin/auditor roles.
- gRPC `AdminCheckpointService`, which requires admin role and, when a client ID is authenticated, restricts checkpoint access to the same `client_id`.
- gRPC admin event streams, which allow `EVENT_SCOPE_ADMIN` for admin/auditor roles without requiring a user ID; non-admin/user-scoped streams still require `getUserID`.

Supported request shapes are:

- OIDC bearer token only: the token supplies user identity and OIDC client identity.
- OIDC bearer token plus `X-API-Key`: the token supplies user identity, and the API key supplies an additional trusted agent-app client identity.
- API key only: `X-API-Key` supplies client identity only; allowed only on admin/operational APIs that do not require user context, whether or not OIDC is configured.
- No-OIDC bearer API key compatibility: `Authorization: Bearer <api-key>` supplies client identity only when `OIDCIssuer` is empty.
- Testing raw bearer user, optionally paired with `X-API-Key` or testing-only `X-Client-ID`: allowed only when both `cfg.Mode == config.ModeTesting` and the `auth_testfixtures` build tag are active.

Unsupported request shapes are:

- API-key-only requests to normal user-scoped APIs.
- `Authorization: Bearer <api-key>` when OIDC is configured, because the bearer slot is reserved for OIDC JWTs in that mode.
- raw bearer users in production, even when paired with a valid `X-API-Key`.

### Examples

Keycloak-only deployment:

```bash
MEMORY_SERVICE_OIDC_ISSUER=http://localhost:8081/realms/memory-service
MEMORY_SERVICE_OIDC_DISCOVERY_URL=http://keycloak:8080/realms/memory-service
MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=memory-service-client,frontend,developer-frontend
MEMORY_SERVICE_ROLES_ADMIN_OIDC_ROLE=admin
```

OIDC bearer token plus API-key client identity:

```bash
MEMORY_SERVICE_OIDC_ISSUER=http://localhost:8081/realms/memory-service
MEMORY_SERVICE_OIDC_DISCOVERY_URL=http://keycloak:8080/realms/memory-service
MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=memory-service-client,frontend,developer-frontend
MEMORY_SERVICE_API_KEYS_AGENT=agent-api-key-1
MEMORY_SERVICE_ROLES_ADMIN_CLIENTS=agent
```

The caller sends both identities:

```bash
curl \
  -H 'Authorization: Bearer eyJ...' \
  -H 'X-API-Key: agent-api-key-1' \
  http://localhost:8082/v1/admin/conversations
```

Mixed OIDC plus API-key admin processor with no user context:

```bash
MEMORY_SERVICE_OIDC_ISSUER=http://localhost:8081/realms/memory-service
MEMORY_SERVICE_OIDC_DISCOVERY_URL=http://keycloak:8080/realms/memory-service
MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=memory-service-client,frontend,developer-frontend
MEMORY_SERVICE_API_KEYS_TURN_TRACES=turn-traces-secret
MEMORY_SERVICE_ROLES_ADMIN_CLIENTS=turn-traces
```

In this deployment, frontend or agent-app callers use OIDC JWTs that carry user identity, while the async turn-trace processor uses only its API key. The processor can call admin/client-role-protected APIs with no user context:

```bash
curl \
  -H 'X-API-Key: turn-traces-secret' \
  -H 'Content-Type: application/json' \
  -X PUT \
  -d '{"contentType":"application/vnd.memory-service.turn-trace-checkpoint+json;v=1","value":{"lastEventCursor":"42"}}' \
  http://localhost:8082/v1/admin/checkpoints/turn-traces
```

That same request identity must not be accepted for ordinary user-scoped APIs such as `GET /v1/conversations`, because no user principal exists for membership filtering.

No-OIDC admin processor deployments use the same API-key shape without the OIDC settings:

```bash
MEMORY_SERVICE_API_KEYS_TURN_TRACES=turn-traces-secret
MEMORY_SERVICE_ROLES_ADMIN_CLIENTS=turn-traces
```

## Testing

### BDD Runner Plan

Use two production-mode BDD runners instead of forcing every auth case through Keycloak:

- Extend the existing Keycloak-backed Go BDD coverage in `internal/bdd/cucumber_pg_keycloak_test.go` or add a sibling runner such as `TestFeaturesPgKeycloakAuthClients` for OIDC-enabled behavior.
- Add a no-OIDC production-mode API-key runner, for example `TestFeaturesPgAPIKeys`, that starts memory-service with configured `APIKeys`, client role lists, and no `OIDCIssuer`.

Current BDD coverage does not exercise production-style pure API-key authentication. Most non-Keycloak BDD runners use `cfg.Mode = config.ModeTesting`, where `X-Client-ID` is accepted as a relaxed test identity path. Steps such as `I am authenticated as agent with API key "test-agent-key"` set `X-Client-ID`; they do not validate a configured `MEMORY_SERVICE_API_KEYS_<CLIENT_ID>` secret through `X-API-Key` in production mode. The existing Keycloak runner uses `cfg.Mode = config.ModeProd`, but it currently focuses on OIDC tokens.

For this enhancement, prioritize Keycloak-backed production-mode scenarios for OIDC behavior:

- OIDC allowed-client enforcement
- optional audience enforcement
- audience-only configuration
- scope gate plus user/client role two-gate behavior
- OIDC bearer token plus API-key client identity
- rejection of raw bearer-user plus API-key fallback when OIDC is enabled
- mixed OIDC/API-key deployments accepting `X-API-Key`-only admin client access without user context
- rejection of `X-API-Key`-only requests on normal user-scoped APIs
- rejection of bearer API-key compatibility when OIDC is enabled
- rejection of raw bearer-user plus API-key fallback in production mode

Prioritize no-OIDC production-mode scenarios for API-key behavior:

- API-key-only admin/service-principal access without user context
- rejection of API-key-only requests on normal user-scoped APIs
- bearer API-key compatibility for existing no-OIDC deployments such as `deploy/fly`
- rejection of raw bearer-user plus API-key credentials in production mode

The Keycloak runner should start one PostgreSQL instance and one Keycloak test server, then run a feature file such as:

```text
internal/bdd/testdata/features-oidc/auth-clients-rest.feature
```

The no-OIDC API-key runner should start one PostgreSQL instance and run a feature file such as:

```text
internal/bdd/testdata/features/auth-api-keys-rest.feature
```

Because each scenario needs a different memory-service auth configuration, add a scenario setup hook or dedicated steps that start a fresh in-process memory-service with:

- shared `DBURL`
- `CacheType=none`
- `AttachType=db`
- `SearchSemanticEnabled=false`
- `SearchFulltextEnabled=false`
- scenario-specific `OIDCIssuer`, `OIDCDiscoveryURL`, `OIDCAllowedClients`, `OIDCAllowedAudiences`, `APIKeys`, and role allowlists

Keep the current `testkeycloak.Server` as the token provider for OIDC scenarios so they validate against a real Keycloak issuer and JWKS endpoint. Do not start Keycloak for the no-OIDC API-key runner.

### Step Additions

Add BDD steps for real API-key credentials instead of relying on `X-Client-ID` testing shortcuts:

```gherkin
Given memory-service is running with OIDC allowed client "memory-service-client"
Given memory-service is running with OIDC allowed clients "memory-service-client,frontend"
Given memory-service is running with OIDC allowed client "memory-service-client" and API keys
Given memory-service is running with API keys and no OIDC
And API key "agent-api-key-1" maps to client "agent"
And client "agent" has the "admin" role
Given I authenticate with API key header "agent-api-key-1"
Given I authenticate with bearer API key "agent-api-key-1"
Given I authenticate as bearer user "alice" with API key "agent-api-key-1"
Given I authenticate with both OIDC user "bob" and API key "agent-api-key-1"
Given I authenticate with only API key header "agent-api-key-1"
```

The existing OIDC steps can continue to provision isolated Keycloak users and set JWT bearer tokens:

```gherkin
Given I login via OIDC as user "alice" with password "alice"
```

### Scenarios

OIDC-backed scenarios:

```gherkin
@oidc @auth-modes
Feature: OIDC client and API key authentication
  As a memory-service operator
  I want signed token claims and configured API keys to define accepted clients
  So Keycloak and paired API-key deployments behave predictably without extra auth modes

  Scenario: OIDC-only mode accepts a Keycloak user token
    Given memory-service is running with OIDC allowed client "memory-service-client"
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 200

  Scenario: OIDC-only mode rejects raw bearer values
    Given memory-service is running with OIDC allowed client "memory-service-client"
    And I set the "Authorization" header to "Bearer bob"
    When I call GET "/v1/conversations"
    Then the response status should be 401
    And the response body should contain "invalid"

  Scenario: OIDC-only mode rejects a token from a disallowed client
    Given memory-service is running with OIDC allowed client "frontend"
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 403

  Scenario: Mixed mode accepts a Keycloak admin token
    Given memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And API key "agent-api-key-1" maps to client "agent"
    And I login via OIDC as user "alice" with password "alice"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: OIDC mixed mode rejects raw bearer user plus API-key client
    Given memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate as bearer user "alice" with API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 401

  Scenario: Mixed mode combines Keycloak user identity with API-key client identity
    Given memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate with both OIDC user "bob" and API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: OIDC mixed mode accepts API-key-only admin client access
    Given memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: OIDC mixed mode rejects API-key-only access to user-scoped APIs
    Given memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/conversations"
    Then the response status should be 401

  Scenario: OIDC mixed mode rejects bearer API-key compatibility
    Given memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate with bearer API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 401

  Scenario: Mixed mode rejects invalid JWT-shaped bearer tokens
    Given memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And API key "agent-api-key-1" maps to client "agent"
    And I set the "Authorization" header to "Bearer not.a.valid.jwt"
    When I call GET "/v1/conversations"
    Then the response status should be 401
    And the response body should contain "invalid JWT"
```

No-OIDC API-key scenarios:

```gherkin
@api-keys @auth-modes
Feature: No-OIDC API key authentication
  As a memory-service operator
  I want configured API keys to authenticate admin clients without user context
  So processors and small no-OIDC deployments can use one credential without weakening user-scoped APIs

  Scenario: No-OIDC mode accepts API-key-only admin client access
    Given memory-service is running with API keys and no OIDC
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: No-OIDC mode rejects API-key-only access to user-scoped APIs
    Given memory-service is running with API keys and no OIDC
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/conversations"
    Then the response status should be 401

  Scenario: No-OIDC mode accepts bearer API key compatibility
    Given memory-service is running with API keys and no OIDC
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate with bearer API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: No-OIDC production mode rejects raw bearer user plus API-key client
    Given memory-service is running with API keys and no OIDC
    And API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And I authenticate as bearer user "alice" with API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 401
```

### Unit Coverage

Add focused unit tests around resolver construction and credential parsing:

- startup validation errors when OIDC is enabled but both allowed clients and allowed audiences are empty
- resolver matrix tests covering each credential shape through the transport-neutral `RequestCredentials` input
- paired API-key lookup from `X-API-Key` when a bearer user/JWT is present
- `X-API-Key`-only requests resolve `ClientID` with empty `UserID` in both OIDC and no-OIDC deployments
- `X-API-Key`-only requests are accepted by admin role checks when the client has an admin role
- `X-API-Key`-only requests are rejected by normal user-scoped API guards
- no-OIDC bearer-API-key-only requests resolve `ClientID` with empty `UserID`
- OIDC-enabled bearer API-key requests are rejected
- raw bearer-user plus API-key requests are rejected in production mode, regardless of OIDC configuration
- `CredentialRawBearerUserAPIKey` is only produced when both `ModeTesting` and the `auth_testfixtures` build tag are active
- default builds reject raw bearer users even when `ModeTesting` is configured
- OIDC client ID extraction from `azp` and `client_id`
- OIDC allowed-client rejection
- optional OIDC `aud` enforcement
- audience-only configuration accepts tokens with an allowed audience and rejects tokens without one
- configured OIDC scope gate requires both the mapped scope and the corresponding user/client role
- configured OIDC scope alone does not grant admin/auditor/indexer access
- absence of OIDC scope gates does not deny otherwise authorized requests
- OIDC-only rejection of raw bearer user values
- role union when both OIDC and API key are present
- guard tests proving `RequireUser` rejects client-only identities while admin/auditor guards accept properly role-granted client identities
- route registration tests or BDD coverage proving normal REST user/agent route groups use `RequireUser`

## Tasks

- [ ] Add `OIDCAllowedClients` and `OIDCAllowedAudiences` to `internal/config.Config`.
- [ ] Add `--oidc-allowed-clients` and `--oidc-allowed-audiences` flags in `internal/cmd/serve/serve.go`.
- [ ] Add fail-closed startup validation for OIDC provider discovery and missing OIDC token acceptance boundary.
- [ ] Refactor `internal/security` into auditable resolver, role, middleware, guard, identity, credential, and raw-bearer build-tag files.
- [ ] Refactor `TokenResolver` to accept transport-neutral `RequestCredentials` and resolve typed credential kinds.
- [ ] Split raw bearer user fixture support behind an `auth_testfixtures` build tag with a default-build rejection path.
- [ ] Update HTTP middleware so paired `X-API-Key` can reach the resolver alongside bearer auth.
- [ ] Update CORS allowed headers to include `X-API-Key`.
- [ ] Add explicit user-principal enforcement for normal REST user/agent endpoints so `X-API-Key`-only client identities cannot create, list, or read user-scoped resources.
- [ ] Update gRPC interceptors to use the same credential rules.
- [ ] Add real API-key BDD steps that set `X-API-Key`, bearer API-key compatibility credentials, paired `X-API-Key` credentials, and API-key-only user-scope rejection cases.
- [ ] Add Keycloak-backed BDD scenarios for allowed clients, optional audiences, scope gates, OIDC API-key pairing, `X-API-Key` admin service principals, and OIDC rejection of raw bearer/bearer API-key fallback.
- [ ] Add mixed OIDC/API-key and no-OIDC production-mode scenarios for API-key-only admin clients and normal user-scoped API rejection.
- [ ] Update Go test commands or BDD runner tasks so relaxed raw-bearer fixture suites opt into `auth_testfixtures`, while production-mode API-key/OIDC auth suites run without that tag.
- [ ] Update configuration documentation and examples.

## Files to Modify

| File | Changes |
| --- | --- |
| `internal/config/config.go` | Add OIDC allowed client and optional audience fields. |
| `internal/config/compat.go` | Load new OIDC allowlist/audience environment variables if compat loading remains outside CLI flags. |
| `internal/cmd/serve/serve.go` | Add OIDC allowlist/audience flags and startup validation wiring. |
| `internal/cmd/serve/cors.go` | Add `X-API-Key` to `Access-Control-Allow-Headers`. |
| `internal/security/identity.go` | Add `Identity`, `CredentialKind`, role constants, and context identity helpers. |
| `internal/security/credentials.go` | Add `RequestCredentials` and shared header/metadata names. |
| `internal/security/resolver.go` | Refactor credential parsing and OIDC/API-key resolution into one auditable state machine. |
| `internal/security/roles.go` | Move user/client/OIDC role mapping and scope gates out of middleware. |
| `internal/security/raw_bearer_disabled.go` | Default-build helper that rejects raw bearer user fixture auth. |
| `internal/security/raw_bearer_testfixtures.go` | `//go:build auth_testfixtures` helper that enables raw bearer user fixture auth only in `ModeTesting`. |
| `internal/security/http_middleware.go` | Keep Gin credential extraction/context attachment only. |
| `internal/security/grpc_middleware.go` | Keep gRPC metadata extraction/context attachment only. |
| `internal/security/guards.go` | Centralize `RequireAuthenticated`, `RequireUser`, `RequireAdminRole`, and `RequireAuditorRole`. |
| `internal/cmd/serve/wrapper_routes.go` and/or route handlers under `internal/plugin/route/{conversations,entries,memories,search,...}` | Ensure normal user/agent endpoints require a non-empty authenticated user while admin routes can continue to authorize by role. |
| `internal/bdd/cucumber_pg_keycloak_test.go` | Add or delegate to a Keycloak auth/client runner. |
| `internal/bdd/cucumber_pg_apikey_test.go` or similar | Add a no-OIDC production-mode API-key runner. |
| `internal/bdd/steps_auth.go` | Add real API-key auth steps that do not rely on `X-Client-ID`. |
| `internal/bdd/testdata/features-oidc/auth-clients-rest.feature` | Add Keycloak-backed BDD scenarios for OIDC clients, audiences, scopes, OIDC API-key pairing, `X-API-Key` admin service principals, user-scoped rejection, and OIDC rejection of bearer API keys/raw bearer users. |
| `internal/bdd/testdata/features/auth-api-keys-rest.feature` or similar | Add no-OIDC production-mode API-key-only admin and normal user-scope rejection coverage. |
| `internal/cmd/serve/serve_test.go` | Verify new flags are registered. |
| `internal/security/auth_matrix_test.go` | Add table-driven resolver matrix tests for every accepted/rejected credential shape. |
| `internal/security/guards_test.go` | Add guard behavior tests for user-required and role-required paths. |
| `Taskfile.yml` or dedicated BDD test scripts | Keep normal/default test runs free of raw bearer fixture auth unless a specific relaxed fixture suite passes `-tags auth_testfixtures`. |
| `docs/` and site configuration docs | Document OIDC allowed clients, optional audiences, scope gates, paired API-key client identity, mixed/no-OIDC API-key admin clients, and API-key-only rejection on user-scoped paths. |

## Verification

```bash
# Compile affected Go packages.
go build ./internal/config ./internal/cmd/serve ./internal/security ./internal/bdd

# Run focused resolver and flag tests.
go test ./internal/security ./internal/cmd/serve -count=1 > test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" test.log

# Run resolver fixture tests with raw bearer fixture support compiled in.
go test -tags auth_testfixtures ./internal/security -count=1 > test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" test.log

# Run Keycloak-backed BDD coverage for the new OIDC client/API-key scenarios.
go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1 > test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" test.log

# Run no-OIDC production-mode API-key coverage without auth_testfixtures.
go test ./internal/bdd -run TestFeaturesPgAPIKeys -count=1 > test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" test.log

# Run existing relaxed BDD suites that still use raw bearer fixture users.
# Keep production auth suites such as TestFeaturesPgAPIKeys out of this tagged run.
go test -tags auth_testfixtures ./internal/bdd -run 'TestFeatures($|Pg$|PgEncrypted|SQLite|Mongo)' -count=1 > test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" test.log
```

## Non-Goals

- Do not replace Keycloak/OIDC role mapping with a new authorization system.
- Do not add API-key storage to a database; API keys remain configuration-driven.
- Do not introduce OAuth2 client-credentials token issuance inside memory-service.
- Do not change conversation ownership or sharing semantics; user-scoped operations still require a user principal.

## Design Decisions

1. **Derive credential families from existing config**: `OIDCIssuer` enables OIDC validation, configured API keys enable API-key validation, and both together allow mixed deployments without a separate auth-mode flag.
2. **Require an explicit OIDC token boundary**: `OIDCAllowedClients`, `OIDCAllowedAudiences`, or both make accepted tokens auditable at startup and prevent any valid realm token from calling memory-service by default.
3. **Keep `X-API-Key` canonical**: `X-API-Key` carries configured API-key client identity alongside bearer user/JWT identity or as a client-only service principal.
4. **Allow configured service principals**: Configured API keys may authenticate a client-only service principal for admin/operational APIs that are already role-gated and do not need user context, even when the same deployment also accepts OIDC user tokens.
5. **Keep normal APIs user-scoped**: Client-only API-key identities must not satisfy normal user/agent APIs because those APIs need a user principal for ownership, membership, and policy filtering.
6. **Use scopes as role gates**: Signed `scope` values matched by `MEMORY_SERVICE_ROLES_*_OIDC_SCOPE` cap which coarse roles an OIDC token may exercise; the resolved user/client must still have the role through normal role config.
7. **Use Keycloak in the BDD matrix**: The regression is about API keys, but the risky behavior is the interaction between API keys, bearer tokens, OIDC client claims, audience claims, and scopes, so the OIDC test matrix should run with a real Keycloak issuer.

## Security Considerations

- OIDC-enabled deployments must reject raw bearer values so production Keycloak deployments do not accidentally accept arbitrary user IDs.
- In OIDC-enabled deployments, `Authorization: Bearer` remains reserved for OIDC JWTs. API-key-only service principals must use `X-API-Key` so mixed deployments do not reintroduce raw bearer fallback.
- OIDC bearer tokens must come from an allowed client, and when audiences are configured, must be minted for an accepted audience.
- Scope gates must use signed token claims and namespaced scope values such as `memory-service:admin`, not arbitrary client attributes.
- API-key-only service principals are limited to routes whose authorization is role/client based; normal user-scoped routes must still require a user principal.
- The raw bearer-user fixture model must require both `cfg.Mode == config.ModeTesting` and the `auth_testfixtures` build tag. `Authorization: Bearer <user-id>` must not authenticate in production modes, even with a valid configured API key.
- JWT-looking bearer values in mixed mode must be validated as OIDC and must not fall back to paired raw-bearer user auth.
- API keys are bearer secrets. Logs must not include raw API-key values.
- When both OIDC and API key credentials are present, client roles granted by API key should be intentional and documented because they can elevate a non-admin OIDC user when the API key maps to an admin client.
- When no OIDC token is present, client roles granted to API keys can provide admin access with no user identity; use dedicated per-processor keys and least-privilege client role lists.
- Raw bearer test fixture behavior must remain limited to `cfg.Mode == config.ModeTesting` and the `auth_testfixtures` build tag. Default binaries must not contain a working raw bearer user authentication path.
