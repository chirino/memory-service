# Enhancement 038: Remove Epoch Eviction

## Problem Statement

Enhancement 027 introduced the ability to evict old memory epochs - non-latest epochs whose entries are older than a retention period. While this is useful for reclaiming storage, it creates a data integrity problem when combined with conversation forking (Enhancement 001).

### The Conflict Between Epoch Eviction and Forking

Forks are created at user messages in the HISTORY channel. When an agent retrieves memory for a forked conversation, the system walks the fork ancestry chain and collects MEMORY entries from parent conversations up to each fork point. The epoch that was "active" at the time the fork was created may be an old, non-latest epoch in the parent conversation.

**Example scenario:**

```
Parent conversation timeline:
  Day 1:   MEMORY epoch 1 created (agent's initial memory)
  Day 10:  HISTORY user message "Tell me about X"    <-- fork created here
  Day 50:  MEMORY epoch 2 created (agent compacted memory)
  Day 100: MEMORY epoch 3 created (latest)

Fork conversation (created at the Day 10 user message):
  - Inherits parent entries up to the fork point
  - Agent retrieves memory for the fork
  - Walks ancestry to fork point at Day 10
  - At Day 10, the active memory was epoch 1
  - Agent gets epoch 1 entries as its memory context
```

If epoch eviction runs with a 60-day retention period on Day 100:
- Epoch 1 is eligible: it's not the latest, and its `max(created_at)` is 100 days ago
- Epoch 1 gets evicted from the parent conversation
- **The fork now has NO memory context** - the agent retrieves zero memory entries

This is a silent data loss. The fork appears to work but the agent has lost all historical context that existed when the fork was created. The agent would behave as if the conversation had no prior memory, potentially giving confused or contradictory responses.

### Why This Is Hard to Fix Incrementally

Several alternative approaches were considered:

1. **Block forks before evicted epochs**: This would require tracking which epochs have been evicted and comparing against fork points at fork-creation time. It also retroactively invalidates existing forks if eviction runs later.

2. **Copy epoch entries into fork at fork-creation time**: This defeats the lightweight nature of forking (currently forks copy zero entries). It would also require knowing which client IDs might be relevant.

3. **Track fork dependencies during eviction**: The eviction query would need to join against all fork points across all conversations in the group to determine if any fork depends on a given epoch. This adds significant complexity to what should be a simple batch cleanup operation.

4. **Mark epochs as "pinned" when forks reference them**: Requires maintaining a reference-counting or pinning system between forks and parent epochs, adding ongoing bookkeeping overhead.

All of these approaches add significant complexity for a feature (epoch eviction) that is not yet critical. The simpler path is to remove epoch eviction until a proper solution is designed.

## Decision

Remove the `memory_epochs` resource type from the eviction endpoint. Old epochs will be retained indefinitely. This is acceptable because:

1. **Epochs are already space-efficient**: When an agent compacts memory, the new epoch typically summarizes the old one. Old epochs are small relative to the HISTORY channel entries.

2. **The latest-epoch guarantee is sufficient**: Agents only read the latest epoch during normal operation. Old epochs were retained purely for auditability.

3. **Full conversation eviction still works**: When a conversation group is soft-deleted and evicted via `conversation_groups` resource type, all entries (including all epochs) are deleted. This remains the primary storage reclamation mechanism.

4. **No production deployments depend on epoch eviction yet**: This feature was introduced recently and has not been relied upon for storage management.

## Dependencies

- **Enhancement 027 (Epoch Evictions)**: This enhancement reverses 027's implementation.

## Scope of Changes

| File | Change Type |
|------|-------------|
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Remove `memory_epochs` from `resourceTypes` enum |
| `memory-service/src/main/java/.../service/EvictionService.java` | Remove `memory_epochs` handling |
| `memory-service/src/main/java/.../store/MemoryStore.java` | Remove `findEvictableEpochs()`, `countEvictableEpochEntries()`, `deleteEntriesForEpochs()` |
| `memory-service/src/main/java/.../store/impl/PostgresMemoryStore.java` | Remove epoch eviction implementation |
| `memory-service/src/main/java/.../store/impl/MongoMemoryStore.java` | Remove epoch eviction implementation |
| `memory-service/src/main/java/.../store/EpochKey.java` | Delete file |
| `memory-service/src/test/resources/features/eviction-rest.feature` | Remove epoch eviction test scenarios |

## Implementation Plan

### Step 1: Update OpenAPI Spec

Remove `memory_epochs` from the `resourceTypes` enum in `openapi-admin.yml`. If `memory_epochs` is the only value or is referenced elsewhere, clean up accordingly.

### Step 2: Remove EpochKey Record

Delete `EpochKey.java` - it was only used for epoch eviction.

### Step 3: Remove MemoryStore Interface Methods

Remove the three epoch-eviction methods from the `MemoryStore` interface:
- `findEvictableEpochs()`
- `countEvictableEpochEntries()`
- `deleteEntriesForEpochs()`

### Step 4: Remove Store Implementations

Remove the corresponding implementations from:
- `PostgresMemoryStore`
- `MongoMemoryStore`

### Step 5: Remove EvictionService Logic

Remove the `memory_epochs` branch from `EvictionService.evict()` and any helper methods like `evictMemoryEpochs()`.

### Step 6: Remove Test Scenarios

Remove any Cucumber scenarios or test code that exercises epoch eviction.

### Step 7: Compile and Test

```bash
./mvnw compile
./mvnw test > test.log 2>&1
# Grep for errors in test.log
```

## Future Considerations

If storage from old epochs becomes a concern in the future, a proper solution should:

1. **Be fork-aware**: Check whether any fork in the conversation group depends on an epoch before evicting it.
2. **Consider copying on eviction**: If an epoch is needed by a fork, copy its entries into the fork before deleting them from the parent.
3. **Track epoch dependencies explicitly**: Maintain metadata about which epochs are referenced by which forks, enabling safe eviction decisions.

These approaches are more complex but would allow epoch eviction to coexist safely with forking.

## Verification

```bash
# Compile all modules
./mvnw compile

# Run full test suite
./mvnw test

# Verify eviction still works for other resource types
./mvnw test -Dcucumber.filter.tags="@eviction"
```
