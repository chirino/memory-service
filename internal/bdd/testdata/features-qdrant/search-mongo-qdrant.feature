Feature: Search with MongoDB datastore and Qdrant vector store
  As a user in mongo+qdrant mode
  I want semantic conversation search to use datastore-native ACL lookup
  So that search does not fail through the Postgres fallback path

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Mongo Qdrant Search Conversation"
    And the conversation has an entry "Order status question"

  Scenario: Semantic search does not fail through JDBC path in mongo+qdrant mode
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    Given I am authenticated as indexer user "dave"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conversationId}",
        "entryId": "${firstEntryId}",
        "indexedContent": "Customer account search signal."
      }
    ]
    """
    Then the response status should be 200

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
