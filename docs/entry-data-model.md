# Entry Data Model

This document explains how entries are stored and retrieved in the memory service, covering the core data structure, channels, memory epochs, conversation forking, and how these concepts interact.

## Overview

An **Entry** is the fundamental unit of stored data in a conversation. Entries represent various types of content: chat messages, agent memory snapshots, tool results, or any structured data an agent needs to persist.

```mermaid
erDiagram
    ConversationGroup ||--o{ Conversation : "contains"
    Conversation ||--o{ Entry : "contains"
    ConversationGroup {
        UUID id PK
        timestamp createdAt
        timestamp deletedAt
    }
    Conversation {
        UUID id PK
        UUID conversationGroupId FK
        UUID forkedAtConversationId FK
        UUID forkedAtEntryId FK
        string ownerUserId
        bytes title
        timestamp createdAt
        timestamp updatedAt
        timestamp deletedAt
    }
    Entry {
        UUID id PK
        UUID conversationId FK
        UUID conversationGroupId FK
        string userId
        string clientId
        enum channel
        long epoch
        string contentType
        bytes content
        timestamp createdAt
    }
```

## Entry Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | UUID | Unique entry identifier |
| `conversationId` | UUID | The conversation this entry belongs to |
| `conversationGroupId` | UUID | The conversation group (for efficient fork queries) |
| `userId` | string | Human user who created the entry (null for agent entries) |
| `clientId` | string | Agent/client identifier from API key (null for user entries) |
| `channel` | enum | Logical channel: `HISTORY` or `MEMORY` |
| `epoch` | long | Memory epoch number (only for MEMORY channel) |
| `contentType` | string | Schema identifier describing the content format |
| `content` | bytes | Encrypted JSON content array |
| `createdAt` | timestamp | When the entry was created |

## Channels

Entries are organized into logical **channels** that serve different purposes:

```mermaid
flowchart TB
    subgraph Conversation
        subgraph "HISTORY Channel"
            H1[User: Hello]
            H2[Agent: Hi there!]
            H3[User: What's 2+2?]
            H4[Agent: 4]
        end
        subgraph "MEMORY Channel (per agent)"
            subgraph "Epoch 1 (superseded)"
                M1[context1, context2]
            end
            subgraph "Epoch 2 (current)"
                M2[summary, context3]
            end
        end
    end
```

**Typical interaction order**: User HISTORY → Agent MEMORY updates → Agent HISTORY response → *(repeat)*

### HISTORY Channel

The **HISTORY** channel contains the visible conversation between users and agents. These entries form the chat transcript that users see in the UI.

- **Created by**: Users (via chat UI) or agents (responses)
- **Visible to**: All participants in the conversation
- **Epoch**: Always `null` (not epoch-scoped)
- **Content**: Chat messages in a common format across frameworks

### MEMORY Channel

The **MEMORY** channel stores agent-internal state that persists between interactions. This is the agent's "working memory" - context it needs to continue conversations coherently.

- **Created by**: Agents only (via API key authentication)
- **Visible to**: Only the agent that created it (filtered by `clientId`)
- **Epoch**: Required - memory entries are versioned by epoch
- **Content**: Agent-specific format (e.g., `LC4J` for LangChain4j, `SpringAI` for Spring AI)

## Multi-Agent Support

Multiple agents can participate in the same conversation without interfering with each other's memory. This is achieved through the `clientId` field.

```mermaid
flowchart LR
    subgraph "Conversation conv-123"
        subgraph "Agent A (clientId: agent-a)"
            MA1[Memory Entry 1]
            MA2[Memory Entry 2]
        end
        subgraph "Agent B (clientId: agent-b)"
            MB1[Memory Entry 1]
            MB2[Memory Entry 2]
        end
        subgraph "Shared History"
            H1[User message]
            H2[Agent A response]
            H3[Agent B response]
        end
    end
```

### Client ID Resolution

The `clientId` is derived from the API key used to authenticate the request. The mapping is configured as:

```properties
memory-service.api-keys.agent-a=key1,key2
memory-service.api-keys.agent-b=key3
```

When an agent queries the MEMORY channel, entries are automatically filtered to only return entries matching their `clientId`.

---

## Memory Epochs

Memory epochs enable agents to version their memory state. When an agent's context changes significantly (e.g., after summarization), it creates a new epoch.

### How Epochs Work

```mermaid
sequenceDiagram
    participant Agent
    participant MemoryService

    Note over Agent,MemoryService: Initial conversation
    Agent->>MemoryService: Sync memory [msg1, msg2] (epoch=1)
    MemoryService-->>Agent: OK

    Note over Agent,MemoryService: Add more messages
    Agent->>MemoryService: Sync memory [msg1, msg2, msg3, msg4] (epoch=1)
    MemoryService-->>Agent: OK (appended to epoch 1)

    Note over Agent,MemoryService: Memory compaction/summarization
    Agent->>MemoryService: Sync memory [summary, msg5] (epoch=2)
    MemoryService-->>Agent: OK (new epoch created)
```

### Epoch Semantics

| Scenario | Result |
|----------|--------|
| Incoming content **exactly matches** existing | No-op (no writes) |
| Incoming content **extends** existing (prefix match) | Append delta to current epoch |
| Incoming content **diverges** from existing | Create new epoch with new content |

### Retrieving Memory by Epoch

When querying memory entries:

- **`epoch=latest`** (default): Returns only entries from the highest epoch number
- **`epoch=all`**: Returns entries from all epochs (for debugging)
- **`epoch=N`**: Returns entries from a specific epoch number

```mermaid
flowchart TB
    subgraph "Memory Entries for agent-a in conv-123"
        subgraph "Epoch 1 (superseded)"
            E1M1[msg1]
            E1M2[msg2]
            E1M3[msg3]
        end
        subgraph "Epoch 2 (current)"
            E2M1[summary]
            E2M2[msg4]
            E2M3[msg5]
        end
    end

    Query["GET ?channel=memory&epoch=latest"] --> E2M1
    Query --> E2M2
    Query --> E2M3
```

### Epoch Isolation per Client

Each `(conversationId, clientId)` pair has its own independent epoch sequence:

```
Conversation: conv-123
├── Agent A (clientId: agent-a)
│   ├── Epoch 1: [msg1, msg2]
│   └── Epoch 2: [summary, msg3]  <- Agent A's latest
│
└── Agent B (clientId: agent-b)
    └── Epoch 1: [context1, context2]  <- Agent B's latest
```

Agent A advancing to epoch 2 does not affect Agent B's epoch sequence.

---

## Conversation Forking

Conversations can be forked to create alternative branches from any point. Forking is "cheap" - it doesn't copy entries, but instead uses metadata to define which parent entries are visible to the fork.

### Fork Data Model

```mermaid
erDiagram
    ConversationGroup ||--o{ Conversation : contains
    Conversation ||--o| Conversation : "forked from"

    Conversation {
        UUID id PK
        UUID conversationGroupId FK
        UUID forkedAtConversationId FK "parent conversation"
        UUID forkedAtEntryId FK "last visible entry of parent"
    }
```

- **`conversationGroupId`**: All forks share the same group (for access control)
- **`forkedAtConversationId`**: The parent conversation this was forked from
- **`forkedAtEntryId`**: The last visible entry of the parent conversation

### Fork Semantics: "Fork at Entry X"

When we say "fork at entry X", it means **branch before X**. The fork sees all parent entries up to and including `forkedAtEntryId`, but **not** entry X itself.

A typical chat interaction follows this pattern:
1. **User HISTORY message** - User asks a question
2. **Agent MEMORY entries** - Agent stores/updates its working memory
3. **Agent HISTORY message** - Agent responds to the user
4. *(repeat)*

Forks are typically requested at a **User HISTORY message** - the user wants to "try a different question" at that point in the conversation. The fork excludes the selected user message and includes everything before it.

```mermaid
flowchart TB
    subgraph "Root Conversation"
        A[Entry A - HISTORY User]
        B[Entry B - MEMORY Agent]
        C[Entry C - HISTORY Agent]
        D[Entry D - HISTORY User]
        E[Entry E - MEMORY Agent]
        F[Entry F - HISTORY Agent]
    end

    subgraph "Fork requested at Entry D"
        FA[Fork sees: A, B, C]
        FI[Entry I - HISTORY User]
        FJ[Entry J - MEMORY Agent]
        FK[Entry K - HISTORY Agent]
    end

    C -.->|forkedAtEntryId=C| FA

    style D fill:#ff9999
    style E fill:#ff9999
    style F fill:#ff9999
```

When the user calls the fork API at entry D (a User HISTORY message):
- The API call: `POST /v1/conversations/{id}/entries/D/fork`
- The system calculates `forkedAtEntryId = C` (the entry immediately before D)
- Fork sees: A, B, C (from parent), then its own entries I, J, K
- Fork does **NOT** see: D, E, F (the requested entry and everything after)

### Fork at Beginning

A fork can also occur at the very beginning of a conversation. In this case, `forkedAtEntryId = null`:

```mermaid
flowchart TB
    subgraph "Root Conversation"
        RA[Entry A]
        RB[Entry B]
    end

    subgraph "Fork at Beginning (forkedAtEntryId = null)"
        FC[Entry C]
        FD[Entry D]
    end

    Root[Root] -.->|forkedAtEntryId=null| Fork[Fork]
```

This fork sees **none** of the parent's entries - it's a "blank slate" that shares the conversation group (for access control) but starts fresh.

### Multi-Level Forks

Forks can be nested arbitrarily deep:

```mermaid
flowchart TB
    subgraph "Root"
        R_A[A]
        R_B[B]
        R_C[C]
    end

    subgraph "Fork 1 (at B)"
        F1_D[D]
        F1_E[E]
    end

    subgraph "Fork 2 (at E)"
        F2_F[F]
        F2_G[G]
    end

    R_B -.->|fork| F1_D
    F1_E -.->|fork| F2_F
```

When querying Fork 2:
- Ancestry chain: Root → Fork 1 → Fork 2
- Fork 2 sees: A, D, F, G

### Entry Retrieval Algorithm

When fetching entries for a forked conversation, the algorithm:

1. **Build ancestry stack**: Walk up the parent chain collecting `(conversationId, forkPointEntryId)` pairs
2. **Query by conversation group**: Fetch all entries ordered by `createdAt`
3. **Filter based on ancestry**: Include entries according to the fork boundaries

```mermaid
flowchart TB
    subgraph "Ancestry Stack (reversed)"
        S1["(Root, forkPoint=B)"]
        S2["(Fork1, forkPoint=E)"]
        S3["(Fork2, forkPoint=null)"]
    end

    subgraph "Filtered Result"
        R1[A from Root]
        R2[D from Fork1]
        R3[F from Fork2]
        R4[G from Fork2]
    end

    S1 --> R1
    S2 --> R2
    S3 --> R3
    S3 --> R4
```

**Important**: The fork point stored on a conversation indicates where it was forked **FROM** in its parent. When building the ancestry stack, this fork point is "shifted" to apply to the parent conversation:

| Conversation | Fork Point (stop after this entry) |
|--------------|-----------------------------------|
| Root         | B (from Fork1's `forkedAtEntryId`) |
| Fork1        | E (from Fork2's `forkedAtEntryId`) |
| Fork2        | null (include all its entries)     |

---

## Epochs in Forked Conversations

Memory epochs interact with forking in a specific way: epochs are per-conversation, not per-group. A fork starts its own independent epoch sequence.

### Epoch Divergence at Fork Points

```mermaid
flowchart TB
    subgraph "Root Conversation"
        R_A[A - HISTORY]
        R_B["B - MEMORY (epoch=1)"]
        R_C[C - HISTORY]
        R_D["D - MEMORY (epoch=1)"]
        R_E["E - MEMORY (epoch=1)"]
    end

    subgraph "Fork (at C)"
        F_I["I - MEMORY (epoch=1)"]
        F_J["J - MEMORY (epoch=2)"]
        F_K[K - HISTORY]
    end

    R_C -.->|fork| F_I
```

**Key concept**: Epochs with the same number in different conversations are **completely independent**. The root's epoch=1 is different from the fork's epoch=1.

### Query Results with Epochs

When querying MEMORY with `epoch=latest` from the fork:

| Scenario | Result | Explanation |
|----------|--------|-------------|
| Fork has epoch 2 | J only | Fork's epoch 2 supersedes all previous |
| Fork only has epoch 1 | B, I | Both at epoch 1 from their respective conversations |
| Query root with `epoch=latest` | B, D, E | Root's entries only, fork doesn't affect it |

### Epoch Filtering Algorithm

When iterating through entries in fork order, track the maximum epoch seen:

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
```

This ensures that when a fork creates a new epoch, it supersedes both its own previous epochs AND any inherited parent epochs.

---

## Edge Cases

### 1. Fork at First Entry

When forking at the very first HISTORY entry:

```mermaid
flowchart LR
    subgraph Root
        R_A[A - first entry]
        R_B[B]
    end
    subgraph Fork
        F_C[C]
        F_D[D]
    end
    R_A -.->|fork before A| F_C
```

- `forkedAtEntryId = null` (no previous entry exists)
- Fork sees none of the parent's entries
- Fork has its own independent entry sequence

### 2. Fork with No Memory Entries

A fork may not have any MEMORY entries of its own:

```mermaid
flowchart LR
    subgraph Root
        R_A[A - HISTORY]
        R_B["B - MEMORY (epoch=1)"]
        R_C[C - HISTORY]
    end
    subgraph Fork
        F_D[D - HISTORY]
    end
    R_C -.->|fork| F_D
```

Querying MEMORY from the fork returns: B (inherited from parent at epoch=1)

### 3. Multiple Agents in Forked Conversation

Each agent's memory is independently scoped by `clientId`:

```mermaid
flowchart TB
    subgraph "Root"
        R_H1[HISTORY: user msg]
        R_MA["MEMORY: agent-a (epoch=1)"]
        R_MB["MEMORY: agent-b (epoch=1)"]
    end
    subgraph "Fork"
        F_H1[HISTORY: fork msg]
        F_MA["MEMORY: agent-a (epoch=2)"]
    end
```

- Query fork as `agent-a`: Gets `agent-a`'s epoch=2 entries only (supersedes inherited epoch=1)
- Query fork as `agent-b`: Gets inherited `agent-b` epoch=1 entries from root

### 4. Sibling Forks

Multiple forks can branch from the same point:

```mermaid
flowchart TB
    subgraph Root
        R_A[A]
        R_B[B]
    end
    subgraph Fork1
        F1_C[C]
    end
    subgraph Fork2
        F2_D[D]
    end
    R_A -.-> F1_C
    R_A -.-> F2_D
```

- Fork1 and Fork2 both have `forkedAtEntryId = A`
- They share entry A from the root
- They are independent and don't see each other's entries
- The `allForks=true` query option returns entries from all forks

### 5. Deleted Parent Entry at Fork Point

Entries are never deleted individually - they are only removed when the entire conversation group is deleted. If you see a "deleted" entry in the fork ancestry, the entire group would be inaccessible.

### 6. Empty Content Arrays

A MEMORY entry can have an empty content array `[]` after all messages are removed. This is valid and represents "memory cleared":

```json
{
  "channel": "memory",
  "epoch": 3,
  "content": []
}
```

The empty content at epoch 3 supersedes all entries from epochs 1 and 2.

---

## API Query Parameters

### Entry Retrieval Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `channel` | Filter by channel (`history`, `memory`) | All channels |
| `epoch` | Epoch filter (`latest`, `all`, or specific number) | `latest` for MEMORY |
| `afterEntryId` | Pagination cursor | None (start from beginning) |
| `limit` | Maximum entries to return | 50 |
| `allForks` | Return entries from all forks in the group | `false` |

### Example Queries

```bash
# Get all HISTORY entries for a conversation
GET /v1/conversations/{id}/entries?channel=history

# Get latest MEMORY entries for an agent
GET /v1/conversations/{id}/entries?channel=memory&epoch=latest

# Get all entries across all forks (admin/debug)
GET /v1/conversations/{id}/entries?allForks=true

# Paginated retrieval
GET /v1/conversations/{id}/entries?afterEntryId={lastSeenId}&limit=50
```

---

## Summary

The entry data model supports:

1. **Multiple channels** for different data types (HISTORY, MEMORY)
2. **Multi-agent support** through `clientId` isolation
3. **Memory epochs** for versioning agent context with superseding semantics
4. **Efficient forking** without copying data, using ancestry-based retrieval
5. **Nested forks** with proper epoch handling across fork boundaries

The key insight for forked entry retrieval is the "fork point shifting" - each conversation in the ancestry chain uses its child's `forkedAtEntryId` to determine where to stop including entries.
