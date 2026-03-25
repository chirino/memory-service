Feature: SSE Event Stream Replay
  As a replay-capable consumer
  I want to resume REST event streams from a durable cursor
  So that I can catch up after reconnecting

  Background:
    Given I am authenticated as user "alice"

  Scenario: Replay SSE events after a cursor with full detail
    Given "alice" is connected to the SSE event stream
    And I have a conversation with title "Replay SSE Conversation"
    Then the response status should be 201
    And "alice" should receive an SSE event with kind "conversation" and event "created"
    And the SSE event cursor should be saved as "sseAfterCursor"
    When I update the conversation with request:
    """
    {
      "title": "Replay SSE Conversation Updated"
    }
    """
    Then the response status should be 200
    And "alice" should receive an SSE event with kind "conversation" and event "updated"
    Given "alice" is connected to the SSE event stream with query "after=${sseAfterCursor}&detail=full"
    Then "alice" should receive an SSE event with kind "conversation" and event "updated"
    And the SSE event data should contain "id"
    And the SSE event data "title" should be "Replay SSE Conversation Updated"

  Scenario: Replay admin SSE events after a cursor with full detail
    Given "alice" is connected to the admin SSE event stream with justification "BDD admin replay"
    And I am authenticated as user "bob"
    And I have a conversation with title "Admin Replay SSE Conversation"
    Then the response status should be 201
    And "alice" should receive an SSE event with kind "conversation" and event "created"
    And the SSE event cursor should be saved as "adminSseAfterCursor"
    When I update the conversation with request:
    """
    {
      "title": "Admin Replay SSE Conversation Updated"
    }
    """
    Then the response status should be 200
    And "alice" should receive an SSE event with kind "conversation" and event "updated"
    Given "alice" is connected to the admin SSE event stream with query "justification=BDD+admin+replay&after=${adminSseAfterCursor}&detail=full"
    Then "alice" should receive an SSE event with kind "conversation" and event "updated" where data "title" is "Admin Replay SSE Conversation Updated"
    And the SSE event data should contain "id"
