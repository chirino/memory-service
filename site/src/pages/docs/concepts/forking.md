---
layout: ../../../layouts/DocsLayout.astro
title: Conversation Forking
description: Create branches in conversation history with forking.
---

Conversation forking allows you to create a new conversation branch from any point in an existing conversation, preserving the original while exploring alternative paths.

## What is Forking?

Forking creates a copy of a conversation up to a specific message, allowing you to:

- **Explore alternatives** - Try different responses or approaches
- **Debug issues** - Isolate problematic conversation states
- **A/B test** - Compare different agent behaviors
- **Recover from errors** - Branch from before an error occurred

## How Forking Works

```
Original: msg1 → msg2 → msg3 → msg4 → msg5
                  ↓
Fork at msg2:  msg1 → msg2 → (new messages...)
```

The forked conversation:
- Contains all messages up to the fork point
- Has a new unique ID
- References the parent conversation
- Can be modified independently

## Creating a Fork

### Using the REST API

```bash
curl -X POST http://localhost:8080/api/v1/conversations/original-id/fork \
  -H "Content-Type: application/json" \
  -d '{
    "newId": "forked-conversation",
    "atMessage": 2
  }'
```

### Using Java

```java
@Inject
ConversationService conversationService;

public void forkConversation() {
    String forkedId = conversationService.fork(
        "original-conversation",
        "forked-conversation",
        2  // Fork after message index 2
    );
}
```

## Fork Properties

When you fork a conversation, the new conversation has:

| Property | Value |
|----------|-------|
| `parentId` | ID of the original conversation |
| `forkPoint` | Message index where fork occurred |
| `messages` | Copy of messages up to fork point |
| `ownerId` | Same as original (or specified new owner) |

## Use Cases

### 1. Agent Development

Fork a conversation to test different prompts:

```java
// Original conversation had a poor response at message 5
String testFork = conversationService.fork("prod-conv", "test-fork", 4);

// Try a different approach
ChatMemory testMemory = memoryProvider.get(testFork);
testMemory.add(SystemMessage.from("New instructions..."));
```

### 2. User Correction

Allow users to "go back" and try a different question:

```java
// User wants to rephrase their last question
String correctedConv = conversationService.fork(conversationId, newId, lastGoodMessage);
```

### 3. Parallel Exploration

Create multiple forks to explore different paths:

```java
for (int i = 0; i < 3; i++) {
    String forkId = conversationService.fork(original, "fork-" + i, forkPoint);
    // Each fork can now diverge independently
}
```

## Best Practices

1. **Name forks meaningfully** - Include the purpose or variant in the ID
2. **Track lineage** - Use metadata to record why forks were created
3. **Clean up experiments** - Delete test forks when no longer needed
4. **Consider storage** - Forks duplicate messages, plan storage accordingly

## Limitations

- Forks create copies, not references (storage implications)
- Cannot fork a fork (only one level deep) - use the API to work around
- Fork point must be a valid message index

## Next Steps

- Learn about [Semantic Search](/docs/concepts/semantic-search/)
- Explore the [REST API](/docs/integrations/rest-api/)
