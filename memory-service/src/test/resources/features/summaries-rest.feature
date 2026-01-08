Feature: Summaries REST API
  As an agent
  I want to create summaries for conversations via REST API
  So that they can be searched by users

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has a message "Order status question"

  Scenario: Agent can create a summary and user can search it
    Given I am authenticated as agent with API key "test-agent-key"
    When I list messages for the conversation
    And set "firstMessageId" to the json response field "data[0].id"
    When I create a summary with request:
    """
    {
      "title": "Order summary",
      "summary": "Customer asked about refund policy.",
      "untilMessageId": "${firstMessageId}",
      "summarizedAt": "2024-01-01T00:00:00Z"
    }
    """
    Then the response status should be 201
    And the message should have channel "SUMMARY"
    And the message should have content "Customer asked about refund policy."
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "conversationId": "${conversationId}",
      "channel": "summary",
      "content": [
        {
          "type": "summary",
          "text": "Customer asked about refund policy.",
          "untilMessageId": "${firstMessageId}",
          "summarizedAt": "2024-01-01T00:00:00Z"
        }
      ],
      "createdAt": "${response.body.createdAt}"
    }
    """
    Given I am authenticated as user "alice"
    When I search messages for query "refund policy"
    Then the response status should be 200
    And the search response should contain 1 results
    And search result at index 0 should have message content "Customer asked about refund policy."
    And the response body should be json:
    """
    {
      "data": [
        {
          "message": {
            "id": "${response.body.data[0].message.id}",
            "conversationId": "${response.body.data[0].message.conversationId}",
            "channel": "${response.body.data[0].message.channel}",
            "content": ${response.body.data[0].message.content},
            "createdAt": "${response.body.data[0].message.createdAt}"
          },
          "score": ${response.body.data[0].score}
        }
      ]
    }
    """

  Scenario: Summary creation requires agent API key
    Given I am authenticated as user "alice"
    When I list messages for the conversation
    And set "firstMessageId" to the json response field "data[0].id"
    When I create a summary with request:
    """
    {
      "title": "Blocked summary",
      "summary": "Should not be created.",
      "untilMessageId": "${firstMessageId}",
      "summarizedAt": "2024-01-01T00:00:00Z"
    }
    """
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

  Scenario: Search messages with query
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a message "Customer wants to return item"
    When I list messages for the conversation
    And set "firstMessageId" to the json response field "data[0].id"
    When I create a summary with request:
    """
    {
      "title": "Return request",
      "summary": "Customer wants to return an item and get a refund.",
      "untilMessageId": "${firstMessageId}",
      "summarizedAt": "2024-01-01T00:00:00Z"
    }
    """
    Given I am authenticated as user "alice"
    When I search messages with request:
    """
    {
      "query": "return item",
      "topK": 10
    }
    """
    Then the response status should be 200
    And the search response should contain at least 1 results
