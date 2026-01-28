Feature: Messages gRPC API
  As a client of the memory service
  I want to manage messages via gRPC
  So that I can store and retrieve conversation history using gRPC

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has a message "Hello from Alice"

  Scenario: List messages via gRPC
    When I send gRPC request "MessagesService/ListMessages" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And set "messageId" to the gRPC response field "messages[0].id"
    And the gRPC response text should match text proto:
    """
    messages {
      conversation_id: "${conversationId}"
      user_id: "alice"
      channel: HISTORY
    }
    """

  Scenario: List messages with pagination via gRPC
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
    And the gRPC response text should match text proto:
    """
    messages {
      conversation_id: "${conversationId}"
      channel: HISTORY
    }
    page_info {
      next_page_token: "${response.body.pageInfo.nextPageToken}"
    }
    """

  Scenario: List messages with channel filter via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a message "Memory message" in channel "MEMORY"
    And the conversation has a message "History message" in channel "HISTORY"
    When I send gRPC request "MessagesService/ListMessages" with body:
    """
    conversation_id: "${conversationId}"
    channel: MEMORY
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 message
    And the gRPC response text should match text proto:
    """
    messages {
      conversation_id: "${conversationId}"
      channel: MEMORY
    }
    """

  Scenario: Agent can filter memory messages by epoch via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory message "Epoch One" with epoch 1
    And the conversation has a memory message "Epoch Two" with epoch 2
    When I send gRPC request "MessagesService/ListMessages" with body:
    """
    conversation_id: "${conversationId}"
    channel: MEMORY
    epoch_filter: "1"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 message
    And the gRPC response field "messages[0].content[0].text" should be "Epoch One"

  Scenario: Sync memory messages via gRPC is no-op when there are no changes
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory message "Stable gRPC epoch" with epoch 1
    When I send gRPC request "MessagesService/SyncMessages" with body:
    """
    conversation_id: "${conversationId}"
    messages {
      channel: MEMORY
      content {
        struct_value {
          fields {
            key: "type"
            value {
              string_value: "text"
            }
          }
          fields {
            key: "text"
            value {
              string_value: "Stable gRPC epoch"
            }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "1"
    And the gRPC response field "noOp" should be true
    And the gRPC response field "epochIncremented" should be false
    And the gRPC response should contain 0 messages

  Scenario: Sync memory messages via gRPC creates a new epoch when history diverges
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory message "Original epoch message" with epoch 1
    When I send gRPC request "MessagesService/SyncMessages" with body:
    """
    conversation_id: "${conversationId}"
    messages {
      channel: MEMORY
      content {
        string_value: "Updated epoch message"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "2"
    And the gRPC response field "noOp" should be false
    And the gRPC response field "epochIncremented" should be true
    And the gRPC response should contain 1 message
    And the gRPC response field "messages[0].content[0]" should be "Updated epoch message"

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
    And the gRPC response text should match text proto:
    """
    id: "${response.body.id}"
    conversation_id: "${conversationId}"
    user_id: "alice"
    channel: MEMORY
    content {
      string_value: "Agent message via gRPC"
    }
    """

  Scenario: Agent can append message with multiple content blocks via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "MessagesService/AppendMessage" with body:
    """
    conversation_id: "${conversationId}"
    message {
      user_id: "alice"
      channel: HISTORY
      content {
        string_value: "First part"
      }
      content {
        string_value: "Second part"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "channel" should be "HISTORY"
