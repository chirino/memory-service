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

  Scenario: Live gRPC tail uses Postgres outbox cursor format
    Given "alice" is connected to the gRPC event stream
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/11111111-1111-4111-8111-111111111196/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "gRPC Postgres cursor format"}]
    }
    """
    Then the response status should be 201
    And "alice" should receive a gRPC event with kind "conversation" and event "created"
    And the gRPC event cursor should match the Postgres outbox format
    And "alice" should receive a gRPC event with kind "entry" and event "created"
    And the gRPC event cursor should match the Postgres outbox format

  Scenario: Replay gRPC events after a cursor with full detail
    Given "alice" is connected to the gRPC event stream
    And I have a conversation with title "gRPC Replay Conversation"
    Then the response status should be 201
    And "alice" should receive a gRPC event with kind "conversation" and event "created"
    And the gRPC event cursor should be saved as "grpcAfterCursor"
    When I update the conversation with request:
    """
    {
      "title": "gRPC Replay Conversation Updated"
    }
    """
    Then the response status should be 200
    And "alice" should receive a gRPC event with kind "conversation" and event "updated"
    Given "alice" is connected to the gRPC event stream after cursor "${grpcAfterCursor}" with detail "full"
    Then "alice" should receive a gRPC event with kind "conversation" and event "updated"
    And the gRPC event data should contain "id"
    And the gRPC event data "title" should be "gRPC Replay Conversation Updated"

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
    And "bob" should receive a gRPC event with kind "membership" and event "created"
    And the gRPC event data should contain "user"

  Scenario: Archived conversations are delivered to prior members via gRPC
    Given I have a conversation with title "gRPC Archive Visibility"
    And I share the conversation with user "bob" and access level "reader"
    And "bob" is connected to the gRPC event stream
    When I archive the conversation
    Then the response status should be 200
    And "bob" should receive a gRPC event with kind "conversation" and event "updated"
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
    And "alice" should receive a gRPC event with kind "entry" and event "created"
