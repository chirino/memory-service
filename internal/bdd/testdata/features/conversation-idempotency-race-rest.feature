Feature: Concurrent conversation creation idempotency
  Concurrent retries with one explicit conversation ID create one resource and one event.

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"

  Scenario: Concurrent exact retries create one conversation without orphan groups or duplicate events
    Given "alice" is connected to the SSE event stream
    When I call POST "/v1/conversations" concurrently 5 times with body:
    """
    {
      "id": "race-test-001",
      "title": "Race Condition Test"
    }
    """
    Then exactly one response should have status 201 and the rest should have status 200
    And "alice" should receive an SSE event with kind "conversation" and event "created" where data "conversation" is "race-test-001"
    And "alice" should not receive an SSE event with kind "conversation" and event "created" within 1 seconds
    When I call GET "/v1/conversations/race-test-001"
    Then the response status should be 200
    And the response body "id" should be "race-test-001"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM conversation_groups
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {
      "collection": "conversation_groups",
      "operation": "count",
      "filter": {}
    }
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
