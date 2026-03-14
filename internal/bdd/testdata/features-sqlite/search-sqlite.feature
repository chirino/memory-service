Feature: Search with SQLite datastore and sqlite vector store
  As a user in sqlite+sqlite mode
  I want semantic conversation search to work through the SQLite vector backend
  So that semantic and fulltext search can be combined safely

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "SQLite Search Conversation"
    And the conversation has an entry "Order status question"

  Scenario: Appended history entry is vectorized and removed from unindexed queue
    When I append an entry to the conversation:
    """
    {
      "contentType": "history",
      "channel": "HISTORY",
      "indexedContent": "append-semantic-${conversationId}-signal",
      "content": [{"role": "USER", "text": "A customer note for semantic indexing."}]
    }
    """
    Then the response status should be 201
    And set "appendedEntryId" to the json response field "id"
    Given I am authenticated as indexer user "dave"
    When I call GET "/v1/conversations/unindexed?limit=200"
    Then the response status should be 200
    And the response body should not contain "${appendedEntryId}"

  Scenario: Search can request semantic and fulltext together with per-type limit
    Given I am authenticated as user "alice"
    And the conversation has an entry "Dual mode query seed"
    When I list entries for the conversation
    And set "dualEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "${dualEntryId}",
        "indexedContent": "dual mode search signal"
      }
    ]
    """
    Given I am authenticated as user "alice"
    When I search conversations with request:
    """
    {
      "query": "dual mode search",
      "searchType": ["semantic", "fulltext"],
      "limit": 1
    }
    """
    Then the response status should be 200
    And the search response should contain at least 1 results
    And the search response should contain at most 2 results
