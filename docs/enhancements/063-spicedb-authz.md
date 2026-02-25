---
status: experimental
---

# Enhancement 063: SpiceDB Authorization Backend

> **Status**: Experimental (branch: `experiment/spicedb-authz`)

## Summary

Add SpiceDB (Zanzibar-inspired relationship-based access control) as an optional authorization backend for the memory-service. The existing database-backed membership checks remain the default; SpiceDB can be enabled via configuration for environments that need externalized, policy-driven authorization.

## Motivation

The memory-service enforces access control via `ConversationMembershipRepository.hasAtLeastAccess()`, which queries the membership table directly. This works but tightly couples authorization logic to the data store. SpiceDB offers:

1. **Relationship-based access control**: Model the existing 4-level hierarchy (OWNER > MANAGER > WRITER > READER) plus future organization-scoped permissions.
2. **Multi-tenancy readiness**: The schema includes an `organization` definition that supports Enhancement 060's org admin/owner implicit access to org-scoped conversation groups.
3. **Centralized policy**: Authorization decisions can be audited and managed independently of the data store.

## Design

### Authorization Abstraction

A new `AuthorizationService` interface abstracts permission checks and relationship writes:

```java
public interface AuthorizationService {
    boolean hasAtLeastAccess(String conversationGroupId, String userId, AccessLevel required);
    void writeRelationship(String conversationGroupId, String userId, AccessLevel level);
    void deleteRelationship(String conversationGroupId, String userId, AccessLevel level);
    void writeOrgMembership(String orgId, String userId, String orgRole);
    void deleteOrgMembership(String orgId, String userId, String orgRole);
    void writeOrgConversationGroup(String conversationGroupId, String orgId);
    void writeTeamMembership(String teamId, String userId);
    void deleteTeamMembership(String teamId, String userId);
    void writeTeamOrg(String teamId, String orgId);
    void writeTeamConversationGroup(String conversationGroupId, String teamId);
}
```

Two implementations:
- **`LocalAuthorizationService`**: Delegates `hasAtLeastAccess` to the existing membership repositories with implicit access rules (org owner/admin → MANAGER, team member → WRITER). All write/delete methods are no-ops since the database is the source of truth.
- **`SpiceDbAuthorizationService`**: Uses the authzed-java gRPC client to call SpiceDB's `CheckPermission` and `WriteRelationships` APIs. Uses `atLeastAsFresh` consistency with ZedTokens from prior writes to avoid the cost of fully-consistent reads.

An `AuthorizationServiceSelector` (following the `VectorStoreSelector` pattern) picks the implementation based on `memory-service.authz.type`.

### SpiceDB Schema

```zed
definition user {}

definition organization {
    relation owner: user
    relation admin: user
    relation member: user
    permission delete = owner
    permission manage_members = owner + admin
    permission view_all_conversations = owner + admin
    permission create_conversation = owner + admin + member
    permission is_member = owner + admin + member
}

definition team {
    relation org: organization
    relation member: user
    permission is_member = member
}

definition conversation_group {
    relation org: organization
    relation team: team
    relation owner: user
    relation manager: user
    relation writer: user
    relation reader: user
    permission can_own = owner
    permission can_manage = owner + manager + org->view_all_conversations
    permission can_write = can_manage + writer + team->is_member
    permission can_read = can_write + reader
}
```

### Dual-Write Strategy

Both `PostgresMemoryStore` and `MongoMemoryStore` call `AuthorizationService.writeRelationship()` / `deleteRelationship()` after every membership mutation. With `local` backend these are no-ops. With `spicedb` backend, relationships are written to SpiceDB in the same request (dual-write). This is acceptable for the experimental phase; production use would require an outbox pattern.

### Startup Sync

`SpiceDbBootstrap` optionally loads the schema and syncs all existing memberships to SpiceDB on startup (when `memory-service.authz.spicedb.sync-on-startup=true`).

## Configuration

```properties
# Authorization backend selection (default: local)
memory-service.authz.type=local

# SpiceDB connection (only used when authz.type=spicedb)
memory-service.authz.spicedb.endpoint=localhost:50051
memory-service.authz.spicedb.token=memory-service-dev-key
memory-service.authz.spicedb.tls-enabled=false
memory-service.authz.spicedb.sync-on-startup=false
```

## Files Changed

| File | Action |
|------|--------|
| `security/AuthorizationService.java` | New interface |
| `security/AuthorizationException.java` | New: typed exception for authz failures |
| `security/LocalAuthorizationService.java` | New: local (DB) implementation |
| `security/SpiceDbAuthorizationService.java` | New: SpiceDB implementation |
| `security/SpiceDbBootstrap.java` | New: startup schema + sync |
| `config/AuthorizationServiceSelector.java` | New: selector |
| `config/DatastoreTypeUtil.java` | New: shared datastore type helper |
| `resources/spicedb/schema.zed` | New: SpiceDB schema |
| `store/impl/PostgresMemoryStore.java` | Modified: use AuthorizationService |
| `store/impl/MongoMemoryStore.java` | Modified: use AuthorizationService |
| `pom.xml` | Modified: add authzed dependency |
| `application.properties` | Modified: add authz config |
| `compose.yaml` | Modified: add SpiceDB service |

## Implementation Notes

### Organization & Team Support (Enhancement 060)

The SpiceDB schema, authorization service, and bootstrap sync were extended to support multi-tenancy with organizations and teams:

- **`team` definition** added to `schema.zed` with `org` and `member` relations.
- **`AuthorizationService` interface** extended with 4 team methods: `writeTeamMembership`, `deleteTeamMembership`, `writeTeamOrg`, `writeTeamConversationGroup`.
- **`SpiceDbAuthorizationService`** implements all team methods via gRPC relationship writes.
- **`LocalAuthorizationService`** provides implicit org admin (MANAGER) and team member (WRITER) access checks; team write/delete methods remain no-ops.
- **`SpiceDbBootstrap`** syncs organization memberships, team-org relationships, team memberships, and conversation group org/team scoping on startup.

### Key Files Added/Modified

| File | Change |
|------|--------|
| `resources/spicedb/schema.zed` | Added `team` definition, `team` relation on `conversation_group` |
| `security/AuthorizationService.java` | Added team methods |
| `security/SpiceDbAuthorizationService.java` | Implemented team methods |
| `security/LocalAuthorizationService.java` | Added implicit org/team access checks |
| `security/SpiceDbBootstrap.java` | Added org/team/conversation-group sync |

## Scope Limitations

- **AdminRoleResolver unchanged**: System-level admin/auditor/indexer roles remain separate from resource-level authorization.
- **No migration tooling**: The sync is one-directional (DB -> SpiceDB). There is no reverse sync or conflict resolution.
- **Experiment branch**: This is not intended for production use without further hardening (outbox pattern, retry logic, consistency guarantees).
