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

  Scenario: Search conversations via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    And I send gRPC request "SearchService/IndexTranscript" with body:
    """
    conversation_id: "${conversationId}"
    title: "gRPC Search Test"
    transcript: "This transcript discusses gRPC search functionality"
    until_entry_id: "${firstEntryId}"
    """
    Then the gRPC response should not have an error
    Given I am authenticated as user "alice"
    When I send gRPC request "SearchService/SearchConversations" with body:
    """
    query: "gRPC search"
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "results" should not be null
    And the gRPC response field "results[0].conversationId" should be "${conversationId}"
    And the gRPC response field "results[0].conversationTitle" should be "gRPC Search Test"
    And the gRPC response field "results[0].score" should not be null

  Scenario: Search conversations with pagination via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "gRPC Pagination A"
    And set "conversationA" to "${conversationId}"
    And the conversation has an entry "First topic discussion"
    When I list entries for the conversation
    And set "entryA" to the json response field "data[0].id"
    And I send gRPC request "SearchService/IndexTranscript" with body:
    """
    conversation_id: "${conversationA}"
    title: "gRPC Pagination A"
    transcript: "Discussion about apples and oranges."
    until_entry_id: "${entryA}"
    """
    And I have a conversation with title "gRPC Pagination B"
    And set "conversationB" to "${conversationId}"
    And the conversation has an entry "Second topic discussion"
    When I list entries for the conversation
    And set "entryB" to the json response field "data[0].id"
    And I send gRPC request "SearchService/IndexTranscript" with body:
    """
    conversation_id: "${conversationB}"
    title: "gRPC Pagination B"
    transcript: "Discussion about apples and bananas."
    until_entry_id: "${entryB}"
    """
    And I have a conversation with title "gRPC Pagination C"
    And set "conversationC" to "${conversationId}"
    And the conversation has an entry "Third topic discussion"
    When I list entries for the conversation
    And set "entryC" to the json response field "data[0].id"
    And I send gRPC request "SearchService/IndexTranscript" with body:
    """
    conversation_id: "${conversationC}"
    title: "gRPC Pagination C"
    transcript: "Discussion about apples and grapes."
    until_entry_id: "${entryC}"
    """
    Given I am authenticated as user "alice"
    When I send gRPC request "SearchService/SearchConversations" with body:
    """
    query: "apples"
    limit: 2
    """
    Then the gRPC response should not have an error
    And the gRPC response field "results" should have size 2
    And the gRPC response field "nextCursor" should not be null

  Scenario: Search conversations without entry via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    And I send gRPC request "SearchService/IndexTranscript" with body:
    """
    conversation_id: "${conversationId}"
    title: "No Entry Test"
    transcript: "Testing search without returning entry content"
    until_entry_id: "${firstEntryId}"
    """
    Then the gRPC response should not have an error
    Given I am authenticated as user "alice"
    When I send gRPC request "SearchService/SearchConversations" with body:
    """
    query: "without returning entry"
    limit: 10
    include_entry: false
    """
    Then the gRPC response should not have an error
    And the gRPC response field "results[0].conversationId" should be "${conversationId}"
    And the gRPC response field "results[0].conversationTitle" should be "No Entry Test"
    And the gRPC response field "results[0].entry" should be null
