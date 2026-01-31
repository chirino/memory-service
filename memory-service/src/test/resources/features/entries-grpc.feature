Feature: Entries gRPC API
  As a client of the memory service
  I want to manage entries via gRPC
  So that I can store and retrieve conversation history using gRPC

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has an entry "Hello from Alice"

  Scenario: List entries via gRPC
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And set "entryId" to the gRPC response field "entries[0].id"
    And the gRPC response text should match text proto:
    """
    entries {
      conversation_id: "${conversationId}"
      user_id: "alice"
      channel: HISTORY
      content_type: "message"
    }
    """

  Scenario: List entries with pagination via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has 5 entries
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 2
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "pageInfo.nextPageToken" should not be null
    And the gRPC response text should match text proto:
    """
    entries {
      conversation_id: "${conversationId}"
      channel: HISTORY
      content_type: "message"
    }
    page_info {
      next_page_token: "${response.body.pageInfo.nextPageToken}"
    }
    """

  Scenario: List entries with channel filter via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "Memory entry" in channel "MEMORY" with contentType "test.v1"
    And the conversation has an entry "History entry" in channel "HISTORY"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: MEMORY
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response text should match text proto:
    """
    entries {
      conversation_id: "${conversationId}"
      channel: MEMORY
      content_type: "test.v1"
    }
    """

  Scenario: Agent can filter memory entries by epoch via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "Epoch One" with epoch 1 and contentType "test.v1"
    And the conversation has a memory entry "Epoch Two" with epoch 2 and contentType "test.v1"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: MEMORY
    epoch_filter: "1"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response field "entries[0].content[0].text" should be "Epoch One"

  Scenario: Sync memory entries via gRPC is no-op when there are no changes
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "Stable gRPC epoch" with epoch 1 and contentType "test.v1"
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: MEMORY
      content_type: "test.v1"
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
    And the gRPC response should not have entry

  Scenario: Sync memory entries via gRPC creates a new epoch when history diverges
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "Original epoch entry" with epoch 1 and contentType "test.v1"
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: MEMORY
      content_type: "test.v1"
      content {
        string_value: "Updated epoch entry"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "2"
    And the gRPC response field "noOp" should be false
    And the gRPC response field "epochIncremented" should be true
    And the gRPC response should have entry
    And the gRPC response field "entry.content[0]" should be "Updated epoch entry"

  Scenario: Sync memory entries via gRPC with empty content clears memory
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a memory entry "Memory to clear" with epoch 1 and contentType "test.v1"
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: MEMORY
      content_type: "test.v1"
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "2"
    And the gRPC response field "noOp" should be false
    And the gRPC response field "epochIncremented" should be true
    And the gRPC response should have entry
    And the gRPC response entry content should be empty

  Scenario: Sync memory entries via gRPC with empty content is no-op when no existing memory
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: MEMORY
      content_type: "test.v1"
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "noOp" should be true
    And the gRPC response field "epochIncremented" should be false
    And the gRPC response should not have entry

  Scenario: Append entry requires API key via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      content_type: "message"
      content {
        string_value: "hi"
      }
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Agent can append entry via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: MEMORY
      content_type: "test.v1"
      content {
        string_value: "Agent entry via gRPC"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "conversationId" should be "${conversationId}"
    And the gRPC response field "channel" should be "MEMORY"
    And the gRPC response field "contentType" should be "test.v1"
    And the gRPC response text should match text proto:
    """
    id: "${response.body.id}"
    conversation_id: "${conversationId}"
    user_id: "alice"
    channel: MEMORY
    content_type: "test.v1"
    content {
      string_value: "Agent entry via gRPC"
    }
    """

  Scenario: Agent can append entry with multiple content blocks via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "message"
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
    And the gRPC response field "contentType" should be "message"
