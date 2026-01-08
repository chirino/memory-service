Feature: Summaries gRPC API
  As an agent
  I want to create summaries for conversations via gRPC
  So that they can be searched by users

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has a message "User message"

  Scenario: Create summary requires API key via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    When I list messages for the conversation
    And set "firstMessageId" to the json response field "data[0].id"
    And I send gRPC request "SearchService/CreateSummary" with body:
    """
    conversation_id: "${conversationId}"
    title: "Test Summary"
    summary: "This is a test summary"
    until_message_id: "${firstMessageId}"
    summarized_at: "2025-01-01T00:00:00Z"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Agent can create summary via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I list messages for the conversation
    And set "firstMessageId" to the json response field "data[0].id"
    And I send gRPC request "SearchService/CreateSummary" with body:
    """
    conversation_id: "${conversationId}"
    title: "Test Summary"
    summary: "This is a test summary"
    until_message_id: "${firstMessageId}"
    summarized_at: "2025-01-01T00:00:00Z"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "conversationId" should be "${conversationId}"
    And the gRPC response field "channel" should be "SUMMARY"
    And the gRPC response field "content" should not be null
