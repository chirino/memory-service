---
status: implemented
---

# Admin Access APIs

> **Status**: Implemented.

## Motivation

The memory-service currently provides two API audiences:

1. **User-facing APIs** (`/v1/conversations/*`, `/v1/user/*`) — authenticated via OIDC bearer tokens, scoped to the calling user's own conversations and memberships.
2. **Agent-facing APIs** — authenticated via `X-API-Key`, used by AI agents to manage memory channels.

There is no mechanism for platform administrators to:

- **View all conversations** across all users (for support, compliance, or debugging).
- **Inspect soft-deleted resources** introduced by enhancement 013.
- **Search across the entire system**, not just a single user's conversations.
- **Manage conversations on behalf of users** (e.g., force-delete, restore soft-deleted resources).
- **Audit system state** (e.g., list all conversations deleted in a time range, view membership history).

This enhancement introduces `/v1/admin/*` APIs that mirror the existing user-facing endpoints but operate with elevated, cross-user privileges. It defines how admin and auditor roles are identified, authorized, and audit-logged.

## Design Decisions

### Roles: Admin and Auditor

Two granular roles govern access to the admin APIs:

| Role | Access | Purpose |
|------|--------|---------|
| `admin` | Read + Write | Full administrative access: list, view, delete, restore conversations across all users |
| `auditor` | Read-only | Compliance and support: list and view conversations across all users, but cannot modify anything |

Both roles have cross-user visibility (they can see any user's conversations, including soft-deleted resources). The difference is that `auditor` cannot perform write operations (delete, restore).

### How Roles Are Resolved

The memory-service supports three complementary mechanisms for assigning admin/auditor roles to callers. All three are checked — if **any** mechanism grants a role, the caller has that role.

#### Mechanism 1: OIDC Role Mapping

Map OIDC token roles to internal memory-service roles. This allows the OIDC role names to differ from the internal role names (e.g., the OIDC provider may call its role `administrator` or `platform-admin` rather than `admin`).

```properties
# Map OIDC role "administrator" to internal role "admin"
memory-service.roles.admin.oidc.role=administrator

# Map OIDC role "manager" to internal role "auditor"
memory-service.roles.auditor.oidc.role=manager
```

**How it works:**
- On each request, check if `SecurityIdentity.hasRole(configuredOidcRole)` is true.
- If `memory-service.roles.admin.oidc.role` is not set, fall back to checking `SecurityIdentity.hasRole("admin")` (the internal role name is the default OIDC role name).
- Similarly, if `memory-service.roles.auditor.oidc.role` is not set, fall back to `SecurityIdentity.hasRole("auditor")`.

This uses the existing `quarkus.oidc.roles.source=accesstoken` configuration already in `application.properties`. No additional Quarkus OIDC configuration is needed.

#### Mechanism 2: User-Based Assignment

Assign roles to specific user IDs directly in `application.properties`:

```properties
memory-service.roles.admin.users=alice,bob
memory-service.roles.auditor.users=charlie,dave
```

**How it works:**
- On each request, compare `SecurityIdentity.getPrincipal().getName()` against the configured user list.
- Useful for environments where the OIDC provider doesn't support custom roles, or for emergency access.

#### Mechanism 3: Client-Based Assignment (API Key)

Assign roles to API key client IDs, allowing agents/services to call admin APIs:

```properties
memory-service.roles.admin.clients=admin-agent
memory-service.roles.auditor.clients=monitoring-agent,audit-agent
```

**How it works:**
- On each request that includes an `X-API-Key` header, resolve the client ID via the existing `ApiKeyManager`.
- Check if the resolved client ID appears in the configured list for the role.
- The caller must still be OIDC-authenticated (the API key supplements the OIDC identity, it does not replace it).

#### Role Resolution Logic

```java
@ApplicationScoped
public class AdminRoleResolver {

    @ConfigProperty(name = "memory-service.roles.admin.oidc.role",
                    defaultValue = "admin")
    String adminOidcRole;

    @ConfigProperty(name = "memory-service.roles.auditor.oidc.role",
                    defaultValue = "auditor")
    String auditorOidcRole;

    @ConfigProperty(name = "memory-service.roles.admin.users",
                    defaultValue = "")
    Optional<List<String>> adminUsers;

    @ConfigProperty(name = "memory-service.roles.auditor.users",
                    defaultValue = "")
    Optional<List<String>> auditorUsers;

    @ConfigProperty(name = "memory-service.roles.admin.clients",
                    defaultValue = "")
    Optional<List<String>> adminClients;

    @ConfigProperty(name = "memory-service.roles.auditor.clients",
                    defaultValue = "")
    Optional<List<String>> auditorClients;

    public boolean hasAdminRole(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        // OIDC role check
        if (identity.hasRole(adminOidcRole)) return true;
        // User-based check
        String userId = identity.getPrincipal().getName();
        if (adminUsers.isPresent() && adminUsers.get().contains(userId)) return true;
        // Client-based check
        if (apiKeyContext != null && apiKeyContext.hasValidApiKey()
            && adminClients.isPresent()
            && adminClients.get().contains(apiKeyContext.getClientId())) return true;
        return false;
    }

    public boolean hasAuditorRole(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        // Admin implies auditor
        if (hasAdminRole(identity, apiKeyContext)) return true;
        // OIDC role check
        if (identity.hasRole(auditorOidcRole)) return true;
        // User-based check
        String userId = identity.getPrincipal().getName();
        if (auditorUsers.isPresent() && auditorUsers.get().contains(userId)) return true;
        // Client-based check
        if (apiKeyContext != null && apiKeyContext.hasValidApiKey()
            && auditorClients.isPresent()
            && auditorClients.get().contains(apiKeyContext.getClientId())) return true;
        return false;
    }
}
```

Key details:
- `admin` role implies `auditor` role — admins can do everything auditors can.
- All three mechanisms are OR'd — any match grants the role.
- If no configuration is provided, the defaults use the internal role names (`admin`, `auditor`) for OIDC lookup and no users/clients are assigned.

### Audit Logging

All admin API calls are logged through a dedicated `Logger` instance. Each admin endpoint accepts an optional `justification` field that records why the admin action was taken.

#### Justification Field

Write operations (DELETE, POST restore) accept a `justification` field in the request body:

```json
{
  "justification": "User requested account cleanup per support ticket #1234"
}
```

Read operations (GET) accept `justification` as a query parameter:

```
GET /v1/admin/conversations?userId=bob&justification=Investigating+support+ticket+1234
```

#### Mandatory Justification

An environment variable / config property controls whether the `justification` field is required:

```properties
# Default: false (justification is optional)
memory-service.admin.require-justification=false
```

When set to `true`, any admin API call without a `justification` value returns `400 Bad Request`:

```json
{
  "error": "Justification is required for admin operations",
  "code": "JUSTIFICATION_REQUIRED"
}
```

#### Log Format

```java
private static final Logger ADMIN_AUDIT = Logger.getLogger("io.github.chirino.memory.admin.audit");

// Example log entries:
// INFO  [admin.audit] ADMIN_READ user=alice action=listConversations params={userId=bob,includeDeleted=true} justification="Support ticket #1234"
// INFO  [admin.audit] ADMIN_WRITE user=alice action=restoreConversation target=conv-uuid-123 justification="Accidental deletion recovery"
// INFO  [admin.audit] ADMIN_READ user=charlie role=auditor action=getConversation target=conv-uuid-456 justification="Compliance review Q4"
```

Each log entry includes:
- **Caller identity**: username from OIDC token (and client ID if API key used)
- **Resolved role**: `admin` or `auditor`
- **Action**: the operation performed
- **Target**: conversation ID or search parameters
- **Justification**: the provided reason (or `<none>` if not required and omitted)

### Separate OpenAPI Specification

Admin APIs are defined in a **separate OpenAPI specification** to avoid cluttering the main spec that user-facing and agent-facing clients consume.

**File:** `memory-service-contracts/src/main/resources/openapi-admin.yml`

Rationale:
- The main spec (`openapi.yml`) generates the Java REST client (`memory-service-rest-quarkus`) and the TypeScript client (`chat-frontend`). Neither consumer needs admin endpoints.
- A separate spec keeps generated clients clean — no admin methods appear in user/agent SDKs.
- Admin tooling (CLI scripts, internal dashboards) generates its own client from `openapi-admin.yml`.
- Shared schemas (e.g., `Conversation`, `Message`) are duplicated in the admin spec with the additional `deletedAt` field. Since we are pre-release and the admin spec is small, this duplication is acceptable and avoids `$ref` cross-file complexity.

### Admin API Structure

All admin endpoints live under `/v1/admin/*` on the same HTTP port as the main API.

#### Endpoints

| Endpoint | Method | Role Required | Description |
|----------|--------|---------------|-------------|
| `/v1/admin/conversations` | GET | auditor | List all conversations across all users |
| `/v1/admin/conversations/{id}` | GET | auditor | Get any conversation (including soft-deleted) |
| `/v1/admin/conversations/{id}` | DELETE | admin | Soft-delete any conversation |
| `/v1/admin/conversations/{id}/restore` | POST | admin | Restore a soft-deleted conversation |
| `/v1/admin/conversations/{id}/messages` | GET | auditor | Get messages from any conversation |
| `/v1/admin/conversations/{id}/memberships` | GET | auditor | Get memberships for any conversation |
| `/v1/admin/search/messages` | POST | auditor | System-wide semantic search |

`auditor` role grants read-only access. `admin` role grants read + write access (and implies `auditor`).

#### Additional Query Parameters

Admin endpoints support all existing query parameters plus:

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `userId` | string | — | Filter conversations owned by this user |
| `includeDeleted` | boolean | `false` | Include soft-deleted resources in results |
| `onlyDeleted` | boolean | `false` | Show only soft-deleted resources |
| `deletedAfter` | ISO 8601 datetime | — | Filter: deleted at or after this time |
| `deletedBefore` | ISO 8601 datetime | — | Filter: deleted before this time |
| `justification` | string | — | Reason for the admin action (for audit log) |

#### Response Schema Differences

Admin API responses include additional fields not present in user-facing APIs:

```json
{
  "id": "...",
  "title": "...",
  "ownerUserId": "alice",
  "createdAt": "2024-01-15T10:00:00Z",
  "updatedAt": "2024-01-15T12:00:00Z",
  "deletedAt": "2024-01-20T08:30:00Z",
  "conversationGroupId": "..."
}
```

The `deletedAt` field is included when non-null. User-facing APIs continue to omit it.

### Authorization Enforcement

#### REST Endpoints

The `AdminResource` does **not** use `@RolesAllowed` (since role resolution is custom — combining OIDC roles, user lists, and client lists). Instead, it delegates to `AdminRoleResolver` programmatically:

```java
@Path("/v1/admin")
@Authenticated
public class AdminResource {

    @Inject SecurityIdentity identity;
    @Inject ApiKeyContext apiKeyContext;
    @Inject AdminRoleResolver roleResolver;
    @Inject AdminAuditLogger auditLogger;

    @GET
    @Path("/conversations")
    public Response listConversations(
            @QueryParam("userId") String userId,
            @QueryParam("includeDeleted") @DefaultValue("false") boolean includeDeleted,
            @QueryParam("onlyDeleted") @DefaultValue("false") boolean onlyDeleted,
            @QueryParam("justification") String justification) {
        roleResolver.requireAuditor(identity, apiKeyContext);
        auditLogger.logRead("listConversations",
            Map.of("userId", userId, "includeDeleted", includeDeleted),
            justification, identity, apiKeyContext);
        // ...
    }

    @DELETE
    @Path("/conversations/{id}")
    public Response deleteConversation(
            @PathParam("id") String id,
            AdminActionRequest request) {
        roleResolver.requireAdmin(identity, apiKeyContext);
        auditLogger.logWrite("deleteConversation", id,
            request.getJustification(), identity, apiKeyContext);
        // ...
    }
}
```

Where `requireAuditor()` and `requireAdmin()` throw a `ForbiddenException` (403) if the caller lacks the required role. The `AdminAuditLogger` handles justification validation and logging.

#### HTTP Auth Policy (Defense-in-Depth)

The path-level policy ensures that even if an endpoint method omits its role check, unauthenticated users are still blocked:

```properties
quarkus.http.auth.permission.admin.paths=/v1/admin/*
quarkus.http.auth.permission.admin.policy=authenticated
```

Note: This uses `authenticated` (not a roles-based policy) because the custom `AdminRoleResolver` handles role checking. The HTTP policy ensures the caller is at least authenticated.

### Store Layer Changes

Separate admin methods on the `MemoryStore` interface for security and clarity:

```java
public interface MemoryStore {
    // Existing user-scoped methods (unchanged)
    List<Conversation> listConversations(String userId, ...);

    // Admin methods — no userId scoping, configurable deleted-resource visibility
    List<Conversation> adminListConversations(AdminConversationQuery query);
    Optional<Conversation> adminGetConversation(String conversationId, boolean includeDeleted);
    void adminDeleteConversation(String conversationId);
    void adminRestoreConversation(String conversationId);
    List<Message> adminGetMessages(String conversationId, AdminMessageQuery query);
    SearchResult adminSearchMessages(AdminSearchQuery query);
}
```

Key difference from user methods:
- No `userId` scoping (or optional `userId` filter).
- Configurable `deletedAt` filtering (include, exclude, or only deleted).
- No `ensureHasAccess()` calls.

### Restore Operation (Undo Soft Delete)

```
POST /v1/admin/conversations/{id}/restore
```

**Request body:**
```json
{
  "justification": "Accidental deletion recovery per ticket #5678"
}
```

**Behavior:**
1. Find the conversation group by ID (including deleted records).
2. Verify it is currently soft-deleted (`deletedAt IS NOT NULL`).
3. Set `deletedAt = NULL` on the `ConversationGroupEntity`, all `ConversationEntity` records in the group, and all `ConversationMembershipEntity` records in the group.
4. Return the restored conversation.

**Edge cases:**
- Restoring an already-active conversation returns `409 Conflict`.
- Restoring a hard-deleted (purged) conversation returns `404 Not Found`.
- Ownership transfers that were `EXPIRED` due to deletion are NOT automatically restored (they must be re-initiated).

## Scope of Changes

### 1. Admin Role Resolver

**New file:** `memory-service/src/main/java/io/github/chirino/memory/security/AdminRoleResolver.java`

Reads `memory-service.roles.{admin,auditor}.{oidc.role,users,clients}` config properties. Provides `hasAdminRole()`, `hasAuditorRole()`, `requireAdmin()`, `requireAuditor()` methods.

### 2. Admin Audit Logger

**New file:** `memory-service/src/main/java/io/github/chirino/memory/security/AdminAuditLogger.java`

Dedicated `Logger` instance (`io.github.chirino.memory.admin.audit`). Provides `logRead()` and `logWrite()` methods. Validates `justification` when `memory-service.admin.require-justification=true`.

### 3. Keycloak Realm Configuration

**File:** `deploy/keycloak/memory-service-realm.json`

Add `admin` and `auditor` realm roles. Assign `admin` to a test user (e.g., `alice`).

### 4. OpenAPI Specification (Admin)

**New file:** `memory-service-contracts/src/main/resources/openapi-admin.yml`

Admin endpoint definitions with:
- Bearer auth security scheme
- Query parameters (`userId`, `includeDeleted`, `onlyDeleted`, `deletedAfter`, `deletedBefore`, `justification`)
- Admin response schemas including `deletedAt`
- `AdminActionRequest` schema with `justification` field

### 5. Application Configuration

**File:** `memory-service/src/main/resources/application.properties`

```properties
# Admin path requires authentication (role checking is application-level)
quarkus.http.auth.permission.admin.paths=/v1/admin/*
quarkus.http.auth.permission.admin.policy=authenticated

# Admin audit justification (default: optional)
memory-service.admin.require-justification=false

# Role mapping defaults (uncomment to customize)
# memory-service.roles.admin.oidc.role=admin
# memory-service.roles.auditor.oidc.role=auditor
# memory-service.roles.admin.users=
# memory-service.roles.auditor.users=
# memory-service.roles.admin.clients=
# memory-service.roles.auditor.clients=
```

### 6. Admin Query Models

**New file:** `memory-service/src/main/java/io/github/chirino/memory/model/AdminConversationQuery.java`

```java
public class AdminConversationQuery {
    private String userId;
    private boolean includeDeleted;
    private boolean onlyDeleted;
    private OffsetDateTime deletedAfter;
    private OffsetDateTime deletedBefore;
    // pagination fields
}
```

**New file:** `memory-service/src/main/java/io/github/chirino/memory/model/AdminActionRequest.java`

```java
public class AdminActionRequest {
    private String justification;
}
```

### 7. Store Interface

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java`

Add admin method signatures (see Store Layer Changes section above).

### 8. PostgresMemoryStore Implementation

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`

Implement admin methods with configurable `deletedAt` filtering and no per-user scoping.

### 9. MongoMemoryStore Implementation

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java`

Mirror the PostgresMemoryStore admin methods for MongoDB.

### 10. Admin REST Resource

**New file:** `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java`

JAX-RS resource under `/v1/admin`. Uses `AdminRoleResolver` for authorization and `AdminAuditLogger` for audit logging. Read endpoints require `auditor`, write endpoints require `admin`.

### 11. Test Configuration

**File:** `memory-service/src/test/resources/application.properties`

```properties
# Assign roles for testing
%test.quarkus.keycloak.devservices.roles.alice=admin
%test.quarkus.keycloak.devservices.roles.charlie=auditor

# Role configuration for tests
%test.memory-service.roles.admin.oidc.role=admin
%test.memory-service.roles.auditor.oidc.role=auditor
```

### 12. Cucumber Tests

**New file:** `memory-service/src/test/resources/features/admin-rest.feature`

Scenarios:
- Admin can list all conversations across users
- Admin can view soft-deleted conversations with `includeDeleted=true`
- Admin can filter by `userId`, `onlyDeleted`, and date ranges
- Admin can delete any conversation
- Admin can restore a soft-deleted conversation
- Auditor can list and view conversations across users
- Auditor receives `403 Forbidden` on write operations (delete, restore)
- Non-admin/non-auditor user receives `403 Forbidden` on all admin endpoints
- Justification is logged when provided
- When `require-justification=true`, requests without justification return `400`
- Role assignment via `memory-service.roles.admin.users` works
- Role assignment via `memory-service.roles.admin.clients` works with API key

### 13. Cucumber Step Definitions

**File:** `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java`

Add steps for:
```gherkin
Given I am authenticated as admin user "alice"
Given I am authenticated as auditor user "charlie"
When I call GET "/v1/admin/conversations"
When I call GET "/v1/admin/conversations?userId=bob&includeDeleted=true"
When I call DELETE "/v1/admin/conversations/{conversationId}" with justification "ticket #123"
When I call POST "/v1/admin/conversations/{conversationId}/restore" with justification "recovery"
Then the admin audit log should contain "listConversations"
```

### 14. Site Documentation

**File:** `site/src/pages/docs/configuration.md`

Add a new `## Admin Access Configuration` section after the existing `## OIDC Authentication` section. This section documents all new configuration properties introduced by this enhancement. The section should follow the existing documentation style (tables for properties, `bash` code blocks for environment variable examples).

Content to add:

```markdown
## Admin Access Configuration

Memory Service provides `/v1/admin/*` APIs for platform administrators and auditors.
Access is controlled through role assignment, which can be configured via OIDC token roles,
explicit user lists, or API key client IDs. All three mechanisms are checked — if any
grants a role, the caller has that role.

### Roles

| Role | Access | Description |
|------|--------|-------------|
| `admin` | Read + Write | Full administrative access across all users. Implies `auditor`. |
| `auditor` | Read-only | View any user's conversations and search system-wide. Cannot modify data. |

### Role Assignment

Roles can be assigned through three complementary mechanisms:

#### OIDC Role Mapping

Map OIDC token roles to internal Memory Service roles. This is useful when the OIDC
provider uses different role names (e.g., `administrator` instead of `admin`).

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.roles.admin.oidc.role` | `admin` | OIDC role name that maps to the internal `admin` role |
| `memory-service.roles.auditor.oidc.role` | `auditor` | OIDC role name that maps to the internal `auditor` role |

```bash
# Map OIDC "administrator" role to internal "admin" role
MEMORY_SERVICE_ROLES_ADMIN_OIDC_ROLE=administrator

# Map OIDC "manager" role to internal "auditor" role
MEMORY_SERVICE_ROLES_AUDITOR_OIDC_ROLE=manager
```

#### User-Based Assignment

Assign roles directly to user IDs (matched against the OIDC token principal name):

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.roles.admin.users` | _(empty)_ | Comma-separated list of user IDs with admin access |
| `memory-service.roles.auditor.users` | _(empty)_ | Comma-separated list of user IDs with auditor access |

```bash
MEMORY_SERVICE_ROLES_ADMIN_USERS=alice,bob
MEMORY_SERVICE_ROLES_AUDITOR_USERS=charlie,dave
```

#### Client-Based Assignment (API Key)

Assign roles to API key client IDs, allowing agents or services to call admin APIs.
The client ID is resolved from the `X-API-Key` header via the existing API key configuration.

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.roles.admin.clients` | _(empty)_ | Comma-separated list of API key client IDs with admin access |
| `memory-service.roles.auditor.clients` | _(empty)_ | Comma-separated list of API key client IDs with auditor access |

```bash
MEMORY_SERVICE_ROLES_ADMIN_CLIENTS=admin-agent
MEMORY_SERVICE_ROLES_AUDITOR_CLIENTS=monitoring-agent,audit-agent
```

### Audit Logging

All admin API calls are logged to a dedicated logger (`io.github.chirino.memory.admin.audit`).
Each request can include a `justification` field explaining why the admin action was taken.

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.admin.require-justification` | `true`, `false` | `false` | When `true`, all admin API calls must include a `justification` or receive `400 Bad Request` |

```bash
# Require justification for all admin operations
MEMORY_SERVICE_ADMIN_REQUIRE_JUSTIFICATION=true
```

To route admin audit logs to a separate file or external system, configure the Quarkus
logging category:

```bash
# Set admin audit log level
QUARKUS_LOG_CATEGORY__IO_GITHUB_CHIRINO_MEMORY_ADMIN_AUDIT__LEVEL=INFO
```
```

### 15. Regenerate Clients

The admin spec generates a separate client. The main user/agent clients are unaffected.

```bash
# Main Java client (unchanged — uses openapi.yml only)
./mvnw -pl quarkus/memory-service-rest-quarkus clean compile

# Admin Java client (new module or generation target)
# TBD: may be a new Maven module or generated alongside

# Main TypeScript client (unchanged)
cd examples/chat-frontend && npm run generate
```

## Implementation Order

1. **Admin role resolver** — `AdminRoleResolver` with config-driven role mapping
2. **Admin audit logger** — `AdminAuditLogger` with justification support
3. **Keycloak realm** — Add `admin` and `auditor` roles to realm JSON
4. **Application config** — Add HTTP auth policy and role/audit config properties
5. **Admin query models** — `AdminConversationQuery`, `AdminActionRequest`, etc.
6. **Store interface** — Add admin method signatures to `MemoryStore`
7. **PostgresMemoryStore** — Implement admin methods
8. **MongoMemoryStore** — Implement admin methods
9. **OpenAPI admin spec** — `openapi-admin.yml` with admin endpoints
10. **AdminResource** — REST implementation
11. **Test configuration** — Keycloak roles for test users
12. **Cucumber tests** — Admin API test scenarios
13. **Site documentation** — Add Admin Access Configuration section to `site/src/pages/docs/configuration.md`
14. **Compile and test** — Full verification

## Verification

```bash
# Compile all modules
./mvnw compile

# Run all tests
./mvnw test

# Run admin-specific tests
./mvnw test -Dcucumber.filter.tags="@admin"
```

## Files to Modify (Complete List)

| File | Change Type |
|------|-------------|
| `memory-service/src/main/java/io/github/chirino/memory/security/AdminRoleResolver.java` | New file |
| `memory-service/src/main/java/io/github/chirino/memory/security/AdminAuditLogger.java` | New file |
| `deploy/keycloak/memory-service-realm.json` | Add admin/auditor roles |
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | New file |
| `memory-service/src/main/resources/application.properties` | Add admin config |
| `memory-service/src/main/java/io/github/chirino/memory/model/AdminConversationQuery.java` | New file |
| `memory-service/src/main/java/io/github/chirino/memory/model/AdminActionRequest.java` | New file |
| `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java` | Add admin methods |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java` | Implement admin methods |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java` | Implement admin methods |
| `memory-service/src/main/java/io/github/chirino/memory/api/AdminResource.java` | New file |
| `memory-service/src/test/resources/application.properties` | Add test roles |
| `memory-service/src/test/resources/features/admin-rest.feature` | New file |
| `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java` | Add admin steps |
| `site/src/pages/docs/configuration.md` | Add Admin Access Configuration section |

## Assumptions

1. **`admin` implies `auditor`.** An admin can do everything an auditor can, plus write operations. There is no need to assign both roles to a user.

2. **All three role mechanisms are OR'd.** A caller is an admin if _any_ of the following is true: their OIDC token has the mapped admin role, their username is in `memory-service.roles.admin.users`, or their API key client ID is in `memory-service.roles.admin.clients`.

3. **API key does not replace OIDC.** Client-based role assignment (`memory-service.roles.admin.clients`) still requires the caller to be OIDC-authenticated. The API key identifies the client for role assignment but is not sufficient on its own.

4. **Admin APIs do not bypass data encryption.** Admin sees the same decrypted content that users see.

5. **Admin cannot impersonate users.** Admin endpoints return data directly — there is no "act as user X" mechanism.

6. **Restore only applies to soft-deleted conversations.** There is no "undo" for hard-deleted (purged) data.

7. **The `/v1/admin/*` path prefix is the only admin surface.** No separate port.

8. **Audit logging uses the standard logging framework.** No separate database table for audit records — the dedicated logger category can be routed to a file, syslog, or external system via Quarkus logging configuration.

9. **Vector store search for admin uses the same two-phase pattern.** Admin search queries all conversation IDs (not just the caller's), then passes them to the vector store.

## Future Considerations

- **Admin UI** — A dedicated admin panel in the web UI for browsing and managing all conversations.
- **Additional granular roles** — e.g., `admin-super` for hard-delete/purge operations.
- **Structured audit trail** — Database table for admin actions (beyond log output) for queryable audit history.
- **Rate limiting** — Admin APIs could return large result sets; pagination caps and rate limiting may be needed.
- **Hard delete via admin** — Allow admins to trigger immediate hard deletes (bypassing the retention period) for GDPR "right to erasure" requests.
- **User impersonation** — Allow admins to "act as" a specific user for debugging (with audit logging).
- **Separate admin port** — Run admin APIs on a different port for network-level isolation if needed in the future.
- **Admin client SDK** — Generate a dedicated Java/TypeScript client from `openapi-admin.yml` in a separate Maven module.
