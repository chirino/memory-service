Feature: PostgreSQL Outbox SSE Cursor Format
  As a replay-capable consumer
  I want live SSE tail events to use the PostgreSQL numeric outbox cursor format
  So that live tail and replay share the same cursor space

  Background:
    Given I am authenticated as user "alice"

  Scenario: Live SSE tail uses Postgres numeric outbox cursor format
    Given "alice" is connected to the SSE event stream
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/11111111-1111-4111-8111-111111111195/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Postgres numeric cursor format"}]
    }
    """
    Then the response status should be 201
    And "alice" should receive an SSE event with kind "conversation" and event "created"
    And the SSE event cursor should match the Postgres outbox format
    And "alice" should receive an SSE event with kind "entry" and event "created"
    And the SSE event cursor should match the Postgres outbox format
