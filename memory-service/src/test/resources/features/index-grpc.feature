Feature: Index Entries gRPC API
  As an indexer
  I want to index entries for conversations via gRPC
  So that they can be searched by users

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has an entry "User entry"

  Scenario: Index entries requires indexer role via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as user "bob"
    When I send gRPC request "SearchService/IndexConversations" with body:
    """
    entries {
      conversation_id: "${conversationId}"
      entry_id: "${firstEntryId}"
      indexed_content: "This is a test text"
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Indexer can index entries via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I send gRPC request "SearchService/IndexConversations" with body:
    """
    entries {
      conversation_id: "${conversationId}"
      entry_id: "${firstEntryId}"
      indexed_content: "This is a test text"
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "indexed" should be "1"

  Scenario: Admin can index entries via gRPC
    Given I am authenticated as admin user "alice"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    And I send gRPC request "SearchService/IndexConversations" with body:
    """
    entries {
      conversation_id: "${conversationId}"
      entry_id: "${firstEntryId}"
      indexed_content: "Admin indexed this text"
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "indexed" should be "1"

  Scenario: Search conversations via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I send gRPC request "SearchService/IndexConversations" with body:
    """
    entries {
      conversation_id: "${conversationId}"
      entry_id: "${firstEntryId}"
      indexed_content: "This text discusses gRPC search functionality"
    }
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
    And the gRPC response field "results[0].entryId" should be "${firstEntryId}"
    And the gRPC response field "results[0].score" should not be null

  Scenario: List unindexed entries via gRPC
    Given I am authenticated as indexer user "dave"
    When I send gRPC request "SearchService/ListUnindexedEntries" with body:
    """
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "entries" should not be null

  Scenario: List unindexed entries requires indexer role via gRPC
    Given I am authenticated as user "bob"
    When I send gRPC request "SearchService/ListUnindexedEntries" with body:
    """
    limit: 10
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Search conversations with pagination via gRPC
    Given I am authenticated as user "alice"
    And I have a conversation with title "gRPC Pagination A"
    And set "conversationA" to "${conversationId}"
    And the conversation has an entry "First topic discussion"
    When I list entries for the conversation
    And set "entryA" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I send gRPC request "SearchService/IndexConversations" with body:
    """
    entries {
      conversation_id: "${conversationA}"
      entry_id: "${entryA}"
      indexed_content: "Discussion about apples and oranges."
    }
    """
    Given I am authenticated as user "alice"
    And I have a conversation with title "gRPC Pagination B"
    And set "conversationB" to "${conversationId}"
    And the conversation has an entry "Second topic discussion"
    When I list entries for the conversation
    And set "entryB" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I send gRPC request "SearchService/IndexConversations" with body:
    """
    entries {
      conversation_id: "${conversationB}"
      entry_id: "${entryB}"
      indexed_content: "Discussion about apples and bananas."
    }
    """
    Given I am authenticated as user "alice"
    And I have a conversation with title "gRPC Pagination C"
    And set "conversationC" to "${conversationId}"
    And the conversation has an entry "Third topic discussion"
    When I list entries for the conversation
    And set "entryC" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I send gRPC request "SearchService/IndexConversations" with body:
    """
    entries {
      conversation_id: "${conversationC}"
      entry_id: "${entryC}"
      indexed_content: "Discussion about apples and grapes."
    }
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
    Given I am authenticated as user "alice"
    And the conversation exists
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I send gRPC request "SearchService/IndexConversations" with body:
    """
    entries {
      conversation_id: "${conversationId}"
      entry_id: "${firstEntryId}"
      indexed_content: "Testing search without returning entry content"
    }
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
    And the gRPC response field "results[0].entryId" should be "${firstEntryId}"
    And the gRPC response field "results[0].entry" should be null
