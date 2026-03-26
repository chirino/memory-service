Feature: Conversations REST API
  As a user
  I want to manage conversations via REST API
  So that I can create, list, get, and delete conversations

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"

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
    And the response body should not contain "clientId"

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
    And the response body "data[0].title" should be "Second Conversation"
    And the response body "data[1].title" should be "First Conversation"
    And the response body should not contain "clientId"

  Scenario: List conversations with pagination
    Given I have a conversation with title "Conversation 1"
    And I have a conversation with title "Conversation 2"
    And I have a conversation with title "Conversation 3"
    When I list conversations with limit 2
    Then the response status should be 200
    And the response should contain 2 conversations
    And the response body "data[0].title" should be "Conversation 3"
    And the response body "data[1].title" should be "Conversation 2"
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
    And the response body should not contain "clientId"

  Scenario: Get non-existent conversation
    When I get conversation "00000000-0000-0000-0000-000000000000"
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Get conversation without access
    Given there is a conversation owned by "bob"
    When I get that conversation
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Append-created child conversation is excluded from roots list and visible as a child
    Given I have a conversation with title "Parent Conversation"
    And set "parentConversationId" to "${conversationId}"
    And set "childConversationId" to "00000000-0000-0000-0000-cccccccccccc"
    When I call POST "/v1/conversations/${childConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "startedByConversationId": "${parentConversationId}",
      "content": [
        {
          "role": "USER",
          "text": "Child task"
        }
      ]
    }
    """
    Then the response status should be 201
    When I call GET "/v1/conversations/${childConversationId}"
    Then the response status should be 200
    And the response body field "id" should be "${childConversationId}"
    And the response body field "startedByConversationId" should be "${parentConversationId}"
    When I list conversations
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${parentConversationId}"
    When I call GET "/v1/conversations?ancestry=children"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${childConversationId}"
    And the response body "data[0].startedByConversationId" should be "${parentConversationId}"
    When I execute SQL query:
    """
    SELECT id, started_by_conversation_id FROM conversations WHERE id = '${childConversationId}'
    """
    Then the SQL result should match:
      | id                                   | started_by_conversation_id         |
      | ${childConversationId}               | ${parentConversationId}            |
    When I execute MongoDB query:
    """
    {
      "collection": "conversations",
      "operation": "find",
      "filter": {
        "_id": "${childConversationId}"
      },
      "projection": {
        "_id": 1,
        "started_by_conversation_id": 1
      }
    }
    """
    Then the MongoDB result should match:
      | _id                                  | started_by_conversation_id         |
      | ${childConversationId}               | ${parentConversationId}            |

  Scenario: Started child conversation is returned by the dedicated children endpoint
    Given I have a conversation with title "Parent Conversation"
    And set "parentConversationId" to "${conversationId}"
    And set "childConversationId" to "00000000-0000-0000-0000-dddddddddddd"
    When I call POST "/v1/conversations/${childConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "startedByConversationId": "${parentConversationId}",
      "content": [
        {
          "role": "USER",
          "text": "Child task from endpoint"
        }
      ]
    }
    """
    Then the response status should be 201
    When I call GET "/v1/conversations/${parentConversationId}/children?limit=200"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${childConversationId}"
    And the response body "data[0].startedByConversationId" should be "${parentConversationId}"

  Scenario: Archiving a conversation keeps it readable and marks it archived
    Given I have a conversation with title "To Be Deleted"
    When I archive the conversation
    Then the response status should be 200
    And the response body field "archived" should be "true"
    When I get the conversation
    Then the response status should be 200
    And the response body field "archived" should be "true"
    When I execute SQL query:
    """
    SELECT id, archived_at FROM conversations WHERE id = '${conversationId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "archived_at" should be non-null
    When I execute MongoDB query:
    """
    {
      "collection": "conversations",
      "operation": "find",
      "filter": {
        "_id": "${conversationId}"
      },
      "projection": {
        "_id": 1,
        "archived_at": 1
      }
    }
    """
    Then the MongoDB result should have 1 row
    And the MongoDB result column "archived_at" should be non-null

  Scenario: Archived conversation filters work for user lists
    Given I have a conversation with title "Active Conversation"
    And set "activeConversationId" to "${conversationId}"
    And I have a conversation with title "To Be Deleted"
    And set "deletedConversationId" to "${conversationId}"
    When I archive the conversation
    And I list conversations
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${activeConversationId}"
    When I call GET "/v1/conversations?archived=include"
    Then the response status should be 200
    And the response should contain 2 conversations
    When I call GET "/v1/conversations?archived=only"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${deletedConversationId}"
    And the response body field "data[0].archived" should be "true"
    When I call GET "/v1/conversations?mode=roots&archived=only"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${deletedConversationId}"
    And the response body field "data[0].archived" should be "true"
    # Verify both still exist in database
    When I execute SQL query:
    """
    SELECT id, title, archived_at FROM conversations ORDER BY created_at
    """
    Then the SQL result should have 2 rows
    When I execute MongoDB query:
    """
    {
      "collection": "conversations",
      "operation": "find",
      "projection": {
        "_id": 1,
        "archived_at": 1
      },
      "sort": {
        "created_at": 1
      }
    }
    """
    Then the MongoDB result should have 2 rows

  Scenario: Archiving cascades to conversation group and preserves memberships and entries until eviction
    Given I have a conversation with title "Test Conversation"
    And I resolve the conversation group ID for conversation "${conversationId}" into "groupId"
    When I archive the conversation
    Then the response status should be 200
    # Verify conversation group is archived
    When I execute SQL query:
    """
    SELECT id, archived_at FROM conversation_groups WHERE id = '${groupId}'
    """
    Then the SQL result should have 1 row
    And the SQL result column "archived_at" should be non-null
    When I execute MongoDB query:
    """
    {
      "collection": "conversation_groups",
      "operation": "find",
      "filter": {
        "_id": "${groupId}"
      },
      "projection": {
        "_id": 1,
        "archived_at": 1
      }
    }
    """
    Then the MongoDB result should have 1 row
    And the MongoDB result column "archived_at" should be non-null
    # Verify memberships remain so the archived conversation stays readable until eviction.
    When I execute SQL query:
    """
    SELECT COUNT(*) as count FROM conversation_memberships WHERE conversation_group_id = '${groupId}'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {
      "collection": "conversation_memberships",
      "operation": "count",
      "filter": {
        "conversation_group_id": "${groupId}"
      }
    }
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    # Verify entries remain until hard-delete eviction.
    When I execute SQL query:
    """
    SELECT COUNT(*) as count FROM entries WHERE conversation_group_id = '${groupId}'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I call GET "/v1/conversations/${conversationId}"
    Then the response status should be 200
    When I execute MongoDB query:
    """
    {
      "collection": "entries",
      "operation": "count",
      "filter": {
        "conversation_group_id": "${groupId}"
      }
    }
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Archive non-existent conversation
    When I archive conversation "00000000-0000-0000-0000-000000000000"
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Archive conversation without access
    Given there is a conversation owned by "bob"
    When I archive that conversation
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

  Scenario: Archiving a conversation archives all forks
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
    And I archive conversation "${rootConversationId}"
    Then the response status should be 200
    When I get conversation "${rootConversationId}"
    Then the response status should be 200
    When I get conversation "${forkConversationId}"
    Then the response status should be 200

  Scenario: Archiving a fork archives the entire fork tree
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
    And I archive conversation "${forkConversationId}"
    Then the response status should be 200
    When I get conversation "${rootConversationId}"
    Then the response status should be 200
    When I get conversation "${forkConversationId}"
    Then the response status should be 200

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
