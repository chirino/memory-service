# Checkpoint 03: With History

This checkpoint adds conversation history recording and exposes conversation APIs for frontend applications.

## What's Included

- `ConversationHistoryStreamAdvisor` for automatic history recording
- `MemoryServiceProxyController` exposing conversation APIs:
  - `GET /v1/conversations` - List conversations
  - `GET /v1/conversations/{id}` - Get conversation
  - `GET /v1/conversations/{id}/entries` - Get messages
- Messages now visible in UI

## Tutorial Reference

This checkpoint corresponds to the [Conversation History](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/spring/conversation-history.mdx) guide.

## Prerequisites

- Same as checkpoint 02

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw spring-boot:run
```

## Testing

```bash
# Send a message
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Give me a random number between 1 and 100."'

# View conversation
curl -sSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Authorization: Bearer $(get-token)" | jq

# View messages
curl -sSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/entries \
  -H "Authorization: Bearer $(get-token)" | jq
```

**Expected behavior**: Messages appear in conversation history and can be viewed via API.

## Next Step

Continue to [checkpoint 04](../04-conversation-forking/) for conversation forking, or [checkpoint 05](../05-response-resumption/) for streaming and response resumption.
