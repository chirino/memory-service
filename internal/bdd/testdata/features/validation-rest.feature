Feature: API Field Validation
  As a developer
  I want the API to reject invalid input with structured 400 responses
  So that callers get clear feedback on field constraints

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Validation Test"

  Scenario: Creating conversation with title exceeding 500 characters
    When I call POST "/v1/conversations" with body:
    """
    {
      "title": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaX"
    }
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"

  Scenario: Creating entry without contentType
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "content": [{"text": "Hello", "role": "USER"}]
    }
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"

  Scenario: Creating entry with more than 1000 content elements
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    And set "bigContent" to a JSON array of 1001 empty objects
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "contentType": "memory",
      "channel": "MEMORY",
      "content": ${bigContent}
    }
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"

  Scenario: Search with empty query
    When I call POST "/v1/conversations/search" with body:
    """
    {
      "query": ""
    }
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"

  Scenario: Search with limit of 0
    When I call POST "/v1/conversations/search" with body:
    """
    {
      "query": "test",
      "limit": 0
    }
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"

  Scenario: Share with empty userId
    When I call POST "/v1/conversations/${conversationId}/memberships" with body:
    """
    {
      "userId": "",
      "accessLevel": "reader"
    }
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"

  Scenario: Ownership transfer with empty newOwnerUserId
    When I call POST "/v1/ownership-transfers" with body:
    """
    {
      "conversationId": "${conversationId}",
      "newOwnerUserId": ""
    }
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"
