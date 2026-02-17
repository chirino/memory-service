Feature: Update Conversation REST API
  As a user
  I want to update conversation properties via REST API
  So that I can rename conversations to keep my history organized

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Original Title"

  Scenario: Update conversation title
    When I update the conversation with request:
    """
    {
      "title": "Updated Title"
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "id": "${conversationId}",
      "title": "Updated Title",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner"
    }
    """

  Scenario: Update title to null clears the title
    When I update the conversation with request:
    """
    {
      "title": null
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "id": "${conversationId}",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner"
    }
    """

  Scenario: Update non-existent conversation returns 404
    When I call PATCH "/v1/conversations/00000000-0000-0000-0000-000000000099" with body:
    """
    {
      "title": "New Title"
    }
    """
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Reader cannot update title
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "reader"
    }
    """
    Then the response status should be 201
    Given I am authenticated as user "bob"
    When I update the conversation with request:
    """
    {
      "title": "Bob's Title"
    }
    """
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Writer can update title
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "writer"
    }
    """
    Then the response status should be 201
    Given I am authenticated as user "bob"
    When I update the conversation with request:
    """
    {
      "title": "Writer Updated Title"
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "id": "${conversationId}",
      "title": "Writer Updated Title",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "writer"
    }
    """

  Scenario: Owner can update title
    When I update the conversation with request:
    """
    {
      "title": "Owner Updated Title"
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "id": "${conversationId}",
      "title": "Owner Updated Title",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner"
    }
    """
