---
status: partial
superseded-by:
  - implemented/028-membership-hard-delete.md
---

# Soft Deletes for Audit Retention

> **Status**: Partial. Membership archives replaced by hard deletes with
> audit logging in [028](../implemented/028-membership-hard-delete.md).
>
> **Current Contract Note**: Conversation and memory archive behavior is now defined by [094](../implemented/094-archive-operations.md). References below to conversation `DELETE`, admin `/restore`, `includeArchived` / `onlyArchived`, or archived resources being unreadable after archive are obsolete.

## Motivation

Currently, all delete operations in the memory-service perform hard deletes, permanently removing data from the database. This creates several problems:

1. **Audit Trail Loss**: When a conversation is deleted, all associated messages, memberships, and metadata are permanently destroyed, making it impossible to audit past interactions.

2. **Cascade Destruction**: Deleting a conversation group cascades to delete all conversations, messages, memberships, and ownership transfers. This is irreversible and prevents any future recovery or investigation.

3. **Compliance Risk**: Many regulatory frameworks (GDPR, HIPAA, SOX) require organizations to maintain audit trails of data access and modifications. Hard deletes make compliance difficult.

4. **Accidental Deletion**: Users who accidentally delete conversations have no recourse for recovery.

This enhancement introduces archives at the data layer, marking resources as deleted with a `archivedAt` timestamp instead of removing them. All API operations will treat archived resources as if they were actually deleted (returning 404, excluding from lists), while preserving the underlying data for auditing purposes.

**Scope**: This enhancement covers only the archive mechanism. A follow-up enhancement will implement a batch job for data retention policies that performs actual hard deletes of archived resources older than a configurable retention period.

## Design Decisions

### Scope of Soft Deletes

Soft deletes will apply to:

| Entity | Soft Delete | Rationale |
|--------|-------------|-----------|
| `ConversationGroupEntity` | Yes | Contains the full conversation tree; must be preserved for audit |
| `ConversationEntity` | Yes | Primary user-facing resource; audit trail essential |
| `MessageEntity` | No | Messages are immutable and only deleted via cascade from conversation; will be filtered by parent's `archivedAt` |
| `ConversationMembershipEntity` | Yes | Tracks who had access; important for security audits |
| `ConversationOwnershipTransferEntity` | No | Already has status tracking (PENDING, ACCEPTED, REJECTED, EXPIRED); historical records naturally preserved |

### Delete Behavior

When a conversation is deleted:

1. The `ConversationGroupEntity` is marked with `archivedAt = now()`
2. All `ConversationEntity` records in the group are marked with `archivedAt = now()`
3. All `ConversationMembershipEntity` records for the group are marked with `archivedAt = now()`
4. `MessageEntity` records are NOT modified (they inherit deleted status from their conversation)
5. Pending `ConversationOwnershipTransferEntity` records are updated to status `EXPIRED`

When a membership is deleted:

1. The `ConversationMembershipEntity` is marked with `archivedAt = now()`
2. No cascade to other entities

### Database Schema Changes

Add `archived_at` columns to the relevant tables:

```sql
-- Add archived_at to conversation_groups
ALTER TABLE conversation_groups ADD COLUMN archived_at TIMESTAMPTZ;
CREATE INDEX idx_conversation_groups_archived_at ON conversation_groups(archived_at) WHERE archived_at IS NULL;

-- Add archived_at to conversations
ALTER TABLE conversations ADD COLUMN archived_at TIMESTAMPTZ;
CREATE INDEX idx_conversations_archived_at ON conversations(archived_at) WHERE archived_at IS NULL;

-- Add archived_at to conversation_memberships
ALTER TABLE conversation_memberships ADD COLUMN archived_at TIMESTAMPTZ;
CREATE INDEX idx_conversation_memberships_archived_at ON conversation_memberships(archived_at) WHERE archived_at IS NULL;
```

The partial indexes (`WHERE archived_at IS NULL`) optimize queries that filter for non-deleted records, which is the common case.

**Note on `ON DELETE CASCADE`**: The existing CASCADE constraints are intentionally preserved. Since the application layer performs only archives, CASCADE never triggers during normal operations. When the future data retention purge job performs hard deletes of expired archived records, CASCADE will automatically clean up child records (messages, memberships, etc.), simplifying the purge implementation.

### Query Filtering

All read operations must filter out archived records:

```java
// Repository queries must include:
// AND (e.archivedAt IS NULL)

// Example for listing conversations:
@Query("SELECT c FROM ConversationEntity c WHERE c.conversationGroup.id = ?1 AND c.archivedAt IS NULL")
List<ConversationEntity> findActiveByGroupId(UUID groupId);
```

### API Response Codes

| Scenario | Current | After Soft Delete |
|----------|---------|-------------------|
| GET deleted resource | 404 Not Found | 404 Not Found (unchanged) |
| DELETE already-deleted resource | 404 Not Found | 404 Not Found (unchanged) |
| LIST with deleted resources | Not included | Not included (unchanged) |

The API contract remains unchanged from the client perspective. Soft deletes are an implementation detail.

### Vector Store Handling

The memory-service supports mixed storage configurations (e.g., PostgreSQL data store with MongoDB vector store). Since cross-store joins are not possible, the existing **two-phase query pattern** naturally handles archives:

**Current search flow:**
1. **Phase 1 (Data Store)**: Get user's memberships → Get accessible conversation IDs
2. **Phase 2 (Vector Store)**: Search within those conversation IDs

**With archives:**
1. **Phase 1**: Filter memberships and conversations where `archivedAt IS NULL`
2. **Phase 2**: Vector store receives only valid (non-deleted) conversation IDs - no changes needed

This means:
- **No vector store schema changes required**
- **No cross-store coordination needed**
- Vector embeddings are preserved (enables future restore functionality)
- Soft-deleted conversations are automatically excluded from search results because their IDs never reach the vector store query

### MongoDB Implementation

MongoDB documents will include an optional `archivedAt` field:

```javascript
{
  "_id": "...",
  "archivedAt": null,  // or ISODate("2024-01-15T10:30:00Z")
  // ... other fields
}

// All queries must include:
{ "archivedAt": null }
// or
{ "archivedAt": { "$exists": false } }
```

## Scope of Changes

### 1. Database Schema (`schema.sql`)

**File:** `memory-service/src/main/resources/db/schema.sql`

Add `archived_at` columns to relevant tables:

```sql
-- conversation_groups table
CREATE TABLE conversation_groups (
    id UUID PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at TIMESTAMPTZ  -- NEW
);

-- conversations table
CREATE TABLE conversations (
    -- ... existing columns ...
    archived_at TIMESTAMPTZ  -- NEW
);

-- conversation_memberships table
CREATE TABLE conversation_memberships (
    -- ... existing columns ...
    archived_at TIMESTAMPTZ  -- NEW
);
```

### 2. Liquibase Migration

**File:** `memory-service/src/main/resources/db/changelog/db.changelog-master.yaml`

Add new changeset:

```yaml
- changeSet:
    id: add-archive-columns
    author: memory-service
    changes:
      - addColumn:
          tableName: conversation_groups
          columns:
            - column:
                name: archived_at
                type: TIMESTAMPTZ
      - addColumn:
          tableName: conversations
          columns:
            - column:
                name: archived_at
                type: TIMESTAMPTZ
      - addColumn:
          tableName: conversation_memberships
          columns:
            - column:
                name: archived_at
                type: TIMESTAMPTZ
      - createIndex:
          indexName: idx_conversation_groups_not_deleted
          tableName: conversation_groups
          columns:
            - column:
                name: archived_at
          where: archived_at IS NULL
      - createIndex:
          indexName: idx_conversations_not_deleted
          tableName: conversations
          columns:
            - column:
                name: archived_at
          where: archived_at IS NULL
      - createIndex:
          indexName: idx_conversation_memberships_not_deleted
          tableName: conversation_memberships
          columns:
            - column:
                name: archived_at
          where: archived_at IS NULL
```

### 3. JPA Entity Classes

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/ConversationGroupEntity.java`

```java
// Add field
@Column(name = "archived_at")
private OffsetDateTime archivedAt;

// Add getter/setter
public OffsetDateTime getArchivedAt() { return archivedAt; }
public void setArchivedAt(OffsetDateTime archivedAt) { this.archivedAt = archivedAt; }

// Add helper method
public boolean isDeleted() { return archivedAt != null; }
```

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/ConversationEntity.java`

```java
// Add field
@Column(name = "archived_at")
private OffsetDateTime archivedAt;

// Add getter/setter and helper (same pattern)
```

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/ConversationMembershipEntity.java`

```java
// Add field
@Column(name = "archived_at")
private OffsetDateTime archivedAt;

// Add getter/setter and helper (same pattern)
```

### 4. Repository Classes

Update all repository queries to filter out deleted records.

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/repository/ConversationGroupRepository.java`

```java
// Update findById to exclude deleted
default Optional<ConversationGroupEntity> findActiveById(UUID id) {
    return find("id = ?1 AND archivedAt IS NULL", id).firstResultOptional();
}
```

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/repository/ConversationRepository.java`

```java
// Update all queries to include: AND archivedAt IS NULL
// AND conversationGroup.archivedAt IS NULL (to handle parent deletion)
```

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/repository/ConversationMembershipRepository.java`

```java
// Update queries to include: AND archivedAt IS NULL
```

**File:** `memory-service/src/main/java/io/github/chirino/memory/persistence/repository/MessageRepository.java`

```java
// Update queries to join conversation and filter: AND conversation.archivedAt IS NULL
```

### 5. PostgresMemoryStore Implementation

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java`

Replace hard delete with archive:

```java
// BEFORE
public void deleteConversation(String userId, String conversationId) {
    // ... validation ...
    deleteConversationGroupData(conversationGroupId);
}

private void deleteConversationGroupData(UUID conversationGroupId) {
    messageRepository.delete("conversationGroupId", conversationGroupId);
    conversationRepository.delete("conversationGroup.id", conversationGroupId);
    membershipRepository.delete("id.conversationGroupId", conversationGroupId);
    ownershipTransferRepository.delete("conversationGroup.id", conversationGroupId);
    conversationGroupRepository.delete("id", conversationGroupId);
}

// AFTER
public void deleteConversation(String userId, String conversationId) {
    // ... validation ...
    softDeleteConversationGroup(conversationGroupId);
}

private void softDeleteConversationGroup(UUID conversationGroupId) {
    OffsetDateTime now = OffsetDateTime.now();

    // Mark conversation group as deleted
    conversationGroupRepository.update(
        "archivedAt = ?1 WHERE id = ?2 AND archivedAt IS NULL",
        now, conversationGroupId);

    // Mark all conversations in the group as deleted
    conversationRepository.update(
        "archivedAt = ?1 WHERE conversationGroup.id = ?2 AND archivedAt IS NULL",
        now, conversationGroupId);

    // Mark all memberships as deleted
    membershipRepository.update(
        "archivedAt = ?1 WHERE id.conversationGroupId = ?2 AND archivedAt IS NULL",
        now, conversationGroupId);

    // Expire pending ownership transfers
    ownershipTransferRepository.update(
        "status = ?1, updatedAt = ?2 WHERE conversationGroup.id = ?3 AND status = ?4",
        TransferStatus.EXPIRED, now, conversationGroupId, TransferStatus.PENDING);
}

// BEFORE
public void deleteMembership(String userId, String conversationId, String memberUserId) {
    // ... validation ...
    membershipRepository.findMembership(groupId, memberUserId)
        .ifPresent(membershipRepository::delete);
}

// AFTER
public void deleteMembership(String userId, String conversationId, String memberUserId) {
    // ... validation ...
    membershipRepository.update(
        "archivedAt = ?1 WHERE id.conversationGroupId = ?2 AND id.userId = ?3 AND archivedAt IS NULL",
        OffsetDateTime.now(), groupId, memberUserId);
}
```

Update all query methods to filter deleted records:

```java
// Example: getConversation
public Conversation getConversation(String userId, String conversationId) {
    ConversationEntity entity = conversationRepository
        .find("id = ?1 AND archivedAt IS NULL AND conversationGroup.archivedAt IS NULL",
              UUID.fromString(conversationId))
        .firstResultOptional()
        .orElseThrow(() -> new NotFoundException("Conversation not found"));
    // ... rest of method
}
```

### 6. MongoDB Entity Classes

**File:** `memory-service/src/main/java/io/github/chirino/memory/mongo/model/MongoConversationGroup.java`

```java
// Add field
private Instant archivedAt;

// Add getter/setter
public Instant getArchivedAt() { return archivedAt; }
public void setArchivedAt(Instant archivedAt) { this.archivedAt = archivedAt; }
```

**File:** `memory-service/src/main/java/io/github/chirino/memory/mongo/model/MongoConversation.java`

```java
// Add field
private Instant archivedAt;
// Add getter/setter
```

**File:** `memory-service/src/main/java/io/github/chirino/memory/mongo/model/MongoConversationMembership.java`

```java
// Add field
private Instant archivedAt;
// Add getter/setter
```

### 7. MongoMemoryStore Implementation

**File:** `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java`

Replace hard delete with archive:

```java
// BEFORE
public void deleteConversation(String userId, String conversationId) {
    // ... validation ...
    deleteConversationGroupData(conversationGroupId);
}

// AFTER
public void deleteConversation(String userId, String conversationId) {
    // ... validation ...
    softDeleteConversationGroup(conversationGroupId);
}

private void softDeleteConversationGroup(String conversationGroupId) {
    Instant now = Instant.now();

    // Mark conversation group as deleted
    conversationGroupCollection.updateOne(
        Filters.and(Filters.eq("_id", conversationGroupId), Filters.eq("archivedAt", null)),
        Updates.set("archivedAt", now));

    // Mark all conversations as deleted
    conversationCollection.updateMany(
        Filters.and(Filters.eq("conversationGroupId", conversationGroupId), Filters.eq("archivedAt", null)),
        Updates.set("archivedAt", now));

    // Mark all memberships as deleted
    membershipCollection.updateMany(
        Filters.and(Filters.eq("conversationGroupId", conversationGroupId), Filters.eq("archivedAt", null)),
        Updates.set("archivedAt", now));

    // Expire pending ownership transfers
    transferCollection.updateMany(
        Filters.and(
            Filters.eq("conversationGroupId", conversationGroupId),
            Filters.eq("status", "PENDING")),
        Updates.combine(
            Updates.set("status", "EXPIRED"),
            Updates.set("updatedAt", now)));
}
```

Update all query methods to include `archivedAt: null` filter:

```java
// Example filter for all queries
Filters.and(
    existingFilters,
    Filters.or(
        Filters.eq("archivedAt", null),
        Filters.exists("archivedAt", false)
    )
)
```

### 8. Test Infrastructure: Reusable SQL Assertion Steps

To verify archive behavior at the database level, add reusable Cucumber steps for executing raw SQL queries and asserting results match expected tables. These steps will be useful beyond archives for any database-level verification.

**File:** `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java`

Add new step definitions:

```java
@Inject
EntityManager entityManager;

private List<Map<String, Object>> lastSqlResult;

@io.cucumber.java.en.When("I execute SQL query:")
public void iExecuteSqlQuery(String sql) {
    String renderedSql = renderTemplate(sql);

    if (entityManager == null) {
        throw new IllegalStateException("SQL steps only available with PostgreSQL profile");
    }

    @SuppressWarnings("unchecked")
    List<Object[]> rows = entityManager.createNativeQuery(renderedSql).getResultList();

    // Get column names from the query (requires parsing or using metadata)
    // For simplicity, we'll require the step to specify columns in the assertion
    lastSqlResult = new ArrayList<>();
    for (Object[] row : rows) {
        Map<String, Object> rowMap = new LinkedHashMap<>();
        for (int i = 0; i < row.length; i++) {
            rowMap.put("col" + i, row[i]);
        }
        lastSqlResult.add(rowMap);
    }
}

@io.cucumber.java.en.Then("the SQL result should have {int} row(s)")
public void theSqlResultShouldHaveRows(int expectedCount) {
    assertThat("SQL result row count", lastSqlResult.size(), is(expectedCount));
}

@io.cucumber.java.en.Then("the SQL result should match:")
public void theSqlResultShouldMatch(io.cucumber.datatable.DataTable dataTable) {
    List<Map<String, String>> expected = dataTable.asMaps();
    assertThat("SQL result row count", lastSqlResult.size(), is(expected.size()));

    for (int i = 0; i < expected.size(); i++) {
        Map<String, String> expectedRow = expected.get(i);
        Map<String, Object> actualRow = lastSqlResult.get(i);

        for (Map.Entry<String, String> entry : expectedRow.entrySet()) {
            String column = entry.getKey();
            String expectedValue = renderTemplate(entry.getValue());
            Object actualValue = actualRow.get(column);

            if ("*".equals(expectedValue)) {
                // Wildcard: just check column exists and is not null
                assertThat("Column " + column + " should exist and be non-null",
                    actualValue, notNullValue());
            } else if ("NULL".equals(expectedValue)) {
                assertThat("Column " + column + " should be null",
                    actualValue, nullValue());
            } else {
                assertThat("Column " + column + " in row " + i,
                    String.valueOf(actualValue), is(expectedValue));
            }
        }
    }
}

@io.cucumber.java.en.Then("the SQL result column {string} should be non-null")
public void theSqlResultColumnShouldBeNonNull(String column) {
    assertThat("SQL result should have at least one row", lastSqlResult.size(), greaterThan(0));
    for (Map<String, Object> row : lastSqlResult) {
        assertThat("Column " + column + " should be non-null", row.get(column), notNullValue());
    }
}

@io.cucumber.java.en.Then("the SQL result column {string} should be null")
public void theSqlResultColumnShouldBeNull(String column) {
    assertThat("SQL result should have at least one row", lastSqlResult.size(), greaterThan(0));
    for (Map<String, Object> row : lastSqlResult) {
        assertThat("Column " + column + " should be null", row.get(column), nullValue());
    }
}
```

### 9. Test Updates

**File:** `memory-service/src/test/resources/features/conversations-rest.feature`

Update delete scenario to verify archive behavior using the new SQL assertion steps:

```gherkin
Scenario: Delete a conversation performs archive
    Given I have a conversation with title "To Be Deleted"
    When I delete the conversation
    Then the response status should be 204
    # API should treat it as deleted
    When I get the conversation
    Then the response status should be 404
    # But data should still exist in database with archived_at set
    When I execute SQL query:
    """
    SELECT id, archived_at FROM conversations WHERE id = '${conversationId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "archived_at" should be non-null

Scenario: Deleted conversation excluded from list
    Given I have a conversation with title "Active Conversation"
    And set "activeConversationId" to "${conversationId}"
    And I have a conversation with title "To Be Deleted"
    And set "deletedConversationId" to "${conversationId}"
    When I delete the conversation
    And I list conversations
    Then the response status should be 200
    And the response should contain 1 conversation
    # Verify both still exist in database
    When I execute SQL query:
    """
    SELECT id, title, archived_at FROM conversations ORDER BY created_at
    """
    Then the SQL result should have 2 rows

Scenario: Soft delete cascades to conversation group and memberships
    Given I have a conversation with title "Test Conversation"
    And set "groupId" to "${response.body.conversationGroupId}"
    When I delete the conversation
    Then the response status should be 204
    # Verify conversation group is archived
    When I execute SQL query:
    """
    SELECT id, archived_at FROM conversation_groups WHERE id = '${groupId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "archived_at" should be non-null
    # Verify membership is archived
    When I execute SQL query:
    """
    SELECT conversation_group_id, user_id, archived_at
    FROM conversation_memberships
    WHERE conversation_group_id = '${groupId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "archived_at" should be non-null
    # Verify messages still exist (not archived, just orphaned by parent)
    When I execute SQL query:
    """
    SELECT COUNT(*) as count FROM messages WHERE conversation_group_id = '${groupId}'
    """
    Then the SQL result should match:
      | count |
      | 0     |
```

**File:** `memory-service/src/test/resources/features/conversations-grpc.feature`

Add similar scenarios for gRPC API.

**File:** `memory-service/src/test/resources/features/memberships-rest.feature`

```gherkin
Scenario: Delete membership performs archive
    Given I have a conversation with title "Test Conversation"
    And the conversation is shared with user "bob" with access level "reader"
    When I delete membership for user "bob"
    Then the response status should be 204
    # API should treat membership as deleted
    When I list memberships for the conversation
    Then the response should not contain a membership for user "bob"
    # But record should still exist in database
    When I execute SQL query:
    """
    SELECT user_id, archived_at
    FROM conversation_memberships
    WHERE conversation_group_id = '${response.body.conversationGroupId}'
    AND user_id = 'bob'
    """
    Then the SQL result should have 1 row
    And the SQL result column "archived_at" should be non-null
```

## Implementation Order

1. **Database schema** - Add migration for `archived_at` columns
2. **Entity classes** - Add `archivedAt` field to JPA and MongoDB entities
3. **Repository layer** - Update all queries to filter `archivedAt IS NULL`
4. **PostgresMemoryStore** - Replace hard deletes with archives
5. **MongoMemoryStore** - Replace hard deletes with archives
6. **Test infrastructure** - Add SQL assertion Cucumber steps
7. **Tests** - Update Cucumber features and add archive verification
8. **Compile and test** - Verify everything works

Note: No vector store changes are required - the two-phase query pattern automatically excludes archived conversations.

## Verification

After implementation:

```bash
# Compile all modules
./mvnw compile

# Run tests
./mvnw test

# Verify specific archive behavior
./mvnw test -Dcucumber.filter.tags="@archive"
```

## Files to Modify (Complete List)

| File | Change Type |
|------|-------------|
| `memory-service/src/main/resources/db/schema.sql` | Add columns |
| `memory-service/src/main/resources/db/changelog/db.changelog-master.yaml` | Add migration |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/ConversationGroupEntity.java` | Add field |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/ConversationEntity.java` | Add field |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/entity/ConversationMembershipEntity.java` | Add field |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/repository/ConversationGroupRepository.java` | Update queries |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/repository/ConversationRepository.java` | Update queries |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/repository/ConversationMembershipRepository.java` | Update queries |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/repository/MessageRepository.java` | Update queries |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java` | Soft delete logic |
| `memory-service/src/main/java/io/github/chirino/memory/mongo/model/MongoConversationGroup.java` | Add field |
| `memory-service/src/main/java/io/github/chirino/memory/mongo/model/MongoConversation.java` | Add field |
| `memory-service/src/main/java/io/github/chirino/memory/mongo/model/MongoConversationMembership.java` | Add field |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java` | Soft delete logic |
| `memory-service/src/test/java/io/github/chirino/memory/cucumber/StepDefinitions.java` | Add SQL assertion steps |
| `memory-service/src/test/resources/features/conversations-rest.feature` | Update tests |
| `memory-service/src/test/resources/features/conversations-grpc.feature` | Update tests |
| `memory-service/src/test/resources/features/memberships-rest.feature` | Update tests |

## Future Considerations

### Admin API (`/v1/admin/*`) - Out of Scope

Admin APIs are **out of scope** for this enhancement but will be implemented in a future enhancement. When implemented, the `/v1/admin/*` endpoints will provide:

1. **Cross-user access**: Admins can view conversations owned by any user (not just their own)
2. **Soft-deleted resource visibility**: Deleted resources will be included in responses
3. **Hidden field exposure**: Additional fields not shown in user-facing APIs will be exposed, including:
   - `archivedAt` - timestamp when the resource was archived
   - `deletedBy` - (if tracked) the user who performed the deletion
   - Other internal/audit fields as needed

**API Structure:**

The `/v1/admin/*` APIs will mirror the existing `/v1/*` user-facing APIs:

| User API | Admin API |
|----------|-----------|
| `GET /v1/conversations` | `GET /v1/admin/conversations` |
| `GET /v1/conversations/{id}` | `GET /v1/admin/conversations/{id}` |
| `DELETE /v1/conversations/{id}` | `DELETE /v1/admin/conversations/{id}` |
| `GET /v1/conversations/{id}/memberships` | `GET /v1/admin/conversations/{id}/memberships` |
| `GET /v1/conversations/{id}/messages` | `GET /v1/admin/conversations/{id}/messages` |
| ... | ... |

**Additional query parameters for admin APIs:**

```yaml
# Filter by deletion status
?includeArchived=true    # Include archived resources (default: false)
?onlyArchived=true       # Show only archived resources

# Filter by deletion time range (for audit queries)
?archivedAfter=2024-01-01T00:00:00Z
?archivedBefore=2024-02-01T00:00:00Z

# Filter by user (admins can view any user's data)
?userId=alice
```

**Response schema differences from user APIs:**

```json
{
  "id": "...",
  "title": "...",
  "ownerUserId": "alice",
  "createdAt": "2024-01-15T10:00:00Z",
  "updatedAt": "2024-01-15T12:00:00Z",
  "archivedAt": "2024-01-20T08:30:00Z",  // Hidden from user APIs
  "conversationGroupId": "..."
}
```

### Hard Delete / Purge (Planned Follow-up)

A separate enhancement will implement a batch job for data retention policies that performs actual hard deletes of archived resources after a configurable retention period. This addresses:

- **GDPR "right to be forgotten"**: Legal requirement to permanently delete personal data upon request
- **Storage management**: Prevent unbounded growth of archived records
- **Compliance**: Meet organizational data retention policy requirements

The retention policy batch job is explicitly **out of scope** for this enhancement but is a planned follow-up. This enhancement only implements the archive mechanism; the purge mechanism will be added separately.

```java
// Planned for future enhancement:
// Scheduled job to purge records where archivedAt < (now - retentionPeriod)
@Scheduled(cron = "${data.retention.purge.cron:0 0 2 * * ?}")  // Default: 2 AM daily
void purgeExpiredArchivedRecords() {
    OffsetDateTime cutoff = OffsetDateTime.now().minus(retentionPeriod);
    // Hard delete: conversation_groups WHERE archived_at < cutoff
    // Cascade will remove conversations, messages, memberships, etc.
}
```

### Restore Operation

A future enhancement could add restore functionality:

```yaml
# Potential future endpoint
POST /v1/admin/conversations/{id}/restore
```

## Notes

- No backward compatibility concerns per CLAUDE.md - this is pre-release development
- The API contract (OpenAPI spec) does not need changes; archives are transparent to clients
- Existing data remains unaffected; `archivedAt` will be NULL for all existing records
- Performance impact is minimal due to partial indexes on `archived_at IS NULL`
