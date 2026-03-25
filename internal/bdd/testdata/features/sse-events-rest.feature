Feature: SSE Event Stream
  As a frontend application
  I want to receive real-time events via Server-Sent Events
  So that I can invalidate caches and update the UI when server-side changes occur

  Background:
    Given I am authenticated as user "alice"

  Scenario: Receive conversation created event
    Given "alice" is connected to the SSE event stream
    When I call POST "/v1/conversations" with body:
    """
    {
      "title": "SSE Test Conversation"
    }
    """
    Then the response status should be 201
    And "alice" should receive an SSE event with kind "conversation" and event "created"
    And the SSE event data should contain "conversation"
    And the SSE event data should contain "conversation_group"

  Scenario: Receive conversation updated event
    Given I have a conversation with title "Update Test"
    And "alice" is connected to the SSE event stream
    When I call PATCH "/v1/conversations/${conversationId}" with body:
    """
    {
      "title": "Updated Title"
    }
    """
    Then the response status should be 200
    And "alice" should receive an SSE event with kind "conversation" and event "updated"
    And the SSE event data should contain "conversation"

  Scenario: Receive conversation deleted event
    Given I have a conversation with title "Delete Test"
    And "alice" is connected to the SSE event stream
    When I delete the conversation
    Then the response status should be 204
    And "alice" should receive an SSE event with kind "conversation" and event "deleted"
    And the SSE event data should contain "conversation"

  Scenario: Receive entry created event
    Given I have a conversation with title "Entry Test"
    And "alice" is connected to the SSE event stream
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Hello from SSE test"}]
    }
    """
    Then the response status should be 201
    And "alice" should receive an SSE event with kind "entry" and event "created"
    And the SSE event data should contain "conversation"
    And the SSE event data should contain "conversation_group"
    And the SSE event data should contain "entry"

  Scenario: Events are filtered by access — no leakage
    Given I have a conversation with title "Private Conversation"
    And "bob" is connected to the SSE event stream
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
    And "bob" should not receive any SSE event within 2 seconds

  Scenario: Receive events after being granted access
    Given I have a conversation with title "Shared Conversation"
    And "bob" is connected to the SSE event stream
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "reader"
    }
    """
    Then the response status should be 201
    And "bob" should receive an SSE event with kind "membership" and event "created"
    # Now bob should receive events for this conversation
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Shared message"}]
    }
    """
    Then the response status should be 201
    And "bob" should receive an SSE event with kind "entry" and event "created"

  Scenario: Stop receiving events after access revoked
    Given I have a conversation with title "Revoke Test"
    And I share the conversation with user "bob" and access level "reader"
    And "bob" is connected to the SSE event stream
    When I delete membership for user "bob"
    Then the response status should be 204
    And "bob" should receive an SSE event with kind "membership" and event "deleted"
    # Now bob should NOT receive events for this conversation
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "After revoke"}]
    }
    """
    Then the response status should be 201
    And "bob" should not receive any SSE event within 2 seconds

  Scenario: Filter events by kind
    Given I have a conversation with title "Kind Filter Test"
    And "alice" is connected to the SSE event stream filtered to kinds "entry"
    When I call PATCH "/v1/conversations/${conversationId}" with body:
    """
    {
      "title": "Updated Kind Filter"
    }
    """
    Then the response status should be 200
    # conversation update should be filtered out; entry append should come through
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
    And "alice" should receive an SSE event with kind "entry" and event "created"

  Scenario: Admin SSE endpoint requires justification
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/events"
    Then the response status should be 400

  Scenario: Admin SSE endpoint streams all events
    Given I am authenticated as admin user "alice"
    And "alice" is connected to the admin SSE event stream with justification "BDD test"
    And I am authenticated as user "bob"
    And I have a conversation with title "Admin Visibility"
    Then "alice" should receive an SSE event with kind "conversation" and event "created"

  Scenario: Membership updated event
    Given I have a conversation with title "Membership Update Test"
    And I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "reader"
    }
    """
    And "bob" is connected to the SSE event stream
    When I update membership for user "bob" with request:
    """
    {
      "accessLevel": "writer"
    }
    """
    Then the response status should be 200
    And "bob" should receive an SSE event with kind "membership" and event "updated"
    And the SSE event data should contain "role"
