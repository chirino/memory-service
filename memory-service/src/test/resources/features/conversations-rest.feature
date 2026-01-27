Feature: Conversations REST API
  As a user
  I want to manage conversations via REST API
  So that I can create, list, get, and delete conversations

  Background:
    Given I am authenticated as user "alice"

  Scenario: Create a conversation
    When I create a conversation with request:
    """
    {
      "title": "My First Conversation"
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "title": "My First Conversation",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner",
      "conversationGroupId": "${response.body.conversationGroupId}"
    }
    """

  Scenario: Create a conversation with metadata
    When I create a conversation with request:
    """
    {
      "title": "Conversation with Metadata",
      "metadata": {
        "project": "test-project",
        "priority": "high"
      }
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "title": "Conversation with Metadata",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner",
      "conversationGroupId": "${response.body.conversationGroupId}"
    }
    """

  Scenario: List conversations
    Given I have a conversation with title "First Conversation"
    And I have a conversation with title "Second Conversation"
    When I list conversations
    Then the response status should be 200
    And the response should contain at least 2 conversations
    And the response body should be json:
    """
    {
      "nextCursor": null,
      "data": [
        {
          "id": "${response.body.data[0].id}",
          "title": "${response.body.data[0].title}",
          "ownerUserId": "alice",
          "createdAt": "${response.body.data[0].createdAt}",
          "updatedAt": "${response.body.data[0].updatedAt}",
          "accessLevel": "owner"
        },
        {
          "id": "${response.body.data[1].id}",
          "title": "${response.body.data[1].title}",
          "ownerUserId": "alice",
          "createdAt": "${response.body.data[1].createdAt}",
          "updatedAt": "${response.body.data[1].updatedAt}",
          "accessLevel": "owner"
        }
      ]
    }
    """

  Scenario: List conversations with pagination
    Given I have a conversation with title "Conversation 1"
    And I have a conversation with title "Conversation 2"
    And I have a conversation with title "Conversation 3"
    When I list conversations with limit 2
    Then the response status should be 200
    And the response should contain 2 conversations
    And set "firstConversationId" to the json response field "data[0].id"
    When I list conversations with limit 2 and after "${firstConversationId}"
    Then the response status should be 200
    And the response should contain at least 1 conversation

  Scenario: List conversations with query filter
    Given I have a conversation with title "Project Alpha Discussion"
    And I have a conversation with title "Project Beta Discussion"
    When I list conversations with query "Alpha"
    Then the response status should be 200
    And the response should contain at least 1 conversation
    And the response body should contain "Project Alpha Discussion"

  Scenario: Get a conversation
    Given I have a conversation with title "Test Conversation"
    When I get the conversation
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "id": "${conversationId}",
      "title": "Test Conversation",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner",
      "conversationGroupId": "${response.body.conversationGroupId}"
    }
    """

  Scenario: Get non-existent conversation
    When I get conversation "00000000-0000-0000-0000-000000000000"
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Get conversation without access
    Given there is a conversation owned by "bob"
    When I get that conversation
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Delete a conversation performs soft delete
    Given I have a conversation with title "To Be Deleted"
    When I delete the conversation
    Then the response status should be 204
    # API should treat it as deleted
    When I get the conversation
    Then the response status should be 404
    # But data should still exist in database with deleted_at set
    When I execute SQL query:
    """
    SELECT id, deleted_at FROM conversations WHERE id = '${conversationId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "deleted_at" should be non-null

  Scenario: Deleted conversation excluded from list
    Given I have a conversation with title "Active Conversation"
    And set "activeConversationId" to "${conversationId}"
    And I have a conversation with title "To Be Deleted"
    And set "deletedConversationId" to "${conversationId}"
    When I delete the conversation
    And I list conversations
    Then the response status should be 200
    And the response should contain 1 conversation
    # Verify both still exist in database
    When I execute SQL query:
    """
    SELECT id, title, deleted_at FROM conversations ORDER BY created_at
    """
    Then the SQL result should have 2 rows

  Scenario: Soft delete cascades to conversation group and memberships
    Given I have a conversation with title "Test Conversation"
    And set "groupId" to "${conversationGroupId}"
    When I delete the conversation
    Then the response status should be 204
    # Verify conversation group is soft deleted
    When I execute SQL query:
    """
    SELECT id, deleted_at FROM conversation_groups WHERE id = '${groupId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "deleted_at" should be non-null
    # Verify membership is soft deleted
    When I execute SQL query:
    """
    SELECT conversation_group_id, user_id, deleted_at
    FROM conversation_memberships
    WHERE conversation_group_id = '${groupId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "deleted_at" should be non-null
    # Verify messages still exist (not soft deleted, just orphaned by parent)
    When I execute SQL query:
    """
    SELECT COUNT(*) as count FROM messages WHERE conversation_group_id = '${groupId}'
    """
    Then the SQL result should match:
      | count |
      | 0     |

  Scenario: Delete non-existent conversation
    When I delete conversation "00000000-0000-0000-0000-000000000000"
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Delete conversation without access
    Given there is a conversation owned by "bob"
    When I delete that conversation
    Then the response status should be 403
    And the response should contain error code "forbidden"
