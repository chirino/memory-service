Feature: Ownership Transfers gRPC API
  As a conversation owner
  I want to transfer ownership to other users via gRPC API
  So that I can delegate conversation management

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Transfer Test Conversation"
    And I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "manager"
    }
    """

  # ===== Create Transfer =====

  Scenario: Owner can create ownership transfer via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "fromUserId" should be "alice"
    And the gRPC response field "toUserId" should be "bob"
    And the gRPC response field "conversationId" should be "${conversationId}"

  Scenario: Cannot create transfer if not owner via gRPC
    Given I am authenticated as user "bob"
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "charlie"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Cannot create second transfer while one is pending via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should have status "ALREADY_EXISTS"

  Scenario: Cannot transfer to non-member via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "stranger"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Cannot transfer to self via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "alice"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  # ===== List Transfers =====

  Scenario: List transfers as sender via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "OwnershipTransfersService/ListOwnershipTransfers" with body:
    """
    role: SENDER
    """
    Then the gRPC response should not have an error
    And the gRPC response field "transfers" should not be null

  Scenario: List transfers as recipient via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    Given I am authenticated as user "bob"
    When I send gRPC request "OwnershipTransfersService/ListOwnershipTransfers" with body:
    """
    role: RECIPIENT
    """
    Then the gRPC response should not have an error
    And the gRPC response field "transfers" should not be null

  Scenario: List all transfers via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "OwnershipTransfersService/ListOwnershipTransfers" with body:
    """
    """
    Then the gRPC response should not have an error
    And the gRPC response field "transfers" should not be null

  # ===== Get Transfer =====

  Scenario: Sender can get transfer details via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    When I send gRPC request "OwnershipTransfersService/GetOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${transferId}"

  Scenario: Recipient can get transfer details via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    Given I am authenticated as user "bob"
    When I send gRPC request "OwnershipTransfersService/GetOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${transferId}"

  Scenario: Non-participant cannot see transfer via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    Given I am authenticated as user "charlie"
    When I send gRPC request "OwnershipTransfersService/GetOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should have status "NOT_FOUND"

  # ===== Accept Transfer =====

  Scenario: Recipient can accept transfer via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    Given I am authenticated as user "bob"
    When I send gRPC request "OwnershipTransfersService/AcceptOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should not have an error
    # Verify ownership changed
    When I send gRPC request "ConversationMembershipsService/ListMemberships" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error

  Scenario: Sender cannot accept own transfer via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    When I send gRPC request "OwnershipTransfersService/AcceptOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Cannot accept already-accepted transfer via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    Given I am authenticated as user "bob"
    When I send gRPC request "OwnershipTransfersService/AcceptOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should not have an error
    # Transfer is deleted after acceptance, so second accept returns NOT_FOUND
    When I send gRPC request "OwnershipTransfersService/AcceptOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should have status "NOT_FOUND"

  # ===== Delete Transfer =====

  Scenario: Sender can cancel (delete) transfer via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    When I send gRPC request "OwnershipTransfersService/DeleteOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should not have an error
    # Verify hard deleted
    When I send gRPC request "OwnershipTransfersService/GetOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Recipient can decline (delete) transfer via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    Given I am authenticated as user "bob"
    When I send gRPC request "OwnershipTransfersService/DeleteOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should not have an error

  Scenario: Non-participant cannot delete transfer via gRPC
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    Given I am authenticated as user "charlie"
    When I send gRPC request "OwnershipTransfersService/DeleteOwnershipTransfer" with body:
    """
    transfer_id: "${transferId}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"
