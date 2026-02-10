# Checkpoint 07: With Search

This checkpoint adds search indexing and a search API endpoint, building on conversation history recording.

## What's Included

- `PassThroughIndexedContentProvider` implementing `IndexedContentProvider` to enable search indexing
- Search endpoint added to `ConversationsResource`:
  - `POST /v1/conversations/search` - Semantic search across conversations

## Tutorial Reference

This checkpoint corresponds to the [Indexing and Search](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/quarkus/indexing-and-search.mdx) guide.

## Prerequisites

- Same as checkpoint 03

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw quarkus:dev
```

## Testing

```bash
# Send a message
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: text/plain" \
  -H "Authorization: Bearer $(get-token)" \
  -d "Give me a random number between 1 and 100."

# Search for conversations
curl -sSfX POST http://localhost:9090/v1/conversations/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '{"query": "random number"}' | jq
```

**Expected behavior**: Messages are indexed for search and can be found via the search API.

## Next Step

Continue to [checkpoint 04](../04-conversation-forking/) for conversation forking, or [checkpoint 05](../05-response-resumption/) for streaming and response resumption.
