Feature: Text-based gRPC requests
  As a client of the memory service
  I want to invoke gRPC using the proto text format
  So that we can test the gRPC boundary from Cucumber

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has a message "Hello from Alice"

  Scenario: Call SystemService.GetHealth via text proto
    When I send gRPC request "SystemService/GetHealth" with body:
    """
    {}
    """
    Then the gRPC response field "status" should be "ok"

  Scenario: List messages via text proto gRPC call
    When I send gRPC request "MessagesService/ListMessages" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 10
    }
    """
    Then set "messageId" to the gRPC response field "messages[0].id"
    And the gRPC response text should match text proto:
    """
    messages {
      conversation_id: "${conversationId}"
      user_id: "alice"
      channel: HISTORY
    }
    """

  Scenario: List messages with API key and pagination via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has 5 messages
    When I send gRPC request "MessagesService/ListMessages" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 2
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 messages
    And the gRPC response field "pageInfo.nextPageToken" should not be null

  Scenario: Append message requires API key via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    When I send gRPC request "MessagesService/AppendMessage" with body:
    """
    conversation_id: "${conversationId}"
    message {
      user_id: "alice"
      content {
        string_value: "hi"
      }
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Agent can append message via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "MessagesService/AppendMessage" with body:
    """
    conversation_id: "${conversationId}"
    message {
      user_id: "alice"
      channel: MEMORY
      content {
        string_value: "Agent message via gRPC"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "conversationId" should be "${conversationId}"
    And the gRPC response field "channel" should be "MEMORY"

  Scenario: Create summary requires API key via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    And the conversation has a message "User message"
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
    And the conversation has a message "User message"
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
