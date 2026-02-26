Feature: Conversations gRPC API
  As a client of the memory service
  I want to manage conversations via gRPC
  So that I can create, list, get, and delete conversations using gRPC

  Background:
    Given I am authenticated as user "alice"

  Scenario: Call SystemService.GetHealth via gRPC
    When I send gRPC request "SystemService/GetHealth" with body:
    """
    {}
    """
    Then the gRPC response field "status" should be "ok"
    And the gRPC response text should match text proto:
    """
    status: "ok"
    """

  Scenario: Create a conversation via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "My First Conversation"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "title" should be "My First Conversation"
    And the gRPC response field "ownerUserId" should be "alice"
    And the gRPC response field "accessLevel" should be "OWNER"
    And the gRPC response text should match text proto:
    """
    id: "${response.body.id | base64_to_hex_string}"
    title: "My First Conversation"
    owner_user_id: "alice"
    access_level: OWNER
    """

  Scenario: Create a conversation with metadata via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Conversation with Metadata"
    metadata {
      fields {
        key: "project"
        value {
          string_value: "test-project"
        }
      }
      fields {
        key: "priority"
        value {
          string_value: "high"
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "title" should be "Conversation with Metadata"

  Scenario: List conversations via gRPC
    Given I have a conversation with title "First Conversation"
    And I have a conversation with title "Second Conversation"
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should not be null
    And the gRPC response text should match text proto:
    """
    conversations {
      title: "${response.body.conversations[0].title}"
      owner_user_id: "alice"
      access_level: OWNER
    }
    """

  Scenario: List conversations with pagination via gRPC
    Given I have a conversation with title "Conversation 1"
    And I have a conversation with title "Conversation 2"
    And I have a conversation with title "Conversation 3"
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    page {
      page_size: 2
    }
    """
    Then the gRPC response should not have an error
    And set "firstConversationId" to the gRPC response field "conversations[0].id"
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    page {
      page_token: "${firstConversationId}"
      page_size: 2
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should not be null

  Scenario: List conversations with query filter via gRPC
    Given I have a conversation with title "Project Alpha Discussion"
    And I have a conversation with title "Project Beta Discussion"
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    query: "Alpha"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should not be null

  Scenario: Get a conversation via gRPC
    Given I have a conversation with title "Test Conversation"
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${conversationId}"
    And the gRPC response field "title" should be "Test Conversation"
    And the gRPC response text should match text proto:
    """
    id: "${conversationId | uuid_to_hex_string}"
    title: "Test Conversation"
    owner_user_id: "alice"
    access_level: OWNER
    """

  Scenario: Get non-existent conversation via gRPC
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${"00000000-0000-0000-0000-000000000000" | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Get conversation without access via gRPC
    Given there is a conversation owned by "bob"
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Delete a conversation via gRPC
    Given I have a conversation with title "To Be Deleted"
    When I send gRPC request "ConversationsService/DeleteConversation" with body:
    """
    conversation_id: "${conversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Delete non-existent conversation via gRPC
    When I send gRPC request "ConversationsService/DeleteConversation" with body:
    """
    conversation_id: "${"00000000-0000-0000-0000-000000000000" | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Delete conversation without access via gRPC
    Given there is a conversation owned by "bob"
    When I send gRPC request "ConversationsService/DeleteConversation" with body:
    """
    conversation_id: "${conversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Conversation response does not contain conversation_group_id via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Test Conversation"
    """
    Then the gRPC response should not have an error
    And the gRPC response should not contain field "conversationGroupId"

  Scenario: Deleting a conversation deletes all forks via gRPC
    Given I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "entryId" to "${response.body.id}"
    When I fork the conversation at entry "${entryId}"
    And set "forkConversationId" to "${response.body.id}"
    When I send gRPC request "ConversationsService/DeleteConversation" with body:
    """
    conversation_id: "${rootConversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${rootConversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "NOT_FOUND"
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${forkConversationId | uuid_to_hex_string}"
    """
    Then the gRPC response should have status "NOT_FOUND"
