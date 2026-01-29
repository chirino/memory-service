Feature: Index Transcript gRPC API
  As an agent
  I want to index transcripts for conversations via gRPC
  So that they can be searched by users

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has an entry "User entry"

  Scenario: Index transcript requires API key via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    And I send gRPC request "SearchService/IndexTranscript" with body:
    """
    conversation_id: "${conversationId}"
    title: "Test Title"
    transcript: "This is a test transcript"
    until_entry_id: "${firstEntryId}"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Agent can index transcript via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    And I send gRPC request "SearchService/IndexTranscript" with body:
    """
    conversation_id: "${conversationId}"
    title: "Test Title"
    transcript: "This is a test transcript"
    until_entry_id: "${firstEntryId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "conversationId" should be "${conversationId}"
    And the gRPC response field "channel" should be "TRANSCRIPT"
    And the gRPC response field "contentType" should be "transcript"
    And the gRPC response field "content" should not be null
