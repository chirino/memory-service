---
status: implemented
supersedes:
  - 013-soft-deletes.md
---

# Hard Delete for Conversation Memberships with Audit Logging

> **Status**: Implemented. Replaces membership soft deletes from
> [013](013-soft-deletes.md) with hard deletes and audit logging.

## Motivation

Enhancement 013 (Soft Deletes) introduced soft deletion for conversation memberships, marking them with a `deletedAt` timestamp instead of removing them. This was designed for audit trail retention. Enhancement 016 (Data Eviction) then added a mechanism to hard-delete these soft-deleted memberships after a configurable retention period.

Upon review, this two-phase approach (soft delete + deferred eviction) adds unnecessary complexity for memberships:

1. **Memberships are metadata, not content.** Unlike conversations and messages (which contain user data with compliance implications), memberships are access control records. The value of retaining deleted membership records is low compared to the operational overhead.

2. **Eviction adds operational burden.** Operators must run periodic eviction jobs to prevent unbounded growth of soft-deleted membership records.

3. **Query complexity.** Every membership query must filter by `deletedAt IS NULL`, adding overhead and potential for bugs.

4. **Audit logging is a better fit.** Rather than retaining the data itself, logging membership changes provides a complete audit trail without the storage overhead.

This enhancement changes conversation memberships to use **immediate hard deletes** while adding **comprehensive audit logging** for all membership operations. This simplifies the data model while preserving the audit trail through logs rather than data retention.

## Dependencies

- **Enhancement 014 (Admin Access)**: Uses the `AdminAuditLogger` pattern for membership audit logging.

## Design Decisions

### Hard Delete Instead of Soft Delete

Membership delete operations will immediately remove the record from the database instead of setting `deletedAt`:

| Aspect | Before (Soft Delete) | After (Hard Delete) |
|--------|---------------------|---------------------|
| DELETE operation | Sets `deletedAt = NOW()` | Removes row from database |
| Query filtering | `WHERE deletedAt IS NULL` | No filter needed |
| Data retention | Retained until eviction | Not retained |
| Audit trail | Query deleted records | Audit log entries |
| Eviction | Required to reclaim storage | Not applicable |

### Audit Logging for All Membership Operations

A new `MembershipAuditLogger` will log all operations that affect conversation membership. This mirrors the `AdminAuditLogger` pattern from enhancement 014 but applies to regular user operations, not just admin actions.

#### Operations to Log

| Operation | Trigger | Log Data |
|-----------|---------|----------|
| Add member | `shareConversation()` | Actor, conversation, target user, access level granted |
| Update member | `updateMembership()` | Actor, conversation, target user, old access level, new access level |
| Remove member | `deleteMembership()` | Actor, conversation, target user, access level at removal |
| Transfer ownership | `completeOwnershipTransfer()` | Actor, conversation, from user, to user |

#### Log Format

The logger uses a dedicated category for easy filtering and routing:

```
Logger: io.github.chirino.memory.membership.audit
```

Log entry format:
```
MEMBERSHIP_CHANGE actor=<userId> [client=<clientId>] action=<action> conversation=<conversationId> target=<targetUserId> [accessLevel=<level>] [fromLevel=<old>] [toLevel=<new>]
```

Examples:
```
INFO  [membership.audit] MEMBERSHIP_CHANGE actor=alice action=add conversation=conv-123 target=bob accessLevel=READER
INFO  [membership.audit] MEMBERSHIP_CHANGE actor=alice action=update conversation=conv-123 target=bob fromLevel=READER toLevel=WRITER
INFO  [membership.audit] MEMBERSHIP_CHANGE actor=alice action=remove conversation=conv-123 target=bob accessLevel=WRITER
INFO  [membership.audit] MEMBERSHIP_CHANGE actor=alice action=transfer_ownership conversation=conv-123 target=bob fromOwner=alice
```

#### Configuration

```properties
# Route membership audit logs to a separate file (optional)
quarkus.log.category."io.github.chirino.memory.membership.audit".level=INFO

# Example: route to a separate file handler
# quarkus.log.handler.file.membership-audit.path=logs/membership-audit.log
# quarkus.log.handler.file.membership-audit.categories=io.github.chirino.memory.membership.audit
```

### Remove Membership Eviction

Since memberships are now hard-deleted immediately, the membership eviction functionality becomes unnecessary:

1. Remove `conversation_memberships` from the valid `resourceTypes` in the eviction endpoint.
2. Remove `countEvictableMemberships()` and `hardDeleteMembershipsBatch()` from `MemoryStore` interface.
3. Remove corresponding implementations from `PostgresMemoryStore` and `MongoMemoryStore`.
4. Remove membership eviction from `EvictionService`.
5. Remove the `deleted_at` column from the `conversation_memberships` table.

### Database Schema Changes

Remove the `deleted_at` column and related indexes from the memberships table:

```sql
-- Remove soft delete column
ALTER TABLE conversation_memberships DROP COLUMN deleted_at;

-- Drop soft delete indexes (no longer needed)
DROP INDEX IF EXISTS idx_conversation_memberships_not_deleted;
DROP INDEX IF EXISTS idx_conversation_memberships_deleted;
```

For the updated schema:

```sql
CREATE TABLE IF NOT EXISTS conversation_memberships (
    conversation_group_id   UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    user_id                 TEXT NOT NULL,
    access_level            TEXT NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- deleted_at removed
    PRIMARY KEY (conversation_group_id, user_id)
);

-- Simple index on conversation group for listing members
CREATE INDEX IF NOT EXISTS idx_conversation_memberships_group
    ON conversation_memberships (conversation_group_id);
```

### API Behavior Changes

The external API behavior remains **unchanged**:

| Endpoint | Before | After |
|----------|--------|-------|
| `DELETE /v1/conversations/{id}/memberships/{userId}` | 204 No Content | 204 No Content |
| `GET /v1/conversations/{id}/memberships` | Returns active members | Returns all members (same - no soft deleted to filter) |
| `PUT /v1/conversations/{id}/memberships/{userId}` | Updates membership | Updates membership |

The only difference is internal: the record is permanently removed rather than marked deleted.

### Cascade Behavior on Conversation Delete

When a conversation group is soft-deleted (the whole conversation tree), the associated memberships should be **hard-deleted immediately** rather than soft-deleted:

```java
// Before: soft delete memberships with conversation
private void softDeleteConversationGroup(UUID conversationGroupId) {
    OffsetDateTime now = OffsetDateTime.now();
    // ... soft delete group and conversations ...

    // OLD: Soft delete memberships
    membershipRepository.update(
        "deletedAt = ?1 WHERE id.conversationGroupId = ?2 AND deletedAt IS NULL",
        now, conversationGroupId);
}

// After: hard delete memberships when conversation is deleted
private void softDeleteConversationGroup(UUID conversationGroupId) {
    OffsetDateTime now = OffsetDateTime.now();
    // ... soft delete group and conversations ...

    // NEW: Log all memberships being removed, then hard delete
    List<ConversationMembershipEntity> memberships =
        membershipRepository.listForConversationGroup(conversationGroupId);
    for (ConversationMembershipEntity m : memberships) {
        membershipAuditLogger.logRemove(
            "system", // or the userId performing the delete
            conversationGroupId.toString(),
            m.getId().getUserId(),
            m.getAccessLevel());
    }

    // Hard delete memberships
    membershipRepository.delete("id.conversationGroupId", conversationGroupId);
}
```

**Rationale:** Since the conversation group itself is being deleted, the membership records serve no purpose. Hard deleting them simplifies the data model and reduces the records that would need eviction later. The audit log captures the membership state at the time of deletion.

### Admin API Considerations

The admin APIs introduced in enhancement 014 include the ability to view soft-deleted resources. Since memberships are no longer soft-deleted:

- `GET /v1/admin/conversations/{id}/memberships` returns only active memberships (there are no soft-deleted ones).
- The `includeDeleted` and `onlyDeleted` query parameters have no effect on membership queries.
- Admin users can query the membership audit log for historical membership data.

### Migration Strategy

For existing deployments with soft-deleted membership records:

1. **Run final eviction** with `resourceTypes=["conversation_memberships"]` to clear any pending soft-deleted records.
2. **Apply schema migration** to drop the `deleted_at` column.
3. **Deploy new code** with hard delete behavior.

The migration is backward-compatible: the new code without `deleted_at` will work correctly whether or not old soft-deleted records exist (since they would have been evicted in step 1).

## Scope of Changes

### 1. Membership Audit Logger

**New file:** `memory-service/src/main/java/io/github/chirino/memory/security/MembershipAuditLogger.java`

```java
@ApplicationScoped
public class MembershipAuditLogger {

    private static final Logger AUDIT_LOG =
        Logger.getLogger("io.github.chirino.memory.membership.audit");

    /**
     * Log a membership addition.
     */
    public void logAdd(String actorUserId, String conversationId,
                       String targetUserId, AccessLevel accessLevel) {
        AUDIT_LOG.infof(
            "MEMBERSHIP_CHANGE actor=%s action=add conversation=%s target=%s accessLevel=%s",
            actorUserId, conversationId, targetUserId, accessLevel);
    }

    /**
     * Log a membership update.
     */
    public void logUpdate(String actorUserId, String conversationId,
                          String targetUserId, AccessLevel fromLevel, AccessLevel toLevel) {
        AUDIT_LOG.infof(
            "MEMBERSHIP_CHANGE actor=%s action=update conversation=%s target=%s fromLevel=%s toLevel=%s",
            actorUserId, conversationId, targetUserId, fromLevel, toLevel);
    }

    /**
     * Log a membership removal.
     */
    public void logRemove(String actorUserId, String conversationId,
                          String targetUserId, AccessLevel accessLevel) {
        AUDIT_LOG.infof(
            "MEMBERSHIP_CHANGE actor=%s action=remove conversation=%s target=%s accessLevel=%s",
            actorUserId, conversationId, targetUserId, accessLevel);
    }

    /**
     * Log an ownership transfer.
     */
    public void logOwnershipTransfer(String actorUserId, String conversationId,
                                     String fromOwner, String toOwner) {
        AUDIT_LOG.infof(
            "MEMBERSHIP_CHANGE actor=%s action=transfer_ownership conversation=%s target=%s fromOwner=%s",
            actorUserId, conversationId, toOwner, fromOwner);
    }
}
```

### 2. Database Schema

**File:** `memory-service/src/main/resources/db/schema.sql`

Update the `conversation_memberships` table definition:

```sql
CREATE TABLE IF NOT EXISTS conversation_memberships (
    conversation_group_id   UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    user_id                 TEXT NOT NULL,
    access_level            TEXT NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- deleted_at column removed
    PRIMARY KEY (conversation_group_id, user_id)
);

-- Remove soft-delete indexes, add simple group index
CREATE INDEX IF NOT EXISTS idx_conversation_memberships_group
    ON conversation_memberships (conversation_group_id);
```

### 3. JPA Entity

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/ConversationMembershipEntity.java`

Remove the `deletedAt` field and `isDeleted()` method:

```java
@Entity
@Table(name = "conversation_memberships")
public class ConversationMembershipEntity {

    @EmbeddedId
    private ConversationMembershipId id;

    @ManyToOne(fetch = FetchType.LAZY)
    @JoinColumn(name = "conversation_group_id", insertable = false, updatable = false)
    private ConversationGroupEntity conversationGroup;

    @Enumerated(EnumType.STRING)
    @Column(name = "access_level", nullable = false)
    private AccessLevel accessLevel;

    @Column(name = "created_at", nullable = false, updatable = false)
    private OffsetDateTime createdAt;

    // REMOVED: deletedAt field and isDeleted() method

    @PrePersist
    protected void onCreate() {
        if (createdAt == null) {
            createdAt = OffsetDateTime.now();
        }
    }

    // getters and setters (without deletedAt)
}
```

### 4. MongoDB Model

**File:** `memory-service/src/main/java/io/github/chirino/memory/mongo/model/MongoConversationMembership.java`

Remove the `deletedAt` field:

```java
public class MongoConversationMembership {
    public String id;  // format: "conversationGroupId:userId"
    public String conversationGroupId;
    public String userId;
    public AccessLevel accessLevel;
    public Instant createdAt;
    // REMOVED: deletedAt field
}
```

### 5. PostgreSQL Repository

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/repo/ConversationMembershipRepository.java`

Remove all `deletedAt IS NULL` filters from queries:

```java
@ApplicationScoped
public class ConversationMembershipRepository implements PanacheRepository<ConversationMembershipEntity> {

    // BEFORE: "FROM ConversationMembershipEntity WHERE id.conversationGroupId = ?1 AND deletedAt IS NULL"
    // AFTER:
    public List<ConversationMembershipEntity> listForConversationGroup(UUID conversationGroupId) {
        return list("id.conversationGroupId", conversationGroupId);
    }

    // BEFORE: "... AND deletedAt IS NULL"
    // AFTER:
    public List<ConversationMembershipEntity> listForUser(String userId, int limit) {
        return find("id.userId = ?1", Sort.by("createdAt").descending(), userId)
            .page(0, limit)
            .list();
    }

    // BEFORE: "... AND deletedAt IS NULL"
    // AFTER:
    public Optional<ConversationMembershipEntity> findMembership(UUID groupId, String userId) {
        return find("id.conversationGroupId = ?1 AND id.userId = ?2", groupId, userId)
            .firstResultOptional();
    }

    // Similar updates for all other query methods
}
```

### 6. MongoDB Repository

**File:** `memory-service/src/main/java/io/github/chirino/memory/mongo/repo/MongoConversationMembershipRepository.java`

Remove all `deletedAt == null` filters:

```java
// BEFORE: stream.filter(m -> m.deletedAt == null)
// AFTER: no filter needed
public List<MongoConversationMembership> listForConversationGroup(String conversationGroupId) {
    return find("conversationGroupId", conversationGroupId).list();
}
```

### 7. PostgresMemoryStore - Delete Membership

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`

Change from soft delete to hard delete with audit logging:

```java
@Inject
MembershipAuditLogger membershipAuditLogger;

@Override
@Transactional
public void deleteMembership(String userId, String conversationId, String memberUserId) {
    UUID cid = UUID.fromString(conversationId);
    UUID groupId = resolveGroupId(cid);
    ensureHasAccess(groupId, userId, AccessLevel.MANAGER);

    // Get the membership before deletion for audit logging
    Optional<ConversationMembershipEntity> membership =
        membershipRepository.findMembership(groupId, memberUserId);

    if (membership.isPresent()) {
        AccessLevel level = membership.get().getAccessLevel();

        // Hard delete the membership
        membershipRepository.delete("id.conversationGroupId = ?1 AND id.userId = ?2",
            groupId, memberUserId);

        // Audit log the removal
        membershipAuditLogger.logRemove(userId, conversationId, memberUserId, level);
    }

    // Delete any pending ownership transfer to the removed member
    ownershipTransferRepository.deleteByConversationGroupAndToUser(groupId, memberUserId);
}
```

### 8. PostgresMemoryStore - Share Conversation

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`

Add audit logging to membership creation:

```java
@Override
@Transactional
public void shareConversation(String userId, String conversationId,
                              String targetUserId, AccessLevel accessLevel) {
    UUID cid = UUID.fromString(conversationId);
    UUID groupId = resolveGroupId(cid);
    ensureHasAccess(groupId, userId, AccessLevel.MANAGER);

    // Prevent sharing with owner access level
    if (accessLevel == AccessLevel.OWNER) {
        throw new IllegalArgumentException("Cannot share with OWNER access level");
    }

    // Check if membership already exists
    Optional<ConversationMembershipEntity> existing =
        membershipRepository.findMembership(groupId, targetUserId);

    if (existing.isPresent()) {
        throw new ConflictException("User already has access to this conversation");
    }

    // Create the membership
    membershipRepository.createMembership(groupId, targetUserId, accessLevel);

    // Audit log the addition
    membershipAuditLogger.logAdd(userId, conversationId, targetUserId, accessLevel);
}
```

### 9. PostgresMemoryStore - Update Membership

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`

Add audit logging to membership updates:

```java
@Override
@Transactional
public void updateMembership(String userId, String conversationId,
                             String targetUserId, AccessLevel newAccessLevel) {
    UUID cid = UUID.fromString(conversationId);
    UUID groupId = resolveGroupId(cid);
    ensureHasAccess(groupId, userId, AccessLevel.MANAGER);

    // Get current membership
    ConversationMembershipEntity membership = membershipRepository
        .findMembership(groupId, targetUserId)
        .orElseThrow(() -> new NotFoundException("Membership not found"));

    AccessLevel oldLevel = membership.getAccessLevel();

    // Update the access level
    membership.setAccessLevel(newAccessLevel);
    membershipRepository.persist(membership);

    // Audit log the update
    membershipAuditLogger.logUpdate(userId, conversationId, targetUserId, oldLevel, newAccessLevel);
}
```

### 10. PostgresMemoryStore - Delete Conversation Group

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`

Log membership removals when conversation is deleted, then hard delete:

```java
private void softDeleteConversationGroup(UUID conversationGroupId, String actorUserId) {
    OffsetDateTime now = OffsetDateTime.now();

    // Log and hard delete memberships BEFORE soft-deleting the group
    List<ConversationMembershipEntity> memberships =
        membershipRepository.listForConversationGroup(conversationGroupId);
    for (ConversationMembershipEntity m : memberships) {
        membershipAuditLogger.logRemove(
            actorUserId,
            conversationGroupId.toString(),  // or get first conversation ID
            m.getId().getUserId(),
            m.getAccessLevel());
    }
    membershipRepository.delete("id.conversationGroupId", conversationGroupId);

    // Mark conversation group as deleted
    conversationGroupRepository.update(
        "deletedAt = ?1 WHERE id = ?2 AND deletedAt IS NULL",
        now, conversationGroupId);

    // Mark all conversations in the group as deleted
    conversationRepository.update(
        "deletedAt = ?1 WHERE conversationGroup.id = ?2 AND deletedAt IS NULL",
        now, conversationGroupId);

    // Expire pending ownership transfers
    ownershipTransferRepository.update(
        "status = ?1, updatedAt = ?2 WHERE conversationGroup.id = ?3 AND status = ?4",
        TransferStatus.EXPIRED, now, conversationGroupId, TransferStatus.PENDING);
}
```

### 11. MongoMemoryStore - Similar Changes

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java`

Apply the same changes as PostgresMemoryStore:
- Hard delete instead of soft delete for `deleteMembership()`
- Add audit logging for add, update, remove operations
- Hard delete memberships when conversation group is deleted

### 12. MemoryStore Interface - Remove Eviction Methods

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java`

Remove membership eviction methods:

```java
// REMOVE these methods:
// long countEvictableMemberships(OffsetDateTime cutoff);
// int hardDeleteMembershipsBatch(OffsetDateTime cutoff, int limit);
```

### 13. Eviction Service - Remove Membership Eviction

**File:** `memory-service/src/main/java/io/github/chirino/memory/service/EvictionService.java`

Remove membership eviction logic:

```java
// REMOVE the evictMemberships() method

// UPDATE the evict() method to remove membership handling:
public void evict(Duration retentionPeriod, Set<String> resourceTypes,
                  Consumer<Integer> progressCallback) {
    // Validate resource types - memberships no longer valid
    for (String type : resourceTypes) {
        if (!VALID_RESOURCE_TYPES.contains(type)) {
            throw new IllegalArgumentException("Invalid resource type: " + type);
        }
    }

    OffsetDateTime cutoff = OffsetDateTime.now().minus(retentionPeriod);

    // Only conversation_groups and memory_epochs are evictable now
    if (resourceTypes.contains("conversation_groups")) {
        evictConversationGroups(cutoff, ...);
    }
    if (resourceTypes.contains("memory_epochs")) {
        evictMemoryEpochs(cutoff, ...);
    }
    // REMOVED: conversation_memberships handling
}

private static final Set<String> VALID_RESOURCE_TYPES = Set.of(
    "conversation_groups",
    "memory_epochs"
    // REMOVED: "conversation_memberships"
);
```

### 14. OpenAPI Admin Spec - Update Eviction Endpoint

**File:** `memory-service-contracts/src/main/resources/openapi-admin.yml`

Update the `resourceTypes` enum to remove `conversation_memberships` and rename `conversation_groups` to `conversations` (since "conversation groups" is an internal implementation detail):

```yaml
EvictRequest:
  type: object
  required:
    - retentionPeriod
    - resourceTypes
  properties:
    retentionPeriod:
      type: string
      description: ISO 8601 duration
      example: "P90D"
    resourceTypes:
      type: array
      items:
        type: string
        enum:
          - conversations  # API-facing name (internally uses conversation_groups)
          - memory_epochs
          # REMOVED: conversation_memberships
```

### 15. Application Configuration

**File:** `memory-service/src/main/resources/application.properties`

Add membership audit log configuration:

```properties
# Membership audit logging
quarkus.log.category."io.github.chirino.memory.membership.audit".level=INFO
```

### 16. Cucumber Tests - Update Membership Tests

**File:** `memory-service/src/test/resources/features/memberships-rest.feature`

Update tests to reflect hard delete behavior:

```gherkin
Scenario: Delete membership permanently removes the record
    Given I have a conversation with title "Test Conversation"
    And the conversation is shared with user "bob" with access level "reader"
    When I delete membership for user "bob"
    Then the response status should be 204
    # Membership should not be in the list
    When I list memberships for the conversation
    Then the response should not contain a membership for user "bob"
    # Verify record is actually deleted (not soft deleted)
    When I execute SQL query:
    """
    SELECT COUNT(*) as count
    FROM conversation_memberships
    WHERE conversation_group_id = '${conversationGroupId}'
    AND user_id = 'bob'
    """
    Then the SQL result should match:
      | count |
      | 0     |

Scenario: Membership audit log captures deletion
    Given I have a conversation with title "Test Conversation"
    And the conversation is shared with user "bob" with access level "reader"
    When I delete membership for user "bob"
    Then the response status should be 204
    And the membership audit log should contain "action=remove"
    And the membership audit log should contain "target=bob"
    And the membership audit log should contain "accessLevel=READER"
```

### 17. Cucumber Tests - Add Audit Logging Tests

**New scenarios in:** `memory-service/src/test/resources/features/memberships-rest.feature`

```gherkin
Scenario: Adding a member is audit logged
    Given I have a conversation with title "Test Conversation"
    When I share the conversation with user "bob" with access level "writer"
    Then the response status should be 201
    And the membership audit log should contain "action=add"
    And the membership audit log should contain "target=bob"
    And the membership audit log should contain "accessLevel=WRITER"

Scenario: Updating a membership is audit logged
    Given I have a conversation with title "Test Conversation"
    And the conversation is shared with user "bob" with access level "reader"
    When I update membership for user "bob" to access level "writer"
    Then the response status should be 200
    And the membership audit log should contain "action=update"
    And the membership audit log should contain "target=bob"
    And the membership audit log should contain "fromLevel=READER"
    And the membership audit log should contain "toLevel=WRITER"

Scenario: Deleting a conversation logs membership removals
    Given I have a conversation with title "Test Conversation"
    And the conversation is shared with user "bob" with access level "reader"
    And the conversation is shared with user "charlie" with access level "writer"
    When I delete the conversation
    Then the response status should be 204
    And the membership audit log should contain 2 "action=remove" entries
```

### 18. Cucumber Step Definitions - Add Audit Log Verification

**File:** `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java`

Add steps for verifying membership audit logs:

```java
// Capture log output for verification
private List<String> capturedMembershipAuditLogs = new ArrayList<>();

@io.cucumber.java.en.Then("the membership audit log should contain {string}")
public void theMembershipAuditLogShouldContain(String expectedContent) {
    boolean found = capturedMembershipAuditLogs.stream()
        .anyMatch(log -> log.contains(expectedContent));
    assertThat("Membership audit log should contain: " + expectedContent, found, is(true));
}

@io.cucumber.java.en.Then("the membership audit log should contain {int} {string} entries")
public void theMembershipAuditLogShouldContainEntries(int count, String pattern) {
    long matchCount = capturedMembershipAuditLogs.stream()
        .filter(log -> log.contains(pattern))
        .count();
    assertThat("Membership audit log entry count for: " + pattern,
        matchCount, is((long) count));
}
```

## Implementation Order

1. **MembershipAuditLogger** - Create the new audit logger class
2. **Entity changes** - Remove `deletedAt` from JPA entity and MongoDB model
3. **Repository changes** - Remove `deletedAt` filters from all queries
4. **Store implementation** - Update PostgresMemoryStore:
   - Change `deleteMembership()` to hard delete with audit logging
   - Add audit logging to `shareConversation()`
   - Add audit logging to `updateMembership()`
   - Update `softDeleteConversationGroup()` to hard delete memberships
5. **Store implementation** - Apply same changes to MongoMemoryStore
6. **Remove eviction** - Remove membership eviction from EvictionService and MemoryStore
7. **OpenAPI update** - Remove `conversation_memberships` from eviction resource types
8. **Database schema** - Update schema.sql to remove `deleted_at` column
9. **Application config** - Add membership audit log configuration
10. **Cucumber tests** - Update existing tests, add audit log verification
11. **Compile and test**

## Verification

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# Run membership-specific tests
./mvnw test -Dcucumber.filter.tags="@memberships"

# Verify audit logs appear during test
grep "MEMBERSHIP_CHANGE" memory-service/target/quarkus.log
```

## Files to Modify (Complete List)

| File | Change Type |
|------|-------------|
| `memory-service/src/main/java/io/github/chirino/memory/security/MembershipAuditLogger.java` | New file |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/ConversationMembershipEntity.java` | Remove `deletedAt` |
| `memory-service/src/main/java/io/github/chirino/memory/mongo/model/MongoConversationMembership.java` | Remove `deletedAt` |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/repo/ConversationMembershipRepository.java` | Remove `deletedAt` filters |
| `memory-service/src/main/java/io/github/chirino/memory/mongo/repo/MongoConversationMembershipRepository.java` | Remove `deletedAt` filters |
| `memory-service/src/main/java/io/github/chirino/memory/store/MemoryStore.java` | Remove eviction methods |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java` | Hard delete + audit logging |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java` | Hard delete + audit logging |
| `memory-service/src/main/java/io/github/chirino/memory/service/EvictionService.java` | Remove membership eviction |
| `memory-service/src/main/resources/db/schema.sql` | Remove `deleted_at` column |
| `memory-service/src/main/resources/application.properties` | Add audit log config |
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Update eviction resourceTypes |
| `memory-service/src/test/resources/features/memberships-rest.feature` | Update tests |
| `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java` | Add audit log steps |

## Assumptions

1. **Audit logs provide sufficient audit trail.** The membership audit log captures all changes (add, update, remove) with full context, replacing the need for data retention.

2. **Log retention is handled externally.** The application does not manage log retention; operators route logs to external systems (ELK, Splunk, S3) with their own retention policies.

3. **Existing soft-deleted memberships are evicted before migration.** Deployments should run a final eviction before applying the schema change to ensure no orphaned records.

4. **Membership history is not queryable via API.** Unlike conversations (which can be restored via admin API), membership history is only available in logs. This is acceptable because membership changes are access control operations, not user content.

5. **The CASCADE delete on `conversation_groups` remains unchanged.** When a conversation group is hard-deleted (via eviction), any remaining memberships are automatically cascade-deleted by the database.

6. **Ownership transfers continue to expire independently.** The `ConversationOwnershipTransferEntity` still has its status field and is not affected by this change.

## Migration Notes

For existing deployments upgrading to this version:

### Pre-Migration Steps

1. **Run final membership eviction:**
   ```bash
   curl -X POST /v1/admin/evict \
     -H "Authorization: Bearer $ADMIN_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"retentionPeriod": "PT0S", "resourceTypes": ["conversation_memberships"]}'
   ```
   This hard-deletes all soft-deleted membership records (retention period of 0 seconds means all).

2. **Verify no soft-deleted memberships remain:**
   ```sql
   SELECT COUNT(*) FROM conversation_memberships WHERE deleted_at IS NOT NULL;
   -- Should return 0
   ```

### Schema Migration

Apply the schema migration to drop the `deleted_at` column:

```sql
-- Drop indexes first
DROP INDEX IF EXISTS idx_conversation_memberships_not_deleted;
DROP INDEX IF EXISTS idx_conversation_memberships_deleted;

-- Drop the column
ALTER TABLE conversation_memberships DROP COLUMN deleted_at;

-- Add new index
CREATE INDEX IF NOT EXISTS idx_conversation_memberships_group
    ON conversation_memberships (conversation_group_id);
```

### Post-Migration

Deploy the new application version. The code no longer references `deleted_at` and uses hard delete with audit logging.

## Future Considerations

- **Structured audit storage**: A future enhancement could store audit events in a queryable database table (in addition to logs) for administrative queries and compliance reporting.

- **Retention policies for audit logs**: Integration with external log management systems for configurable retention periods.

- **Audit log viewer in admin UI**: A dedicated interface for viewing and searching membership audit logs.
