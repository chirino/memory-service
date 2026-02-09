# Checkpoint 04: Conversation Forking

This checkpoint adds conversation forking — letting users branch off from any point in a conversation to explore alternative paths.

## What's Included

- Memory service extension with conversation recording
- `HistoryRecordingAgent` wrapper with `@RecordConversation` annotation
- `ConversationsResource` exposing conversation fork APIs:
  - `GET /v1/conversations/{id}/entries` - List entries (needed to get entry IDs for forking)
  - `POST /v1/conversations/{id}/entries/{entryId}/fork` - Fork at entry
  - `GET /v1/conversations/{id}/forks` - List forks

## Tutorial Reference

This checkpoint corresponds to the [Conversation Forking](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/quarkus/conversation-forking.mdx) guide.

## Prerequisites

- Same as checkpoint 01, plus memory-service running

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw quarkus:dev
```

## Testing

```bash
# Send a message to create conversation entries
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: text/plain" \
  -H "Authorization: Bearer $(get-token)" \
  -d "Hello"

# Get the first entry ID
FIRST_ENTRY_ID=$(curl -sSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/entries \
  -H "Authorization: Bearer $(get-token)" | jq -r '.data[0].id')

# Fork at that entry
curl -sSfX POST http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/entries/$FIRST_ENTRY_ID/fork \
  -H "Authorization: Bearer $(get-token)" \
  -H "Content-Type: application/json" \
  -d '{"title": "Alternative approach"}' | jq
```

**Expected behavior**: A new conversation is created as a fork of the original.

## Next Step

Continue to [checkpoint 05](../05-response-resumption/) for streaming and response resumption, or [checkpoint 06](../06-sharing/) for conversation sharing.
