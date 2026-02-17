Feature: Update Conversation gRPC API
  As a client of the memory service
  I want to update conversation properties via gRPC
  So that I can rename conversations to keep my history organized

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Original Title"

  Scenario: Update conversation title via gRPC
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    title: "Updated Title"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "title" should be "Updated Title"
    And the gRPC response field "ownerUserId" should be "alice"
    And the gRPC response field "accessLevel" should be "OWNER"

  Scenario: Update non-existent conversation via gRPC
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    title: "New Title"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Update conversation without access via gRPC
    Given there is a conversation owned by "bob"
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    title: "Hacked Title"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Writer can update title via gRPC
    When I share the conversation with user "bob" with request:
    """
    {
      "userId": "bob",
      "accessLevel": "writer"
    }
    """
    Then the response status should be 201
    Given I am authenticated as user "bob"
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    title: "Writer Updated Title"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "title" should be "Writer Updated Title"
    And the gRPC response field "accessLevel" should be "WRITER"

  Scenario: Reader cannot update title via gRPC
    When I share the conversation with user "charlie" with request:
    """
    {
      "userId": "charlie",
      "accessLevel": "reader"
    }
    """
    Then the response status should be 201
    Given I am authenticated as user "charlie"
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    title: "Reader Title"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"
