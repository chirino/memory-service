Feature: Turn trace processor
  The processor derives turn spans from the gRPC event stream and persists
  checkpoint state through the shared processor lifecycle API.

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Turn Trace Conversation"

  Scenario: Processor emits a completed turn span from live entry events
    Given the turn trace processor is running for user "alice" with scope "user"
    And I am authenticated as agent with API key "test-agent-key"
    When I append an entry to the conversation:
      """
      {
        "channel": "HISTORY",
        "contentType": "history",
        "content": [{"role": "USER", "text": "What should I remember?"}]
      }
      """
    And I append an entry to the conversation:
      """
      {
        "channel": "CONTEXT",
        "contentType": "application/vnd.memory-service.memory+json",
        "epoch": 1,
        "content": [{"type": "text", "text": "The user likes precise BDD tests."}]
      }
      """
    And I append an entry to the conversation:
      """
      {
        "channel": "HISTORY",
        "contentType": "history",
        "content": [{"role": "AI", "text": "I will remember that."}]
      }
      """
    Then the turn trace processor should emit a turn span for conversation "${conversationId}" with end reason "agent_history_entry" within 10 seconds
    And the last turn trace span should have context entry count 1
    And the last turn trace span should use session "${conversationGroupId}"
    And the last turn trace span metadata "conversation_group_id" should be "${conversationGroupId}"

  Scenario: Processor resumes an open turn from its checkpoint after restart
    Given the turn trace processor is running for user "alice" with scope "user"
    And I am authenticated as agent with API key "test-agent-key"
    When I append an entry to the conversation:
      """
      {
        "channel": "HISTORY",
        "contentType": "history",
        "content": [{"role": "USER", "text": "Start a turn before restart."}]
      }
      """
    And I append an entry to the conversation:
      """
      {
        "channel": "CONTEXT",
        "contentType": "application/vnd.memory-service.memory+json",
        "epoch": 1,
        "content": [{"type": "text", "text": "Checkpointed context before restart."}]
      }
      """
    And the turn trace processor is stopped
    And the turn trace processor is running for user "alice" with scope "user"
    And I am authenticated as agent with API key "test-agent-key"
    When I append an entry to the conversation:
      """
      {
        "channel": "HISTORY",
        "contentType": "history",
        "content": [{"role": "AI", "text": "Complete the turn after restart."}]
      }
      """
    Then the turn trace processor should emit a turn span for conversation "${conversationId}" with end reason "agent_history_entry" within 10 seconds
    And the last turn trace span should have context entry count 1
    And the last turn trace span should use session "${conversationGroupId}"
