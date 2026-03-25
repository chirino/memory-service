Feature: Admin REST API
  As an administrator or auditor
  I want to manage conversations across all users via REST API
  So that I can perform administrative tasks and audits

  Background:
    Given I am authenticated as admin user "alice"
    And there is a conversation owned by "bob" with title "Bob's Conversation"
    And set "bobConversationId" to "${conversationId}"
    And there is a conversation owned by "alice" with title "Alice's Conversation"
    And set "aliceConversationId" to "${conversationId}"

  # Serial today only because this feature shares the serial admin runner; this scenario only asserts a minimum count on a global list and appears parallel-safe after user/client isolation.
  Scenario: Admin can list all conversations across users
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
    And the response should contain at least 2 conversations

  # Serial today only because this feature shares the serial admin runner; this scenario scopes the query to the scenario-local user ID and appears parallel-safe.
  Scenario: Admin can filter conversations by userId
    When I call GET "/v1/admin/conversations?userId=bob"
    Then the response status should be 200
    And the response should contain at least 1 conversation
    And all conversations should have ownerUserId "bob"

  # Serial today only because this feature shares the serial admin runner; this scenario only checks that at least one deleted conversation is visible and appears parallel-safe.
  Scenario: Admin can view soft-deleted conversations with includeDeleted=true
    Given the conversation owned by "bob" is deleted
    When I call GET "/v1/admin/conversations?includeDeleted=true"
    Then the response status should be 200
    And the response should contain at least 1 conversation with deletedAt set

  # Serial today only because this feature shares the serial admin runner; this scenario filters to deleted records and appears parallel-safe.
  Scenario: Admin can filter by onlyDeleted
    Given the conversation owned by "bob" is deleted
    When I call GET "/v1/admin/conversations?onlyDeleted=true"
    Then the response status should be 200
    And all conversations should have deletedAt set

  # Serial today only because this feature shares the serial admin runner; this scenario reads one scenario-local conversation by ID and appears parallel-safe.
  Scenario: Admin can get any conversation including soft-deleted
    Given the conversation owned by "bob" is deleted
    When I call GET "/v1/admin/conversations/${bobConversationId}?includeDeleted=true"
    Then the response status should be 200
    And the response body should have field "deletedAt" that is not null

  # Serial today only because this feature shares the serial admin runner; this scenario inspects children for one scenario-local root conversation and appears parallel-safe.
  Scenario: Admin can list child conversations for any conversation
    Given I am authenticated as user "bob"
    And set "parentConversationId" to "${bobConversationId}"
    And set "childConversationId" to "00000000-0000-0000-0000-eeeeeeeeeeee"
    When I call POST "/v1/conversations/${childConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "startedByConversationId": "${parentConversationId}",
      "content": [
        {
          "role": "USER",
          "text": "Admin-visible child task"
        }
      ]
    }
    """
    Then the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${bobConversationId}/children?limit=200"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${childConversationId}"
    And the response body "data[0].startedByConversationId" should be "${bobConversationId}"

  # Serial today only because this feature shares the serial admin runner; this scenario soft-deletes one scenario-local conversation by ID and appears parallel-safe.
  Scenario: Admin can delete any conversation
    When I call DELETE "/v1/admin/conversations/${bobConversationId}" with body:
    """
    {
      "justification": "Test deletion"
    }
    """
    Then the response status should be 204
    And set "conversationId" to "${bobConversationId}"
    And the conversation should be soft-deleted

  # Serial today only because this feature shares the serial admin runner; this scenario restores one scenario-local conversation by ID and appears parallel-safe.
  Scenario: Admin can restore a soft-deleted conversation
    Given the conversation owned by "bob" is deleted
    When I call POST "/v1/admin/conversations/${bobConversationId}/restore" with body:
    """
    {
      "justification": "Test restoration"
    }
    """
    Then the response status should be 200
    And set "conversationId" to "${bobConversationId}"
    And the conversation should not be deleted

  # Serial today only because this feature shares the serial admin runner; this scenario only mutates one scenario-local fork tree and appears parallel-safe.
  Scenario: Admin deleting a fork soft-deletes all conversations in the group
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Entry to fork at"}]
    }
    """
    And set "forkEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${forkEntryId}" with request:
    """
    {}
    """
    And set "forkConversationId" to "${forkedConversationId}"
    Given I am authenticated as admin user "alice"
    When I call DELETE "/v1/admin/conversations/${forkConversationId}" with body:
    """
    {
      "justification": "Test fork deletion"
    }
    """
    Then the response status should be 204
    # Both the fork and the root should be soft-deleted (same conversation group)
    And set "conversationId" to "${forkConversationId}"
    And the conversation should be soft-deleted
    And set "conversationId" to "${bobConversationId}"
    And the conversation should be soft-deleted

  # Serial today only because this feature shares the serial admin runner; this scenario only mutates one scenario-local fork tree and appears parallel-safe.
  Scenario: Admin deleting the root soft-deletes all conversations in the group
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Entry to fork at"}]
    }
    """
    And set "forkEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${forkEntryId}" with request:
    """
    {}
    """
    And set "forkConversationId" to "${forkedConversationId}"
    Given I am authenticated as admin user "alice"
    When I call DELETE "/v1/admin/conversations/${bobConversationId}" with body:
    """
    {
      "justification": "Test root deletion"
    }
    """
    Then the response status should be 204
    # Both the root and the fork should be soft-deleted (same conversation group)
    And set "conversationId" to "${bobConversationId}"
    And the conversation should be soft-deleted
    And set "conversationId" to "${forkConversationId}"
    And the conversation should be soft-deleted

  # Serial today only because this feature shares the serial admin runner; this scenario only restores one scenario-local fork tree and appears parallel-safe.
  Scenario: Admin restoring via fork ID restores all conversations in the group
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Entry to fork at"}]
    }
    """
    And set "forkEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${forkEntryId}" with request:
    """
    {}
    """
    And set "forkConversationId" to "${forkedConversationId}"
    Given I am authenticated as admin user "alice"
    And I call DELETE "/v1/admin/conversations/${forkConversationId}" with body:
    """
    {
      "justification": "Test fork deletion before restore"
    }
    """
    And the response status should be 204
    When I call POST "/v1/admin/conversations/${forkConversationId}/restore" with body:
    """
    {
      "justification": "Test fork restoration"
    }
    """
    Then the response status should be 200
    # Both the fork and the root should be restored (same conversation group)
    And set "conversationId" to "${forkConversationId}"
    And the conversation should not be deleted
    And set "conversationId" to "${bobConversationId}"
    And the conversation should not be deleted

  # Serial today only because this feature shares the serial admin runner; this scenario checks one scenario-local restore conflict and appears parallel-safe.
  Scenario: Restoring an already-active conversation returns conflict
    When I call POST "/v1/admin/conversations/${aliceConversationId}/restore" with body:
    """
    {
      "justification": "Test restoration"
    }
    """
    Then the response status should be 409

  # Serial today only because this feature shares the serial admin runner; this scenario only checks that the auditor can read the global list and appears parallel-safe.
  Scenario: Auditor can list conversations across users
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
    And the response should contain at least 1 conversation

  # Serial today only because this feature shares the serial admin runner; this scenario reads one scenario-local conversation by ID and appears parallel-safe.
  Scenario: Auditor can view any conversation
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/conversations/${bobConversationId}"
    Then the response status should be 200

  # Serial today only because this feature shares the serial admin runner; this scenario reads one scenario-local conversation by ID and appears parallel-safe.
  Scenario: Admin conversation payload exposes clientId
    When I call GET "/v1/admin/conversations/${bobConversationId}"
    Then the response status should be 200
    And the response body field "clientId" should not be null

  # Serial today only because this feature shares the serial admin runner; this scenario is an authorization check against one scenario-local conversation and appears parallel-safe.
  Scenario: Auditor receives 403 Forbidden on delete operation
    Given I am authenticated as auditor user "charlie"
    When I call DELETE "/v1/admin/conversations/${bobConversationId}" with body:
    """
    {
      "justification": "Test deletion"
    }
    """
    Then the response status should be 403

  # Serial today only because this feature shares the serial admin runner; this scenario is an authorization check against one scenario-local conversation and appears parallel-safe.
  Scenario: Auditor receives 403 Forbidden on restore operation
    Given I am authenticated as auditor user "charlie"
    And the conversation owned by "bob" is deleted
    When I call POST "/v1/admin/conversations/${bobConversationId}/restore" with body:
    """
    {
      "justification": "Test restoration"
    }
    """
    Then the response status should be 403

  # Serial today only because this feature shares the serial admin runner; this scenario is a pure authorization check and appears parallel-safe.
  Scenario: Non-admin user receives 403 Forbidden on all admin endpoints
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 403

  # Serial today only because this feature shares the serial admin runner; this scenario only checks that the request succeeds with a justification parameter and appears parallel-safe.
  Scenario: Justification is logged when provided
    When I call GET "/v1/admin/conversations?justification=Support+ticket+1234"
    Then the response status should be 200
    And the admin audit log should contain "listConversations"
    And the admin audit log should contain "Support ticket 1234"

  # Serial today only because this feature shares the serial admin runner; this scenario reads entries for one scenario-local conversation and appears parallel-safe.
  Scenario: Admin can get entries from any conversation
    Given the conversation owned by "bob" has an entry "Test entry"
    When I call GET "/v1/admin/conversations/${bobConversationId}/entries"
    Then the response status should be 200
    And the response should contain at least 1 items

  # Serial today only because this feature shares the serial admin runner; this scenario reads context entries for one scenario-local conversation and appears parallel-safe.
  Scenario: Admin can get context channel entries from any conversation
    # Create context entries as an agent (which sets clientId).
    # Agent auth defaults to user "alice", so use alice's conversation.
    Given I am authenticated as agent with API key "test-agent-key"
    And set "conversationId" to "${aliceConversationId}"
    When I append an entry with content "Agent memory" and channel "CONTEXT" and contentType "test.v1"
    Then the response status should be 201
    # Now query as admin - should see the context entry without clientId filtering
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${aliceConversationId}/entries?channel=context"
    Then the response status should be 200
    And the response should contain at least 1 items

  # Serial today only because this feature shares the serial admin runner; this scenario reads memberships for one scenario-local conversation and appears parallel-safe.
  Scenario: Admin can get memberships for any conversation
    When I call GET "/v1/admin/conversations/${bobConversationId}/memberships"
    Then the response status should be 200
    And the response should contain at least 1 memberships

  # Serial today only because this feature shares the serial admin runner; this scenario searches for a scenario-unique marker and appears parallel-safe.
  Scenario: Admin can perform system-wide semantic search
    Given the conversation owned by "bob" has an entry "Searchable content"
    When I call POST "/v1/admin/conversations/search" with body:
    """
    {
      "query": "Searchable"
    }
    """
    Then the response status should be 200
    And the response should contain at least 1 items

  # Serial today only because this feature shares the serial admin runner; this scenario scopes the search to the scenario-local user ID and appears parallel-safe.
  Scenario: Admin search can filter by userId
    Given the conversation owned by "bob" has an entry "Bob's entry"
    Given the conversation owned by "alice" has an entry "Alice's entry"
    When I call POST "/v1/admin/conversations/search" with body:
    """
    {
      "query": "entry",
      "userId": "bob"
    }
    """
    Then the response status should be 200
    And all search results should have conversation owned by "bob"

  # Serial today only because this feature shares the serial admin runner; this scenario searches for a scenario-unique marker and appears parallel-safe.
  Scenario: Admin search can include deleted conversations when requested
    Given the conversation owned by "bob" has an entry "Deleted-only search marker"
    And the conversation owned by "bob" is deleted
    When I call POST "/v1/admin/conversations/search" with body:
    """
    {
      "query": "Deleted-only search marker",
      "includeDeleted": false
    }
    """
    Then the response status should be 200
    And the response should contain 0 items
    When I call POST "/v1/admin/conversations/search" with body:
    """
    {
      "query": "Deleted-only search marker",
      "includeDeleted": true
    }
    """
    Then the response status should be 200
    And the response should contain at least 1 items

  # Serial today only because this feature shares the serial admin runner; this scenario paginates over scenario-unique search markers and appears parallel-safe.
  Scenario: Admin search supports afterCursor pagination
    Given the conversation owned by "bob" has an entry "Admin cursor marker one"
    Given the conversation owned by "alice" has an entry "Admin cursor marker two"
    When I call POST "/v1/admin/conversations/search" with body:
    """
    {
      "query": "Admin cursor marker",
      "limit": 1
    }
    """
    Then the response status should be 200
    And the response should have an afterCursor
    And set "adminSearchCursor" to the json response field "afterCursor"
    When I call POST "/v1/admin/conversations/search" with body:
    """
    {
      "query": "Admin cursor marker",
      "limit": 1,
      "afterCursor": "${adminSearchCursor}"
    }
    """
    Then the response status should be 200

  # Serial today only because this feature shares the serial admin runner; this scenario reads one scenario-local conversation by ID and appears parallel-safe.
  Scenario: Admin conversation response does not contain conversationGroupId
    When I call GET "/v1/admin/conversations/${bobConversationId}"
    Then the response status should be 200
    And the response body should not contain "conversationGroupId"

  # Serial today only because this feature shares the serial admin runner; this scenario reads memberships for one scenario-local conversation and appears parallel-safe.
  Scenario: Admin membership response contains conversationId
    When I call GET "/v1/admin/conversations/${bobConversationId}/memberships"
    Then the response status should be 200
    And the response should contain at least 1 memberships
    And the response body "data[0].conversationId" should be "${bobConversationId}"
    And the response body should not contain "conversationGroupId"

  # Serial today only because this feature shares the serial admin runner; this scenario scopes latest-fork mode to the scenario-local user ID and appears parallel-safe.
  Scenario: Admin can list conversations with mode=latest-fork
    # First authenticate as bob to create an entry
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "firstEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${firstEntryId}" with request:
    """
    {}
    """
    And set "forkConversationId" to "${forkedConversationId}"
    # Add an entry to the fork to make it the most recently updated
    And I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Fork entry"}]
    }
    """
    # Now switch back to admin to test the admin API
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?mode=latest-fork&userId=bob"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${forkConversationId}"

  # Serial required: this scenario asserts the exact global latest-fork result set across users, so concurrent scenarios can change both the count and ordering.
  Scenario: Admin latest-fork returns one conversation per group across users
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "firstEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${firstEntryId}" with request:
    """
    {}
    """
    And set "forkConversationId" to "${forkedConversationId}"
    And I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Fork entry"}]
    }
    """
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?mode=latest-fork"
    Then the response status should be 200
    And the response should contain 2 conversations
    And the response body "data[0].id" should be "${forkConversationId}"
    And the response body "data[1].id" should be "${aliceConversationId}"

  # Serial today only because this feature shares the serial admin runner; this scenario scopes latest-fork mode to the scenario-local user ID and appears parallel-safe.
  Scenario: Admin latest-fork honors deleted filters within a fork tree
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "firstEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${firstEntryId}" with request:
    """
    {}
    """
    And set "forkConversationId" to "${forkedConversationId}"
    And I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Fork entry"}]
    }
    """
    And I soft-delete conversation "${forkConversationId}" directly in storage
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?mode=latest-fork&userId=bob"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${bobConversationId}"
    When I call GET "/v1/admin/conversations?mode=latest-fork&userId=bob&includeDeleted=true"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${forkConversationId}"
    When I call GET "/v1/admin/conversations?mode=latest-fork&userId=bob&onlyDeleted=true"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${forkConversationId}"

  # Serial today only because this feature shares the serial admin runner; this scenario scopes roots mode to the scenario-local user ID and appears parallel-safe.
  Scenario: Admin can list conversations with mode=roots
    # First authenticate as bob to create an entry and fork
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "firstEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${firstEntryId}" with request:
    """
    {}
    """
    And set "forkConversationId" to "${forkedConversationId}"
    # Now switch back to admin to test the admin API
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?mode=roots&userId=bob"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${bobConversationId}"

  # Serial today only because this feature shares the serial admin runner; this scenario scopes all mode to the scenario-local user ID and appears parallel-safe.
  Scenario: Admin can list conversations with mode=all
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "firstEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${firstEntryId}" with request:
    """
    {}
    """
    And set "forkConversationId" to "${forkedConversationId}"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?mode=all&userId=bob"
    Then the response status should be 200
    And the response should contain 2 conversations
    And the response body "data[0].id" should be "${forkConversationId}"
    And the response body "data[1].id" should be "${bobConversationId}"

  # Serial today only because this feature shares the serial admin runner; this scenario lists forks for one scenario-local conversation tree and appears parallel-safe.
  Scenario: Admin can list forks for any conversation
    # First authenticate as bob to create entries and forks
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "First entry"}]
    }
    """
    And set "firstEntryId" to "${response.body.id}"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Second entry"}]
    }
    """
    And set "secondEntryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${secondEntryId}" with request:
    """
    {}
    """
    And set "fork1Id" to "${forkedConversationId}"
    And I fork conversation "${bobConversationId}" at entry "${secondEntryId}" with request:
    """
    {}
    """
    And set "fork2Id" to "${forkedConversationId}"
    # Now switch back to admin to test the admin API
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${bobConversationId}/forks"
    Then the response status should be 200
    # Should return the original conversation plus the 2 forks
    And the response should contain at least 3 conversations
    # Results are ordered by conversation ID (ASC) for cursor-based pagination
    And the response body should contain "${bobConversationId}"

  # Serial today only because this feature shares the serial admin runner; this scenario lists forks for one scenario-local conversation tree and appears parallel-safe.
  Scenario: Auditor can list forks for any conversation
    # First authenticate as bob to create an entry and fork
    Given I am authenticated as user "bob"
    And I call POST "/v1/conversations/${bobConversationId}/entries" with body:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "Entry for auditor test"}]
    }
    """
    And set "entryId" to "${response.body.id}"
    And I fork conversation "${bobConversationId}" at entry "${entryId}" with request:
    """
    {}
    """
    # Now switch to auditor to test access
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/conversations/${bobConversationId}/forks"
    Then the response status should be 200
    And the response should contain at least 2 conversations

  # Serial today only because this feature shares the serial admin runner; this scenario is a pure authorization check and appears parallel-safe.
  Scenario: Non-admin user receives 403 Forbidden on admin forks endpoint
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/conversations/${bobConversationId}/forks"
    Then the response status should be 403

  # Serial today only because this feature shares the serial admin runner; this scenario only checks that the request succeeds with a justification parameter and appears parallel-safe.
  Scenario: Admin forks justification is logged
    When I call GET "/v1/admin/conversations/${bobConversationId}/forks?justification=Investigating+fork+history"
    Then the response status should be 200
    And the admin audit log should contain "listForks"
    And the admin audit log should contain "Investigating fork history"
