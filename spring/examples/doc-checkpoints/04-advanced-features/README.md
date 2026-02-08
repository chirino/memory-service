# Checkpoint 04: Advanced Features

This checkpoint adds streaming responses, conversation forking, and response resumption.

## What's Included

- Streaming responses with `SseEmitter` and `Flux<String>`
- Conversation forking endpoints
- `ResumeController` for response resumption
- All advanced Memory Service features

## Tutorial Reference

This checkpoint corresponds to the [Advanced Features](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/spring/advanced-features.mdx) guide.

## Prerequisites

- Same as checkpoint 03

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw spring-boot:run
```

## Testing

```bash
# Streaming chat
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: text/plain" \
  -H "Authorization: Bearer $(get-token)" \
  -d "Write a 4 paragraph story about a cat."

# Fork conversation
FIRST_ENTRY_ID=$(curl -sSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/entries \
  -H "Authorization: Bearer $(get-token)" | jq -r '.data[0].id')

curl -sSfX POST http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/entries/$FIRST_ENTRY_ID/fork \
  -H "Authorization: Bearer $(get-token)" \
  -H "Content-Type: application/json" \
  -d '{"title": "Alternative approach"}' | jq

# Check for in-progress responses
curl -sSfX POST http://localhost:9090/v1/conversations/resume-check \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '["3579aac5-c86e-4b67-bbea-6ec1a3644942"]' | jq
```

**Expected behavior**: Streaming responses, conversation forking, and resumption all work.

## Complete Example

For a production-ready example with frontend, see [spring/examples/chat-spring](../../chat-spring/).
