Feature: PostgreSQL Outbox SSE Replay Edge Cases
  As a replay-capable consumer
  I want stale Postgres outbox cursors to invalidate cleanly
  So that reconnect logic can recover after retention eviction

  Background:
    Given I am authenticated as user "alice"

  Scenario: Replay SSE returns invalidate for a stale cursor after outbox eviction
    Given "alice" is connected to the SSE event stream
    And I have a conversation with title "Replay SSE Eviction Cursor"
    Then the response status should be 201
    And "alice" should receive an SSE event with kind "conversation" and event "created"
    And the SSE event cursor should be saved as "staleSseAfterCursor"
    Given I am authenticated as admin user "alice"
    When I call POST "/v1/admin/evict" with body:
    """
    {
      "retentionPeriod": "PT0S",
      "resourceTypes": ["outbox_events"]
    }
    """
    Then the response status should be 204
    Given I am authenticated as user "alice"
    And "alice" is connected to the SSE event stream with query "after=${staleSseAfterCursor}"
    Then "alice" should receive an SSE event with kind "stream" and event "invalidate"
    And the SSE event data "reason" should be "cursor beyond retention window"
