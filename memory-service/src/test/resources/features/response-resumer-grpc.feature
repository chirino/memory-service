Feature: Response Recorder gRPC API
  As a client of the memory service
  I want to record, replay, and check response recordings via gRPC
  So that I can resume interrupted agent responses

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Response Recorder Test"

  Scenario: Check if response recorder is enabled
    When I send gRPC request "ResponseRecorderService/IsEnabled" with body:
    """
    """
    Then the gRPC response should not have an error
    And the gRPC response field "enabled" should not be null

  Scenario: Check if conversation has recording in progress when none exists
    When I send gRPC request "ResponseRecorderService/CheckRecordings" with body:
    """
    conversation_ids: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversationIds" should be "[]"

  Scenario: Check recording in progress for non-existent conversation
    When I send gRPC request "ResponseRecorderService/CheckRecordings" with body:
    """
    conversation_ids: "00000000-0000-0000-0000-000000000000"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversationIds" should be "[]"

  Scenario: Check recording in progress without access
    Given there is a conversation owned by "bob"
    When I send gRPC request "ResponseRecorderService/CheckRecordings" with body:
    """
    conversation_ids: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversationIds" should be "[]"

  Scenario: Check multiple conversations for recordings in progress
    Given I have a conversation with title "Conversation 1"
    And I set context variable "conversationId1" to "${conversationId}"
    And I have a conversation with title "Conversation 2"
    And I set context variable "conversationId2" to "${conversationId}"
    When I send gRPC request "ResponseRecorderService/CheckRecordings" with body:
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
    When I send gRPC request "ResponseRecorderService/CheckRecordings" with body:
    """
    conversation_ids: "${myConversationId}"
    conversation_ids: "${bobConversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversationIds" should not be null

  Scenario: Record response content for a conversation
    When I send gRPC request "ResponseRecorderService/Record" with body:
    """
    conversation_id: "${conversationId}"
    content: "Hello"
    complete: true
    """
    Then the gRPC response should not have an error
    And the gRPC response field "status" should be "RECORD_STATUS_SUCCESS"

  Scenario: Record multiple response content chunks
    Given I have streamed tokens "Hello World!" to the conversation
    # This scenario tests that multiple content chunks can be streamed in a single request
    # The iHaveStreamedTokensToTheConversation step handles streaming all content
    # in a single gRPC stream, which is the correct way to use the API

  Scenario: Replay response while recording is in progress
    Given I start streaming tokens "Hello World" to the conversation with 50ms delay and keep the stream open for 1500ms
    And I wait for the response stream to send at least 2 tokens
    When I replay response tokens from the beginning in a second session and collect tokens "Hello World"
    Then the replay should start before the stream completes
    And I wait for the response stream to complete

  Scenario: Cancel an in-progress recording
    Given I start streaming tokens "Hello cancel" to the conversation with 50ms delay and keep the stream open until canceled
    And I wait for the response stream to send at least 2 tokens
    When I send gRPC request "ResponseRecorderService/Cancel" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "accepted" should be true
    And I wait for the response stream to complete

  Scenario: Record without conversation_id
    When I send gRPC request "ResponseRecorderService/Record" with body:
    """
    content: "Hello"
    complete: false
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Record without access
    Given there is a conversation owned by "bob"
    When I send gRPC request "ResponseRecorderService/Record" with body:
    """
    conversation_id: "${conversationId}"
    content: "Hello"
    complete: false
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Record for non-existent conversation
    When I send gRPC request "ResponseRecorderService/Record" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    content: "Hello"
    complete: false
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Replay from beginning
    Given I have streamed tokens "Hello World" to the conversation
    When I send gRPC request "ResponseRecorderService/Replay" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    # Note: Server streaming responses need special handling in step definitions

  Scenario: Replay without access
    Given there is a conversation owned by "bob"
    When I send gRPC request "ResponseRecorderService/Replay" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Replay for non-existent conversation
    When I send gRPC request "ResponseRecorderService/Replay" with body:
    """
    conversation_id: "00000000-0000-0000-0000-000000000000"
    """
    Then the gRPC response should have status "NOT_FOUND"
