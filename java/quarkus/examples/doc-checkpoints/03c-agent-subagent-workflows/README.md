# Checkpoint 03c: Agent and Sub-Agent Workflows

This checkpoint branches from checkpoint 03 and shows how to start a child conversation for a delegated sub-task while keeping the parent and child transcripts separate.

## What's Included

- `HistoryRecordingAgent` wrapper with `@RecordConversation` annotation for the parent conversation
- `SubAgentWorkflowResource` that creates a child conversation atomically with its first entry
- `ConversationsResource` exposing conversation APIs:
  - `GET /v1/conversations` - List conversations
  - `GET /v1/conversations/{id}/children` - List child conversations
  - `GET /v1/conversations/{id}` - Get conversation
  - `GET /v1/conversations/{id}/entries` - Get messages
- Parent and child history stays separate, with agent attribution on child entries

## Tutorial Reference

This checkpoint corresponds to the [Agent and Sub-Agent Workflows](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/quarkus/agent-subagent-workflows.mdx) guide.

## Prerequisites

- Same as checkpoint 02

## Running

```bash
export OPENAI_API_KEY=your-api-key
mvn quarkus:dev
```

## Testing

```bash
# Send a parent message
curl -NsSfX POST http://localhost:9090/chat/cd015a18-39b7-485d-9f09-2890f9ae282e \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Give me a random number between 1 and 100."'

# Find the parent entry id that triggered delegation
curl -sSfX GET http://localhost:9090/v1/conversations/cd015a18-39b7-485d-9f09-2890f9ae282e/entries \
  -H "Authorization: Bearer $(get-token)" | jq

# Delegate to the sub-agent
curl -NsSfX POST "http://localhost:9090/delegate/cd015a18-39b7-485d-9f09-2890f9ae282e/dc0719bd-a06c-4675-b7b0-e96e187be3b4?startedByEntryId=<entry-id>" \
  -H "Content-Type: text/plain" \
  -H "Authorization: Bearer $(get-token)" \
  -d "Research the best option and summarize the tradeoffs."

# List child conversations
curl -sSfX GET http://localhost:9090/v1/conversations/cd015a18-39b7-485d-9f09-2890f9ae282e/children \
  -H "Authorization: Bearer $(get-token)" | jq
```

**Expected behavior**: the child conversation starts with the delegating entry, the sub-agent response is recorded in the child, and the child is discoverable through `/children` and `ancestry=children`.

## Next Step

Continue to [checkpoint 04](../04-conversation-forking/) for conversation forking, or [checkpoint 05](../05-response-resumption/) for response recording and resumption.
