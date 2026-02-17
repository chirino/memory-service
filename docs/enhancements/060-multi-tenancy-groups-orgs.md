---
status: proposed
---

# Enhancement 060: Multi-Tenancy — Groups & Organizations

> **Status**: Proposed.

## Summary

Introduce an organization/tenant layer above the existing per-user access model, enabling multiple users to be grouped into organizations with shared conversation visibility, centralized administration, and data isolation between tenants.

## Motivation

The current access control model is **user-centric**:

- Each conversation has an **owner** (a single user).
- Conversations can be **shared** with individual users at specific access levels (`owner`, `manager`, `writer`, `reader`).
- **Admin access** is granted via OIDC roles or explicit user/client lists in configuration.
- There is **no concept of a group, team, or organization** — sharing is always one-to-one.

This model works for individual developers using the memory service, but breaks down for organizational use cases:

### Use Cases Not Supported Today

1. **Team-shared agents**: A customer support team wants all members to see conversations handled by their shared AI agent. Today, the agent would need to individually share each conversation with every team member.

2. **Data isolation**: A SaaS platform hosts multiple customers on a single memory service instance. Today, there is no way to ensure Customer A's conversations are invisible to Customer B's agents — isolation depends entirely on correct API key usage.

3. **Centralized administration**: An org admin wants to manage all conversations across their organization (audit, delete, transfer). Today, the admin role is global — an admin sees everything, not just their org.

4. **Onboarding/offboarding**: When a user joins or leaves a team, there is no way to bulk-grant or revoke access to all team conversations.

5. **Usage tracking**: Billing or quota enforcement per organization requires knowing which conversations belong to which org.

## Design

### Core Concepts

```
Organization (tenant boundary)
├── Members (users with org-level roles)
├── Teams (optional grouping within an org)
│   └── Members (users with team-level roles)
└── Conversations (scoped to org)
    └── Memberships (existing per-user access, scoped within org)
```

### Data Model

#### New Tables

```sql
CREATE TABLE organizations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE,       -- URL-safe identifier
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ                -- soft delete
);

CREATE TABLE organization_members (
    organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    role            TEXT NOT NULL,              -- 'owner', 'admin', 'member'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, user_id)
);
CREATE INDEX idx_org_members_user ON organization_members (user_id);
```

#### Modified Tables

```sql
-- Add org scope to conversations
ALTER TABLE conversation_groups
    ADD COLUMN organization_id UUID REFERENCES organizations (id);

CREATE INDEX idx_conversation_groups_org ON conversation_groups (organization_id)
    WHERE organization_id IS NOT NULL;
```

#### Organization Roles

| Role | Permissions |
|------|-------------|
| `owner` | Full control: manage members, delete org, all admin rights |
| `admin` | Manage members (except owners), view all org conversations, manage conversations |
| `member` | Create conversations within org, see conversations shared with them or their teams |

### API Design

#### Organization Management

```
POST   /v1/organizations                    # Create org
GET    /v1/organizations                    # List user's orgs
GET    /v1/organizations/{orgId}            # Get org details
PATCH  /v1/organizations/{orgId}            # Update org
DELETE /v1/organizations/{orgId}            # Delete org (owner only)
```

#### Membership Management

```
GET    /v1/organizations/{orgId}/members            # List members
POST   /v1/organizations/{orgId}/members            # Add member
PATCH  /v1/organizations/{orgId}/members/{userId}   # Update role
DELETE /v1/organizations/{orgId}/members/{userId}   # Remove member
```

#### Org-Scoped Conversations

Conversations can optionally be scoped to an organization:

```
POST /v1/conversations
{
  "title": "Support case #1234",
  "organizationId": "org-uuid",     // NEW: optional org scope
  "metadata": {}
}
```

When `organizationId` is set:
- The conversation is visible to org admins/owners automatically.
- Sharing is restricted to members of the same organization.
- The conversation appears in org-scoped listing queries.

#### Org-Scoped Listing

```
GET /v1/conversations?organizationId={orgId}
```

Returns conversations scoped to the given organization that the current user can access.

### Request Schema

```yaml
CreateOrganizationRequest:
  type: object
  required: [name]
  properties:
    name:
      type: string
      maxLength: 200
    slug:
      type: string
      maxLength: 100
      pattern: "^[a-z0-9][a-z0-9-]*[a-z0-9]$"
    metadata:
      type: object

AddOrganizationMemberRequest:
  type: object
  required: [userId, role]
  properties:
    userId:
      type: string
      maxLength: 255
    role:
      type: string
      enum: [admin, member]
```

### Access Control Changes

The existing `ConversationMembershipRepository.findAccessLevel()` check must be extended to consider organization membership:

```java
public Optional<AccessLevel> findEffectiveAccessLevel(UUID conversationGroupId, String userId) {
    // 1. Check direct membership (existing behavior)
    Optional<AccessLevel> direct = findAccessLevel(conversationGroupId, userId);
    if (direct.isPresent()) {
        return direct;
    }

    // 2. Check org-level access (new)
    Optional<UUID> orgId = findOrganizationId(conversationGroupId);
    if (orgId.isPresent()) {
        Optional<OrgRole> orgRole = findOrgRole(orgId.get(), userId);
        if (orgRole.isPresent()) {
            return switch (orgRole.get()) {
                case OWNER, ADMIN -> Optional.of(AccessLevel.MANAGER);
                case MEMBER -> Optional.empty(); // Members need explicit sharing
            };
        }
    }

    return Optional.empty();
}
```

### Data Isolation

Organization-scoped conversations enforce isolation:

1. **Query filtering**: All conversation listing queries include `WHERE organization_id = :orgId OR organization_id IS NULL` depending on context.
2. **Sharing restrictions**: Cannot share an org-scoped conversation with a user outside the org (400 error).
3. **Admin scoping**: Org admins only see conversations within their org via org-scoped admin endpoints. Global admins (OIDC role) retain cross-org visibility.

### Backward Compatibility

- `organization_id` on `conversation_groups` is **nullable**. Existing conversations without an org continue to work exactly as before.
- All existing APIs remain unchanged. The org features are additive.
- Users who don't use organizations see no difference in behavior.

## Testing

### Unit Tests

```java
class OrganizationAccessControlTest {

    @Test
    void orgOwnerHasManagerAccessToOrgConversations() {
        // Create org, add owner
        // Create conversation scoped to org
        // Verify owner has MANAGER access without explicit sharing
    }

    @Test
    void orgMemberCannotAccessWithoutExplicitSharing() {
        // Create org, add member
        // Create conversation scoped to org
        // Verify member has NO access unless explicitly shared
    }

    @Test
    void cannotShareOrgConversationOutsideOrg() {
        // Create org with user A
        // Create conversation scoped to org
        // Try to share with user B (not in org)
        // Verify 400 error
    }

    @Test
    void unscopedConversationsUnaffected() {
        // Create conversation without organizationId
        // Verify existing access control works identically
    }
}
```

### Cucumber Scenarios

```gherkin
Feature: Organization Management

  Scenario: Create an organization
    Given Alice has an access token
    When Alice creates an organization named "Acme Corp"
    Then the response status is 201
    And Alice is the owner of the organization

  Scenario: Add a member to an organization
    Given Alice owns organization "Acme Corp"
    When Alice adds Bob as a member
    Then Bob appears in the organization members list

  Scenario: Org admin sees all org conversations
    Given Alice owns organization "Acme Corp"
    And Bob is a member of "Acme Corp"
    And Bob creates a conversation in "Acme Corp"
    When Alice lists conversations for "Acme Corp"
    Then Alice can see Bob's conversation

  Scenario: Cannot share org conversation outside org
    Given Alice owns organization "Acme Corp"
    And Alice creates a conversation in "Acme Corp"
    When Alice tries to share the conversation with Charlie who is not in "Acme Corp"
    Then the response status is 400

  Scenario: Conversations without org are unaffected
    Given Alice has an access token
    When Alice creates a conversation without an organization
    Then the conversation has no organization
    And existing sharing rules apply
```

### Migration Test

```gherkin
Scenario: Existing conversations remain accessible after migration
  Given the database has pre-existing conversations without organization_id
  When the migration runs
  Then all existing conversations have null organization_id
  And all existing access controls still work
```

## Files to Create/Modify

| File | Change |
|------|--------|
| `memory-service/src/main/resources/db/schema.sql` | Add `organizations`, `organization_members` tables; add `organization_id` to `conversation_groups` |
| `memory-service/src/main/resources/db/changelog/` | **New**: Liquibase migration changeset |
| `memory-service/.../persistence/entity/OrganizationEntity.java` | **New**: Organization JPA entity |
| `memory-service/.../persistence/entity/OrganizationMemberEntity.java` | **New**: Org membership JPA entity |
| `memory-service/.../persistence/entity/ConversationGroupEntity.java` | Add `organizationId` field |
| `memory-service/.../persistence/repo/OrganizationRepository.java` | **New**: Organization CRUD |
| `memory-service/.../persistence/repo/OrganizationMemberRepository.java` | **New**: Membership CRUD |
| `memory-service/.../persistence/repo/ConversationMembershipRepository.java` | Extend access checks for org membership |
| `memory-service/.../api/OrganizationResource.java` | **New**: Organization REST endpoints |
| `memory-service/.../api/dto/CreateOrganizationRequest.java` | **New**: Request DTO |
| `memory-service/.../api/dto/AddOrganizationMemberRequest.java` | **New**: Request DTO |
| `memory-service/.../api/dto/OrganizationDto.java` | **New**: Response DTO |
| `memory-service/.../api/dto/OrganizationMemberDto.java` | **New**: Response DTO |
| `memory-service/.../api/dto/CreateConversationRequest.java` | Add optional `organizationId` field |
| `memory-service/.../api/ConversationResource.java` | Add `organizationId` query parameter to listing |
| `memory-service-contracts/.../openapi.yml` | Add organization endpoints and schemas |
| `memory-service/.../mongo/model/MongoOrganization.java` | **New**: MongoDB equivalent |
| `memory-service/.../mongo/model/MongoOrganizationMember.java` | **New**: MongoDB equivalent |
| `memory-service/.../mongo/repo/MongoOrganizationRepository.java` | **New**: MongoDB equivalent |
| `memory-service/.../test/.../features/organizations.feature` | **New**: Cucumber scenarios |

## Verification

```bash
# Compile
./mvnw compile

# Run all tests
./mvnw test -pl memory-service > test.log 2>&1

# Regenerate clients after OpenAPI changes
./mvnw generate-sources
```

## Design Decisions

1. **Organization is optional**: The `organization_id` is nullable. This preserves full backward compatibility — the feature is opt-in. Deployments that don't need multi-tenancy are unaffected.
2. **No teams in v1**: Teams (sub-groups within orgs) add complexity. The first version supports only flat org membership. Teams can be added later with a `teams` table and `team_members` join table.
3. **Org admins get MANAGER access, not OWNER**: Org admins can manage conversations (read, write, share) but cannot delete the conversation or transfer ownership. This prevents accidental data loss by admins.
4. **Sharing restricted to org members**: When a conversation is org-scoped, it can only be shared with members of that org. This enforces data isolation at the API level.
5. **Slug for URL-safe identification**: Organizations have both a display `name` and a unique `slug` for use in URLs and API references (e.g., `acme-corp`).
6. **No row-level security (RLS)**: PostgreSQL RLS could enforce tenant isolation at the database level, but adds complexity to the Hibernate/Panache integration. Application-level filtering is simpler and sufficient for the current scale. RLS can be considered as a hardening step later.

## Future Considerations

- **Teams**: Sub-groups within organizations for finer-grained access control.
- **Billing/quotas**: Per-org conversation limits, storage quotas, rate limiting.
- **SSO/directory sync**: Automatically sync org membership from an identity provider (e.g., SCIM).
- **Row-level security**: Database-level tenant isolation for defense-in-depth.
- **Cross-org sharing**: Allow specific conversations to be shared across org boundaries (with explicit opt-in).
