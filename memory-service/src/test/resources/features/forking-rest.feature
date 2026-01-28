Feature: Conversation Forking REST API
  As a user
  I want to fork conversations at specific entries via REST API
  So that I can create alternative conversation branches

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Base Conversation"
    And the conversation has an entry "First entry"
    And the conversation has an entry "Second entry"
    And the conversation has an entry "Third entry"

  Scenario: Fork a conversation at an entry
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${secondEntryId}" with request:
    """
    {
      "title": "Forked Conversation"
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "title": "Forked Conversation",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner",
      "forkedAtEntryId": "${firstEntryId}",
      "forkedAtConversationId": "${conversationId}"
    }
    """

  Scenario: Fork a conversation without title
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {}
    """
    Then the response status should be 201
    And the response body should contain "id"
    And set "forkedConversationId" to the json response field "id"
    When I get conversation "${forkedConversationId}"
    Then the response status should be 200
    And the response body should contain "forkedAtConversationId"

  Scenario: List forks for a conversation
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${secondEntryId}" with request:
    """
    {
      "title": "Fork 1"
    }
    """
    And set "fork1Id" to the json response field "id"
    When I fork the conversation at entry "${secondEntryId}" with request:
    """
    {
      "title": "Fork 2"
    }
    """
    And set "fork2Id" to the json response field "id"
    When I list forks for the conversation
    Then the response status should be 200
    And the response should contain at least 3 conversations
    # The response includes the original conversation (no forkedAtEntryId) plus the 2 forks
    # Verify: original at [0] has no forkedAtEntryId, forks at [1] and [2] have forkedAtEntryId
    And the response body should contain "forkedAtEntryId"
    And the response body "data[0].conversationId" should be "${conversationId}"
    And the response body "data[0].forkedAtEntryId" should be "null"
    And the response body "data[1].forkedAtEntryId" should be "${firstEntryId}"
    And the response body "data[1].forkedAtConversationId" should be "${conversationId}"
    And the response body "data[2].forkedAtEntryId" should be "${firstEntryId}"
    And the response body "data[2].forkedAtConversationId" should be "${conversationId}"

  Scenario: Fork non-existent conversation
    When I fork conversation "00000000-0000-0000-0000-000000000000" at entry "00000000-0000-0000-0000-000000000001" with request:
    """
    {
      "title": "Fork"
    }
    """
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Fork at non-existent entry
    When I fork the conversation at entry "00000000-0000-0000-0000-000000000000" with request:
    """
    {
      "title": "Fork"
    }
    """
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Fork conversation without access
    Given there is a conversation owned by "bob"
    When I fork that conversation at entry "00000000-0000-0000-0000-000000000000" with request:
    """
    {
      "title": "Fork"
    }
    """
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: List forks for non-existent conversation
    When I list forks for conversation "00000000-0000-0000-0000-000000000000"
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: List forks without access
    Given there is a conversation owned by "bob"
    When I list forks for that conversation
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Forked conversation shares membership with root
    Given I share the conversation with user "bob" and access level "reader"
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {
      "title": "Forked Conversation"
    }
    """
    And set "forkConversationId" to "${response.body.id}"
    And I authenticate as user "bob"
    When I get conversation "${forkConversationId}"
    Then the response status should be 200
