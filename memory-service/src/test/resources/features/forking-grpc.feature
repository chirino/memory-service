Feature: Conversation Forking gRPC API
  As a client of the memory service
  I want to fork conversations at specific messages via gRPC
  So that I can create alternative conversation branches using gRPC

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Base Conversation"
    And the conversation has a message "First message"
    And the conversation has a message "Second message"
    And the conversation has a message "Third message"

  Scenario: Fork a conversation at a message via gRPC
    When I list messages for the conversation
    And set "secondMessageId" to the json response field "data[1].id"
    And set "firstMessageId" to the json response field "data[0].id"
    When I send gRPC request "ConversationsService/ForkConversation" with body:
    """
    conversation_id: "${conversationId}"
    message_id: "${secondMessageId}"
    title: "Forked Conversation"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "title" should be "Forked Conversation"
    And the gRPC response field "forkedAtMessageId" should be "${firstMessageId}"
    And the gRPC response field "forkedAtConversationId" should be "${conversationId}"
    And the gRPC response field "title" should be "Forked Conversation"
    And the gRPC response field "ownerUserId" should be "alice"
    And the gRPC response field "accessLevel" should be "OWNER"

  Scenario: Fork a conversation without title via gRPC
    When I list messages for the conversation
    And set "firstMessageId" to the json response field "data[0].id"
    When I send gRPC request "ConversationsService/ForkConversation" with body:
    """
    conversation_id: "${conversationId}"
    message_id: "${firstMessageId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And set "forkedConversationId" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${forkedConversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "forkedAtConversationId" should be "${conversationId}"

  Scenario: List forks for a conversation via gRPC
    When I list messages for the conversation
    And set "secondMessageId" to the json response field "data[1].id"
    And set "firstMessageId" to the json response field "data[0].id"
    When I send gRPC request "ConversationsService/ForkConversation" with body:
    """
    conversation_id: "${conversationId}"
    message_id: "${secondMessageId}"
    title: "Fork 1"
    """
    And set "fork1Id" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/ForkConversation" with body:
    """
    conversation_id: "${conversationId}"
    message_id: "${secondMessageId}"
    title: "Fork 2"
    """
    And set "fork2Id" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/ListForks" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "forks" should not be null
    # The response includes the original conversation (forks[0]) plus the 2 forks
    # Check that forks[1] and forks[2] are the actual forks
    And the gRPC response field "forks[1].forkedAtMessageId" should be "${firstMessageId}"
    And the gRPC response field "forks[1].forkedAtConversationId" should be "${conversationId}"
    And the gRPC response field "forks[2].forkedAtMessageId" should be "${firstMessageId}"
    And the gRPC response field "forks[2].forkedAtConversationId" should be "${conversationId}"

  Scenario: Fork non-existent conversation via gRPC
    When I send gRPC request "ConversationsService/ForkConversation" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    message_id: "00000000-0000-0000-0000-000000000001"
    title: "Fork"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Fork at non-existent message via gRPC
    When I send gRPC request "ConversationsService/ForkConversation" with body:
    """
    conversation_id: "${conversationId}"
    message_id: "00000000-0000-0000-0000-000000000000"
    title: "Fork"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Fork conversation without access via gRPC
    Given there is a conversation owned by "bob"
    When I send gRPC request "ConversationsService/ForkConversation" with body:
    """
    conversation_id: "${conversationId}"
    message_id: "00000000-0000-0000-0000-000000000000"
    title: "Fork"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: List forks for non-existent conversation via gRPC
    When I send gRPC request "ConversationsService/ListForks" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: List forks without access via gRPC
    Given there is a conversation owned by "bob"
    When I send gRPC request "ConversationsService/ListForks" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"
