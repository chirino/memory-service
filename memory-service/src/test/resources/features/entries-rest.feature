Feature: Entries REST API
  As a user or agent
  I want to manage entries in conversations via REST API
  So that I can store and retrieve conversation history

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"

  Scenario: List entries from a conversation
    Given the conversation has no entries
    When I list entries for the conversation
    Then the response status should be 200
    And the response should contain an empty list of entries
    And the response body should be json:
    """
    {
      "nextCursor": null,
      "data": []
    }
    """

  Scenario: List entries with history entries
    Given the conversation has an entry "Hello from Alice"
    And the conversation has an entry "How are you?"
    When I list entries for the conversation
    Then the response status should be 200
    And the response should contain 2 entries
    Then set "firstEntryId" to the json response field "data[0].id"
    And entry at index 0 should have content "Hello from Alice"
    And entry at index 1 should have content "How are you?"
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
          "contentType": "history",
          "content": ${response.body.data[0].content},
          "createdAt": "${response.body.data[0].createdAt}"
        },
        {
          "id": "${response.body.data[1].id}",
          "conversationId": "${response.body.data[1].conversationId}",
          "userId": "alice",
          "channel": "history",
          "contentType": "history",
          "content": ${response.body.data[1].content},
          "createdAt": "${response.body.data[1].createdAt}"
        }
      ]
    }
    """

  Scenario: List entries with pagination
    Given the conversation has 5 entries
    When I list entries with limit 2
    Then the response status should be 200
    And the response should contain 2 entries
    And the response should have a nextCursor

  Scenario: Agent can append memory entries to conversation
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I append an entry with content "Agent response" and channel "MEMORY" and contentType "test.v1"
    Then the response status should be 201
    And the response should contain the created entry
    And the entry should have content "Agent response"
    And the entry should have channel "MEMORY"
    And the entry should have contentType "test.v1"
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "conversationId": "${conversationId}",
      "channel": "memory",
      "epoch": 1,
      "contentType": "test.v1",
      "content": [
        {
          "type": "text",
          "text": "Agent response"
        }
      ],
      "createdAt": "${response.body.createdAt}"
    }
    """

  Scenario: User cannot append memory entries to conversation
    Given I am authenticated as user "alice"
    And the conversation exists
    When I append an entry with content "User entry" and channel "MEMORY" and contentType "test.v1"
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

  Scenario: Agent can list all entries including memory channel
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "User entry"
    And the conversation has an entry "Agent entry" in channel "MEMORY" with contentType "test.v1"
    When I list entries for the conversation
    Then the response status should be 200
    And the response should contain 2 entries

  Scenario: Agent can filter memory entries by epoch
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "First epoch entry" with epoch 1 and contentType "test.v1"
    And the conversation has a memory entry "Second epoch entry" with epoch 2 and contentType "test.v1"
    When I list memory entries for the conversation with epoch "1"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "First epoch entry"

  Scenario: Sync memory entries is no-op when there are no changes
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "Stable epoch entry" with epoch 1 and contentType "test.v1"
    When I sync memory entries with request:
    """
    {
      "channel": "MEMORY",
      "contentType": "test.v1",
      "content": [
        {
          "type": "text",
          "text": "Stable epoch entry"
        }
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epoch" should be "1"
    And the response body field "noOp" should be "true"
    And the response body field "epochIncremented" should be "false"
    And the sync response entry should be null

  Scenario: Sync memory entries appends new items within the current epoch
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "Epoch delta entry" with epoch 1 and contentType "test.v1"
    When I sync memory entries with request:
    """
    {
      "channel": "MEMORY",
      "contentType": "test.v1",
      "content": [
        {
          "type": "text",
          "text": "Epoch delta entry"
        },
        {
          "type": "text",
          "text": "Appended via sync"
        }
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epoch" should be "1"
    And the response body field "noOp" should be "false"
    And the response body field "epochIncremented" should be "false"
    And the response body field "entry.content[0].text" should be "Appended via sync"
    And the sync response entry should not be null

  Scenario: Sync memory entries creates a new epoch when history diverges
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "Original epoch entry" with epoch 1 and contentType "test.v1"
    When I sync memory entries with request:
    """
    {
      "channel": "MEMORY",
      "contentType": "test.v1",
      "content": [
        {
          "type": "text",
          "text": "New epoch entry"
        }
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epoch" should be "2"
    And the response body field "noOp" should be "false"
    And the response body field "epochIncremented" should be "true"
    And the sync response entry should not be null

  Scenario: Sync memory entries with empty content clears memory by creating an empty epoch
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "Memory to clear" with epoch 1 and contentType "test.v1"
    When I sync memory entries with request:
    """
    {
      "channel": "MEMORY",
      "contentType": "test.v1",
      "content": []
    }
    """
    Then the response status should be 200
    And the response body field "epoch" should be "2"
    And the response body field "noOp" should be "false"
    And the response body field "epochIncremented" should be "true"
    And the sync response entry should not be null
    And the sync response entry content should be empty

  Scenario: Sync memory entries with empty content is no-op when no existing memory
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I sync memory entries with request:
    """
    {
      "channel": "MEMORY",
      "contentType": "test.v1",
      "content": []
    }
    """
    Then the response status should be 200
    And the response body field "noOp" should be "true"
    And the response body field "epochIncremented" should be "false"
    And the sync response entry should be null

  Scenario: User can only see history channel entries
    Given I am authenticated as user "alice"
    And the conversation has an entry "User entry"
    And the conversation has an entry "Agent entry" in channel "MEMORY" with contentType "test.v1"
    When I list entries for the conversation
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "User entry"

  Scenario: List entries with channel filter for agent
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "Memory entry" in channel "MEMORY" with contentType "test.v1"
    And the conversation has an entry "History entry" in channel "HISTORY"
    When I list entries for the conversation with channel "MEMORY"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Memory entry"

  Scenario: Derived conversation title from first entry
    Given the conversation id is "00000000-0000-0000-0000-000000000099"
    And the conversation has an entry "Sensitive information about the new compliance project"
    Then the conversation title should be "Sensitive information about the new comp"

  Scenario: List entries from non-existent conversation
    Given I am authenticated as user "alice"
    When I list entries for conversation "non-existent-id"
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: List entries from conversation without access
    Given I am authenticated as user "bob"
    And there is a conversation owned by "alice"
    When I list entries for that conversation
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Agent can append entry with inline indexed content
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"text": "Entry with inline index", "role": "AI"}],
      "indexedContent": "Searchable inline content for testing"
    }
    """
    Then the response status should be 201
    Given I am authenticated as user "alice"
    When I search conversations for query "inline content"
    Then the response status should be 200
    And the search response should contain 1 results
    And search result at index 0 should have conversationId "${conversationId}"

  Scenario: Inline indexedContent only allowed on history channel
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "MEMORY",
      "contentType": "message",
      "content": [{"type": "text", "text": "Memory entry"}],
      "indexedContent": "This should fail"
    }
    """
    Then the response status should be 400

  Scenario: History channel entries must use 'history' or 'history/*' contentType
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "message",
      "content": [{"text": "Test message", "role": "AI"}]
    }
    """
    Then the response status should be 400
    And the response body field "details.message" should be "History channel entries must use 'history' or 'history/<subtype>' as the contentType"

  Scenario: History channel accepts 'history/lc4j' contentType with events
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history/lc4j",
      "content": [{
        "role": "AI",
        "text": "Hello world",
        "events": [{"eventType": "PartialResponse", "chunk": "Hello world"}]
      }]
    }
    """
    Then the response status should be 201

  Scenario: History channel entries must have exactly 1 content object
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"text": "First", "role": "AI"}, {"text": "Second", "role": "USER"}]
    }
    """
    Then the response status should be 400
    And the response body field "details.message" should be "History channel entries must contain exactly 1 content object"

  Scenario: History channel entries must have text field
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "AI"}]
    }
    """
    Then the response status should be 400
    And the response body field "details.message" should be "History channel content must have either a 'text' field or an 'events' array"

  Scenario: History channel entries must have valid role field
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"text": "Test message", "role": "INVALID"}]
    }
    """
    Then the response status should be 400
    And the response body field "details.message" should be "History channel content must have a 'role' field with value 'USER' or 'AI'"

  # Regression test for bug: memory entries created via append had null epoch,
  # causing channel=memory filter to return no results (SQL: epoch = null never matches)
  Scenario: Memory entries created via append are retrievable via channel filter
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    # Create multiple memory entries via append (not sync)
    When I append an entry with content "First memory entry" and channel "MEMORY" and contentType "test.v1"
    Then the response status should be 201
    And the response body field "epoch" should be "1"
    When I append an entry with content "Second memory entry" and channel "MEMORY" and contentType "test.v1"
    Then the response status should be 201
    And the response body field "epoch" should be "1"
    # Verify entries are retrievable without channel filter
    When I list entries for the conversation
    Then the response status should be 200
    And the response should contain 2 entries
    # Verify entries are retrievable with channel=memory filter
    When I list entries for the conversation with channel "MEMORY"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "First memory entry"
    And entry at index 1 should have content "Second memory entry"
    # Verify epoch filter also works
    When I list memory entries for the conversation with epoch "1"
    Then the response status should be 200
    And the response should contain 2 entries

  # Test that history entries don't get epochs (they should have null epoch)
  Scenario: History entries have null epoch
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"text": "History entry without epoch", "role": "AI"}]
    }
    """
    Then the response status should be 201
    And the response body field "epoch" should be null
