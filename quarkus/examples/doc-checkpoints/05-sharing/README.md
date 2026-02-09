# Checkpoint 05: Conversation Sharing

This checkpoint adds conversation sharing and ownership transfer features.

## What's Included

- Membership management endpoints for sharing conversations
- Ownership transfer endpoints for transferring conversation ownership
- Support for four access levels: owner, manager, writer, reader
- Two-step ownership transfer workflow (request + accept)

## Tutorial Reference

This checkpoint corresponds to the [Conversation Sharing](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/quarkus/sharing.mdx) guide.

## Prerequisites

- Same as checkpoint 03

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw quarkus:dev
```

## Access Levels

| Level | Can Read | Can Write | Can Share | Can Transfer Ownership |
|-------|----------|-----------|-----------|------------------------|
| owner | ✅ | ✅ | ✅ | ✅ |
| manager | ✅ | ✅ | ✅ (limited) | ❌ |
| writer | ✅ | ✅ | ❌ | ❌ |
| reader | ✅ | ❌ | ❌ | ❌ |

**Sharing constraints:**
- Owners can assign any level except owner (manager, writer, reader)
- Managers can only assign writer or reader levels
- Writers and readers cannot share conversations
- Ownership transfers are separate from membership management

## Testing

```bash
# Helper function to get access token
function get-token() {
  local username=${1:-bob}
  local password=${2:-$username}
  curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "client_id=memory-service-client" \
    -d "client_secret=change-me" \
    -d "grant_type=password" \
    -d "username=$username" \
    -d "password=$password" \
    | jq -r '.access_token'
}

# List memberships
curl -sSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships \
  -H "Authorization: Bearer $(get-token bob bob)" | jq

# Share conversation with alice as writer
curl -sSfX POST http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships \
  -H "Authorization: Bearer $(get-token bob bob)" \
  -H "Content-Type: application/json" \
  -d '{"userId": "alice", "accessLevel": "writer"}' | jq

# Update alice to manager
curl -sSfX PATCH http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships/alice \
  -H "Authorization: Bearer $(get-token bob bob)" \
  -H "Content-Type: application/json" \
  -d '{"accessLevel": "manager"}' | jq

# Initiate ownership transfer
curl -sSfX POST http://localhost:9090/v1/ownership-transfers \
  -H "Authorization: Bearer $(get-token bob bob)" \
  -H "Content-Type: application/json" \
  -d '{
    "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
    "newOwnerUserId": "alice"
  }' | jq

# List pending transfers (as alice)
curl -sSfX GET http://localhost:9090/v1/ownership-transfers?role=recipient \
  -H "Authorization: Bearer $(get-token alice alice)" | jq

# Accept transfer (use transferId from create response)
TRANSFER_ID="<transfer-id-from-previous-response>"
curl -sSfX POST http://localhost:9090/v1/ownership-transfers/$TRANSFER_ID/accept \
  -H "Authorization: Bearer $(get-token alice alice)"
```

**Expected behavior**: Users can share conversations, manage memberships, and transfer ownership.

## Complete Example

For a production-ready example with frontend, see [quarkus/examples/chat-quarkus](../../chat-quarkus/).
