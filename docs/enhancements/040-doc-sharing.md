---
status: implemented
---

# Conversation Sharing Documentation

> **Status**: Implemented.

**Status**: 📋 Planned

## Overview

This enhancement adds comprehensive documentation for the conversation sharing and access control features of the memory service. Users will learn how to:
- Share conversations with other users using different access levels
- Manage conversation memberships (add, update, remove members)
- Transfer conversation ownership to other users
- Handle pending ownership transfer requests

The documentation will continue after the "Advanced Features" checkpoint and provide both Spring and Quarkus implementations with working curl examples.

## Goals

1. Document the sharing API features in both Spring and Quarkus tutorials
2. Explain the four access levels: owner, manager, writer, reader
3. Provide working curl examples for all sharing operations
4. Demonstrate both membership management and ownership transfer workflows
5. Show real-world usage patterns from the chat-frontend implementation
6. Create checkpoint 05 with complete sharing functionality

## Access Levels Explained

The memory service provides four hierarchical access levels:

| Access Level | Can Read | Can Write | Can Share | Can Transfer Ownership | Notes |
|-------------|----------|-----------|-----------|------------------------|-------|
| **owner** | ✅ | ✅ | ✅ | ✅ | Only one owner per conversation |
| **manager** | ✅ | ✅ | ✅ (limited) | ❌ | Can add/remove writer/reader members |
| **writer** | ✅ | ✅ | ❌ | ❌ | Can participate in conversations |
| **reader** | ✅ | ❌ | ❌ | ❌ | Read-only access |

**Sharing constraints:**
- Owners can assign any level except owner (manager, writer, reader)
- Managers can only assign writer or reader levels
- Writers and readers cannot share conversations
- Ownership transfers are separate from membership management

## Sharing Features

### 1. Conversation Memberships

Memberships control who has access to a conversation and at what level.

**Operations:**
- `GET /v1/conversations/{conversationId}/memberships` - List all members
- `POST /v1/conversations/{conversationId}/memberships` - Add a new member
- `PATCH /v1/conversations/{conversationId}/memberships/{userId}` - Update access level
- `DELETE /v1/conversations/{conversationId}/memberships/{userId}` - Remove a member

### 2. Ownership Transfers

Ownership can only be transferred through a two-step process (request + accept).

**Operations:**
- `GET /v1/ownership-transfers` - List pending transfers (with role filter)
- `POST /v1/ownership-transfers` - Initiate a transfer request
- `GET /v1/ownership-transfers/{transferId}` - Get transfer details
- `POST /v1/ownership-transfers/{transferId}/accept` - Accept a transfer (recipient only)
- `DELETE /v1/ownership-transfers/{transferId}` - Cancel/decline a transfer

**Transfer workflow:**
1. Owner initiates transfer to an existing member
2. Recipient receives pending transfer notification
3. Recipient can accept (becomes owner, previous owner becomes manager) or decline
4. Either party can cancel/delete the pending transfer

## Implementation Structure

### Checkpoint 05: Conversation Sharing

Building on checkpoint 04 (Advanced Features), we add sharing functionality to the proxy controller/resource.

**Spring (spring/examples/doc-checkpoints/05-sharing/):**
- Update `MemoryServiceProxyController` with membership endpoints
- Update `MemoryServiceProxyController` with ownership transfer endpoints (via proxy to SharingService)
- Test membership CRUD operations
- Test ownership transfer workflow

**Quarkus (quarkus/examples/doc-checkpoints/05-sharing/):**
- Update `MemoryServiceProxyResource` with membership endpoints
- Update `MemoryServiceProxyResource` with ownership transfer endpoints (via proxy to SharingService)
- Test membership CRUD operations
- Test ownership transfer workflow

## Documentation Content

### Spring Tutorial: Conversation Sharing

Location: `site/src/pages/docs/spring/sharing.mdx`

**Sections:**

#### 1. Prerequisites
- Starting from checkpoint 04 (Advanced Features)
- Memory service running with authentication
- Multiple test users in Keycloak (bob, alice, charlie)

#### 2. Understanding Access Levels
- Explain the four levels and their permissions
- Clarify ownership transfer vs. membership management

#### 3. Adding Membership Endpoints

The membership endpoints are already proxied through the `MemoryServiceProxyController` from checkpoint 04:

```java
@GetMapping("/{conversationId}/memberships")
public ResponseEntity<?> listConversationMemberships(@PathVariable String conversationId) {
    return proxy.listConversationMemberships(conversationId);
}

@PostMapping("/{conversationId}/memberships")
public ResponseEntity<?> shareConversation(
        @PathVariable String conversationId, @RequestBody String body) {
    return proxy.shareConversation(conversationId, body);
}

@PatchMapping("/{conversationId}/memberships/{userId}")
public ResponseEntity<?> updateConversationMembership(
        @PathVariable String conversationId,
        @PathVariable String userId,
        @RequestBody String body) {
    return proxy.updateConversationMembership(conversationId, userId, body);
}

@DeleteMapping("/{conversationId}/memberships/{userId}")
public ResponseEntity<?> deleteConversationMembership(
        @PathVariable String conversationId, @PathVariable String userId) {
    return proxy.deleteConversationMembership(conversationId, userId);
}
```

#### 4. Testing Membership Management

**List members:**
```bash
# As bob (owner), list members of the conversation
curl -sSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships \
  -H "Authorization: Bearer $(get-token bob bob)" | jq

# Response shows bob as owner:
# {
#   "data": [
#     {
#       "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
#       "userId": "bob",
#       "accessLevel": "owner"
#     }
#   ]
# }
```

**Add a member:**
```bash
# Share conversation with alice as a writer
curl -sSfX POST http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships \
  -H "Authorization: Bearer $(get-token bob bob)" \
  -H "Content-Type: application/json" \
  -d '{
    "userId": "alice",
    "accessLevel": "writer"
  }' | jq

# alice can now read and write to the conversation
curl -sSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Authorization: Bearer $(get-token alice alice)" \
  -H "Content-Type: application/json" \
  -d '"Hi from Alice!"'
```

**Update access level:**
```bash
# Promote alice from writer to manager
curl -sSfX PATCH http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships/alice \
  -H "Authorization: Bearer $(get-token bob bob)" \
  -H "Content-Type: application/json" \
  -d '{
    "accessLevel": "manager"
  }' | jq

# Now alice can share with others (but only assign writer/reader levels)
curl -sSfX POST http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships \
  -H "Authorization: Bearer $(get-token alice alice)" \
  -H "Content-Type: application/json" \
  -d '{
    "userId": "charlie",
    "accessLevel": "reader"
  }' | jq
```

**Remove a member:**
```bash
# Remove charlie from the conversation
curl -sSfX DELETE http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships/charlie \
  -H "Authorization: Bearer $(get-token bob bob)"

# charlie can no longer access the conversation
curl -sSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Authorization: Bearer $(get-token charlie charlie)"
# Returns 403 Forbidden
```

#### 5. Testing Ownership Transfers

The ownership transfer feature uses a separate API that requires the `SharingService` client. The endpoints should be added to a new controller or to the existing proxy controller.

**Note:** For the ownership transfer APIs, you'll need to add these proxy methods that weren't in checkpoint 04. Add to `MemoryServiceProxyController`:

```java
// Add these imports at the top of MemoryServiceProxyController.java
import org.springframework.web.bind.annotation.RequestParam;

// Add these methods to the class:

@GetMapping("/ownership-transfers")
public ResponseEntity<?> listPendingTransfers(
        @RequestParam(value = "role", required = false) String role) {
    return proxy.listPendingTransfers(role);
}

@PostMapping("/ownership-transfers")
public ResponseEntity<?> createOwnershipTransfer(@RequestBody String body) {
    return proxy.createOwnershipTransfer(body);
}

@GetMapping("/ownership-transfers/{transferId}")
public ResponseEntity<?> getTransfer(@PathVariable String transferId) {
    return proxy.getTransfer(transferId);
}

@PostMapping("/ownership-transfers/{transferId}/accept")
public ResponseEntity<?> acceptTransfer(@PathVariable String transferId) {
    return proxy.acceptTransfer(transferId);
}

@DeleteMapping("/ownership-transfers/{transferId}")
public ResponseEntity<?> deleteTransfer(@PathVariable String transferId) {
    return proxy.deleteTransfer(transferId);
}
```

**You'll also need to add these methods to the `MemoryServiceProxy` class** (in the memory-service-spring-boot-starter module):

```java
// In io.github.chirino.memoryservice.client.MemoryServiceProxy class:

public ResponseEntity<?> listPendingTransfers(String role) {
    String url = baseUrl + "/v1/ownership-transfers";
    if (role != null) {
        url += "?role=" + role;
    }
    return restClient.get().uri(url).retrieve().toEntity(String.class);
}

public ResponseEntity<?> createOwnershipTransfer(String body) {
    return restClient.post()
            .uri(baseUrl + "/v1/ownership-transfers")
            .contentType(MediaType.APPLICATION_JSON)
            .body(body)
            .retrieve()
            .toEntity(String.class);
}

public ResponseEntity<?> getTransfer(String transferId) {
    return restClient.get()
            .uri(baseUrl + "/v1/ownership-transfers/" + transferId)
            .retrieve()
            .toEntity(String.class);
}

public ResponseEntity<?> acceptTransfer(String transferId) {
    return restClient.post()
            .uri(baseUrl + "/v1/ownership-transfers/" + transferId + "/accept")
            .retrieve()
            .toEntity(String.class);
}

public ResponseEntity<?> deleteTransfer(String transferId) {
    return restClient.delete()
            .uri(baseUrl + "/v1/ownership-transfers/" + transferId)
            .retrieve()
            .toEntity(String.class);
}
```

**Initiate a transfer:**
```bash
# Bob (owner) wants to transfer ownership to alice
curl -sSfX POST http://localhost:9090/v1/ownership-transfers \
  -H "Authorization: Bearer $(get-token bob bob)" \
  -H "Content-Type: application/json" \
  -d '{
    "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
    "newOwnerUserId": "alice"
  }' | jq

# Response includes the transfer ID:
# {
#   "id": "f3a8b2c1-1234-5678-9abc-def012345678",
#   "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
#   "fromUserId": "bob",
#   "toUserId": "alice",
#   "createdAt": "2025-02-08T10:30:00Z"
# }
```

**List pending transfers:**
```bash
# Alice sees transfers where she is the recipient
curl -sSfX GET http://localhost:9090/v1/ownership-transfers?role=recipient \
  -H "Authorization: Bearer $(get-token alice alice)" | jq

# Bob sees transfers where he is the sender
curl -sSfX GET http://localhost:9090/v1/ownership-transfers?role=sender \
  -H "Authorization: Bearer $(get-token bob bob)" | jq

# List all (sent or received)
curl -sSfX GET http://localhost:9090/v1/ownership-transfers?role=all \
  -H "Authorization: Bearer $(get-token alice alice)" | jq
```

**Accept a transfer:**
```bash
# Alice accepts the transfer (use the transfer ID from the create response)
TRANSFER_ID="f3a8b2c1-1234-5678-9abc-def012345678"
curl -sSfX POST http://localhost:9090/v1/ownership-transfers/$TRANSFER_ID/accept \
  -H "Authorization: Bearer $(get-token alice alice)"

# Returns 204 No Content on success
# Now alice is the owner and bob is a manager
curl -sSfX GET http://localhost:9090/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships \
  -H "Authorization: Bearer $(get-token alice alice)" | jq

# Response:
# {
#   "data": [
#     { "userId": "alice", "accessLevel": "owner" },
#     { "userId": "bob", "accessLevel": "manager" }
#   ]
# }
```

**Decline/cancel a transfer:**
```bash
# Alice can decline the transfer (or bob can cancel it)
TRANSFER_ID="f3a8b2c1-1234-5678-9abc-def012345678"
curl -sSfX DELETE http://localhost:9090/v1/ownership-transfers/$TRANSFER_ID \
  -H "Authorization: Bearer $(get-token alice alice)"

# Returns 204 No Content
# The transfer is deleted and ownership remains with bob
```

#### 6. Testing Helper Functions

Add these helper functions to your shell for easier testing:

```bash
# Get access token for a user
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

# List memberships for a conversation
function list-members() {
  local conv_id=$1
  local username=${2:-bob}
  curl -sSfX GET http://localhost:9090/v1/conversations/$conv_id/memberships \
    -H "Authorization: Bearer $(get-token $username $username)" | jq
}

# Share conversation with a user
function share-conversation() {
  local conv_id=$1
  local target_user=$2
  local access_level=${3:-writer}
  local owner=${4:-bob}
  curl -sSfX POST http://localhost:9090/v1/conversations/$conv_id/memberships \
    -H "Authorization: Bearer $(get-token $owner $owner)" \
    -H "Content-Type: application/json" \
    -d "{\"userId\": \"$target_user\", \"accessLevel\": \"$access_level\"}" | jq
}
```

### Quarkus Tutorial: Conversation Sharing

Location: `site/src/pages/docs/quarkus/sharing.mdx`

**Same content as Spring but with Quarkus-specific code:**

The membership endpoints are already proxied in `MemoryServiceProxyResource` from checkpoint 04:

```java
@GET
@Path("/{conversationId}/memberships")
@Produces(MediaType.APPLICATION_JSON)
public Response listConversationMemberships(
        @PathParam("conversationId") String conversationId) {
    return proxy.listConversationMemberships(conversationId);
}

@POST
@Path("/{conversationId}/memberships")
@Consumes(MediaType.APPLICATION_JSON)
@Produces(MediaType.APPLICATION_JSON)
public Response shareConversation(
        @PathParam("conversationId") String conversationId, String body) {
    return proxy.shareConversation(conversationId, body);
}

@PATCH
@Path("/{conversationId}/memberships/{userId}")
@Consumes(MediaType.APPLICATION_JSON)
@Produces(MediaType.APPLICATION_JSON)
public Response updateConversationMembership(
        @PathParam("conversationId") String conversationId,
        @PathParam("userId") String userId,
        String body) {
    return proxy.updateConversationMembership(conversationId, userId, body);
}

@DELETE
@Path("/{conversationId}/memberships/{userId}")
public Response deleteConversationMembership(
        @PathParam("conversationId") String conversationId,
        @PathParam("userId") String userId) {
    return proxy.deleteConversationMembership(conversationId, userId);
}
```

**For ownership transfers, add these endpoints to `MemoryServiceProxyResource`:**

```java
@GET
@Path("/ownership-transfers")
@Produces(MediaType.APPLICATION_JSON)
public Response listPendingTransfers(@QueryParam("role") String role) {
    return proxy.listPendingTransfers(role);
}

@POST
@Path("/ownership-transfers")
@Consumes(MediaType.APPLICATION_JSON)
@Produces(MediaType.APPLICATION_JSON)
public Response createOwnershipTransfer(String body) {
    return proxy.createOwnershipTransfer(body);
}

@GET
@Path("/ownership-transfers/{transferId}")
@Produces(MediaType.APPLICATION_JSON)
public Response getTransfer(@PathParam("transferId") String transferId) {
    return proxy.getTransfer(transferId);
}

@POST
@Path("/ownership-transfers/{transferId}/accept")
@Produces(MediaType.APPLICATION_JSON)
public Response acceptTransfer(@PathParam("transferId") String transferId) {
    return proxy.acceptTransfer(transferId);
}

@DELETE
@Path("/ownership-transfers/{transferId}")
public Response deleteTransfer(@PathParam("transferId") String transferId) {
    return proxy.deleteTransfer(transferId);
}
```

**You'll also need to add these methods to the `MemoryServiceProxy` class** (in the memory-service-extension module):

```java
// In io.github.chirino.memory.runtime.MemoryServiceProxy class:

public Response listPendingTransfers(String role) {
    String url = "/v1/ownership-transfers";
    if (role != null) {
        url += "?role=" + role;
    }
    return client.get(url);
}

public Response createOwnershipTransfer(String body) {
    return client.post("/v1/ownership-transfers", body);
}

public Response getTransfer(String transferId) {
    return client.get("/v1/ownership-transfers/" + transferId);
}

public Response acceptTransfer(String transferId) {
    return client.post("/v1/ownership-transfers/" + transferId + "/accept", "");
}

public Response deleteTransfer(String transferId) {
    return client.delete("/v1/ownership-transfers/" + transferId);
}
```

**The curl examples are identical to the Spring version.**

## Implementation Steps

### Phase 1: Update Spring Boot Starter

1. **Add ownership transfer methods to `MemoryServiceProxy`**
   - File: `spring/memory-service-spring-boot-starter/src/main/java/io/github/chirino/memoryservice/client/MemoryServiceProxy.java`
   - Add methods: `listPendingTransfers`, `createOwnershipTransfer`, `getTransfer`, `acceptTransfer`, `deleteTransfer`
   - Verify builds: `./mvnw clean install -pl spring/memory-service-spring-boot-starter`

### Phase 2: Update Quarkus Extension

1. **Add ownership transfer methods to `MemoryServiceProxy`**
   - File: `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/runtime/MemoryServiceProxy.java`
   - Add methods: `listPendingTransfers`, `createOwnershipTransfer`, `getTransfer`, `acceptTransfer`, `deleteTransfer`
   - Verify builds: `./mvnw clean install -pl quarkus/memory-service-extension`

### Phase 3: Create Spring Checkpoint 05

1. **Copy checkpoint 04 to 05**
   ```bash
   cp -r spring/examples/doc-checkpoints/04-advanced-features spring/examples/doc-checkpoints/05-sharing
   ```

2. **Update `MemoryServiceProxyController`**
   - Add ownership transfer endpoints at the class level (not under `/v1/conversations`)
   - The membership endpoints already exist from checkpoint 04

3. **Verify builds and test**
   ```bash
   cd spring/examples/doc-checkpoints/05-sharing
   ./mvnw clean compile
   ./mvnw spring-boot:run
   # Test with curl commands from documentation
   ```

4. **Create README.md** in checkpoint directory

### Phase 4: Create Quarkus Checkpoint 05

1. **Copy checkpoint 04 to 05**
   ```bash
   cp -r quarkus/examples/doc-checkpoints/04-advanced-features quarkus/examples/doc-checkpoints/05-sharing
   ```

2. **Update `MemoryServiceProxyResource`**
   - Add ownership transfer endpoints at the class level (not under `/v1/conversations`)
   - The membership endpoints already exist from checkpoint 04

3. **Verify builds and test**
   ```bash
   cd quarkus/examples/doc-checkpoints/05-sharing
   ./mvnw clean compile
   ./mvnw quarkus:dev
   # Test with curl commands from documentation
   ```

4. **Create README.md** in checkpoint directory

### Phase 5: Create Documentation Pages

1. **Create `site/src/pages/docs/spring/sharing.mdx`**
   - Follow the structure outlined above
   - Include all curl examples
   - Add navigation links to previous/next guides

2. **Create `site/src/pages/docs/quarkus/sharing.mdx`**
   - Same content as Spring with Quarkus-specific code
   - Include all curl examples
   - Add navigation links

3. **Update navigation**
   - Update `site/src/layouts/DocsLayout.astro` to include sharing links
   - Ensure proper ordering after Advanced Features

### Phase 6: Update Existing Documentation

1. **Update `site/src/pages/docs/spring/advanced-features.mdx`**
   - Add "Next Steps" section at the end pointing to Sharing guide

2. **Update `site/src/pages/docs/quarkus/advanced-features.mdx`**
   - Add "Next Steps" section at the end pointing to Sharing guide

3. **Update checkpoint 04 references**
   - Add note that checkpoint 05 includes sharing features

## Testing Strategy

### Functional Testing

For each framework (Spring and Quarkus):

1. **Membership Management**
   - List memberships for a new conversation (should show only owner)
   - Add a member with each access level (owner cannot be assigned)
   - Update member access levels (test promotion/demotion)
   - Remove members
   - Verify permission enforcement (manager cannot assign manager level)

2. **Ownership Transfer**
   - Initiate transfer as owner
   - List pending transfers (as sender and recipient)
   - Accept transfer (verify ownership change and previous owner becomes manager)
   - Initiate new transfer and decline it
   - Initiate new transfer and cancel it (as sender)
   - Try to initiate duplicate transfer (should fail with 409)

3. **Permission Enforcement**
   - Verify readers cannot write
   - Verify writers cannot share
   - Verify managers can only assign writer/reader
   - Verify non-owners cannot transfer ownership

### Multi-User Testing

Test with three Keycloak users (bob, alice, charlie):

```bash
# Setup test users in Keycloak
# bob: owner
# alice: manager
# charlie: writer/reader

# Test collaboration workflow
CONV_ID=$(uuidgen)

# Bob creates conversation and shares with alice
curl -sSfX POST http://localhost:9090/chat/$CONV_ID \
  -H "Authorization: Bearer $(get-token bob bob)" \
  -H "Content-Type: application/json" \
  -d '"Start conversation"'

curl -sSfX POST http://localhost:9090/v1/conversations/$CONV_ID/memberships \
  -H "Authorization: Bearer $(get-token bob bob)" \
  -H "Content-Type: application/json" \
  -d '{"userId": "alice", "accessLevel": "manager"}'

# Alice adds charlie as reader
curl -sSfX POST http://localhost:9090/v1/conversations/$CONV_ID/memberships \
  -H "Authorization: Bearer $(get-token alice alice)" \
  -H "Content-Type: application/json" \
  -d '{"userId": "charlie", "accessLevel": "reader"}'

# Charlie can read but not write
curl -sSfX GET http://localhost:9090/v1/conversations/$CONV_ID/entries \
  -H "Authorization: Bearer $(get-token charlie charlie)" | jq

curl -sSfX POST http://localhost:9090/chat/$CONV_ID \
  -H "Authorization: Bearer $(get-token charlie charlie)" \
  -H "Content-Type: application/json" \
  -d '"Try to write"'
# Should fail with 403 Forbidden
```

## Success Criteria

- ✅ Spring Boot starter has ownership transfer proxy methods
- ✅ Quarkus extension has ownership transfer proxy methods
- ✅ Checkpoint 05 builds and runs for both Spring and Quarkus
- ✅ All curl examples work correctly
- ✅ Documentation clearly explains access levels and permissions
- ✅ Ownership transfer workflow is well documented with examples
- ✅ Multi-user testing scenarios pass
- ✅ Navigation updated to include sharing guides

## Benefits

1. **Complete Feature Coverage**: Users learn about all memory service features including sharing
2. **Collaboration Workflows**: Enables building multi-user applications with proper access control
3. **Real-World Patterns**: Examples match the chat-frontend implementation patterns
4. **Security Awareness**: Users understand access level implications and constraints
5. **Testing Confidence**: Comprehensive curl examples let users verify their implementation

## Maintenance Notes

**When sharing APIs change:**
1. Update MemoryServiceProxy in both Spring and Quarkus
2. Update checkpoint 05 implementations
3. Update curl examples in documentation
4. Re-test all multi-user scenarios

**When access level semantics change:**
1. Update access level table in documentation
2. Review and update permission enforcement examples
3. Update related error messages in examples

## Related Enhancements

- [Enhancement 038: Tutorial Checkpoints](./038-tutorial-checkpoints.md) - Checkpoint structure this builds upon
- [Enhancement 012: Spring Site Docs](./012-spring-site-docs.md) - Documentation site structure
- [Enhancement 023: Chat App Implementation](./023-chat-app-implementation.md) - Reference implementation using sharing APIs

## Frontend Reference

The chat-frontend implementation provides excellent reference patterns:

- **Hooks**: `frontends/chat-frontend/src/hooks/useSharing.ts` - React hooks for all sharing operations
- **Components**: `frontends/chat-frontend/src/components/sharing/` - UI components for share modal, transfer management
- **Utilities**: Helper functions for permission checking (`canManageMembers`, `canTransferOwnership`, `getAssignableAccessLevels`)

These patterns can inform documentation examples and help users understand real-world usage.
