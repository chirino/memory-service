# Checkpoint 05: Response Resumption

This checkpoint adds streaming responses with resume and cancel support — so users can reconnect after a disconnect and pick up where they left off.

## What's Included

- Memory service extension with conversation recording
- Streaming `Agent` using `Multi<String>` return type
- `HistoryRecordingAgent` wrapper with `@RecordConversation` (streaming)
- `ChatResource` with SSE streaming
- `ResumeResource` exposing resume/cancel APIs:
  - `POST /v1/conversations/resume-check` - Check for in-progress responses
  - `GET /v1/conversations/{id}/resume` - Resume streaming response
  - `POST /v1/conversations/{id}/cancel` - Cancel in-progress response

## Tutorial Reference

This checkpoint corresponds to the [Response Resumption](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/quarkus/response-resumption.mdx) guide.

## Prerequisites

- Same as checkpoint 01, plus memory-service running

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw quarkus:dev
```

## Testing

```bash
# Start a streaming chat (in one terminal)
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: text/plain" \
  -H "Authorization: Bearer $(get-token)" \
  -d "Write a 4 paragraph story about a cat."

# Check for in-progress responses (in another terminal)
curl -sSfX POST http://localhost:9090/v1/conversations/resume-check \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '["3579aac5-c86e-4b67-bbea-6ec1a3644942"]' | jq

# Resume the streaming response
curl -NsSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/resume \
  -H "Authorization: Bearer $(get-token)"
```

**Expected behavior**: Streaming response can be checked, resumed, and canceled.

## Next Step

Continue to [checkpoint 06](../06-sharing/) for conversation sharing.
