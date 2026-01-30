Feature: Ownership Transfers REST API
  As a conversation owner
  I want to transfer ownership to other users via REST API
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

  Scenario: Owner can create ownership transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And the response body "fromUserId" should be "alice"
    And the response body "toUserId" should be "bob"
    And the response body "conversationId" should be "${conversationId}"

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
    And the response should contain error code "forbidden"

  Scenario: Cannot create second transfer while one is pending
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "existingTransferId" to "${response.body.id}"
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 409
    And the response body should contain "existingTransferId"
    And the response body "code" should be "TRANSFER_ALREADY_PENDING"

  Scenario: Cannot transfer to non-member
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "stranger"
    }
    """
    Then the response status should be 400
    And the response should contain error code "bad_request"

  Scenario: Cannot transfer to self
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "alice"
    }
    """
    Then the response status should be 400
    And the response should contain error code "bad_request"

  # ===== List Transfers =====

  Scenario: List transfers as sender
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    When I call GET "/v1/ownership-transfers?role=sender"
    Then the response status should be 200
    And the response body "data" should have at least 1 item

  Scenario: List transfers as recipient
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    Given I am authenticated as user "bob"
    When I call GET "/v1/ownership-transfers?role=recipient"
    Then the response status should be 200
    And the response body "data" should have at least 1 item

  Scenario: List all transfers
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    When I call GET "/v1/ownership-transfers"
    Then the response status should be 200
    And the response body "data" should have at least 1 item

  # ===== Get Transfer =====

  Scenario: Sender can get transfer details
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    When I call GET "/v1/ownership-transfers/${transferId}"
    Then the response status should be 200
    And the response body "id" should be "${transferId}"

  Scenario: Recipient can get transfer details
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    Given I am authenticated as user "bob"
    When I call GET "/v1/ownership-transfers/${transferId}"
    Then the response status should be 200
    And the response body "id" should be "${transferId}"

  Scenario: Non-participant cannot see transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    Given I am authenticated as user "charlie"
    When I call GET "/v1/ownership-transfers/${transferId}"
    Then the response status should be 404

  # ===== Accept Transfer =====

  Scenario: Recipient can accept transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    Given I am authenticated as user "bob"
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the response status should be 204
    # Verify ownership changed
    When I list memberships for the conversation
    Then the response status should be 200
    And the response should contain a membership for user "bob" with access level "owner"
    And the response should contain a membership for user "alice" with access level "manager"

  Scenario: Sender cannot accept own transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Cannot accept already-accepted transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    Given I am authenticated as user "bob"
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the response status should be 204
    # Transfer is deleted after acceptance, so second accept returns 404
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the response status should be 404

  # ===== Delete Transfer =====

  Scenario: Sender can cancel (delete) transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    When I call DELETE "/v1/ownership-transfers/${transferId}"
    Then the response status should be 204
    # Verify hard deleted
    When I call GET "/v1/ownership-transfers/${transferId}"
    Then the response status should be 404

  Scenario: Recipient can decline (delete) transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    Given I am authenticated as user "bob"
    When I call DELETE "/v1/ownership-transfers/${transferId}"
    Then the response status should be 204

  Scenario: Non-participant cannot delete transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    Given I am authenticated as user "charlie"
    When I call DELETE "/v1/ownership-transfers/${transferId}"
    Then the response status should be 403
    And the response should contain error code "forbidden"

  # ===== Member Removal Cancels Transfer =====

  Scenario: Removing transfer recipient deletes pending transfer
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": "bob"
    }
    """
    Then the response status should be 201
    And set "transferId" to "${response.body.id}"
    # Verify transfer exists
    When I call GET "/v1/ownership-transfers/${transferId}"
    Then the response status should be 200
    # Remove bob from the conversation
    When I delete membership for user "bob"
    Then the response status should be 204
    # Verify transfer was automatically deleted
    When I call GET "/v1/ownership-transfers/${transferId}"
    Then the response status should be 404
