Feature: Journal channel (REST)
  As an agent client
  I want to append opaque execution audit log entries to the journal channel
  So that I can record agent execution steps for replay and debugging

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "Journal Test Conversation"

  Scenario: Append a journal entry
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call", "model": "gpt-4"}]
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "conversationId": "${conversationId}",
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call", "model": "gpt-4"}],
      "createdAt": "${response.body.createdAt}"
    }
    """

  Scenario: Reject indexedContent on journal entries
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call"}],
      "indexedContent": "should not be allowed"
    }
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"

  Scenario: User (no client id) cannot append journal entries
    Given I am authenticated as user "alice"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call"}]
    }
    """
    Then the response status should be 403

  Scenario: List journal entries via channel filter
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "history",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Hello"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "tool_call", "tool": "search"}]
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations/${conversationId}/entries?channel=journal"
    Then the response status should be 200
    And the response should contain 1 entry
    And the response body should be json:
    """
    {
      "afterCursor": null,
      "data": [
        {
          "channel": "journal",
          "contentType": "agent/step",
          "content": [{"stepType": "tool_call", "tool": "search"}]
        }
      ]
    }
    """

  Scenario: User listing without channel filter does not expose journal entries
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "history",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Hello"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call"}]
    }
    """
    And the response status should be 201
    Given I am authenticated as user "alice"
    When I call GET "/v1/conversations/${conversationId}/entries"
    Then the response status should be 200
    And the response should contain 1 entry
    And the response body should be json:
    """
    {
      "afterCursor": null,
      "data": [
        {"channel": "history"}
      ]
    }
    """

  Scenario: Agent listing without channel filter includes journal entries
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "history",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Hello"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call"}]
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations/${conversationId}/entries"
    Then the response status should be 200
    And the response should contain 2 entries

  Scenario: Explicit channel=journal without client identity returns 403
    Given I am authenticated as user "alice"
    And the conversation exists
    When I call GET "/v1/conversations/${conversationId}/entries?channel=journal"
    Then the response status should be 403

  Scenario: User cannot list journal entries by forging clientId query parameter
    Given I am authenticated as user "alice"
    And the conversation exists
    When I call GET "/v1/conversations/${conversationId}/entries?channel=journal&clientId=test-agent"
    Then the response status should be 403

  Scenario: Explicit channel=context without client identity returns 403
    Given I am authenticated as user "alice"
    And the conversation exists
    When I call GET "/v1/conversations/${conversationId}/entries?channel=context"
    Then the response status should be 403

  Scenario: User cannot append journal entries by forging clientId query parameter
    Given I am authenticated as user "alice"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries?clientId=test-agent" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call"}]
    }
    """
    Then the response status should be 403

  Scenario: Client B cannot read Client A journal entries on a user-created conversation
    Given I am authenticated as user "alice"
    And I have a conversation with title "User-owned no-clientid conv"
    And I am authenticated as agent with API key "test-agent-key"
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call", "client": "a"}]
    }
    """
    And the response status should be 201
    And I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key-b"
    When I call GET "/v1/conversations/${conversationId}/entries?channel=journal"
    Then the response status should be 200
    And the response should contain 0 entries

  Scenario: Client B default listing cannot read Client A journal entries on a user-created conversation
    Given I am authenticated as user "alice"
    And I have a conversation with title "User-owned no-clientid default list"
    And I am authenticated as agent with API key "test-agent-key"
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "history",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Shared history"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call", "client": "a"}]
    }
    """
    And the response status should be 201
    And I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key-b"
    When I call GET "/v1/conversations/${conversationId}/entries"
    Then the response status should be 200
    And the response should contain 1 entry
    And the response body should be json:
    """
    {
      "afterCursor": null,
      "data": [
        {"channel": "history"}
      ]
    }
    """

  Scenario: Client B cannot append journal entries to a conversation owned by Client A
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key-b"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "journal",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call"}]
    }
    """
    Then the response status should be 403
