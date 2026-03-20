Feature: Event Stream gRPC API
  As a frontend or agent client
  I want to receive real-time events via gRPC
  So that I can invalidate caches and react to server-side changes

  Background:
    Given I am authenticated as user "alice"

  Scenario: Receive conversation created event via gRPC
    Given "alice" is connected to the gRPC event stream
    When I call POST "/v1/conversations" with body:
    """
    {
      "title": "gRPC Event Stream Conversation"
    }
    """
    Then the response status should be 201
    And "alice" should receive a gRPC event with kind "conversation" and event "created"
    And the gRPC event data should contain "conversation"
    And the gRPC event data should contain "conversation_group"

  Scenario: Events are filtered by access via gRPC
    Given I have a conversation with title "gRPC Private Conversation"
    And "bob" is connected to the gRPC event stream
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Secret message"}]
    }
    """
    Then the response status should be 201
    And "bob" should not receive any gRPC event within 2 seconds

  Scenario: Membership events reach the target user via gRPC
    Given I have a conversation with title "gRPC Shared Conversation"
    And "bob" is connected to the gRPC event stream
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "reader"
    }
    """
    Then the response status should be 201
    And "bob" should receive a gRPC event with kind "membership" and event "added"
    And the gRPC event data should contain "user"

  Scenario: Deleted conversations are delivered to prior members via gRPC
    Given I have a conversation with title "gRPC Delete Visibility"
    And I share the conversation with user "bob" and access level "reader"
    And "bob" is connected to the gRPC event stream
    When I delete the conversation
    Then the response status should be 204
    And "bob" should receive a gRPC event with kind "conversation" and event "deleted"
    And the gRPC event data should contain "conversation"

  Scenario: Filter gRPC events by kind
    Given I have a conversation with title "gRPC Kind Filter"
    And "alice" is connected to the gRPC event stream filtered to kinds "entry"
    When I call PATCH "/v1/conversations/${conversationId}" with body:
    """
    {
      "title": "Updated over gRPC stream"
    }
    """
    Then the response status should be 200
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Kind filtered"}]
    }
    """
    Then the response status should be 201
    And "alice" should receive a gRPC event with kind "entry" and event "appended"
