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

  Scenario: gRPC event stream reports eviction when connection cap is exceeded
    Given the current client is connected to the gRPC event stream as "alice-stream-1"
    And the current client is connected to the gRPC event stream as "alice-stream-2"
    And the current client is connected to the gRPC event stream as "alice-stream-3"
    And the current client is connected to the gRPC event stream as "alice-stream-4"
    And the current client is connected to the gRPC event stream as "alice-stream-5"
    And the current client is connected to the gRPC event stream as "alice-stream-6"
    Then "alice-stream-1" should receive a gRPC event with kind "stream" and event "evicted"
    And the gRPC event data "reason" should be "too many connections"

  Scenario: gRPC conversation create publishes an event
    Given "alice" is connected to the gRPC event stream
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Created by gRPC mutation"
    """
    Then the gRPC response should not have an error
    And "alice" should receive a gRPC event with kind "conversation" and event "created"
    And the gRPC event data should contain "conversation"

  Scenario: gRPC conversation update publishes an event
    Given I have a conversation with title "gRPC Event Update Before"
    And "alice" is connected to the gRPC event stream
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    title: "gRPC Event Update After"
    """
    Then the gRPC response should not have an error
    And "alice" should receive a gRPC event with kind "conversation" and event "updated"
    And the gRPC event data should contain "conversation"

  Scenario: gRPC membership share publishes an event to target user
    Given I have a conversation with title "gRPC Membership Event"
    And "bob" is connected to the gRPC event stream
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: READER
    """
    Then the gRPC response should not have an error
    And "bob" should receive a gRPC event with kind "membership" and event "created"
    And the gRPC event data should contain "user"

  Scenario: Live gRPC tail uses Postgres numeric outbox cursor format
    Given "alice" is connected to the gRPC event stream
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/11111111-1111-4111-8111-111111111196/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "gRPC Postgres numeric cursor format"}]
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

  Scenario: gRPC entry stream defaults to history entries
    Given I have a conversation with title "gRPC Entry Filter Defaults"
    And "alice" is connected to the gRPC event stream filtered to kinds "entry"
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "content": [{"type": "text", "text": "Internal context over gRPC stream"}]
    }
    """
    Then the response status should be 201
    And "alice" should not receive a gRPC event with kind "entry" and event "created" within 2 seconds
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Visible history over gRPC stream"}]
    }
    """
    Then the response status should be 201
    And "alice" should receive a gRPC event with kind "entry" and event "created" where data "entry_channel" is "history"

  Scenario: gRPC entry stream can opt into context entries
    Given I have a conversation with title "gRPC Entry Context Filter"
    And "alice" is connected to the gRPC event stream filtered to kinds "entry" and entry channels "context"
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "content": [{"type": "text", "text": "Subscribed context over gRPC stream"}]
    }
    """
    Then the response status should be 201
    And "alice" should receive a gRPC event with kind "entry" and event "created" where data "entry_channel" is "context"

  Scenario: gRPC entry stream filters by content type and role
    Given I have a conversation with title "gRPC Entry Metadata Filter"
    And "alice" is connected to the gRPC event stream filtered to kinds "entry" entry channels "history" content types "history/lc4j" and roles "AI"
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Filtered user history over gRPC"}]
    }
    """
    Then the response status should be 201
    And "alice" should not receive a gRPC event with kind "entry" and event "created" within 2 seconds
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history/lc4j",
      "content": [{"role": "AI", "text": "Matched AI history over gRPC"}]
    }
    """
    Then the response status should be 201
    And "alice" should receive a gRPC event with kind "entry" and event "created" where data "entry_role" is "AI"
