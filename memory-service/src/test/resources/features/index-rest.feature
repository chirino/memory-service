Feature: Index Transcript REST API
  As an agent
  I want to index transcripts for conversations via REST API
  So that they can be searched by users

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has an entry "Order status question"

  Scenario: Agent can index a transcript and user can search it
    Given I am authenticated as agent with API key "test-agent-key"
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I index a transcript with request:
    """
    {
      "conversationId": "${conversationId}",
      "title": "Order transcript",
      "transcript": "Customer asked about refund policy.",
      "untilEntryId": "${firstEntryId}"
    }
    """
    Then the response status should be 201
    And the entry should have channel "TRANSCRIPT"
    And the entry should have content "Customer asked about refund policy."
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "conversationId": "${conversationId}",
      "channel": "transcript",
      "contentType": "transcript",
      "content": [
        {
          "type": "transcript",
          "text": "Customer asked about refund policy.",
          "untilEntryId": "${firstEntryId}"
        }
      ],
      "createdAt": "${response.body.createdAt}"
    }
    """
    Given I am authenticated as user "alice"
    When I search conversations for query "refund policy"
    Then the response status should be 200
    And the search response should contain 1 results
    And search result at index 0 should have entry content "Customer asked about refund policy."
    And the response body should be json:
    """
    {
      "data": [
        {
          "entry": {
            "id": "${response.body.data[0].entry.id}",
            "conversationId": "${response.body.data[0].entry.conversationId}",
            "channel": "${response.body.data[0].entry.channel}",
            "contentType": "${response.body.data[0].entry.contentType}",
            "content": ${response.body.data[0].entry.content},
            "createdAt": "${response.body.data[0].entry.createdAt}"
          },
          "score": ${response.body.data[0].score}
        }
      ]
    }
    """

  Scenario: Transcript indexing requires agent API key
    Given I am authenticated as user "alice"
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I index a transcript with request:
    """
    {
      "conversationId": "${conversationId}",
      "title": "Blocked transcript",
      "transcript": "Should not be created.",
      "untilEntryId": "${firstEntryId}"
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

  Scenario: Search conversations with query
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "Customer wants to return item"
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I index a transcript with request:
    """
    {
      "conversationId": "${conversationId}",
      "title": "Return request",
      "transcript": "Customer wants to return an item and get a refund.",
      "untilEntryId": "${firstEntryId}"
    }
    """
    Given I am authenticated as user "alice"
    When I search conversations with request:
    """
    {
      "query": "return item",
      "topK": 10
    }
    """
    Then the response status should be 200
    And the search response should contain at least 1 results
