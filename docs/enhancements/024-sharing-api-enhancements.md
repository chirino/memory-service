# Enhancement 024: Sharing API Enhancements

## Overview

This document designs API enhancements for conversation ownership transfer functionality. The current `transfer-ownership` endpoint initiates a transfer, but there are no endpoints for the recipient to accept/reject the transfer or for either party to view pending transfers.

## Goals

1. Allow users to view pending ownership transfers they're involved in
2. Enable transfer recipients to accept or reject transfers
3. Allow owners to cancel pending transfers before acceptance
4. Prevent multiple concurrent transfers for the same conversation
5. Provide clear transfer state visibility to all parties

## Current State

The existing API has a single transfer endpoint:

```
POST /v1/conversations/{conversationId}/transfer-ownership
```

This endpoint will be **replaced** by `POST /v1/ownership-transfers` to consolidate all transfer operations under a single resource path.

The current API provides no mechanism for:
- Viewing pending transfers
- Accepting a transfer
- Rejecting a transfer
- Canceling a transfer

## Proposed API Endpoints

### 1. List Pending Transfers

List all pending ownership transfers involving the current user (as sender or recipient).

```yaml
/v1/ownership-transfers:
  get:
    tags: [Sharing]
    summary: List pending ownership transfers
    description: |-
      Returns all pending ownership transfers where the current user is either
      the current owner (sender) or the proposed new owner (recipient).
    operationId: listPendingTransfers
    parameters:
      - name: role
        in: query
        required: false
        description: Filter by user's role in the transfer.
        schema:
          type: string
          enum: [sender, recipient, all]
          default: all
    responses:
      '200':
        description: List of pending transfers.
        content:
          application/json:
            schema:
              type: object
              properties:
                data:
                  type: array
                  items:
                    $ref: '#/components/schemas/OwnershipTransfer'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []
```

### 2. Get Transfer Details

Get details of a specific transfer.

```yaml
/v1/ownership-transfers/{transferId}:
  get:
    tags: [Sharing]
    summary: Get transfer details
    description: |-
      Returns details of a specific ownership transfer. Only accessible to
      the current owner (sender) or proposed new owner (recipient).
    operationId: getTransfer
    parameters:
      - name: transferId
        in: path
        required: true
        description: Transfer identifier (UUID format).
        schema:
          type: string
          format: uuid
    responses:
      '200':
        description: Transfer details.
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/OwnershipTransfer'
      '404':
        $ref: '#/components/responses/NotFound'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []
```

### 3. Accept Transfer

Accept a pending ownership transfer (recipient only).

```yaml
/v1/ownership-transfers/{transferId}/accept:
  post:
    tags: [Sharing]
    summary: Accept ownership transfer
    description: |-
      Accepts a pending ownership transfer. Only the proposed new owner
      (recipient) can accept. Upon acceptance:
      - The recipient becomes the new owner
      - The previous owner becomes a manager
      - The transfer record is marked as completed
    operationId: acceptTransfer
    parameters:
      - name: transferId
        in: path
        required: true
        description: Transfer identifier (UUID format).
        schema:
          type: string
          format: uuid
    responses:
      '200':
        description: Transfer accepted successfully.
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/OwnershipTransfer'
      '403':
        description: Not authorized (not the recipient).
        $ref: '#/components/responses/Error'
      '404':
        $ref: '#/components/responses/NotFound'
      '409':
        description: Transfer already completed or cancelled.
        $ref: '#/components/responses/Error'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []
```

### 4. Delete Transfer (Cancel or Reject)

Cancel or reject a pending ownership transfer. Either the sender (owner) or recipient can delete.

```yaml
/v1/ownership-transfers/{transferId}:
  delete:
    tags: [Sharing]
    summary: Cancel or reject ownership transfer
    description: |-
      Deletes a pending ownership transfer. Can be called by either:
      - The current owner (sender) to cancel the transfer
      - The proposed new owner (recipient) to reject the transfer

      The transfer record is **hard deleted** from the database.
      This action is recorded in the audit log.
    operationId: deleteTransfer
    parameters:
      - name: transferId
        in: path
        required: true
        description: Transfer identifier (UUID format).
        schema:
          type: string
          format: uuid
    responses:
      '204':
        description: Transfer deleted successfully.
      '403':
        description: Not authorized (not the sender or recipient).
        $ref: '#/components/responses/Error'
      '404':
        $ref: '#/components/responses/NotFound'
      '409':
        description: Transfer already completed (accepted).
        $ref: '#/components/responses/Error'
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []
```

### 5. Create Ownership Transfer

Create a new ownership transfer request.

```yaml
/v1/ownership-transfers:
  post:
    tags: [Sharing]
    summary: Request ownership transfer
    description: |-
      Initiates a transfer of conversation ownership to another user.

      **Constraints**:
      - Only the current owner can initiate a transfer
      - The recipient must be an existing member of the conversation
      - Only one pending transfer can exist per conversation at a time
      - If a pending transfer already exists, returns 409 Conflict

      The recipient must accept the transfer via `POST /v1/ownership-transfers/{transferId}/accept`
      for the transfer to complete. Either party can delete the pending transfer
      via `DELETE /v1/ownership-transfers/{transferId}`.
    operationId: createOwnershipTransfer
    requestBody:
      required: true
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/CreateOwnershipTransferRequest'
    responses:
      '201':
        description: Transfer initiated successfully.
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/OwnershipTransfer'
      '403':
        description: Not authorized (not the owner).
        $ref: '#/components/responses/Error'
      '404':
        $ref: '#/components/responses/NotFound'
      '409':
        description: A pending transfer already exists for this conversation.
        content:
          application/json:
            schema:
              type: object
              properties:
                error:
                  type: string
                  example: "A pending ownership transfer already exists for this conversation"
                code:
                  type: string
                  example: "TRANSFER_ALREADY_PENDING"
                existingTransferId:
                  type: string
                  format: uuid
                  description: ID of the existing pending transfer
      default:
        $ref: '#/components/responses/Error'
    security:
      - BearerAuth: []
```

## New Schemas

### OwnershipTransfer

```yaml
OwnershipTransfer:
  type: object
  required:
    - id
    - conversationId
    - fromUserId
    - toUserId
    - status
    - createdAt
  properties:
    id:
      type: string
      format: uuid
      description: Unique identifier for the transfer.
    conversationId:
      type: string
      format: uuid
      description: The conversation being transferred.
    conversationTitle:
      type: string
      nullable: true
      description: Title of the conversation (for display purposes).
    fromUserId:
      type: string
      description: Current owner initiating the transfer.
    toUserId:
      type: string
      description: Proposed new owner (recipient).
    status:
      $ref: '#/components/schemas/TransferStatus'
    createdAt:
      type: string
      format: date-time
      description: When the transfer was initiated.
    completedAt:
      type: string
      format: date-time
      nullable: true
      description: When the transfer was accepted (null while pending).
  example:
    id: "7c9e6679-7425-40de-944b-e07fc1f90ae7"
    conversationId: "550e8400-e29b-41d4-a716-446655440000"
    conversationTitle: "Help with React hooks"
    fromUserId: "user_john_doe"
    toUserId: "user_jane_smith"
    status: "pending"
    createdAt: "2025-01-28T10:30:00Z"
    completedAt: null
```

### TransferStatus

```yaml
TransferStatus:
  type: string
  description: |-
    Status of an ownership transfer.
    - `pending`: Transfer initiated, awaiting recipient action
    - `accepted`: Transfer completed, ownership changed

    Note: Rejected/cancelled transfers are hard deleted (no status needed).
  enum:
    - pending
    - accepted
```

### CreateOwnershipTransferRequest

```yaml
CreateOwnershipTransferRequest:
  type: object
  required:
    - conversationId
    - newOwnerUserId
  properties:
    conversationId:
      type: string
      format: uuid
      description: The conversation to transfer ownership of.
    newOwnerUserId:
      type: string
      description: User ID of the proposed new owner. Must be an existing member.
  example:
    conversationId: "550e8400-e29b-41d4-a716-446655440000"
    newOwnerUserId: "user_jane_smith"
```

## Business Rules

### Transfer Lifecycle

```
┌─────────────┐
│   Created   │
│  (pending)  │
└──────┬──────┘
       │
       ├──────────────────┐
       │                  │
       ▼                  ▼
┌─────────────┐    ┌─────────────┐
│  Accepted   │    │  Deleted    │
│  (by recip) │    │ (by either) │
│             │    │ HARD DELETE │
│  Record     │    │             │
│  preserved  │    │  No record  │
└─────────────┘    └─────────────┘
```

### Permission Matrix

| Action | Owner (Sender) | Recipient | Other Members |
|--------|:--------------:|:---------:|:-------------:|
| Create transfer | ✅ | ❌ | ❌ |
| View transfer | ✅ | ✅ | ❌ |
| Accept transfer | ❌ | ✅ | ❌ |
| Delete transfer | ✅ | ✅ | ❌ |

### Constraints

1. **Single Pending Transfer**: Only one pending transfer can exist per conversation at a time. Attempting to create a second transfer returns `409 Conflict` with the existing transfer ID.

2. **Recipient Must Be Member**: The proposed new owner must already be a member of the conversation (any access level: manager, writer, or reader).

3. **Member Removal Cancels Transfer**: If a member is removed from the conversation while there is a pending ownership transfer to them, the transfer is automatically deleted. This ensures transfers cannot exist for non-members.

4. **Hard Delete on Cancel/Reject**: When a transfer is deleted (cancelled by owner or rejected by recipient), the transfer record is **hard deleted** from the database. Only accepted transfers are preserved.

5. **Owner Role on Acceptance**: When a transfer is accepted:
   - The recipient becomes the new owner
   - The previous owner is demoted to manager (not removed)

6. **Transfer Expiration** (Optional): Transfers may optionally expire after a configurable period (e.g., 7 days). Expired transfers are automatically hard deleted.

## Database Schema

### ownership_transfers Table

```sql
CREATE TABLE ownership_transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    from_user_id VARCHAR(255) NOT NULL,
    to_user_id VARCHAR(255) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,

    -- Only 'pending' and 'accepted' statuses exist
    -- Rejected/cancelled transfers are hard deleted
    CONSTRAINT valid_status CHECK (status IN ('pending', 'accepted')),
    CONSTRAINT different_users CHECK (from_user_id != to_user_id)
);

-- Ensure only one pending transfer per conversation
CREATE UNIQUE INDEX idx_one_pending_transfer_per_conversation
    ON ownership_transfers(conversation_id)
    WHERE status = 'pending';

-- Index for listing user's transfers
CREATE INDEX idx_transfers_by_user
    ON ownership_transfers(from_user_id, to_user_id, status);

-- Index for conversation lookup
CREATE INDEX idx_transfers_by_conversation
    ON ownership_transfers(conversation_id, status);
```

## Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `TRANSFER_ALREADY_PENDING` | 409 | A pending transfer already exists for this conversation |
| `TRANSFER_NOT_FOUND` | 404 | The specified transfer does not exist |
| `TRANSFER_ALREADY_ACCEPTED` | 409 | The transfer has already been accepted |
| `NOT_TRANSFER_RECIPIENT` | 403 | Only the recipient can accept a transfer |
| `NOT_TRANSFER_PARTICIPANT` | 403 | Only the sender or recipient can delete a transfer |
| `RECIPIENT_NOT_MEMBER` | 400 | The proposed new owner is not a member of the conversation |
| `CANNOT_TRANSFER_TO_SELF` | 400 | Cannot transfer ownership to yourself |

## Audit Logging

All ownership transfer and sharing operations MUST be recorded in the audit log, similar to Admin operations. This provides accountability and traceability for sensitive access control changes.

### Audited Operations

| Operation | Event Type | Details Logged |
|-----------|------------|----------------|
| Create transfer | `TRANSFER_CREATED` | conversationId, fromUserId, toUserId |
| Accept transfer | `TRANSFER_ACCEPTED` | transferId, conversationId, fromUserId, toUserId |
| Delete transfer | `TRANSFER_DELETED` | transferId, conversationId, deletedBy, wasRecipient |
| Add member | `MEMBER_ADDED` | conversationId, userId, accessLevel, addedBy |
| Update member | `MEMBER_UPDATED` | conversationId, userId, oldAccessLevel, newAccessLevel, updatedBy |
| Remove member | `MEMBER_REMOVED` | conversationId, userId, removedBy |

### Audit Log Entry Schema

```typescript
interface AuditLogEntry {
  id: string;
  timestamp: Date;
  eventType: string;
  actorUserId: string;       // Who performed the action
  conversationId?: string;
  targetUserId?: string;     // Who was affected (for membership changes)
  details: Record<string, unknown>;
}
```

### Rationale

1. **Compliance**: Audit logs support compliance requirements (SOC2, GDPR, etc.)
2. **Security**: Detect unauthorized access attempts or suspicious patterns
3. **Debugging**: Investigate issues with access control
4. **Accountability**: Track who made what changes and when

## Data Retention: Hard Deletes vs Soft Deletes

### Ownership Transfers: Hard Delete

Pending transfers that are cancelled/rejected are **hard deleted**:
- No business value in keeping rejected transfer requests
- Audit log captures the event for compliance/debugging
- Simplifies queries (no need to filter out cancelled transfers)
- Reduces storage requirements

Only **accepted** transfers are preserved in the database as a historical record of ownership changes.

### Memberships: Hard Delete (Recommended)

Given that all membership operations are audit logged, **soft deletes are not recommended** for memberships:

| Approach | Pros | Cons |
|----------|------|------|
| **Soft delete** | Can "undo" removals, historical queries | Complexity, storage, query filters everywhere |
| **Hard delete + audit log** | Simple, clean data, audit provides history | Cannot restore without re-adding |

**Recommendation**: Use hard deletes for memberships because:
1. The audit log captures the full history of who had access and when
2. Restoring access is as simple as re-adding the member
3. Simpler queries without `WHERE deleted_at IS NULL` everywhere
4. The audit log is the authoritative source for "who had access when"

If historical membership queries are needed (e.g., "who had access on date X"), query the audit log rather than maintaining soft-deleted records.

## UI Integration

The UI (Enhancement 025) will need to:

1. **Check for pending transfers** when opening the share modal
2. **Show transfer status** if a pending transfer exists:
   - For sender: Show "Transfer pending" with cancel option (calls DELETE)
   - For recipient: Show accept/decline buttons (accept calls POST, decline calls DELETE)
3. **Disable new transfer** button if a transfer is already pending
4. **Show incoming transfers** notification/badge (future enhancement)

## Implementation Notes

### Atomicity

The accept operation must be atomic:
1. Update transfer status to `accepted`
2. Update conversation owner to new user
3. Update previous owner's membership to `manager`

Use a database transaction to ensure all-or-nothing.

### Notifications (Future)

Consider adding notification support:
- Notify recipient when transfer is initiated
- Notify sender when transfer is accepted/rejected
- This could be via email, in-app notifications, or webhooks

## Testing Plan

### Existing Test Updates

The following existing Cucumber feature files need updates to reflect API changes:

| File | Changes Needed |
|------|----------------|
| `sharing-rest.feature` | Update membership tests for hard delete behavior; remove soft delete expectations |
| `conversations-rest.feature` | May need updates if conversation delete behavior changes |

### New Cucumber Feature Files

#### `ownership-transfers-rest.feature`

```gherkin
Feature: Ownership Transfers API

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And I share the conversation with "bob" as "manager"

  # ===== Create Transfer =====

  Scenario: Owner can create ownership transfer
    When I call POST "/v1/ownership-transfers" with body:
      """
      {
        "conversationId": "${conversationId}",
        "newOwnerUserId": "bob"
      }
      """
    Then the response status should be 201
    And the response body should contain:
      | fromUserId | alice   |
      | toUserId   | bob     |
      | status     | pending |

  Scenario: Cannot create transfer if not owner
    Given I am authenticated as user "bob"
    When I call POST "/v1/ownership-transfers" with body:
      """
      {
        "conversationId": "${conversationId}",
        "newOwnerUserId": "charlie"
      }
      """
    Then the response status should be 403

  Scenario: Cannot create second transfer while one is pending
    Given I have a pending ownership transfer to "bob"
    When I call POST "/v1/ownership-transfers" with body:
      """
      {
        "conversationId": "${conversationId}",
        "newOwnerUserId": "charlie"
      }
      """
    Then the response status should be 409
    And the response body should contain "existingTransferId"

  Scenario: Cannot transfer to non-member
    When I call POST "/v1/ownership-transfers" with body:
      """
      {
        "conversationId": "${conversationId}",
        "newOwnerUserId": "stranger"
      }
      """
    Then the response status should be 400

  Scenario: Cannot transfer to self
    When I call POST "/v1/ownership-transfers" with body:
      """
      {
        "conversationId": "${conversationId}",
        "newOwnerUserId": "alice"
      }
      """
    Then the response status should be 400

  # ===== List Transfers =====

  Scenario: List transfers as sender
    Given I have a pending ownership transfer to "bob"
    When I call GET "/v1/ownership-transfers?role=sender"
    Then the response status should be 200
    And the response body should contain 1 transfer

  Scenario: List transfers as recipient
    Given I am authenticated as user "bob"
    And user "alice" has a pending transfer to me for conversation "${conversationId}"
    When I call GET "/v1/ownership-transfers?role=recipient"
    Then the response status should be 200
    And the response body should contain 1 transfer

  Scenario: Non-participant cannot see transfer
    Given I have a pending ownership transfer to "bob"
    And I am authenticated as user "charlie"
    When I call GET "/v1/ownership-transfers/${transferId}"
    Then the response status should be 404

  # ===== Accept Transfer =====

  Scenario: Recipient can accept transfer
    Given I have a pending ownership transfer to "bob"
    And I am authenticated as user "bob"
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the response status should be 200
    And the response body should contain:
      | status | accepted |
    # Verify ownership changed
    When I call GET "/v1/conversations/${conversationId}/memberships"
    Then user "bob" should have access level "owner"
    And user "alice" should have access level "manager"

  Scenario: Sender cannot accept own transfer
    Given I have a pending ownership transfer to "bob"
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the response status should be 403

  Scenario: Cannot accept already-accepted transfer
    Given I have an accepted ownership transfer to "bob"
    And I am authenticated as user "bob"
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the response status should be 409

  # ===== Delete Transfer =====

  Scenario: Sender can cancel (delete) transfer
    Given I have a pending ownership transfer to "bob"
    When I call DELETE "/v1/ownership-transfers/${transferId}"
    Then the response status should be 204
    # Verify hard deleted
    When I call GET "/v1/ownership-transfers/${transferId}"
    Then the response status should be 404

  Scenario: Recipient can decline (delete) transfer
    Given I have a pending ownership transfer to "bob"
    And I am authenticated as user "bob"
    When I call DELETE "/v1/ownership-transfers/${transferId}"
    Then the response status should be 204

  Scenario: Non-participant cannot delete transfer
    Given I have a pending ownership transfer to "bob"
    And I am authenticated as user "charlie"
    When I call DELETE "/v1/ownership-transfers/${transferId}"
    Then the response status should be 403

  Scenario: Cannot delete accepted transfer
    Given I have an accepted ownership transfer to "bob"
    When I call DELETE "/v1/ownership-transfers/${transferId}"
    Then the response status should be 409
```

#### `sharing-audit-rest.feature`

```gherkin
Feature: Sharing Audit Logging

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Audited Conversation"

  Scenario: Member add is audit logged
    When I call POST "/v1/conversations/${conversationId}/memberships" with body:
      """
      {"userId": "bob", "accessLevel": "writer"}
      """
    Then the audit log should contain:
      | eventType   | MEMBER_ADDED |
      | actorUserId | alice        |
      | targetUserId| bob          |

  Scenario: Member update is audit logged
    Given I share the conversation with "bob" as "writer"
    When I call PATCH "/v1/conversations/${conversationId}/memberships/bob" with body:
      """
      {"accessLevel": "reader"}
      """
    Then the audit log should contain:
      | eventType       | MEMBER_UPDATED |
      | actorUserId     | alice          |
      | details.oldLevel| writer         |
      | details.newLevel| reader         |

  Scenario: Member remove is audit logged
    Given I share the conversation with "bob" as "writer"
    When I call DELETE "/v1/conversations/${conversationId}/memberships/bob"
    Then the audit log should contain:
      | eventType   | MEMBER_REMOVED |
      | actorUserId | alice          |
      | targetUserId| bob            |

  Scenario: Transfer create is audit logged
    Given I share the conversation with "bob" as "manager"
    When I call POST "/v1/ownership-transfers" with body:
      """
      {"conversationId": "${conversationId}", "newOwnerUserId": "bob"}
      """
    Then the audit log should contain:
      | eventType      | TRANSFER_CREATED |
      | actorUserId    | alice            |
      | details.toUserId| bob             |

  Scenario: Transfer accept is audit logged
    Given I share the conversation with "bob" as "manager"
    And I have a pending ownership transfer to "bob"
    And I am authenticated as user "bob"
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the audit log should contain:
      | eventType   | TRANSFER_ACCEPTED |
      | actorUserId | bob               |

  Scenario: Transfer delete is audit logged
    Given I share the conversation with "bob" as "manager"
    And I have a pending ownership transfer to "bob"
    When I call DELETE "/v1/ownership-transfers/${transferId}"
    Then the audit log should contain:
      | eventType            | TRANSFER_DELETED |
      | actorUserId          | alice            |
      | details.wasRecipient | false            |
```

### Test Checklist

#### Ownership Transfer API Tests

- [ ] Create transfer - success
- [ ] Create transfer - fails if not owner (403)
- [ ] Create transfer - fails if pending transfer exists (409)
- [ ] Create transfer - fails if recipient not a member (400)
- [ ] Create transfer - fails if transferring to self (400)
- [ ] List transfers - returns sender's pending transfers
- [ ] List transfers - returns recipient's pending transfers
- [ ] List transfers - filters by role parameter
- [ ] Get transfer - success for sender
- [ ] Get transfer - success for recipient
- [ ] Get transfer - fails for non-participant (404)
- [ ] Accept transfer - success (recipient)
- [ ] Accept transfer - fails if not recipient (403)
- [ ] Accept transfer - fails if already accepted (409)
- [ ] Accept transfer - correctly updates ownership
- [ ] Accept transfer - demotes previous owner to manager
- [ ] Delete transfer - success by sender (cancel)
- [ ] Delete transfer - success by recipient (decline)
- [ ] Delete transfer - fails if not participant (403)
- [ ] Delete transfer - fails if already accepted (409)
- [ ] Delete transfer - hard deletes record from database

#### Membership API Tests (Hard Delete)

- [ ] Add member - success
- [ ] Add member - fails if not owner/manager (403)
- [ ] Update member access level - success
- [ ] Update member - manager cannot update other managers
- [ ] Remove member - success
- [ ] Remove member - hard deletes (not soft delete)
- [ ] Remove member - manager cannot remove other managers

#### Audit Log Tests

- [ ] Transfer create is logged with correct details
- [ ] Transfer accept is logged with correct details
- [ ] Transfer delete is logged (with who deleted and wasRecipient flag)
- [ ] Member add is logged
- [ ] Member update is logged with old/new access levels
- [ ] Member remove is logged

#### Concurrency Tests

- [ ] Two simultaneous create requests - only one succeeds (unique index)
- [ ] Accept during delete - proper conflict handling
- [ ] Delete during accept - proper conflict handling
- [ ] Accept after conversation deleted - proper error

### Implementation Notes for Tests

1. **Step Definitions**: Add new step definitions for ownership transfer operations
2. **Test Data Setup**: Create helpers for setting up transfer scenarios
3. **Audit Log Verification**: Add steps to query and verify audit log entries
4. **Hard Delete Verification**: Add SQL verification steps to confirm records are truly deleted

## Impact on Data Eviction (Enhancement 016)

Since memberships will use **hard deletes** (not soft deletes), the eviction API and implementation need to be updated:

### Changes Required

1. **Remove `conversation_memberships` from evictable resource types**
   - Memberships are hard deleted immediately, so there's nothing to evict
   - The eviction endpoint should only accept `conversation_groups`

2. **Update OpenAPI admin spec** (`openapi-admin.yml`):
   ```yaml
   resourceTypes:
     type: array
     items:
       type: string
       enum:
         - conversation_groups
         # Remove: conversation_memberships
   ```

3. **Simplify eviction implementation**:
   - Remove `countEvictableMemberships()` method
   - Remove `hardDeleteMembershipsBatch()` method
   - Update `EvictionService` to only handle conversation groups

4. **Update eviction tests**:
   - Remove test scenarios for membership eviction
   - Update documentation to reflect simplified scope

### Rationale

- With audit logging capturing all membership changes, soft deletes provide no additional value
- Hard deletes simplify the data model and queries
- Eviction becomes simpler with only one resource type to handle

## References

- Sharing UI: [Enhancement 025](./025-sharing-ui.md)
- API Spec: `memory-service-contracts/src/main/resources/openapi.yml`
