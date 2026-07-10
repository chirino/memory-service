Feature: Client-assigned entry sequence (REST)
  As an agent client
  I want to assign sequence numbers to entries
  So that I can use a stable cursor for ordered replay

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "Seq Test Conversation"

  Scenario: Accept an entry with a seq value
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 1,
      "content": [{"type": "text", "text": "First"}]
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "conversationId": "${conversationId}",
      "channel": "context",
      "contentType": "test.v1",
      "seq": 1,
      "epoch": 1,
      "content": [{"type": "text", "text": "First"}],
      "createdAt": "${response.body.createdAt}"
    }
    """

  Scenario: Reject a duplicate seq in the same conversation
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 5,
      "content": [{"type": "text", "text": "First"}]
    }
    """
    And the response status should be 201
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 5,
      "content": [{"type": "text", "text": "Duplicate"}]
    }
    """
    Then the response status should be 409

  Scenario: Allow the same seq in a different conversation
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 7,
      "content": [{"type": "text", "text": "Entry in conv-a"}]
    }
    """
    And the response status should be 201
    And I have a conversation with title "Other Seq Conversation"
    Then set "otherConvId" to the json response field "id"
    When I call POST "/v1/conversations/${otherConvId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 7,
      "content": [{"type": "text", "text": "Entry in conv-b"}]
    }
    """
    Then the response status should be 201

  Scenario: fromSeq returns seq-ordered entries and excludes null-seq entries
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "content": [{"type": "text", "text": "No seq"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 30,
      "content": [{"type": "text", "text": "Seq 30"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 10,
      "content": [{"type": "text", "text": "Seq 10"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 20,
      "content": [{"type": "text", "text": "Seq 20"}]
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations/${conversationId}/entries?channel=context&fromSeq=10"
    Then the response status should be 200
    And the response should contain 3 entries
    And the response body should be json:
    """
    {
      "afterCursor": null,
      "data": [
        {"seq": 10, "content": [{"type": "text", "text": "Seq 10"}]},
        {"seq": 20, "content": [{"type": "text", "text": "Seq 20"}]},
        {"seq": 30, "content": [{"type": "text", "text": "Seq 30"}]}
      ]
    }
    """

  Scenario: fromSeq filters with minimum threshold
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 10,
      "content": [{"type": "text", "text": "Seq 10"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 20,
      "content": [{"type": "text", "text": "Seq 20"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 30,
      "content": [{"type": "text", "text": "Seq 30"}]
    }
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "seq": 40,
      "content": [{"type": "text", "text": "Seq 40"}]
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations/${conversationId}/entries?channel=context&fromSeq=25"
    Then the response status should be 200
    And the response should contain 2 entries
    And the response body should be json:
    """
    {
      "afterCursor": null,
      "data": [
        {"seq": 30, "content": [{"type": "text", "text": "Seq 30"}]},
        {"seq": 40, "content": [{"type": "text", "text": "Seq 40"}]}
      ]
    }
    """

  Scenario: Omitting fromSeq preserves default created_at ordering
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel": "CONTEXT", "contentType": "test.v1", "seq": 100, "content": [{"type": "text", "text": "High seq first"}]}
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel": "CONTEXT", "contentType": "test.v1", "content": [{"type": "text", "text": "No seq second"}]}
    """
    And the response status should be 201
    When I call GET "/v1/conversations/${conversationId}/entries?channel=context&epoch=all"
    Then the response status should be 200
    And the response should contain 2 entries

  Scenario: Default timestamp ties use null seq first then seq order
    Given I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "seq": 20, "content": [{"role": "USER", "text": "Seq 20"}]}
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"role": "USER", "text": "No seq"}]}
    """
    And the response status should be 201
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "seq": 10, "content": [{"role": "USER", "text": "Seq 10"}]}
    """
    And the response status should be 201
    And the conversation entries share the same createdAt timestamp
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history"
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "afterCursor": null,
      "data": [
        {"seq": null, "content": [{"role": "USER", "text": "No seq"}]},
        {"seq": 10, "content": [{"role": "USER", "text": "Seq 10"}]},
        {"seq": 20, "content": [{"role": "USER", "text": "Seq 20"}]}
      ]
    }
    """
