Feature: Conversation client context via REST
  As multiple agent clients
  I want context access bound to the conversation clientId
  So that history can stay shared while context remains client-scoped

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "Client-scoped context"
    And the conversation has an entry "User visible history"

  Scenario: A different client cannot read context for the conversation
    When I append an entry with content "Planner memory" and channel "CONTEXT" and contentType "test.v1"
    Then the response status should be 201
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I list entries for the conversation with channel "CONTEXT"
    Then the response status should be 403

  Scenario: A different client cannot append context for the conversation
    Given I am authenticated as agent with API key "test-agent-key"
    When I append an entry with content "Planner memory" and channel "CONTEXT" and contentType "test.v1"
    Then the response status should be 201
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I append an entry with content "Other client memory" and channel "CONTEXT" and contentType "test.v1"
    Then the response status should be 403

  Scenario: A different client cannot sync context for the conversation
    Given I am authenticated as agent with API key "test-agent-key"
    When I append an entry with content "Planner memory" and channel "CONTEXT" and contentType "test.v1"
    Then the response status should be 201
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I sync context entries with request:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "content": [
        {
          "type": "text",
          "text": "Other client sync"
        }
      ]
    }
    """
    Then the response status should be 403

  Scenario: A different client can still append history when the user has access
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I append an entry with content "Shared history from another client" and channel "HISTORY" and contentType "history"
    Then the response status should be 201
    When I list entries for the conversation with channel "HISTORY"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 1 should have content "Shared history from another client"

  Scenario: Conversation clientId stays unchanged after history append from another client
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${conversationId}"
    Then the response status should be 200
    And the response body field "clientId" should not be null
    And set "originalClientId" to the json response field "clientId"
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I append an entry with content "Shared history from another client" and channel "HISTORY" and contentType "history"
    Then the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${conversationId}"
    Then the response status should be 200
    And the response body field "clientId" should be "${originalClientId}"

  Scenario: Conversation agentId stays unchanged after later append requests
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation id is "00000000-0000-0000-0000-bbbbbbbbbbbb"
    When I create a conversation with request:
    """
    {
      "id": "${conversationId}",
      "title": "Agent attributed conversation",
      "agentId": "planner"
    }
    """
    Then the response status should be 201
    And the response body field "agentId" should be "planner"
    When I append an entry to the conversation:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "agentId": "researcher",
      "content": [
        {
          "role": "USER",
          "text": "Attempt to change agent identity"
        }
      ]
    }
    """
    Then the response status should be 201
    Given I get the conversation
    Then the response status should be 200
    And the response body field "agentId" should be "planner"
