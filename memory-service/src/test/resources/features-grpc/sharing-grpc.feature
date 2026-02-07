Feature: Conversation Sharing gRPC API
  As a client of the memory service
  I want to share conversations with other users and transfer ownership via gRPC
  So that I can collaborate on conversations and manage access using gRPC

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Shared Conversation"

  Scenario: Share a conversation with a user via gRPC
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: WRITER
    """
    Then the gRPC response should not have an error
    And the gRPC response field "userId" should be "bob"
    And the gRPC response field "accessLevel" should be "WRITER"
    And the gRPC response text should match text proto:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: WRITER
    """

  Scenario: Share a conversation with reader access via gRPC
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "charlie"
    access_level: READER
    """
    Then the gRPC response should not have an error
    And the gRPC response field "userId" should be "charlie"
    And the gRPC response field "accessLevel" should be "READER"

  Scenario: Share a conversation with manager access via gRPC
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "dave"
    access_level: MANAGER
    """
    Then the gRPC response should not have an error
    And the gRPC response field "userId" should be "dave"
    And the gRPC response field "accessLevel" should be "MANAGER"

  Scenario: List conversation memberships via gRPC
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: WRITER
    """
    When I send gRPC request "ConversationMembershipsService/ListMemberships" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "memberships" should not be null
    And the gRPC response field "memberships[0].userId" should not be null
    And the gRPC response field "memberships[0].accessLevel" should not be null
    And the gRPC response field "memberships[1].userId" should not be null
    And the gRPC response field "memberships[1].accessLevel" should not be null

  Scenario: Update membership access level via gRPC
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: READER
    """
    When I send gRPC request "ConversationMembershipsService/UpdateMembership" with body:
    """
    conversation_id: "${conversationId}"
    member_user_id: "bob"
    access_level: WRITER
    """
    Then the gRPC response should not have an error
    And the gRPC response field "userId" should be "bob"
    And the gRPC response field "accessLevel" should be "WRITER"
    And the gRPC response text should match text proto:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: WRITER
    """

  Scenario: Delete a membership via gRPC
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: WRITER
    """
    When I send gRPC request "ConversationMembershipsService/DeleteMembership" with body:
    """
    conversation_id: "${conversationId}"
    member_user_id: "bob"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationMembershipsService/ListMemberships" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "memberships" should not be null

  Scenario: Share non-existent conversation via gRPC
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    user_id: "bob"
    access_level: WRITER
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Share conversation without access via gRPC
    Given there is a conversation owned by "bob"
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "charlie"
    access_level: WRITER
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: List memberships as a reader via gRPC
    Given there is a conversation owned by "bob"
    And I am authenticated as user "charlie"
    And the conversation is shared with user "charlie" with access level "reader"
    When I send gRPC request "ConversationMembershipsService/ListMemberships" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error

  Scenario: Update membership without manager access via gRPC
    Given there is a conversation owned by "bob"
    And I am authenticated as user "charlie"
    And the conversation is shared with user "charlie" with access level "writer"
    When I send gRPC request "ConversationMembershipsService/UpdateMembership" with body:
    """
    conversation_id: "${conversationId}"
    member_user_id: "dave"
    access_level: READER
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Delete membership without manager access via gRPC
    Given there is a conversation owned by "bob"
    And I am authenticated as user "charlie"
    And the conversation is shared with user "charlie" with access level "writer"
    When I send gRPC request "ConversationMembershipsService/DeleteMembership" with body:
    """
    conversation_id: "${conversationId}"
    member_user_id: "dave"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Membership response contains conversation_id instead of conversation_group_id via gRPC
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: WRITER
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversationId" should be "${conversationId}"
    And the gRPC response should not contain field "conversationGroupId"
