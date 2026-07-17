Feature: Conversations gRPC API
  As a client of the memory service
  I want to manage conversations via gRPC
  So that I can create, list, get, and archive conversations using gRPC

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

  Scenario: Create a conversation with a client-supplied ID via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    id: "external-session/thread:42"
    title: "Conversation with deterministic ID"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "external-session/thread:42"
    And the gRPC response field "title" should be "Conversation with deterministic ID"
    And the gRPC response field "ownerUserId" should be "alice"
    And the gRPC response field "accessLevel" should be "OWNER"

  Scenario: Reject an empty client-supplied conversation ID via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    id: ""
    title: "Conversation with empty ID"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

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
    And the gRPC response field "metadata.project" should be "test-project"
    And the gRPC response field "metadata.priority" should be "high"

  Scenario: Create a conversation with agent and fork metadata via gRPC
    Given I have a conversation with title "gRPC Fork Source"
    And set "sourceConversationId" to "${conversationId}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "history",
      "channel": "HISTORY",
      "content": [{"role": "USER", "text": "fork source"}]
    }
    """
    And set "sourceEntryId" to "${response.body.id}"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    id: "grpc-fork-target"
    title: "gRPC Fork Target"
    agent_id: "agent-alpha"
    forked_at_conversation_id: "${sourceConversationId}"
    forked_at_entry_id: "${sourceEntryId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "grpc-fork-target"
    And the gRPC response field "agentId" should be "agent-alpha"
    And the gRPC response field "forkedAtConversationId" should be "${sourceConversationId}"
    And the gRPC response field "forkedAtEntryId" should be "${sourceEntryId}"
    And the gRPC response field "hasResponseInProgress" should be false

  Scenario: Update conversation metadata via gRPC
    Given I have a conversation with title "Metadata Before"
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    title: "Metadata After"
    metadata {
      fields {
        key: "status"
        value { string_value: "updated" }
      }
    }
    """
    Then the gRPC response should not have an error
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "metadata.status" should be "updated"

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

  Scenario: List child conversations defaults to the REST page size via gRPC
    Given I have a conversation with title "Parent Conversation"
    And set "parentConversationId" to "${conversationId}"
    And the conversation has 25 child conversations
    When I send gRPC request "ConversationsService/ListChildConversations" with body:
    """
    conversation_id: "${parentConversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 20

  Scenario: Get a conversation via gRPC
    Given I have a conversation with title "Test Conversation"
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${conversationId}"
    And the gRPC response field "title" should be "Test Conversation"
    And the gRPC response text should match text proto:
    """
    id: "${conversationId}"
    title: "Test Conversation"
    owner_user_id: "alice"
    access_level: OWNER
    """

  Scenario: Get non-existent conversation via gRPC
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Admin get conversation exposes admin fields via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And set "expectedClientId" to the current client ID
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Admin gRPC Payload"
    metadata {
      fields {
        key: "project"
        value {
          string_value: "grpc-admin"
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "adminConversationId" to the gRPC response field "id"
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/GetConversation" with body:
    """
    conversation_id: "${adminConversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "clientId" should be "${expectedClientId}"
    And the gRPC response field "metadata.project" should be "grpc-admin"

  Scenario: Get conversation without access via gRPC
    Given there is a conversation owned by "bob"
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Archived conversations can be listed via archive filter
    Given I have a conversation with title "To Be Deleted"
    And I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    archived: true
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    archived: ARCHIVE_FILTER_ONLY
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].archived" should be true

  Scenario: Conversation response does not contain conversation_group_id via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Test Conversation"
    """
    Then the gRPC response should not have an error
    And the gRPC response should not contain field "conversationGroupId"

  Scenario: Archiving a conversation archives all forks via gRPC
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
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${rootConversationId}"
    archived: true
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${rootConversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "archived" should be true
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${forkConversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "archived" should be true
