Feature: Messages REST API
  As a user or agent
  I want to manage messages in conversations via REST API
  So that I can store and retrieve conversation history

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"

  Scenario: List messages from a conversation
    Given the conversation has no messages
    When I list messages for the conversation
    Then the response status should be 200
    And the response should contain an empty list of messages
    And the response body should be json:
    """
    {
      "nextCursor": null,
      "data": []
    }
    """

  Scenario: List messages with history messages
    Given the conversation has a message "Hello from Alice"
    And the conversation has a message "How are you?"
    When I list messages for the conversation
    Then the response status should be 200
    And the response should contain 2 messages
    Then set "firstMessageId" to the json response field "data[0].id"
    And message at index 0 should have content "Hello from Alice"
    And message at index 1 should have content "How are you?"
    And the response body should be json:
    """
    {
      "nextCursor": null,
      "data": [
        {
          "id": "${response.body.data[0].id}",
          "conversationId": "${response.body.data[0].conversationId}",
          "userId": "alice",
          "channel": "history",
          "content": ${response.body.data[0].content},
          "createdAt": "${response.body.data[0].createdAt}"
        },
        {
          "id": "${response.body.data[1].id}",
          "conversationId": "${response.body.data[1].conversationId}",
          "userId": "alice",
          "channel": "history",
          "content": ${response.body.data[1].content},
          "createdAt": "${response.body.data[1].createdAt}"
        }
      ]
    }
    """

  Scenario: List messages with pagination
    Given the conversation has 5 messages
    When I list messages with limit 2
    Then the response status should be 200
    And the response should contain 2 messages
    And the response should have a nextCursor

  Scenario: Agent can append memory messages to conversation
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I append a message with content "Agent response" and channel "MEMORY"
    Then the response status should be 201
    And the response should contain the created message
    And the message should have content "Agent response"
    And the message should have channel "MEMORY"
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "conversationId": "${conversationId}",
      "channel": "memory",
      "content": [
        {
          "type": "text",
          "text": "Agent response"
        }
      ],
      "createdAt": "${response.body.createdAt}"
    }
    """

  Scenario: User cannot append memeory messages to conversation
    Given I am authenticated as user "alice"
    And the conversation exists
    When I append a message with content "User message" and channel "MEMORY"
    Then the response status should be 403
    And the response should contain error code "forbidden"
    And the response body should be json:
    """
    {
      "code": "forbidden",
      "error": "${response.body.error}",
      "details": {
        "message": "${response.body.details.message}"
      }
    }
    """

  Scenario: Agent can list all messages including memory channel
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a message "User message"
    And the conversation has a message "Agent message" in channel "MEMORY"
    When I list messages for the conversation
    Then the response status should be 200
    And the response should contain 2 messages

  Scenario: Agent can filter memory messages by epoch
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory message "First epoch message" with epoch 1
    And the conversation has a memory message "Second epoch message" with epoch 2
    When I list memory messages for the conversation with epoch "1"
    Then the response status should be 200
    And the response should contain 1 message
    And message at index 0 should have content "First epoch message"

  Scenario: Sync memory messages is no-op when there are no changes
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory message "Stable epoch message" with epoch 1
    When I sync memory messages with request:
    """
    {
      "messages": [
        {
          "channel": "MEMORY",
          "content": [
            {
              "type": "text",
              "text": "Stable epoch message"
            }
          ]
        }
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epoch" should be "1"
    And the response body field "noOp" should be "true"
    And the response body field "epochIncremented" should be "false"
    And the sync response should contain 0 messages

  Scenario: Sync memory messages appends new entries within the current epoch
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory message "Epoch delta message" with epoch 1
    When I sync memory messages with request:
    """
    {
      "messages": [
        {
          "channel": "MEMORY",
          "content": [
            {
              "type": "text",
              "text": "Epoch delta message"
            }
          ]
        },
        {
          "channel": "MEMORY",
          "content": [
            {
              "type": "text",
              "text": "Appended via sync"
            }
          ]
        }
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epoch" should be "1"
    And the response body field "noOp" should be "false"
    And the response body field "epochIncremented" should be "false"
    And the response body field "messages[0].content[0].text" should be "Appended via sync"
    And the sync response should contain 1 messages

  Scenario: Sync memory messages creates a new epoch when history diverges
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory message "Original epoch message" with epoch 1
    When I sync memory messages with request:
    """
    {
      "messages": [
        {
          "channel": "MEMORY",
          "content": [
            {
              "type": "text",
              "text": "New epoch message"
            }
          ]
        }
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epoch" should be "2"
    And the response body field "noOp" should be "false"
    And the response body field "epochIncremented" should be "true"
    And the sync response should contain 1 messages

  Scenario: User can only see history channel messages
    Given I am authenticated as user "alice"
    And the conversation has a message "User message"
    And the conversation has a message "Agent message" in channel "MEMORY"
    When I list messages for the conversation
    Then the response status should be 200
    And the response should contain 1 message
    And message at index 0 should have content "User message"

  Scenario: List messages with channel filter for agent
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a message "Memory message" in channel "MEMORY"
    And the conversation has a message "History message" in channel "HISTORY"
    When I list messages for the conversation with channel "MEMORY"
    Then the response status should be 200
    And the response should contain 1 message
    And message at index 0 should have content "Memory message"

  Scenario: Derived conversation title from first message
    Given the conversation id is "00000000-0000-0000-0000-000000000099"
    And the conversation has a message "Sensitive information about the new compliance project"
    Then the conversation title should be "Sensitive information about the new comp"

  Scenario: List messages from non-existent conversation
    Given I am authenticated as user "alice"
    When I list messages for conversation "non-existent-id"
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: List messages from conversation without access
    Given I am authenticated as user "bob"
    And there is a conversation owned by "alice"
    When I list messages for that conversation
    Then the response status should be 403
    And the response should contain error code "forbidden"
