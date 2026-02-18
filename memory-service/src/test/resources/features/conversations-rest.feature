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
      "accessLevel": "owner"
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
      "accessLevel": "owner"
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
      "afterCursor": null,
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
    When I list conversations with limit 2 and afterCursor "${firstConversationId}"
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
      "accessLevel": "owner"
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

  Scenario: Soft delete cascades to conversation group, hard deletes memberships
    Given I have a conversation with title "Test Conversation"
    And I resolve the conversation group ID for conversation "${conversationId}" into "groupId"
    When I delete the conversation
    Then the response status should be 204
    # Verify conversation group is soft deleted
    When I execute SQL query:
    """
    SELECT id, deleted_at FROM conversation_groups WHERE id = '${groupId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "deleted_at" should be non-null
    # Verify membership is hard deleted (not soft deleted)
    When I execute SQL query:
    """
    SELECT COUNT(*) as count FROM conversation_memberships WHERE conversation_group_id = '${groupId}'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    # Verify entries were cascade deleted (foreign key ON DELETE CASCADE)
    When I execute SQL query:
    """
    SELECT COUNT(*) as count FROM entries WHERE conversation_group_id = '${groupId}'
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

  Scenario: Conversation response does not contain conversationGroupId
    When I create a conversation with request:
    """
    {
      "title": "Test Conversation"
    }
    """
    Then the response status should be 201
    And the response body should not contain "conversationGroupId"

  Scenario: Deleting a conversation deletes all forks
    Given I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "entryId" to "${response.body.id}"
    When I fork the conversation at entry "${entryId}"
    And set "forkConversationId" to "${forkedConversationId}"
    And I delete conversation "${rootConversationId}"
    Then the response status should be 204
    When I get conversation "${rootConversationId}"
    Then the response status should be 404
    When I get conversation "${forkConversationId}"
    Then the response status should be 404

  Scenario: Deleting a fork deletes the entire fork tree
    Given I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "entryId" to "${response.body.id}"
    When I fork the conversation at entry "${entryId}"
    And set "forkConversationId" to "${forkedConversationId}"
    And I delete conversation "${forkConversationId}"
    Then the response status should be 204
    When I get conversation "${rootConversationId}"
    Then the response status should be 404
    When I get conversation "${forkConversationId}"
    Then the response status should be 404

  Scenario: List conversations with mode=latest-fork returns only the most recently updated fork
    Given I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "firstEntryId" to "${response.body.id}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Second entry"}]
    }
    """
    And set "secondEntryId" to "${response.body.id}"
    # Create a fork from the root conversation
    When I fork the conversation at entry "${secondEntryId}"
    And set "forkConversationId" to "${forkedConversationId}"
    # Update the fork by adding an entry (this makes it the most recently updated)
    And set "conversationId" to "${forkConversationId}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Fork entry"}]
    }
    """
    # List with mode=latest-fork should return only the forked conversation (most recently updated)
    When I list conversations with mode "latest-fork"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${forkConversationId}"

  Scenario: List conversations with mode=roots returns only root conversations
    Given I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "entryId" to "${response.body.id}"
    When I fork the conversation at entry "${entryId}"
    And set "forkConversationId" to "${forkedConversationId}"
    When I list conversations with mode "roots"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${rootConversationId}"
