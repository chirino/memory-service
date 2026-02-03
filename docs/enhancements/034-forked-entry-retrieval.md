# Forked Entry Retrieval

## Problem Statement

When fetching entries for a forked conversation, the current implementation only returns entries that belong to the specific conversation being queried. It does not include entries from parent conversations up to the fork point.

### Current Behavior

Given this conversation structure:
```
Root Conversation (508e46da-750b-4992-98b7-16f91478056e)
├── Entry A (HISTORY) User
├── Entry B (MEMORY)
├── Entry C (MEMORY)
├── Entry D (HISTORY) Agent ──> Forked Conversation (405430dc-4eb6-4cd8-94e6-c2ec6d4c426d) (forkedAt: D)
├── Entry E (HISTORY) User      ├── Entry I (MEMORY)
├── Entry F (MEMORY)            ├── Entry J (HISTORY)
├── Entry G (MEMORY)            ├── Entry K (HISTORY)
├── Entry H (HISTORY) Agent     └── Entry L (MEMORY)
```

**Expected behavior**: Querying entries for the forked conversation (`405430dc...`) should return:
- For `channel=null` (all channels): A, B, C, I, J, K, L (D is the fork point, NOT included)
- For `channel=HISTORY`: A, J, K
- For `channel=MEMORY`: B, C, I, L

**Important**: "Fork at entry D" means "branch before D" — the fork includes all parent entries up to but NOT including D. The `forkedAtEntryId` stored on the fork is the entry immediately before D (entry C in this case), and entries up to and including `forkedAtEntryId` are visible to the fork.

**Actual behavior**: Only entries from the forked conversation are returned (I, J, K, L or subset based on channel filter).

### Fork at Beginning (forkedAt: null)

A fork can also occur at the very beginning of a conversation, before any entries. In this case, `forkedAtEntryId` is `null`, meaning no entries from the parent should be included:

```
Root Conversation (508e46da-750b-4992-98b7-16f91478056e)
│                               ├──> Forked Conversation (405430dc-4eb6-4cd8-94e6-c2ec6d4c426d) (forkedAt: null)
├── Entry A (HISTORY) User      ├── Entry E (MEMORY)
├── Entry B (MEMORY)            ├── Entry F (HISTORY)
├── Entry C (MEMORY)            ├── Entry G (HISTORY)
├── Entry D (HISTORY) Agent     └── Entry H (MEMORY)
```

**Expected behavior**: Querying entries for the forked conversation should return only its own entries:
- For `channel=null`: E, F, G, H
- For `channel=HISTORY`: F, G
- For `channel=MEMORY`: E, H

This is essentially a "blank slate" fork that shares the same conversation group (for access control) but starts fresh without inheriting any conversation history.

### Root Cause

The entry fetching code in `EntryRepository` queries only by `m.conversation.id = ?1`, which filters to entries belonging to the specific conversation:

```java
// EntryRepository.listByChannel()
String baseQuery =
    "from EntryEntity m where m.conversation.id = ?1 and m.conversation.deletedAt IS"
            + " NULL and m.conversation.conversationGroup.deletedAt IS NULL";
```

The fork metadata is stored on `ConversationEntity`:
- `forkedAtConversationId` - the parent conversation ID
- `forkedAtEntryId` - the entry where the fork happened

But this metadata is not used when fetching entries.

### Impact

- Chat UIs don't show conversation history before the fork point
- Agents lose context from parent conversations
- The fork feature appears broken from a user perspective

### Cross-Database Applicability

**MongoDB has the same problem.** The `MongoMemoryStore` and `MongoEntryRepository` use identical query patterns:

```java
// MongoEntryRepository.listByChannel()
return find("conversationId = ?1", conversationId)
    .page(0, limit)
    .list();
```

The same solution approach applies to both PostgreSQL and MongoDB:
- Build ancestry stack using `forkedAtConversationId` / `forkedAtEntryId`
- Query entries by `conversationGroupId` instead of `conversationId`
- Filter entries in-memory based on the ancestry chain

The implementation will need to be updated in both:
- `PostgresMemoryStore` / `EntryRepository` (PostgreSQL)
- `MongoMemoryStore` / `MongoEntryRepository` (MongoDB)

## Proposed Solution

### Algorithm Overview

When fetching entries for a conversation that is a fork, we need to include entries from the entire parent chain. The algorithm works as follows:

1. **Build the fork ancestry stack**: Starting from the requested conversation, walk up the parent chain to build a stack of `(conversation_id, forked_at_entry_id)` pairs. The root conversation (or earliest ancestor) is pushed last, so it will be popped first.

2. **Query all entries in the conversation group**: Fetch entries ordered by `created_at` for the entire `conversation_group_id`.

3. **Filter entries based on ancestry**: Iterate through entries, adding them to the result based on which conversation they belong to and whether they are before the fork point.

### Pseudocode

```
function getEntriesForConversation(conversationId, channel, limit):
    conversation = findConversation(conversationId)
    groupId = conversation.conversationGroupId

    # Single query: load all conversations in the group
    allConversations = queryConversationsByGroup(groupId)
    conversationsById = Map(c.id -> c for c in allConversations)

    # Build ancestry stack by traversing in-memory (root will be first after reverse)
    stack = []
    current = conversation
    while current != null:
        stack.push((current.id, current.forkedAtEntryId))  # null forkedAtEntryId for root
        parentId = current.forkedAtConversationId
        current = conversationsById.get(parentId) if parentId != null else null
    stack.reverse()  # Root first: [(root, null), (child, forkPoint1), ..., (target, forkPointN)]

    # Query all entries in the conversation group, ordered by created_at
    dataSource = queryEntriesByConversationGroup(groupId, channel, orderBy: created_at)

    result = []
    ancestorIndex = 0
    (currentId, forkAtEntryId) = stack[ancestorIndex]

    for row in dataSource:
        # Are we still processing ancestors?
        if ancestorIndex < stack.length - 1:  # Not yet at target conversation
            if row.conversationId == currentId:
                result.add(row)
                if forkAtEntryId != null and row.id == forkAtEntryId:
                    # Reached fork point, move to next in ancestry chain
                    ancestorIndex++
                    (currentId, forkAtEntryId) = stack[ancestorIndex]

        # Process target conversation entries (last in stack)
        elif row.conversationId == conversationId:
            result.add(row)

        if result.size() >= limit:
            break

    return result
```

**Key points**:
- Single query loads all conversations in the group
- In-memory traversal builds the ancestry stack
- Root conversation has `forkedAtEntryId = null`, meaning include all its entries (no fork point limit)
- Stack is reversed so root is processed first
- Target conversation (last in stack) has all its entries included

### Handling Pagination

Pagination with fork ancestry is more complex because:
- The `afterEntryId` cursor might refer to an entry in a parent conversation
- We need to skip entries until we reach the cursor position
- The limit applies to the total count across all ancestors

```
function getEntriesForConversationPaginated(conversationId, afterEntryId, limit, channel):
    # ... build ancestry stack as before ...

    dataSource = queryEntriesByConversationGroup(...)

    result = []
    skipping = (afterEntryId != null)  # Skip until we find the cursor entry

    # ... iterate through parents and original conversation ...
    for row in filteredEntries:
        if skipping:
            if row.id == afterEntryId:
                skipping = false  # Found cursor, start collecting from next entry
            continue

        result.add(row)
        if result.size() >= limit:
            break

    return result
```

### Memory Channel Considerations

For MEMORY channel entries with epoch filtering:
- The `latest` epoch mode needs special handling since each conversation in the fork chain might have different epochs
- The epoch is scoped per `(conversation_id, client_id)`, not per conversation_group
- We may need to query epochs for each conversation in the ancestry chain

**Example**: Epochs are scoped per `(conversation_id, client_id)` and can diverge at fork points:

```
Root Conversation (508e46da-750b-4992-98b7-16f91478056e)
├── Entry A (HISTORY) User
├── Entry B (MEMORY, epoch=1)
├── Entry C (HISTORY) Agent ──> Forked Conversation (405430dc-...) (forkedAt: C)
├── Entry D (HISTORY)           ├── Entry H (HISTORY)
├── Entry E (MEMORY, epoch=1)   ├── Entry I (MEMORY, epoch=1)
├── Entry F (MEMORY, epoch=1)   ├── Entry J (MEMORY, epoch=2)
└── Entry G (HISTORY)           └── Entry K (HISTORY)
```

**Key concept**: Epochs with the same number can diverge at fork points. Epoch=1 in one `conversationId` is different from epoch=1 in another `conversationId`. They are completely separate epoch sequences. Furthermore, epoch=2 in the root and epoch=2 in the fork are totally different since they share no common fork point—the fork happened when only epoch=1 existed.

In this example:
- **Root conversation** (`508e46da-...`) has MEMORY entries B, E, F all at epoch=1. The next epoch would be **epoch=2**.
- **Forked conversation** (`405430dc-...`) inherits B from the parent (at parent's epoch=1) and starts its own epoch sequence: I at epoch=1, J at epoch=2. The next epoch would be **epoch=3**.
- Note: The fork's epoch=1 (entry I) is a *different* epoch=1 than the root's epoch=1 (entries B, E, F). They diverged at the fork point.

**Query results for MEMORY channel with `latest` epoch mode:**

| Conversation Queried | Result | Explanation |
|---------------------|--------|-------------|
| `508e46da` (root) | B, E, F | All at root's latest epoch (1) |
| `405430dc` (fork) | J | Fork's epoch=2 supersedes all previous epochs |
| `405430dc` (fork, if J didn't exist) | B, I | Both at epoch=1 (from their respective conversations) |

**Algorithm**: When iterating through entries, track the maximum epoch seen. When a MEMORY entry has `epoch > maxEpochSeen`, clear the result list and start fresh with the new epoch. This ensures only the latest "memory refresh" is returned.

```
maxEpochSeen = 0
result = []

for entry in entries (following fork ancestry path):
    if entry.channel == MEMORY and entry.clientId == requestedClientId:
        if entry.epoch > maxEpochSeen:
            result.clear()  // New epoch supersedes all previous
            maxEpochSeen = entry.epoch
        if entry.epoch == maxEpochSeen:
            result.add(entry)
    // Note: Don't filter by clientId in the query - all entries needed for fork point tracking
```

This means a fork can have multiple "memory updates" (new epochs) without affecting the parent conversation's epoch state, and vice versa.

### The `allForks` Option

A new `allForks` boolean parameter (default: `false`) will be added to both user-facing and admin entry retrieval APIs (REST and gRPC).

**When `allForks=false` (default)**:
- Follow the fork ancestry path as described above
- Return entries from the target conversation and its ancestors up to fork points

**When `allForks=true`**:
- Bypass fork tree traversal entirely
- Return all entries in the conversation group (all forks, all branches)
- Useful for debugging, admin views, or getting a complete picture of all activity

**API Changes**:

REST:
```
GET /conversations/{id}/entries?allForks=true
GET /admin/conversations/{id}/entries?allForks=true
```

gRPC:
```protobuf
message GetEntriesRequest {
    string conversation_id = 1;
    // ... existing fields ...
    bool all_forks = 10;  // New field
}
```

## Implementation Plan

### Phase 1: Core Entry Retrieval

1. Add `buildAncestryStack()` method to `PostgresMemoryStore`. To avoid N queries for N ancestors, we load all conversations in the group with a single query, then build the stack in-memory:

   ```java
   record ForkAncestor(UUID conversationId, UUID forkedAtEntryId) {}

   private List<ForkAncestor> buildAncestryStack(ConversationEntity targetConversation) {
       UUID groupId = targetConversation.getConversationGroup().getId();

       // Single query: get all conversations in the group
       List<ConversationEntity> allConversations = conversationRepository
           .find("conversationGroup.id = ?1 and deletedAt is null", groupId)
           .list();

       // Build lookup map
       Map<UUID, ConversationEntity> byId = allConversations.stream()
           .collect(Collectors.toMap(ConversationEntity::getId, c -> c));

       // Build ancestry stack by traversing in-memory
       List<ForkAncestor> stack = new ArrayList<>();
       ConversationEntity current = targetConversation;

       while (current != null) {
           stack.add(new ForkAncestor(
               current.getId(),
               current.getForkedAtEntryId()  // null for root = include all entries
           ));

           UUID parentId = current.getForkedAtConversationId();
           current = (parentId != null) ? byId.get(parentId) : null;
       }

       Collections.reverse(stack);  // Root first
       return stack;
   }
   ```

   **Benefits**:
   - Single DB round-trip regardless of fork depth
   - Simple Java code, no recursive CTE complexity
   - Conversations are likely already needed for access control checks

   **Trade-off**:
   - Loads all conversations in the group, not just ancestors
   - For groups with many sibling forks, this fetches more than needed
   - For most use cases (groups with <100 conversations), this is acceptable

2. Add new repository method to query by conversation_group_id:
   ```java
   // EntryRepository
   List<EntryEntity> listByConversationGroup(
       UUID conversationGroupId,
       Channel channel,
       String clientId
   );
   ```

3. Consolidate user-facing and admin entry retrieval into a shared implementation:

   Currently, `getEntries()` (user API) and `adminGetEntries()` (admin API) have separate implementations that both suffer from the same fork bug. They should share the core fork-aware retrieval logic:

   ```java
   // Shared internal method with fork support
   private PagedEntries getEntriesWithForkSupport(
       ConversationEntity conversation,
       String afterEntryId,
       int limit,
       Channel channel,
       MemoryEpochFilter epochFilter,  // null for admin API
       String clientId                  // null for admin API
   ) {
       List<ForkAncestor> ancestry = buildAncestryStack(conversation);
       // ... shared fork-aware retrieval logic ...
   }

   // User API - with access control and epoch support
   @Override
   public PagedEntries getEntries(String userId, String conversationId, ...) {
       ConversationEntity conversation = findConversation(conversationId);
       ensureHasAccess(conversation.getConversationGroup().getId(), userId, AccessLevel.READER);
       return getEntriesWithForkSupport(conversation, afterEntryId, limit, channel, epochFilter, clientId);
   }

   // Admin API - no access control, simpler params
   @Override
   public PagedEntries adminGetEntries(String conversationId, AdminMessageQuery query) {
       ConversationEntity conversation = findConversation(conversationId);
       return getEntriesWithForkSupport(conversation, query.getAfterEntryId(), query.getLimit(),
           query.getChannel(), null, null);
   }
   ```

   | Aspect | User API | Admin API |
   |--------|----------|-----------|
   | Access control | `ensureHasAccess()` | None (admin privilege) |
   | Epoch filter | Yes (for MEMORY) | No |
   | Client ID filter | Yes (for MEMORY) | No |
   | **Fork support** | **Shared implementation** | **Shared implementation** |

### Phase 2: Pagination Support

1. Update pagination logic to handle cursors that reference parent entries
2. Add integration tests for edge cases:
   - Cursor in parent conversation
   - Cursor at fork point
   - Cursor in forked conversation

### Phase 3: Memory Channel Epoch Handling

Update the entry filtering logic to handle epochs correctly when traversing fork ancestry.

**Algorithm**:

```java
private List<EntryEntity> filterMemoryEntriesWithEpoch(
    List<EntryEntity> allEntries,
    List<ForkAncestor> ancestryStack,
    String clientId,
    MemoryEpochFilter epochFilter
) {
    if (epochFilter != MemoryEpochFilter.LATEST) {
        // For explicit epoch or ALL, use existing logic
        return filterByExplicitEpoch(allEntries, ancestryStack, clientId, epochFilter);
    }

    // LATEST epoch mode: track max epoch seen, clear on new epoch
    List<EntryEntity> result = new ArrayList<>();
    int maxEpochSeen = 0;
    int ancestorIndex = 0;
    ForkAncestor currentAncestor = ancestryStack.get(0);

    for (EntryEntity entry : allEntries) {
        // Fork point tracking (same as Phase 1, uses ALL entries regardless of clientId)
        if (ancestorIndex < ancestryStack.size() - 1) {
            if (!entry.getConversationId().equals(currentAncestor.conversationId())) {
                continue;  // Not in current ancestor, skip
            }
            if (currentAncestor.forkPointEntryId() != null
                && entry.getId().equals(currentAncestor.forkPointEntryId())) {
                ancestorIndex++;
                currentAncestor = ancestryStack.get(ancestorIndex);
            }
        } else if (!entry.getConversationId().equals(currentAncestor.conversationId())) {
            continue;  // Not in target conversation, skip
        }

        // Only add MEMORY entries with matching clientId to result
        if (entry.getChannel() == Channel.MEMORY) {
            if (!clientId.equals(entry.getClientId())) {
                continue;  // Different clientId, skip for result but entry was used for fork tracking
            }

            if (entry.getEpoch() > maxEpochSeen) {
                result.clear();  // New epoch supersedes all previous
                maxEpochSeen = entry.getEpoch();
            }
            if (entry.getEpoch() == maxEpochSeen) {
                result.add(entry);
            }
            // entry.epoch < maxEpochSeen: skip (outdated)
        }
    }

    return result;
}
```

**Key points**:
- Don't filter by `clientId` in the initial query—all entries are needed for fork point tracking
- Only add MEMORY entries with matching `clientId` to the result
- When encountering `epoch > maxEpochSeen`, clear the result list (new epoch supersedes all)
- Entries with `epoch < maxEpochSeen` are skipped (outdated)

**Tests**:
1. Fork with higher epoch clears inherited memories
2. Fork without new epoch inherits parent memories
3. Multi-level forks with varying epochs
4. Different clientIds have independent epoch sequences

### Phase 3.5: `allForks` API Option

Add the `allForks` parameter to both REST and gRPC APIs:

1. **OpenAPI changes**:
   ```yaml
   # User API - GET /conversations/{id}/entries
   parameters:
     - name: allForks
       in: query
       schema:
         type: boolean
         default: false
       description: When true, return entries from all forks in the conversation group

   # Admin API - GET /admin/conversations/{id}/entries
   parameters:
     - name: allForks
       in: query
       schema:
         type: boolean
         default: false
   ```

2. **gRPC changes**:
   ```protobuf
   message GetEntriesRequest {
       // ... existing fields ...
       bool all_forks = 10;
   }

   message AdminGetEntriesRequest {
       // ... existing fields ...
       bool all_forks = 10;
   }
   ```

3. **Implementation**:
   ```java
   if (allForks) {
       // Bypass fork ancestry, return all entries in conversation group
       return entryRepository.listByConversationGroup(groupId, channel, limit);
   } else {
       // Existing fork-aware retrieval
       return getEntriesWithForkSupport(...);
   }
   ```

### Phase 4: MongoDB Implementation

Apply the same fork-aware retrieval logic to MongoDB:

1. Add `buildAncestryStack()` method to `MongoMemoryStore`:
   - Load all conversations in the group with a single query
   - Build ancestry chain in-memory

2. Add `listByConversationGroup()` method to `MongoEntryRepository`:
   ```java
   List<MongoEntryEntity> listByConversationGroup(
       String conversationGroupId,
       Channel channel,
       String clientId
   );
   ```

3. Update `MongoMemoryStore.getEntries()` and `adminGetEntries()` to use the shared fork-aware implementation

4. Add tests for MongoDB fork retrieval (same scenarios as PostgreSQL)

## Database Queries

### Query Entries by Conversation Group

```sql
SELECT e.*
FROM entries e
JOIN conversations c ON c.id = e.conversation_id
WHERE c.conversation_group_id = ?1
  AND c.deleted_at IS NULL
  AND e.channel = ?2
ORDER BY e.created_at, e.id
```

For MEMORY channel with client filtering:
```sql
SELECT e.*
FROM entries e
JOIN conversations c ON c.id = e.conversation_id
WHERE c.conversation_group_id = ?1
  AND c.deleted_at IS NULL
  AND e.channel = 'MEMORY'
  AND e.client_id = ?3
ORDER BY e.created_at, e.id
```

### Existing Index Support

The existing index `entries(conversation_group_id, created_at)` mentioned in the original forking design doc should support these queries efficiently.

## Testing

### Unit Tests

```java
@Test
void getEntries_forkedConversation_includesParentEntries() {
    // Given: root conversation with mixed HISTORY and MEMORY entries
    // Root: A(HISTORY), B(MEMORY), C(MEMORY), D(HISTORY) --fork at D--> Fork: I(MEMORY), J(HISTORY), K(HISTORY), L(MEMORY)
    // Note: "fork at D" means D is NOT included; forkedAtEntryId = C (previous entry)
    var root = createConversation();
    createEntry(root, "A", Channel.HISTORY);
    createEntry(root, "B", Channel.MEMORY);
    createEntry(root, "C", Channel.MEMORY);
    var entryD = createEntry(root, "D", Channel.HISTORY);

    // And: forked conversation from entry D (fork sees entries before D)
    var fork = forkConversation(root, entryD);
    createEntry(fork, "I", Channel.MEMORY);
    createEntry(fork, "J", Channel.HISTORY);
    createEntry(fork, "K", Channel.HISTORY);
    createEntry(fork, "L", Channel.MEMORY);

    // When: fetching all entries for the fork
    var result = store.getEntries(userId, fork.getId().toString(), null, 100, null, null, clientId);

    // Then: includes parent entries BEFORE fork point (not D), then fork entries
    assertThat(result.getEntries()).extracting(EntryDto::getContent)
        .containsExactly("A", "B", "C", "I", "J", "K", "L");
}

@Test
void getEntries_forkedConversation_historyChannelOnly() {
    // Given: same setup as above
    var root = createConversation();
    createEntry(root, "A", Channel.HISTORY);
    createEntry(root, "B", Channel.MEMORY);
    createEntry(root, "C", Channel.MEMORY);
    var entryD = createEntry(root, "D", Channel.HISTORY);

    var fork = forkConversation(root, entryD);
    createEntry(fork, "I", Channel.MEMORY);
    createEntry(fork, "J", Channel.HISTORY);
    createEntry(fork, "K", Channel.HISTORY);

    // When: fetching HISTORY entries only
    var result = store.getEntries(userId, fork.getId().toString(), null, 100, Channel.HISTORY, null, null);

    // Then: only HISTORY entries from parent (before D) and fork
    // Note: D is NOT included because fork at D means "branch before D"
    assertThat(result.getEntries()).extracting(EntryDto::getContent)
        .containsExactly("A", "J", "K");
}

@Test
void getEntries_forkedConversation_memoryChannelOnly() {
    // Given: same setup
    var root = createConversation();
    createEntry(root, "A", Channel.HISTORY);
    createEntry(root, "B", Channel.MEMORY);
    createEntry(root, "C", Channel.MEMORY);
    var entryD = createEntry(root, "D", Channel.HISTORY);

    var fork = forkConversation(root, entryD);
    createEntry(fork, "I", Channel.MEMORY);
    createEntry(fork, "J", Channel.HISTORY);
    createEntry(fork, "L", Channel.MEMORY);

    // When: fetching MEMORY entries only
    var result = store.getEntries(userId, fork.getId().toString(), null, 100, Channel.MEMORY, null, clientId);

    // Then: only MEMORY entries from parent (up to fork) and fork
    assertThat(result.getEntries()).extracting(EntryDto::getContent)
        .containsExactly("B", "C", "I", "L");
}

@Test
void getEntries_multiLevelFork_includesAllAncestors() {
    // Given: root -> fork1 -> fork2
    var root = createConversation();
    createEntry(root, "A", Channel.HISTORY);
    var entryB = createEntry(root, "B", Channel.HISTORY);

    var fork1 = forkConversation(root, entryB);
    createEntry(fork1, "C", Channel.HISTORY);
    var entryD = createEntry(fork1, "D", Channel.HISTORY);

    var fork2 = forkConversation(fork1, entryD);
    createEntry(fork2, "E", Channel.HISTORY);

    // When: fetching entries for fork2
    var result = store.getEntries(userId, fork2.getId().toString(), null, 100, null, null, null);

    // Then: includes all ancestor entries
    assertThat(result.getEntries()).extracting(EntryDto::getContent)
        .containsExactly("A", "B", "C", "D", "E");
}

@Test
void getEntries_forkedConversation_withPagination() {
    // Given: fork with parent entries
    // ... setup ...

    // When: fetching with cursor in parent
    var result = store.getEntries(userId, fork.getId().toString(), parentEntryId, 2, null, null, null);

    // Then: starts after cursor, respects limit
    assertThat(result.getEntries()).hasSize(2);
}

// ============== Admin API Tests ==============

@Test
void adminGetEntries_forkedConversation_includesParentEntries() {
    // Given: root conversation with entries
    var root = createConversation();
    createEntry(root, "A", Channel.HISTORY);
    createEntry(root, "B", Channel.HISTORY);
    var entryC = createEntry(root, "C", Channel.HISTORY);

    // And: forked conversation from entry C
    var fork = forkConversation(root, entryC);
    createEntry(fork, "D", Channel.HISTORY);
    createEntry(fork, "E", Channel.HISTORY);

    // When: admin fetches entries for the fork
    var query = new AdminMessageQuery();
    query.setLimit(100);
    var result = store.adminGetEntries(fork.getId().toString(), query);

    // Then: includes parent entries up to fork point, then fork entries
    assertThat(result.getEntries()).extracting(EntryDto::getContent)
        .containsExactly("A", "B", "C", "D", "E");
}

@Test
void adminGetEntries_forkedConversation_withChannelFilter() {
    // Given: root with mixed channels
    var root = createConversation();
    createEntry(root, "A", Channel.HISTORY);
    createEntry(root, "B", Channel.MEMORY);
    var entryC = createEntry(root, "C", Channel.HISTORY);

    var fork = forkConversation(root, entryC);
    createEntry(fork, "D", Channel.MEMORY);
    createEntry(fork, "E", Channel.HISTORY);

    // When: admin fetches HISTORY entries only
    var query = new AdminMessageQuery();
    query.setChannel(Channel.HISTORY);
    query.setLimit(100);
    var result = store.adminGetEntries(fork.getId().toString(), query);

    // Then: only HISTORY entries from parent (up to fork) and fork
    assertThat(result.getEntries()).extracting(EntryDto::getContent)
        .containsExactly("A", "C", "E");
}

@Test
void adminGetEntries_forkedConversation_withPagination() {
    // Given: fork with parent entries
    var root = createConversation();
    createEntry(root, "A", Channel.HISTORY);
    var entryB = createEntry(root, "B", Channel.HISTORY);

    var fork = forkConversation(root, entryB);
    createEntry(fork, "C", Channel.HISTORY);
    createEntry(fork, "D", Channel.HISTORY);

    // When: admin fetches with cursor and limit
    var query = new AdminMessageQuery();
    query.setAfterEntryId(entryB.getId().toString());
    query.setLimit(2);
    var result = store.adminGetEntries(fork.getId().toString(), query);

    // Then: starts after cursor, respects limit
    assertThat(result.getEntries()).extracting(EntryDto::getContent)
        .containsExactly("C", "D");
}

@Test
void adminGetEntries_forkAtBeginning_returnsOnlyForkEntries() {
    // Given: fork at beginning (forkedAtEntryId = null)
    var root = createConversation();
    createEntry(root, "A", Channel.HISTORY);
    createEntry(root, "B", Channel.HISTORY);

    var fork = forkConversationAtBeginning(root);  // forkedAtEntryId = null
    createEntry(fork, "C", Channel.HISTORY);
    createEntry(fork, "D", Channel.HISTORY);

    // When: admin fetches entries for the fork
    var query = new AdminMessageQuery();
    query.setLimit(100);
    var result = store.adminGetEntries(fork.getId().toString(), query);

    // Then: only fork entries (no parent entries)
    assertThat(result.getEntries()).extracting(EntryDto::getContent)
        .containsExactly("C", "D");
}
```

### Integration Tests (Cucumber)

```gherkin
Feature: Forked conversation entry retrieval

  # Based on example:
  # Root: A(HISTORY), B(MEMORY), C(MEMORY), D(HISTORY) --fork at D--> Fork: I(MEMORY), J(HISTORY), K(HISTORY), L(MEMORY)
  # Root continues: E(HISTORY), F(MEMORY), G(MEMORY), H(HISTORY)
  # Note: "fork at D" means D is NOT included in the fork's view of parent entries

  Scenario: Fetch all entries from forked conversation includes parent entries BEFORE fork point
    Given a root conversation with entries A(HISTORY), B(MEMORY), C(MEMORY), D(HISTORY), E(HISTORY), F(MEMORY), G(MEMORY), H(HISTORY)
    And a fork at entry D with entries I(MEMORY), J(HISTORY), K(HISTORY), L(MEMORY)
    When I fetch entries for the forked conversation
    Then I should see entries "A", "B", "C", "I", "J", "K", "L" in order
    # Note: D is NOT included because "fork at D" means "branch before D"

  Scenario: Fetch HISTORY entries from forked conversation
    Given a root conversation with entries A(HISTORY), B(MEMORY), C(MEMORY), D(HISTORY), E(HISTORY)
    And a fork at entry D with entries I(MEMORY), J(HISTORY), K(HISTORY)
    When I fetch entries for the forked conversation with channel=HISTORY
    Then I should see entries "A", "J", "K" in order
    # Note: D is NOT included

  Scenario: Fetch MEMORY entries from forked conversation
    Given a root conversation with entries A(HISTORY), B(MEMORY), C(MEMORY), D(HISTORY)
    And a fork at entry D with entries I(MEMORY), J(HISTORY), L(MEMORY)
    When I fetch entries for the forked conversation with channel=MEMORY
    Then I should see entries "B", "C", "I", "L" in order

  Scenario: Multi-level fork includes all ancestors (before each fork point)
    Given a root conversation with entries "A", "B"
    And a fork at entry "B" with entries "C", "D"
    And a second fork at entry "D" with entries "E", "F"
    When I fetch entries for the second fork
    Then I should see entries "A", "C", "E", "F" in order
    # B excluded (fork point of first fork), D excluded (fork point of second fork)

  Scenario: Pagination works across fork boundary
    Given a conversation with 5 entries
    And the conversation is forked at entry 3
    And the fork has 5 entries
    When I fetch entries with limit 3 starting after entry 2
    Then I should see entries 3, 4, 5 from the fork
    And the next cursor should point to entry 5

  Scenario: Memory channel entries include parent memories
    Given a conversation with memory entries for client "agent-1"
    And the conversation is forked
    And the fork has additional memory entries for client "agent-1"
    When I fetch memory entries for the fork with client "agent-1"
    Then I should see memory entries from both parent and fork

  # ============== Admin API Scenarios ==============

  Scenario: Admin API - Fetch entries from forked conversation includes parent entries
    Given a root conversation with entries "A", "B", "C"
    And a fork at entry "B" with entries "D", "E"
    When an admin fetches entries for the forked conversation
    Then the admin should see entries "A", "B", "D", "E" in order

  Scenario: Admin API - Fetch entries with channel filter
    Given a root conversation with entries A(HISTORY), B(MEMORY), C(HISTORY)
    And a fork at entry C with entries D(MEMORY), E(HISTORY)
    When an admin fetches HISTORY entries for the forked conversation
    Then the admin should see entries "A", "C", "E" in order

  Scenario: Admin API - Fork at beginning returns only fork entries
    Given a root conversation with entries "A", "B"
    And a fork at the beginning with entries "C", "D"
    When an admin fetches entries for the forked conversation
    Then the admin should see entries "C", "D" in order

  Scenario: Admin API - Pagination works across fork boundary
    Given a root conversation with entries "A", "B", "C"
    And a fork at entry "B" with entries "D", "E", "F"
    When an admin fetches entries with limit 2 starting after entry "B"
    Then the admin should see entries "D", "E" in order

  # ============== Epoch Handling Scenarios (Phase 3) ==============

  Scenario: MEMORY with latest epoch - fork with higher epoch clears inherited memories
    Given a root conversation with entries A(HISTORY), B(MEMORY, epoch=1), C(HISTORY)
    And a fork at entry C with entries I(MEMORY, epoch=1), J(MEMORY, epoch=2)
    When I fetch MEMORY entries for the fork with epochFilter=latest
    Then I should see only entry "J"
    # J at epoch=2 supersedes both inherited B and fork's I

  Scenario: MEMORY with latest epoch - fork without new epoch inherits parent memories
    Given a root conversation with entries A(HISTORY), B(MEMORY, epoch=1), C(HISTORY)
    And a fork at entry C with entries I(MEMORY, epoch=1)
    When I fetch MEMORY entries for the fork with epochFilter=latest
    Then I should see entries "B", "I" in order
    # Both at epoch=1 from their respective conversations

  Scenario: MEMORY with latest epoch - querying root returns only root entries
    Given a root conversation with entries A(HISTORY), B(MEMORY, epoch=1), E(MEMORY, epoch=1), F(MEMORY, epoch=1)
    And a fork at entry A with entries I(MEMORY, epoch=1), J(MEMORY, epoch=2)
    When I fetch MEMORY entries for the root with epochFilter=latest
    Then I should see entries "B", "E", "F" in order
    # Fork's epoch=2 does not affect root queries

  Scenario: MEMORY with different clientIds have independent epochs
    Given a root conversation with entries B(MEMORY, epoch=1, clientId=agent-1)
    And a fork with entries I(MEMORY, epoch=1, clientId=agent-2), J(MEMORY, epoch=2, clientId=agent-2)
    When I fetch MEMORY entries for the fork with clientId=agent-1 and epochFilter=latest
    Then I should see entry "B"
    When I fetch MEMORY entries for the fork with clientId=agent-2 and epochFilter=latest
    Then I should see entry "J"

  # ============== allForks Option Scenarios (Phase 3.5) ==============

  Scenario: allForks returns entries from all branches
    Given a root conversation with entries "A", "B", "C"
    And a fork at entry "B" with entries "D", "E"
    When I fetch entries with allForks=true
    Then I should see entries "A", "B", "C", "D", "E" in order

  Scenario: allForks includes sibling forks
    Given a root conversation with entries "A", "B"
    And fork1 at entry "A" with entries "C", "D"
    And fork2 at entry "A" with entries "E", "F"
    When I fetch entries for fork1 with allForks=true
    Then I should see entries from root, fork1, and fork2

  Scenario: allForks=false (default) follows single ancestry path
    Given a root conversation with entries "A", "B"
    And fork1 at entry "A" with entries "C", "D"
    And fork2 at entry "A" with entries "E", "F"
    When I fetch entries for fork1 with allForks=false
    Then I should see entries "A", "C", "D" in order
    And I should NOT see entries "E", "F" from fork2
```

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Performance with deep fork chains | Slow queries | Cache ancestry stack; limit fork depth |
| Memory usage for large entry sets | OOM | Stream entries; enforce reasonable limits |
| Pagination edge cases | Incorrect results | Comprehensive test coverage |
| Epoch handling complexity | Confusing behavior | Clear documentation; sensible defaults |

## Open Questions

1. **Should we limit fork depth?** Deep fork chains (>10 levels) could impact performance.

2. **How to handle deleted parent entries?** If a parent entry at the fork point is deleted, should the fork still show entries up to that point?

3. **Should the API expose which entries came from which conversation?** This could help UIs show fork provenance.

4. **Memory epoch strategy**: Should we use per-conversation epochs or a simpler group-wide approach?

## Implementation Notes

The following clarifications emerged during the implementation:

### Fork Point Shifting (Critical)

The pseudocode in this document stores `forkedAtEntryId` directly on each ancestor in the stack:

```
stack.push((current.id, current.forkedAtEntryId))
```

However, this is **incorrect**. A conversation's `forkedAtEntryId` indicates where it was forked **FROM** in its parent, not where entries should stop in the current conversation.

**The correct approach**: Each conversation in the ancestry chain should receive the fork point from its **CHILD** (the next conversation in the chain), not its own `forkedAtEntryId`. This "shifts" the fork points down the ancestry:

```java
private List<ForkAncestor> buildAncestryStack(ConversationEntity targetConversation) {
    // ...
    List<ForkAncestor> stack = new ArrayList<>();
    ConversationEntity current = targetConversation;
    UUID forkPointFromChild = null;  // Target includes ALL its entries

    while (current != null) {
        // Use fork point from CHILD, not current's forkedAtEntryId
        stack.add(new ForkAncestor(current.getId(), forkPointFromChild));

        // Current's forkedAtEntryId becomes the fork point for its PARENT
        forkPointFromChild = current.getForkedAtEntryId();

        UUID parentId = current.getForkedAtConversationId();
        current = (parentId != null) ? byId.get(parentId) : null;
    }

    Collections.reverse(stack);  // Root first
    return stack;
}
```

**Example**: Given `Root -> Fork1 -> Fork2`:
- `Root.forkedAtEntryId = null` (it's not a fork)
- `Fork1.forkedAtEntryId = D` (forked from Root at entry D)
- `Fork2.forkedAtEntryId = G` (forked from Fork1 at entry G)

The resulting ancestry stack for Fork2 should be:
| Conversation | Fork Point (stop after this entry) |
|--------------|-----------------------------------|
| Root         | D (from Fork1's `forkedAtEntryId`) |
| Fork1        | G (from Fork2's `forkedAtEntryId`) |
| Fork2        | null (include all entries)         |

### Fork Point Entry Calculation (Critical)

When creating a fork, the `forkedAtEntryId` must be calculated by finding the entry immediately before the target entry, considering **ALL channels** (not just HISTORY).

**Bug fixed**: The original implementation only looked at HISTORY entries:
```java
// WRONG: Only considers HISTORY entries
"... and m.channel = ?2 and (m.createdAt < ?3 ..."
```

This caused MEMORY entries between HISTORY entries to be skipped. For example:
- Entries: A(HISTORY), B(MEMORY), C(MEMORY), D(HISTORY)
- Fork at D
- **Bug**: forkedAtEntryId = A (previous HISTORY), fork sees only A
- **Fix**: forkedAtEntryId = C (previous entry of any channel), fork sees A, B, C

The corrected query considers all channels:
```java
// CORRECT: Considers any channel
"... and (m.createdAt < ?2 or (m.createdAt = ?2 and m.id < ?3)) ..."
```

### MongoDB Query Syntax

MongoDB Panache uses different null-checking syntax than JPA/Hibernate:

- **PostgreSQL (JPA)**: `deletedAt IS NULL`
- **MongoDB Panache**: `deletedAt is null` (lowercase, no `IS`)

Both stores use `= null` for null comparisons in simple queries, but the `is null` form is required for explicit null checks in MongoDB Panache query strings.

### Implementation Status

| Phase | Description | Status |
|-------|-------------|--------|
| Phase 1 | Core Entry Retrieval | ✅ Complete |
| Phase 2 | Pagination Support | ✅ Complete |
| Phase 3 | Memory Channel Epoch Handling | ✅ Complete |
| Phase 3.5 | `allForks` API Option | ✅ Complete |
| Phase 4 | MongoDB Implementation | ✅ Complete |

## References

- [001-conversation-forking-design.md](001-conversation-forking-design.md) - Original forking design
- [PostgresMemoryStore.java](../../memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java) - PostgreSQL implementation
- [EntryRepository.java](../../memory-service/src/main/java/io/github/chirino/memory/persistence/repo/EntryRepository.java) - PostgreSQL entry queries
- [MongoMemoryStore.java](../../memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java) - MongoDB implementation
- [MongoEntryRepository.java](../../memory-service/src/main/java/io/github/chirino/memory/persistence/mongo/repo/MongoEntryRepository.java) - MongoDB entry queries
