# Checkpoint 02: With Memory

This checkpoint adds Memory Service integration with OIDC authentication.

## What's Included

- Memory Service Extension dependency
- OIDC authentication
- `Agent` interface with `@MemoryId` parameter
- Endpoint: `POST /chat/{conversationId}` (now requires conversation ID)
- Conversations persist across requests

## Tutorial Reference

This checkpoint corresponds to **Step 2** of the [Quarkus Getting Started](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/quarkus/getting-started.mdx) guide.

## Prerequisites

- Memory Service running via Docker (see [Getting Started](/docs/getting-started/))
- Keycloak running for OIDC

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw quarkus:dev
```

## Testing

```bash
# Get auth token
function get-token() {
  curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "client_id=memory-service-client" \
    -d "client_secret=change-me" \
    -d "grant_type=password" \
    -d "username=bob" \
    -d "password=bob" \
    | jq -r '.access_token'
}

curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Hi, I'\''m Hiram, who are you?"'

curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Who am I?"'
```

**Expected behavior**: The agent now remembers context within the same conversation ID!

## Next Step

Continue to [checkpoint 03](../03-with-history/) to add conversation history recording.
