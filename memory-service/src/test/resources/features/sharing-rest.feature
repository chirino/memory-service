Feature: Conversation Sharing REST API
  As a user
  I want to share conversations with other users and transfer ownership via REST API
  So that I can collaborate on conversations and manage access

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Shared Conversation"

  Scenario: Share a conversation with a user
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "writer"
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "conversationId": "${conversationId}",
      "userId": "bob",
      "accessLevel": "writer",
      "createdAt": "${response.body.createdAt}"
    }
    """

  Scenario: Share a conversation with reader access
    When I share the conversation with user "charlie" with request:
    """
    {
      "userId": "charlie",
      "accessLevel": "reader"
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "conversationId": "${conversationId}",
      "userId": "charlie",
      "accessLevel": "reader",
      "createdAt": "${response.body.createdAt}"
    }
    """

  Scenario: Share a conversation with manager access
    When I share the conversation with user "dave" with request:
    """
    {
      "userId": "dave",
      "accessLevel": "manager"
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "conversationId": "${conversationId}",
      "userId": "dave",
      "accessLevel": "manager",
      "createdAt": "${response.body.createdAt}"
    }
    """

  Scenario: List conversation memberships
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "writer"
    }
    """
    When I list memberships for the conversation
    Then the response status should be 200
    And the response should contain at least 2 memberships
    And the response body should be json:
    """
    {
      "data": [
        {
          "conversationId": "${conversationId}",
          "userId": "${response.body.data[0].userId}",
          "accessLevel": "${response.body.data[0].accessLevel}",
          "createdAt": "${response.body.data[0].createdAt}"
        },
        {
          "conversationId": "${conversationId}",
          "userId": "${response.body.data[1].userId}",
          "accessLevel": "${response.body.data[1].accessLevel}",
          "createdAt": "${response.body.data[1].createdAt}"
        }
      ]
    }
    """
    And the response should contain a membership for user "alice" with access level "owner"
    And the response should contain a membership for user "bob" with access level "writer"

  Scenario: Update membership access level
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "reader"
    }
    """
    When I update membership for user "bob" with request:
    """
    {
      "accessLevel": "writer"
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "conversationId": "${conversationId}",
      "userId": "bob",
      "accessLevel": "writer",
      "createdAt": "${response.body.createdAt}"
    }
    """

  Scenario: Delete a membership
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "writer"
    }
    """
    When I delete membership for user "bob"
    Then the response status should be 204
    When I list memberships for the conversation
    Then the response should contain 1 membership
    And the response should not contain a membership for user "bob"

  Scenario: Share non-existent conversation
    When I share conversation "00000000-0000-0000-0000-000000000000" with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "writer"
    }
    """
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Share conversation without access
    Given there is a conversation owned by "bob"
    When I share that conversation with user "charlie" with request:
    """
    {
      "userId": "charlie",
      "accessLevel": "writer"
    }
    """
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: List memberships as a reader
    Given there is a conversation owned by "bob"
    And I am authenticated as user "charlie"
    And the conversation is shared with user "charlie" with access level "reader"
    When I list memberships for that conversation
    Then the response status should be 200
    And the response should contain at least 2 memberships

  Scenario: Update membership without manager access
    Given there is a conversation owned by "bob"
    And I am authenticated as user "charlie"
    And the conversation is shared with user "charlie" with access level "writer"
    When I update membership for user "dave" with request:
    """
    {
      "accessLevel": "reader"
    }
    """
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Delete membership without manager access
    Given there is a conversation owned by "bob"
    And I am authenticated as user "charlie"
    And the conversation is shared with user "charlie" with access level "writer"
    When I delete membership for user "dave"
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Membership response contains conversationId instead of conversationGroupId
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "writer"
    }
    """
    Then the response status should be 201
    And the response body should contain "conversationId"
    And the response body should not contain "conversationGroupId"
    And the response body "conversationId" should be "${conversationId}"

  Scenario: Sharing via a fork applies to all conversations in the fork tree
    Given I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "entryId" to "${response.body.id}"
    When I fork the conversation at entry "${entryId}"
    And set "forkConversationId" to "${response.body.id}"
    And I share conversation "${forkConversationId}" with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "reader"
    }
    """
    And I authenticate as user "bob"
    When I get conversation "${rootConversationId}"
    Then the response status should be 200

  Scenario: Listing memberships from a fork returns the same memberships as the root
    Given I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"
    And I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "writer"
    }
    """
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "entryId" to "${response.body.id}"
    When I fork the conversation at entry "${entryId}"
    And set "forkConversationId" to "${response.body.id}"
    When I list memberships for conversation "${forkConversationId}"
    Then the response status should be 200
    And the response should contain at least 2 memberships
    And the response should contain a membership for user "alice" with access level "owner"
    And the response should contain a membership for user "bob" with access level "writer"
