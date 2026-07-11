Feature: Conversation Forking gRPC API
  As a client of the memory service
  I want to fork conversations and verify fork data via gRPC
  So that I can create alternative conversation branches and query them using gRPC

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Base Conversation"
    And set "parentConversationId" to "${conversationId}"
    And the conversation has an entry "First entry"
    And the conversation has an entry "Second entry"
    And the conversation has an entry "Third entry"

  Scenario: Fork a conversation and verify via gRPC
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    When I fork the conversation at entry "${secondEntryId}"
    Then the response status should be 200
    And set "forkedConversationId" to "${forkedConversationId}"
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${forkedConversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${forkedConversationId}"
    And the gRPC response field "forkedAtEntryId" should be "${secondEntryId}"
    And the gRPC response field "forkedAtConversationId" should be "${parentConversationId}"
    And the gRPC response field "ownerUserId" should be "alice"
    And the gRPC response field "accessLevel" should be "OWNER"

  Scenario: Fork a conversation and verify entries via gRPC
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    When I fork the conversation at entry "${secondEntryId}"
    Then the response status should be 200
    And set "forkedConversationId" to "${forkedConversationId}"
    Given I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${forkedConversationId | uuid_to_hex_string}"
    channel: HISTORY
    """
    Then the gRPC response should not have an error
    And the gRPC response field "entries" should not be null

  Scenario: Fork via gRPC AppendEntry with fork metadata
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    Given I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee01" | uuid_to_hex_string}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "Fork message" }
          }
        }
      }
      forked_at_conversation_id: "${parentConversationId | uuid_to_hex_string}"
      forked_at_entry_id: "${secondEntryId | uuid_to_hex_string}"
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    # Verify the fork conversation was created correctly
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee01" | uuid_to_hex_string}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "forkedAtEntryId" should be "${secondEntryId}"
    And the gRPC response field "forkedAtConversationId" should be "${parentConversationId}"

  Scenario: Same client can fork at a journal entry via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${parentConversationId | uuid_to_hex_string}"
    entry {
      user_id: "alice"
      channel: JOURNAL
      content_type: "agent/step"
      content {
        struct_value {
          fields {
            key: "stepType"
            value { string_value: "llm_call" }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "journalEntryId" to the gRPC response field "id"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee02" | uuid_to_hex_string}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "Fork after journal step" }
          }
        }
      }
      forked_at_conversation_id: "${parentConversationId | uuid_to_hex_string}"
      forked_at_entry_id: "${journalEntryId | uuid_to_hex_string}"
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee02" | uuid_to_hex_string}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "forkedAtEntryId" should be "${journalEntryId}"
    And the gRPC response field "forkedAtConversationId" should be "${parentConversationId}"

  Scenario: Different client cannot fork at another client's journal entry via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${parentConversationId | uuid_to_hex_string}"
    entry {
      user_id: "alice"
      channel: JOURNAL
      content_type: "agent/step"
      content {
        struct_value {
          fields {
            key: "stepType"
            value { string_value: "tool_call" }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "journalEntryId" to the gRPC response field "id"
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee03" | uuid_to_hex_string}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "Unauthorized fork" }
          }
        }
      }
      forked_at_conversation_id: "${parentConversationId | uuid_to_hex_string}"
      forked_at_entry_id: "${journalEntryId | uuid_to_hex_string}"
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Context entries cannot be fork anchors via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${parentConversationId | uuid_to_hex_string}"
    entry {
      user_id: "alice"
      channel: CONTEXT
      content_type: "test.v1"
      content {
        string_value: "Context fork point"
      }
    }
    """
    Then the gRPC response should not have an error
    And set "contextEntryId" to the gRPC response field "id"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee04" | uuid_to_hex_string}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "Invalid context fork" }
          }
        }
      }
      forked_at_conversation_id: "${parentConversationId | uuid_to_hex_string}"
      forked_at_entry_id: "${contextEntryId | uuid_to_hex_string}"
    }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: List forks for a conversation via gRPC
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    When I fork the conversation at entry "${secondEntryId}"
    And set "fork1Id" to "${forkedConversationId}"
    When I fork the conversation at entry "${secondEntryId}"
    And set "fork2Id" to "${forkedConversationId}"
    When I send gRPC request "ConversationsService/ListForks" with body:
    """
    conversation_id: "${parentConversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversationIds" should not be null
    And the gRPC response field "forkPoints" should not be null

  Scenario: List forks for non-existent conversation via gRPC
    When I send gRPC request "ConversationsService/ListForks" with body:
    """
    conversation_id: "${"00000000-0000-0000-0000-000000000000" | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: List forks without access via gRPC
    Given there is a conversation owned by "bob"
    When I send gRPC request "ConversationsService/ListForks" with body:
    """
    conversation_id: "${conversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"
