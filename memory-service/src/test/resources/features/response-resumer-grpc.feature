Feature: Response Resumer gRPC API
  As a client of the memory service
  I want to stream, replay, and check response tokens via gRPC
  So that I can resume interrupted agent responses

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Response Resumer Test"

  Scenario: Check if response resumer is enabled
    When I send gRPC request "ResponseResumerService/IsEnabled" with body:
    """
    """
    Then the gRPC response should not have an error
    And the gRPC response field "enabled" should not be null

  Scenario: Check if conversation has response in progress when none exists
    When I send gRPC request "ResponseResumerService/HasResponseInProgress" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "inProgress" should be false

  Scenario: Check response in progress for non-existent conversation
    When I send gRPC request "ResponseResumerService/HasResponseInProgress" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Check response in progress without access
    Given there is a conversation owned by "bob"
    When I send gRPC request "ResponseResumerService/HasResponseInProgress" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Check multiple conversations for responses in progress
    Given I have a conversation with title "Conversation 1"
    And I set context variable "conversationId1" to "${conversationId}"
    And I have a conversation with title "Conversation 2"
    And I set context variable "conversationId2" to "${conversationId}"
    When I send gRPC request "ResponseResumerService/CheckConversations" with body:
    """
    conversation_ids: "${conversationId1}"
    conversation_ids: "${conversationId2}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversationIds" should not be null

  Scenario: Check conversations with mixed access
    Given I have a conversation with title "My Conversation"
    And I set context variable "myConversationId" to "${conversationId}"
    Given there is a conversation owned by "bob"
    And I set context variable "bobConversationId" to "${conversationId}"
    When I send gRPC request "ResponseResumerService/CheckConversations" with body:
    """
    conversation_ids: "${myConversationId}"
    conversation_ids: "${bobConversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversationIds" should not be null

  Scenario: Stream response tokens for a conversation
    When I send gRPC request "ResponseResumerService/StreamResponseTokens" with body:
    """
    conversation_id: "${conversationId}"
    token: "Hello"
    complete: false
    """
    Then the gRPC response should not have an error
    And the gRPC response field "success" should be true
    And the gRPC response field "currentOffset" should be 5

  Scenario: Stream multiple response tokens
    Given I have streamed tokens "Hello World!" to the conversation
    # This scenario tests that multiple tokens can be streamed in a single request
    # The iHaveStreamedTokensToTheConversation step handles streaming all tokens
    # in a single gRPC stream, which is the correct way to use the API

  Scenario: Replay response tokens while stream is in progress
    Given I start streaming tokens "Hello World" to the conversation with 50ms delay and keep the stream open for 500ms
    And I wait for the response stream to send at least 2 tokens
    When I replay response tokens from position 0 in a second session and collect tokens "Hello World"
    Then the replay should finish before the stream completes
    And I wait for the response stream to complete

  Scenario: Stream response tokens without conversation_id
    When I send gRPC request "ResponseResumerService/StreamResponseTokens" with body:
    """
    token: "Hello"
    complete: false
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Stream response tokens without access
    Given there is a conversation owned by "bob"
    When I send gRPC request "ResponseResumerService/StreamResponseTokens" with body:
    """
    conversation_id: "${conversationId}"
    token: "Hello"
    complete: false
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Stream response tokens for non-existent conversation
    When I send gRPC request "ResponseResumerService/StreamResponseTokens" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    token: "Hello"
    complete: false
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Replay response tokens from position zero
    Given I have streamed tokens "Hello World" to the conversation
    When I send gRPC request "ResponseResumerService/ReplayResponseTokens" with body:
    """
    conversation_id: "${conversationId}"
    resume_position: 0
    """
    Then the gRPC response should not have an error
    # Note: Server streaming responses need special handling in step definitions

  Scenario: Replay response tokens from middle position
    Given I have streamed tokens "Hello World" to the conversation
    When I send gRPC request "ResponseResumerService/ReplayResponseTokens" with body:
    """
    conversation_id: "${conversationId}"
    resume_position: 6
    """
    Then the gRPC response should not have an error
    # Note: Server streaming responses need special handling in step definitions

  Scenario: Replay response tokens without access
    Given there is a conversation owned by "bob"
    When I send gRPC request "ResponseResumerService/ReplayResponseTokens" with body:
    """
    conversation_id: "${conversationId}"
    resume_position: 0
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Replay response tokens for non-existent conversation
    When I send gRPC request "ResponseResumerService/ReplayResponseTokens" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    resume_position: 0
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Replay response tokens with invalid resume position
    Given I have streamed tokens "Hello" to the conversation
    When I send gRPC request "ResponseResumerService/ReplayResponseTokens" with body:
    """
    conversation_id: "${conversationId}"
    resume_position: 1000
    """
    Then the gRPC response should not have an error
    # Note: Server streaming responses need special handling in step definitions
