---
status: implemented
---

# Enhancement 107: OIDC Client and API Key Authentication

> **Status**: Implemented.

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

Do not add an auth mode flag or per-credential compatibility toggles. Add optional OIDC client and audience filters and otherwise use the existing configuration as the source of truth:

| Config | Value shape | Required | Meaning |
| --- | --- | --- | --- |
| `MEMORY_SERVICE_OIDC_ISSUER` / `cfg.OIDCIssuer` | Single issuer URL, for example `https://idp.example.com/realms/memory-service`. | Required for OIDC deployments. | Enables OIDC/JWT bearer validation. Token `iss` must match this issuer. |
| `MEMORY_SERVICE_OIDC_DISCOVERY_URL` / `cfg.OIDCDiscoveryURL` | Single URL. Optional internal discovery URL when the issuer URL is not directly reachable from memory-service. | Optional. | Used only for OIDC discovery and JWKS fetches; token issuer validation still uses `OIDCIssuer`. |
| `MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS` / `cfg.OIDCAllowedClients` | Comma-separated OIDC client IDs, for example `memory-service-client,frontend,developer-frontend`. | Optional. | When non-empty, allows only tokens whose signed `azp` or `client_id` claim matches one listed client ID. Empty means no client allowlist check. |
| `MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES` / `cfg.OIDCAllowedAudiences` | Comma-separated audience values, for example `memory-service,memory-service-api`. | Optional. | When non-empty, token `aud` must contain at least one listed value. Empty means no audience check. |
| `MEMORY_SERVICE_API_KEYS_<CLIENT_ID>` / `cfg.APIKeys` | One env var per client ID. Value is one API key or comma-separated API keys. Example: `MEMORY_SERVICE_API_KEYS_AGENT=key-1,key-2`. | Optional. | Maps each configured API key value to the client ID from the env var suffix. Used as service-principal auth for admin/operational APIs, or as paired client identity alongside a bearer user/JWT. |
| `MEMORY_SERVICE_ROLES_ADMIN_OIDC_ROLE` / `cfg.AdminOIDCRole` | Single token role/group value. Empty keeps the current effective default of `admin`. | Optional. | Grants admin role to resolved OIDC users when the signed token contains this value. Admin implies auditor and indexer. |
| `MEMORY_SERVICE_ROLES_AUDITOR_OIDC_ROLE` / `cfg.AuditorOIDCRole` | Single token role/group value. Empty keeps the current effective default of `auditor`. | Optional. | Grants auditor role to resolved OIDC users when the signed token contains this value. |
| `MEMORY_SERVICE_ROLES_INDEXER_OIDC_ROLE` / `cfg.IndexerOIDCRole` | Single token role/group value. Empty disables token-claim indexer grants. | Optional. | Grants indexer role to resolved OIDC users when the signed token contains this value. |
| `MEMORY_SERVICE_OIDC_SCOPES_<PERMISSION_KEY>` / `cfg.OIDCScopes` | One comma-separated scope list per fixed permission key, for example `MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS_READ=memory-service:conversations:read`. | Optional. | Adds an OIDC-only resource/API scope gate after normal user, membership, role, and API-key authorization. Empty means no scope gate for that permission. |
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
OIDCScopes           map[string]string
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

// The fixed permission descriptor table registers one explicit flag/env pair
// for each supported resource/API scope key:
//
// --oidc-scopes-<permission-key-with-dashes>
// MEMORY_SERVICE_OIDC_SCOPES_<PERMISSION_KEY>
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
| `OIDCIssuer` set and API keys configured | OIDC JWTs that pass configured client/audience checks when those optional filters are set, optionally paired with API-key client identity, plus `X-API-Key`-only service-principal requests for admin/operational APIs. Non-JWT bearer values are still rejected while OIDC is enabled. |
| `OIDCIssuer` set and no API keys configured | OIDC JWTs that pass configured client/audience checks when those optional filters are set. |
| API keys configured and `OIDCIssuer` empty | API-key-only service-principal requests using `X-API-Key` or `Authorization: Bearer <api-key>`. No production raw bearer user assertions are accepted. |
| neither configured and `Mode == testing` | existing raw bearer test fixture behavior only when the binary is built with the test-only `auth_testfixtures` build tag. |
| neither configured in production | startup error. |

If `OIDCIssuer` is configured but provider discovery fails, startup should fail instead of silently falling back to API-key/raw-bearer behavior. `OIDCAllowedClients` and `OIDCAllowedAudiences` are optional defense-in-depth filters; when both are empty, memory-service accepts any valid token from the configured issuer and relies on normal endpoint/user/role authorization. When both are configured, both checks must pass.

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
| Issuer only | Configure `MEMORY_SERVICE_OIDC_ISSUER` without allowed clients or audiences. | Matches the pre-boundary relaxed behavior; simplest deployment when the issuer realm itself is trusted. | Any valid token from the configured issuer can reach memory-service endpoint authorization. |
| Allowed clients only | `MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=frontend,agent-app` | Simple to configure; works with ordinary Keycloak access tokens; no token exchange required for services that already receive user tokens from allowed clients. | Does not prove the token was minted specifically for memory-service; any accepted client token from the realm may be usable unless endpoint/user authorization blocks it. |
| Allowed audiences only | `MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES=memory-service-api` | Strong resource-server boundary; proves the token was minted for memory-service or an accepted API audience; works well with service-to-service delegation. | Callers may need IdP/client configuration changes or token exchange/on-behalf-of flow to obtain a token with the memory-service audience. |
| Allowed clients and audiences | Configure both settings. | Strongest boundary: caller must be an allowed client and present a token intended for memory-service. | Most operational setup; services commonly need token exchange unless the original token already includes the memory-service audience. |

For direct frontend or agent-app calls, allowed-client checking may be sufficient and simpler. For delegated service-to-service calls, audience checking is usually the cleaner model, but it often requires exchanging the incoming user token for a token whose `aud` includes memory-service.

Do not use Keycloak client attributes directly. Client attributes are Keycloak administration metadata and are not automatically available to memory-service during bearer-token validation. Reading them directly would require Keycloak admin/introspection calls during authentication, admin credentials, caching, and provider-specific logic. It would also make authorization depend on mutable server-side metadata that is not bound to the signed token presented by the caller.

Client attributes are only appropriate if Keycloak maps them into a signed token claim through a protocol mapper. At that point memory-service is validating a token claim, not querying Keycloak attributes directly. Even then, prefer the standard `azp`, `client_id`, and optional `aud` checks for the first implementation, and reserve custom claims for later policy work.

### OIDC Resource/API Scopes

Scopes are useful for controlling what an allowed OIDC client can do, but they are treated as signed token claims and used only as an additional delegation gate. They do not grant access by themselves. Normal user identity, conversation membership, admin/auditor/indexer roles, API-key client roles, and endpoint authorization must still pass first.

Scope gates are optional. If the fixed `MEMORY_SERVICE_OIDC_SCOPES_<PERMISSION_KEY>` mappings are unset, scopes do not restrict access and memory-service continues to rely on OIDC role grants, user role allowlists, client role allowlists, API-key client roles, ownership, membership, and normal endpoint authorization.

The current resolver already extracts role grant values from:

- `roles`
- `groups`
- whitespace-separated `scope`
- Keycloak `realm_access.roles`

This enhancement keeps that role-grant behavior for `MEMORY_SERVICE_ROLES_*_OIDC_ROLE`. It also stores the signed whitespace-separated `scope` claim on the resolved identity and checks configured resource/API scopes at REST and gRPC endpoint boundaries.

Implemented permission keys, flags, and environment variables are organized by increasing granularity. Operators should start with broad keys, then move to resource aggregate or read/write-specific keys only when they need tighter delegation boundaries.

Coarse keys gate broad API families:

| Key | Flag | Environment variable | Use when |
| --- | --- | --- | --- |
| `user` | `--oidc-scopes-user` | `MEMORY_SERVICE_OIDC_SCOPES_USER` | One OIDC scope should allow all normal user API reads and writes after normal user authorization passes. |
| `user_read` | `--oidc-scopes-user-read` | `MEMORY_SERVICE_OIDC_SCOPES_USER_READ` | One OIDC scope should allow all normal user API reads, but writes need a separate scope. |
| `user_write` | `--oidc-scopes-user-write` | `MEMORY_SERVICE_OIDC_SCOPES_USER_WRITE` | One OIDC scope should allow all normal user API writes, but reads need a separate scope. |
| `admin` | `--oidc-scopes-admin` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN` | One OIDC scope should allow all admin API reads and writes after normal admin/auditor/indexer authorization passes. |
| `admin_read` | `--oidc-scopes-admin-read` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_READ` | One OIDC scope should allow all admin API reads, but writes need a separate scope. |
| `admin_write` | `--oidc-scopes-admin-write` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_WRITE` | One OIDC scope should allow all admin API writes, but reads need a separate scope. |

Resource aggregate keys are more granular than `user`/`admin`, but still cover both read and write for a resource family:

| Key | Flag | Environment variable | Use when |
| --- | --- | --- | --- |
| `conversations` | `--oidc-scopes-conversations` | `MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS` | One scope should cover user conversation, entry, fork, child, and response-cancellation reads and writes. |
| `sharing` | `--oidc-scopes-sharing` | `MEMORY_SERVICE_OIDC_SCOPES_SHARING` | One scope should cover membership and ownership-transfer reads and writes. |
| `search` | `--oidc-scopes-search` | `MEMORY_SERVICE_OIDC_SCOPES_SEARCH` | One scope should cover search/list-unindexed reads and indexing writes. |
| `memories` | `--oidc-scopes-memories` | `MEMORY_SERVICE_OIDC_SCOPES_MEMORIES` | One scope should cover user memory reads and writes. |
| `attachments` | `--oidc-scopes-attachments` | `MEMORY_SERVICE_OIDC_SCOPES_ATTACHMENTS` | One scope should cover user attachment reads and writes. |
| `recordings` | `--oidc-scopes-recordings` | `MEMORY_SERVICE_OIDC_SCOPES_RECORDINGS` | One scope should cover gRPC response-recording reads and writes. |
| `admin_conversations` | `--oidc-scopes-admin-conversations` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CONVERSATIONS` | One scope should cover admin conversation and entry reads and writes. |
| `admin_memories` | `--oidc-scopes-admin-memories` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_MEMORIES` | One scope should cover admin memory, policy, usage, and index APIs. |
| `admin_attachments` | `--oidc-scopes-admin-attachments` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_ATTACHMENTS` | One scope should cover admin attachment reads and writes. |
| `admin_checkpoints` | `--oidc-scopes-admin-checkpoints` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CHECKPOINTS` | One scope should cover admin checkpoint reads and writes. |

Read/write-specific keys are the most granular option:

| Key | Flag | Environment variable | Use when |
| --- | --- | --- | --- |
| `system_read` | `--oidc-scopes-system-read` | `MEMORY_SERVICE_OIDC_SCOPES_SYSTEM_READ` | Capabilities and authenticated system reads need their own scope. Existing public health endpoints remain public. |
| `conversations_read` | `--oidc-scopes-conversations-read` | `MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS_READ` | Conversation, entry, fork, and child reads need a separate scope. |
| `conversations_write` | `--oidc-scopes-conversations-write` | `MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS_WRITE` | Conversation, entry, sync, and response-cancellation writes need a separate scope. |
| `sharing_read` | `--oidc-scopes-sharing-read` | `MEMORY_SERVICE_OIDC_SCOPES_SHARING_READ` | Membership and ownership-transfer reads need a separate scope. |
| `sharing_write` | `--oidc-scopes-sharing-write` | `MEMORY_SERVICE_OIDC_SCOPES_SHARING_WRITE` | Membership and ownership-transfer writes need a separate scope. |
| `search_read` | `--oidc-scopes-search-read` | `MEMORY_SERVICE_OIDC_SCOPES_SEARCH_READ` | Search and list-unindexed reads need a separate scope. |
| `search_write` | `--oidc-scopes-search-write` | `MEMORY_SERVICE_OIDC_SCOPES_SEARCH_WRITE` | Indexing writes need a separate scope. |
| `memories_read` | `--oidc-scopes-memories-read` | `MEMORY_SERVICE_OIDC_SCOPES_MEMORIES_READ` | User memory reads need a separate scope. |
| `memories_write` | `--oidc-scopes-memories-write` | `MEMORY_SERVICE_OIDC_SCOPES_MEMORIES_WRITE` | User memory writes need a separate scope. |
| `attachments_read` | `--oidc-scopes-attachments-read` | `MEMORY_SERVICE_OIDC_SCOPES_ATTACHMENTS_READ` | User attachment reads need a separate scope. |
| `attachments_write` | `--oidc-scopes-attachments-write` | `MEMORY_SERVICE_OIDC_SCOPES_ATTACHMENTS_WRITE` | User attachment writes need a separate scope. |
| `events_read` | `--oidc-scopes-events-read` | `MEMORY_SERVICE_OIDC_SCOPES_EVENTS_READ` | User event streams need their own scope. |
| `recordings_read` | `--oidc-scopes-recordings-read` | `MEMORY_SERVICE_OIDC_SCOPES_RECORDINGS_READ` | gRPC response-recording reads need a separate scope. |
| `recordings_write` | `--oidc-scopes-recordings-write` | `MEMORY_SERVICE_OIDC_SCOPES_RECORDINGS_WRITE` | gRPC response-recording writes need a separate scope. |
| `admin_conversations_read` | `--oidc-scopes-admin-conversations-read` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CONVERSATIONS_READ` | Admin conversation and entry reads need a separate scope. |
| `admin_conversations_write` | `--oidc-scopes-admin-conversations-write` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CONVERSATIONS_WRITE` | Admin conversation writes need a separate scope. |
| `admin_memories_read` | `--oidc-scopes-admin-memories-read` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_MEMORIES_READ` | Admin memory, policy, usage, and index reads need a separate scope. |
| `admin_memories_write` | `--oidc-scopes-admin-memories-write` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_MEMORIES_WRITE` | Admin memory, policy, and indexing writes need a separate scope. |
| `admin_attachments_read` | `--oidc-scopes-admin-attachments-read` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_ATTACHMENTS_READ` | Admin attachment reads need a separate scope. |
| `admin_attachments_write` | `--oidc-scopes-admin-attachments-write` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_ATTACHMENTS_WRITE` | Admin attachment writes need a separate scope. |
| `admin_events_read` | `--oidc-scopes-admin-events-read` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_EVENTS_READ` | Admin event streams need their own scope. |
| `admin_checkpoints_read` | `--oidc-scopes-admin-checkpoints-read` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CHECKPOINTS_READ` | Admin checkpoint reads need a separate scope. |
| `admin_checkpoints_write` | `--oidc-scopes-admin-checkpoints-write` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CHECKPOINTS_WRITE` | Admin checkpoint writes need a separate scope. |
| `admin_stats_read` | `--oidc-scopes-admin-stats-read` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_STATS_READ` | Admin stats APIs need their own read scope. |
| `admin_maintenance_write` | `--oidc-scopes-admin-maintenance-write` | `MEMORY_SERVICE_OIDC_SCOPES_ADMIN_MAINTENANCE_WRITE` | Admin eviction and maintenance APIs need their own write scope. |

Each key is configured with:

```bash
MEMORY_SERVICE_OIDC_SCOPES_USER_READ=memory-service:user:read
MEMORY_SERVICE_OIDC_SCOPES_ADMIN=memory-service:admin
MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS_READ=memory-service:conversations:read
MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS_WRITE=memory-service:conversations:write
MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CONVERSATIONS=memory-service:admin-conversations
```

When scope gates are configured:

- `OIDCAllowedClients`, when configured, answers "which OIDC clients may call memory-service at all?"
- `OIDCAllowedAudiences`, when configured, answers "was this token minted for memory-service?"
- `MEMORY_SERVICE_OIDC_SCOPES_<PERMISSION_KEY>` answers "which Memory Service resource/API permission may this OIDC token exercise after normal authorization passes?"
- normal conversation and memory authorization still uses the resolved user ID and conversation membership

Configured scopes cap delegated capability; they do not grant memory-service roles or resource ownership by themselves. For an admin conversation read, all gates must pass:

1. The resolved user or client must still satisfy the admin/auditor role through OIDC roles, user allowlists, client allowlists, or API-key client roles.
2. If the credential includes an OIDC JWT and `admin_conversations` or `admin_conversations_read` scopes are configured, the token must carry one of those signed scope values.

The same rule applies to user resources: a scoped token does not bypass conversation membership rules, memory policy, or user-principal requirements. API-key-only and embedded MCP identities do not include an OIDC JWT, so these OIDC scope mappings are skipped for them.

For read/write resources, aggregate keys are accepted for both read and write checks. A request for `conversations_read` accepts configured scopes from `user`, `user_read`, `conversations`, and `conversations_read`; a request for `admin_conversations_write` accepts configured scopes from `admin`, `admin_write`, `admin_conversations`, and `admin_conversations_write`.

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
    CredentialEmbeddedMCP         CredentialKind = "embedded-mcp"
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

1. `Authorization: Bearer <jwt>` is validated as OIDC when `OIDCIssuer` is configured. If `OIDCAllowedClients` is non-empty, the token's OIDC client ID must be present in that list; otherwise return `401` or `403`. If `OIDCAllowedAudiences` is non-empty, the token's `aud` claim must contain at least one configured audience. If both lists are empty, a valid token from the configured issuer is accepted by the resolver and normal endpoint/user/role authorization still applies.
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
  roles.go                       # user/client/OIDC role mapping and resource/API scope gates
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

### Embedded MCP Synthetic Identity

Embedded MCP is a real runtime mode, not a test fixture, but it has a different trust boundary from remote HTTP/gRPC clients. In embedded mode, the MCP server creates an in-process memory-service instance and sends requests directly through the Gin router with a private `http.RoundTripper`. There is no external browser, agent app, OIDC provider, or network-facing bearer token involved.

Do not make embedded MCP depend on the raw bearer test fixture path. `Authorization: Bearer <synthetic-user>` must remain rejected in default builds unless it is a real OIDC JWT or configured bearer API key. The `auth_testfixtures` build tag is only for relaxed BDD fixtures and must not be required for `./memory-service mcp embedded`.

Handle embedded MCP with an explicit internal credential path:

- Use a dedicated credential kind such as `CredentialEmbeddedMCP`.
- Add an embedded MCP CLI flag and environment variable for the synthetic user principal:

  ```bash
  ./memory-service mcp embedded --user-id local-agent
  MEMORY_SERVICE_MCP_EMBEDDED_USER_ID=local-agent ./memory-service mcp embedded
  ```

  If unset, use a stable default such as `embedded-mcp-user`.
- The configured embedded user ID answers "which memory-service user should this embedded MCP instance act as?" It is not the proof that the request is trusted.
- The in-process MCP client may attach an internal-only marker header, request context value, or resolver input field that cannot be supplied through normal remote listeners. That internal marker is the proof that the synthetic user is allowed.
- The embedded credential should resolve both `UserID` from the embedded MCP user flag and `ClientID` from the embedded MCP client configuration, because MCP tools create and query normal user-scoped conversations.
- The embedded client ID should remain a fixed internal client such as `embedded-mcp` unless a later requirement needs it configurable. It should still map to the configured embedded API key/client role data where role-gated behavior is needed.
- The marker must be added only by `internal/cmd/mcp` when it builds the in-process client for an embedded server. Remote MCP mode must continue using real `X-API-Key` plus optional real bearer credentials.
- Normal REST/gRPC listeners must not accept this internal marker from network traffic. If a header is used, strip or ignore it unless the request is known to come from the in-process transport.

This keeps three cases separate:

| Case | Runtime | Accepted principal source |
| --- | --- | --- |
| Raw bearer BDD fixtures | Tests only, `auth_testfixtures` build tag plus `ModeTesting` | Synthetic bearer user for legacy fixture coverage |
| Embedded MCP | Normal binaries, in-process only | Explicit internal embedded MCP identity |
| Remote service calls | Normal binaries, network-facing | OIDC JWTs, configured `X-API-Key`, or no-OIDC bearer API-key compatibility |

### Role Resolution

Role mapping stays source-specific and uses the resolved credential kind. Resource/API scopes are not evaluated during role resolution and never grant or remove roles:

| Source | Effect |
| --- | --- |
| `MEMORY_SERVICE_ROLES_ADMIN_USERS` / `AUDITOR_USERS` / `INDEXER_USERS` | Grants the role to a resolved non-empty `UserID`. |
| `MEMORY_SERVICE_ROLES_ADMIN_CLIENTS` / `AUDITOR_CLIENTS` / `INDEXER_CLIENTS` | Grants the role to a resolved non-empty `ClientID` from API key or OIDC client claim. |
| OIDC token values from `roles`, `groups`, `scope`, and `realm_access.roles` matched by `MEMORY_SERVICE_ROLES_*_OIDC_ROLE` | Grants the corresponding base role for OIDC tokens. |

Admin continues to imply auditor and indexer.

`TokenResolver.Resolve` stores final roles in `Identity.Roles` and stores OIDC token scopes separately in `Identity.OIDCScopes` when the credential includes an OIDC JWT. Existing HTTP helpers such as `RequireAdminRole`, `RequireAuditorRole`, `HasRole`, and gRPC service methods check the role map. `RequireOIDCScope` and `CheckGRPCOIDCScope` then apply the configured resource/API scope gate for OIDC-bearing requests.

Role calculation order (as implemented):

1. Resolve base role grants from OIDC role claims, configured user allowlists, and configured client allowlists.
2. Apply admin implication. Admin implies auditor and indexer independently of resource/API scope configuration.
3. Store final roles in `Identity.Roles` and derive `Identity.IsAdmin` from the final admin role.
4. For OIDC-bearing requests, preserve token scopes on the identity so REST/gRPC endpoint guards can enforce `MEMORY_SERVICE_OIDC_SCOPES_<PERMISSION_KEY>` after normal role/user authorization passes.

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

- resolver startup succeeds when OIDC is enabled and both allowed clients and allowed audiences are empty
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
- embedded MCP resolves its synthetic in-process user through `CredentialEmbeddedMCP` in default builds without enabling raw bearer fixture auth
- embedded MCP `--user-id` / `MEMORY_SERVICE_MCP_EMBEDDED_USER_ID` controls the resolved synthetic `UserID`
- embedded MCP does not accept the same synthetic credential through remote HTTP/gRPC listeners
- OIDC client ID extraction from `azp` and `client_id`
- OIDC allowed-client rejection
- optional OIDC `aud` enforcement
- audience-only configuration accepts tokens with an allowed audience and rejects tokens without one
- configured OIDC resource/API scope gates require the mapped scope after normal user/client role and membership authorization passes
- configured OIDC scopes alone do not grant admin/auditor/indexer access or resource ownership
- absence of OIDC resource/API scope gates does not deny otherwise authorized requests
- OIDC-only rejection of raw bearer user values
- role union when both OIDC and API key are present
- guard tests proving `RequireUser` rejects client-only identities while admin/auditor guards accept properly role-granted client identities
- route registration tests or BDD coverage proving normal REST user/agent route groups use `RequireUser`

## Tasks

- [x] Add `OIDCAllowedClients` and `OIDCAllowedAudiences` to `internal/config.Config`.
- [x] Add `--oidc-allowed-clients` and `--oidc-allowed-audiences` flags in `internal/cmd/serve/serve.go`.
- [x] Add fail-closed startup validation for OIDC provider discovery; keep OIDC allowed-client and allowed-audience filters optional.
- [x] Consolidate resolver, role, credential, guard, middleware, identity, and raw-bearer build-tag logic into `internal/security/auth.go` (single-file implementation; the planned multi-file split was not done — all logic lives in `auth.go` plus `raw_bearer_disabled.go` / `raw_bearer_testfixtures.go`).
- [x] Refactor `TokenResolver` to accept transport-neutral `RequestCredentials` and resolve typed credential kinds.
- [x] Split raw bearer user fixture support behind an `auth_testfixtures` build tag with a default-build rejection path (`internal/security/raw_bearer_disabled.go` / `raw_bearer_testfixtures.go`).
- [x] Add an explicit embedded MCP synthetic identity path that works in default builds without enabling raw bearer fixture auth, including `mcp embedded --user-id` and `MEMORY_SERVICE_MCP_EMBEDDED_USER_ID`.
- [x] Update HTTP middleware so paired `X-API-Key` can reach the resolver alongside bearer auth.
- [x] Update CORS allowed headers to include `X-API-Key`.
- [x] Add explicit user-principal enforcement for normal REST user/agent endpoints so `X-API-Key`-only client identities cannot create, list, or read user-scoped resources.
- [x] Update gRPC interceptors to use the same credential rules.
- [x] Add real API-key BDD steps (`internal/bdd/steps_auth_modes.go`) that set `X-API-Key`, bearer API-key compatibility credentials, paired `X-API-Key` credentials, and API-key-only user-scope rejection cases.
- [x] Add Keycloak-backed BDD scenarios for issuer-only OIDC, allowed clients, audience enforcement, resource/API scope gates, OIDC API-key pairing, `X-API-Key` admin service principals, and OIDC rejection of raw bearer/bearer API-key fallback (`internal/bdd/testdata/features-oidc/auth-clients-rest.feature`).
- [x] Add a Keycloak realm audience mapper so the `memory-service-client` fixture emits `aud=memory-service` for audience-enforcement tests.
- [x] Add mixed OIDC/API-key and no-OIDC production-mode scenarios for API-key-only admin clients and normal user-scoped API rejection (`internal/bdd/testdata/features/auth-api-keys-rest.feature`).
- [x] Update Go test commands or BDD runner tasks so relaxed raw-bearer fixture suites opt into `auth_testfixtures`, while production-mode API-key/OIDC auth suites run without that tag (`Taskfile.yml`).
- [x] Update configuration documentation and deployment examples to document optional OIDC allowed-client and allowed-audience filters.

## Files Modified

| File | Changes |
| --- | --- |
| `internal/config/config.go` | Added `OIDCAllowedClients` and `OIDCAllowedAudiences` fields. |
| `internal/cmd/serve/serve.go` | Added `--oidc-allowed-clients` and `--oidc-allowed-audiences` flags and startup validation wiring. |
| `internal/cmd/serve/cors.go` | Added `X-API-Key` to `Access-Control-Allow-Headers`. |
| `internal/security/auth.go` | Consolidated resolver, credential kinds, role constants, optional client/audience filters, resource/API scope gates, identity helpers, HTTP middleware, gRPC interceptors, and admin-audit middleware. No multi-file refactor was done; the design's separate `identity.go`, `credentials.go`, `resolver.go`, `roles.go`, `guards.go`, `http_middleware.go`, `grpc_middleware.go` files were not created — everything is in `auth.go`. |
| `internal/security/raw_bearer_disabled.go` | Default-build helper that rejects raw bearer user fixture auth. |
| `internal/security/raw_bearer_testfixtures.go` | `//go:build auth_testfixtures` helper that enables raw bearer user fixture auth only in `ModeTesting`. |
| `internal/cmd/mcp/cmd.go` and `internal/cmd/mcp/inprocess.go` | Added `mcp embedded --user-id` / `MEMORY_SERVICE_MCP_EMBEDDED_USER_ID` and explicit in-process embedded MCP synthetic identity. |
| `internal/cmd/serve/wrapper_routes.go` | Added `RequireUser` enforcement for normal REST user/agent endpoint groups. |
| `internal/bdd/cucumber_pg_keycloak_auth_test.go` | New Keycloak auth/client/API-key BDD runner. |
| `internal/bdd/cucumber_pg_apikey_test.go` | New no-OIDC production-mode API-key BDD runner. |
| `internal/bdd/steps_auth_modes.go` | New auth-mode BDD steps: real API-key, OIDC audience, paired credentials, and server startup helpers. |
| `internal/bdd/testdata/features-oidc/auth-clients-rest.feature` | Keycloak-backed scenarios for issuer-only OIDC, OIDC clients, audiences, resource/API scope gates, REST/gRPC scope coverage, pairing, `X-API-Key` admin principals, user-scoped rejection, and OIDC rejection of bearer API keys/raw bearer users. |
| `internal/bdd/testdata/features/auth-api-keys-rest.feature` | No-OIDC production-mode API-key-only admin and normal user-scope rejection coverage. |
| `deploy/keycloak/memory-service-realm.json` and kustomize realm fixtures | Added an explicit `memory-service` access-token audience mapper for the `memory-service-client` fixture. |
| `compose.yaml`, kustomize auth overlays, Spring compose docs/examples, and Quarkus Dev Services | Added `MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS=memory-service-client,frontend,developer-frontend` wherever an OIDC issuer is configured. |
| `site/src/pages/docs/configuration.mdx` and `site/src/pages/docs/deployment/docker.mdx` | Documented optional OIDC allowed-client / allowed-audience filters. |
| `internal/security/scope_gate_test.go` | Focused scope-gate unit tests. |
| `internal/security/client_id_middleware_test.go` | Client-ID middleware tests. |
| `internal/security/embedded_mcp_test.go` | Embedded MCP synthetic identity tests. |
| `Taskfile.yml` | Updated BDD runner tasks to separate `auth_testfixtures` suites from production-mode suites. |

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

# Run embedded MCP coverage in a default build; this must not require auth_testfixtures.
go test -tags sqlite_fts5 ./internal/cmd/mcp -run Embedded -count=1 > test.log 2>&1
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
2. **Keep OIDC token filters optional**: `OIDCAllowedClients`, `OIDCAllowedAudiences`, or both can narrow accepted tokens when operators want a stricter boundary, but issuer-only deployments remain valid and match the previous relaxed behavior.
3. **Keep `X-API-Key` canonical**: `X-API-Key` carries configured API-key client identity alongside bearer user/JWT identity or as a client-only service principal.
4. **Allow configured service principals**: Configured API keys may authenticate a client-only service principal for admin/operational APIs that are already role-gated and do not need user context, even when the same deployment also accepts OIDC user tokens.
5. **Keep normal APIs user-scoped**: Client-only API-key identities must not satisfy normal user/agent APIs because those APIs need a user principal for ownership, membership, and policy filtering.
6. **Use scopes as resource/API gates**: Signed `scope` values matched by `MEMORY_SERVICE_OIDC_SCOPES_<PERMISSION_KEY>` cap which Memory Service resource/API permissions an OIDC token may exercise after normal user/client role, ownership, membership, and endpoint checks pass.
7. **Use Keycloak in the BDD matrix**: The regression is about API keys, but the risky behavior is the interaction between API keys, bearer tokens, OIDC client claims, audience claims, and scopes, so the OIDC test matrix should run with a real Keycloak issuer.
8. **Treat embedded MCP as internal auth, not fixture auth**: Embedded MCP needs a synthetic user principal for normal user-scoped APIs, but that principal is valid only for the in-process MCP transport and must not reuse the raw bearer test fixture path.

## Security Considerations

- OIDC-enabled deployments must reject raw bearer values so production Keycloak deployments do not accidentally accept arbitrary user IDs.
- In OIDC-enabled deployments, `Authorization: Bearer` remains reserved for OIDC JWTs. API-key-only service principals must use `X-API-Key` so mixed deployments do not reintroduce raw bearer fallback.
- OIDC bearer tokens must come from the configured issuer. When allowed clients or audiences are configured, tokens must also satisfy those filters.
- Resource/API scope gates must use signed token claims and namespaced scope values such as `memory-service:conversations:read`, not arbitrary client attributes.
- API-key-only service principals are limited to routes whose authorization is role/client based; normal user-scoped routes must still require a user principal.
- Embedded MCP synthetic identity is acceptable only for the in-process transport created by `internal/cmd/mcp`; remote listeners must not honor the same marker or synthetic bearer value.
- The raw bearer-user fixture model must require both `cfg.Mode == config.ModeTesting` and the `auth_testfixtures` build tag. `Authorization: Bearer <user-id>` must not authenticate in production modes, even with a valid configured API key.
- JWT-looking bearer values in mixed mode must be validated as OIDC and must not fall back to paired raw-bearer user auth.
- API keys are bearer secrets. Logs must not include raw API-key values.
- When both OIDC and API key credentials are present, client roles granted by API key should be intentional and documented because they can elevate a non-admin OIDC user when the API key maps to an admin client.
- When no OIDC token is present, client roles granted to API keys can provide admin access with no user identity; use dedicated per-processor keys and least-privilege client role lists.
- Raw bearer test fixture behavior must remain limited to `cfg.Mode == config.ModeTesting` and the `auth_testfixtures` build tag. Default binaries must not contain a working raw bearer user authentication path.
