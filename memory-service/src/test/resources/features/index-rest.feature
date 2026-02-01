Feature: Index Entries REST API
  As an indexer
  I want to index entries for conversations via REST API
  So that they can be searched by users

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has an entry "Order status question"

  Scenario: Indexer can index entries and user can search them
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "${firstEntryId}",
        "indexedContent": "Customer asked about refund policy."
      }
    ]
    """
    Then the response status should be 200
    And the response body "indexed" should be "1"
    Given I am authenticated as user "alice"
    When I search conversations for query "refund policy"
    Then the response status should be 200
    And the search response should contain 1 results
    And search result at index 0 should have conversationId "${conversationId}"
    And the response body "data[0].entryId" should be "${firstEntryId}"

  Scenario: Admin can index entries
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as admin user "alice"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "${firstEntryId}",
        "indexedContent": "Admin indexed content."
      }
    ]
    """
    Then the response status should be 200
    And the response body "indexed" should be "1"

  Scenario: Indexing requires indexer or admin role
    Given I am authenticated as user "alice"
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as user "bob"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "${firstEntryId}",
        "indexedContent": "Should not be allowed."
      }
    ]
    """
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Index with non-existent entryId returns 404
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "00000000-0000-0000-0000-000000000099",
        "indexedContent": "This should fail."
      }
    ]
    """
    Then the response status should be 404

  Scenario: Index with mismatched conversationId returns 404
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "00000000-0000-0000-0000-000000000099",
        "entryId": "${firstEntryId}",
        "indexedContent": "This should fail."
      }
    ]
    """
    Then the response status should be 404

  Scenario: Index multiple entries at once
    Given the conversation has an entry "Second entry"
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    And set "secondEntryId" to the json response field "data[1].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "${firstEntryId}",
        "indexedContent": "First entry indexed content."
      },
      {
        "conversationId": "${conversationId}",
        "entryId": "${secondEntryId}",
        "indexedContent": "Second entry indexed content."
      }
    ]
    """
    Then the response status should be 200
    And the response body "indexed" should be "2"

  Scenario: Index requires at least one entry
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    []
    """
    Then the response status should be 400

  Scenario: Index validates required fields
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "indexedContent": "Missing entryId"
      }
    ]
    """
    Then the response status should be 400

  Scenario: List unindexed entries
    Given I am authenticated as indexer user "dave"
    When I call GET "/v1/conversations/unindexed?limit=10"
    Then the response status should be 200
    And the response body should have field "data" that is not null

  Scenario: List unindexed entries requires indexer role
    Given I am authenticated as user "bob"
    When I call GET "/v1/conversations/unindexed"
    Then the response status should be 403

  Scenario: Search conversations with pagination
    Given I am authenticated as user "alice"
    And I have a conversation with title "Conversation A"
    And set "conversationA" to "${conversationId}"
    And the conversation has an entry "First topic discussion"
    When I list entries for the conversation
    And set "entryA" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationA}",
        "entryId": "${entryA}",
        "indexedContent": "Discussion about apples and oranges."
      }
    ]
    """
    Given I am authenticated as user "alice"
    And I have a conversation with title "Conversation B"
    And set "conversationB" to "${conversationId}"
    And the conversation has an entry "Second topic discussion"
    When I list entries for the conversation
    And set "entryB" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationB}",
        "entryId": "${entryB}",
        "indexedContent": "Discussion about apples and bananas."
      }
    ]
    """
    Given I am authenticated as user "alice"
    And I have a conversation with title "Conversation C"
    And set "conversationC" to "${conversationId}"
    And the conversation has an entry "Third topic discussion"
    When I list entries for the conversation
    And set "entryC" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationC}",
        "entryId": "${entryC}",
        "indexedContent": "Discussion about apples and grapes."
      }
    ]
    """
    Given I am authenticated as user "alice"
    When I search conversations with request:
    """
    {
      "query": "apples",
      "limit": 2
    }
    """
    Then the response status should be 200
    And the search response should contain 2 results
    And the response should have a nextCursor

  Scenario: Search result includes entryId at top level
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "${firstEntryId}",
        "indexedContent": "Searchable content for entryId test."
      }
    ]
    """
    Given I am authenticated as user "alice"
    When I search conversations for query "entryId test"
    Then the response status should be 200
    And the search response should contain 1 results
    And the response body "data[0].entryId" should be "${firstEntryId}"

  Scenario: Search conversations without entry content
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "${firstEntryId}",
        "indexedContent": "This is searchable content for lightweight test."
      }
    ]
    """
    Given I am authenticated as user "alice"
    When I search conversations with request:
    """
    {
      "query": "lightweight test",
      "includeEntry": false
    }
    """
    Then the response status should be 200
    And the search response should contain 1 results
    And search result at index 0 should not have entry
