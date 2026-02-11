Feature: Conversation Forking REST API
  As a user
  I want to fork conversations at specific entries via REST API
  So that I can create alternative conversation branches

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Base Conversation"
    And the conversation has an entry "First entry"
    And the conversation has an entry "Second entry"
    And the conversation has an entry "Third entry"

  Scenario: Fork a conversation at an entry
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${secondEntryId}" with request:
    """
    {
      "title": "Forked Conversation"
    }
    """
    Then the response status should be 201
    And the response body should be json:
    """
    {
      "id": "${response.body.id}",
      "title": "Forked Conversation",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner",
      "forkedAtEntryId": "${firstEntryId}",
      "forkedAtConversationId": "${conversationId}"
    }
    """

  Scenario: Fork a conversation without title
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {}
    """
    Then the response status should be 201
    And the response body should contain "id"
    And set "forkedConversationId" to the json response field "id"
    When I get conversation "${forkedConversationId}"
    Then the response status should be 200
    And the response body should contain "forkedAtConversationId"

  Scenario: List forks for a conversation
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${secondEntryId}" with request:
    """
    {
      "title": "Fork 1"
    }
    """
    And set "fork1Id" to the json response field "id"
    When I fork the conversation at entry "${secondEntryId}" with request:
    """
    {
      "title": "Fork 2"
    }
    """
    And set "fork2Id" to the json response field "id"
    When I list forks for the conversation
    Then the response status should be 200
    And the response should contain at least 3 conversations
    # The response includes the original conversation (no forkedAtEntryId) plus the 2 forks
    # Verify: original at [0] has no forkedAtEntryId, forks at [1] and [2] have forkedAtEntryId
    And the response body should contain "forkedAtEntryId"
    And the response body "data[0].conversationId" should be "${conversationId}"
    And the response body "data[0].forkedAtEntryId" should be "null"
    And the response body "data[1].forkedAtEntryId" should be "${firstEntryId}"
    And the response body "data[1].forkedAtConversationId" should be "${conversationId}"
    And the response body "data[2].forkedAtEntryId" should be "${firstEntryId}"
    And the response body "data[2].forkedAtConversationId" should be "${conversationId}"

  Scenario: Fork non-existent conversation
    When I fork conversation "00000000-0000-0000-0000-000000000000" at entry "00000000-0000-0000-0000-000000000001" with request:
    """
    {
      "title": "Fork"
    }
    """
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Fork at non-existent entry
    When I fork the conversation at entry "00000000-0000-0000-0000-000000000000" with request:
    """
    {
      "title": "Fork"
    }
    """
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Fork conversation without access
    Given there is a conversation owned by "bob"
    When I fork that conversation at entry "00000000-0000-0000-0000-000000000000" with request:
    """
    {
      "title": "Fork"
    }
    """
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: List forks for non-existent conversation
    When I list forks for conversation "00000000-0000-0000-0000-000000000000"
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: List forks without access
    Given there is a conversation owned by "bob"
    When I list forks for that conversation
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Forked conversation shares membership with root
    Given I share the conversation with user "bob" and access level "reader"
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {
      "title": "Forked Conversation"
    }
    """
    And set "forkConversationId" to "${response.body.id}"
    And I authenticate as user "bob"
    When I get conversation "${forkConversationId}"
    Then the response status should be 200

  # Regression test: Root conversation continues after fork - querying root returns only root entries
  # This tests that fork-aware entry retrieval correctly handles the case where the root
  # conversation has entries AFTER a fork was created (the fork point should not limit root entries)
  Scenario: Root conversation entries after fork point are returned when querying root
    Given I am authenticated as user "alice"
    And I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"
    # Create initial entries in root (before fork point)
    And the conversation has an entry "A" in channel "HISTORY"
    And the conversation has an entry "B" in channel "MEMORY"
    And the conversation has an entry "C" in channel "MEMORY"
    And the conversation has an entry "D" in channel "HISTORY"

    # Capture entry D as the fork point (need agent auth to see MEMORY entries)
    When I am authenticated as agent with API key "test-agent-key"
    And I list entries for the conversation
    And set "entryD_Id" to the json response field "data[3].id"
    # Switch back to user for forking
    And I am authenticated as user "alice"

    # Fork the conversation at entry D
    When I fork the conversation at entry "${entryD_Id}" with request:
    """
    {
      "title": "Forked Conversation"
    }
    """
    Then the response status should be 201
    And set "forkConversationId" to the json response field "id"

    # Switch to agent to add entries (users can't add MEMORY entries)
    When I am authenticated as agent with API key "test-agent-key"

    # Add entries to the fork
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "E", "role": "USER"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "MEMORY", "contentType": "test.v1", "content": [{"type": "text", "text": "F"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "MEMORY", "contentType": "test.v1", "content": [{"type": "text", "text": "G"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "H", "role": "AI"}]}
    """
    Then the response status should be 201

    # Root conversation continues with more entries AFTER fork was created
    When I call POST "/v1/conversations/${rootConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "I", "role": "USER"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${rootConversationId}/entries" with body:
    """
    {"channel": "MEMORY", "contentType": "test.v1", "content": [{"type": "text", "text": "J"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${rootConversationId}/entries" with body:
    """
    {"channel": "MEMORY", "contentType": "test.v1", "content": [{"type": "text", "text": "K"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${rootConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "L", "role": "AI"}]}
    """
    Then the response status should be 201

    # Query the ROOT conversation (default, no forks=all)
    # Expected: All root entries A, B, C, D, I, J, K, L (NOT fork entries E, F, G, H)
    When I call GET "/v1/conversations/${rootConversationId}/entries"
    Then the response status should be 200
    And the response should contain 8 entries
    And entry at index 0 should have content "A"
    And entry at index 1 should have content "B"
    And entry at index 2 should have content "C"
    And entry at index 3 should have content "D"
    And entry at index 4 should have content "I"
    And entry at index 5 should have content "J"
    And entry at index 6 should have content "K"
    And entry at index 7 should have content "L"

    # Query the FORK conversation (default, no forks=all)
    # Expected: Parent entries BEFORE fork point (A, B, C) + fork entries (E, F, G, H)
    # Note: D is NOT included because fork at D means "branch before D"
    When I call GET "/v1/conversations/${forkConversationId}/entries"
    Then the response status should be 200
    And the response should contain 7 entries
    And entry at index 0 should have content "A"
    And entry at index 1 should have content "B"
    And entry at index 2 should have content "C"
    And entry at index 3 should have content "E"
    And entry at index 4 should have content "F"
    And entry at index 5 should have content "G"
    And entry at index 6 should have content "H"

    # Query root with forks=all - should return ALL entries from both conversations
    When I call GET "/v1/conversations/${rootConversationId}/entries?forks=all"
    Then the response status should be 200
    And the response should contain 12 entries

  # Regression test: Channel filtering must happen AFTER fork ancestry traversal, not at the DB level.
  # If the fork point entry (forkedAtEntryId) is in a different channel than the query filter,
  # the DB-level filter removes it, breaking ancestry chain detection. The algorithm then never
  # transitions from parent to fork conversation, returning wrong entries entirely.
  Scenario: Channel filter on forked conversation preserves fork point from different channel
    Given I am authenticated as user "alice"
    And I have a conversation with title "Channel Fork Test"
    And set "rootConversationId" to "${conversationId}"

    # Create entries with mixed channels: HISTORY, MEMORY, HISTORY
    And the conversation has an entry "H1" in channel "HISTORY"
    And the conversation has an entry "M1" in channel "MEMORY"
    And the conversation has an entry "H2" in channel "HISTORY"

    # Get entry IDs (agent auth can see all channels)
    When I am authenticated as agent with API key "test-agent-key"
    And I list entries for the conversation
    Then the response should contain 3 entries
    And entry at index 0 should have content "H1"
    And entry at index 1 should have content "M1"
    And entry at index 2 should have content "H2"
    And set "entryH2_Id" to the json response field "data[2].id"

    # Fork at H2 → forkedAtEntryId = M1 (a MEMORY entry)
    When I am authenticated as user "alice"
    And I fork the conversation at entry "${entryH2_Id}" with request:
    """
    {"title": "Channel Fork"}
    """
    Then the response status should be 201
    And set "forkConversationId" to the json response field "id"

    # Add a HISTORY entry to the fork
    When I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "H3", "role": "USER"}]}
    """
    Then the response status should be 201

    # Sanity check: all-channel query returns correct fork entries
    When I call GET "/v1/conversations/${forkConversationId}/entries"
    Then the response status should be 200
    And the response should contain 3 entries
    And entry at index 0 should have content "H1"
    And entry at index 1 should have content "M1"
    And entry at index 2 should have content "H3"

    # KEY TEST: Query fork with channel=HISTORY filter
    # Fork point M1 is MEMORY. If channel filter is applied at DB level, M1 is excluded,
    # ancestry traversal breaks, and we get wrong results (H1, H2 from root instead of H1, H3).
    When I call GET "/v1/conversations/${forkConversationId}/entries?channel=HISTORY"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "H1"
    And entry at index 1 should have content "H3"
