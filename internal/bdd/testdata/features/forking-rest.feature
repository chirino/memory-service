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
    {}
    """
    Then the response status should be 200
    And the response body "forkedAtEntryId" should be "${secondEntryId}"
    And the response body "forkedAtConversationId" should be "${conversationId}"
    And the response body "ownerUserId" should be "alice"
    And the response body "accessLevel" should be "owner"

  Scenario: Fork a conversation without title
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And the response body "forkedAtEntryId" should be "${firstEntryId}"
    And the response body should contain "forkedAtConversationId"
    And set "forkedConversationAtFirstEntryId" to "${forkedConversationId}"
    When I call GET "/v1/conversations/${forkedConversationAtFirstEntryId}/entries"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Fork message"

  Scenario: List forks for a conversation
    When I list entries for the conversation
    And set "secondEntryId" to the json response field "data[1].id"
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${secondEntryId}" with request:
    """
    {}
    """
    And set "fork1Id" to "${forkedConversationId}"
    When I fork the conversation at entry "${secondEntryId}" with request:
    """
    {}
    """
    And set "fork2Id" to "${forkedConversationId}"
    When I list forks for the conversation
    Then the response status should be 200
    And the response body field "forkPoints[0].entryId" should be "${secondEntryId}"
    And the response body should contain "${conversationId}"
    And the response body should contain "${fork1Id}"
    And the response body should contain "${fork2Id}"

  Scenario: Fork on append without forkedAtEntryId shows null fork point in list forks
    # Use a deterministic UUID for stable assertions.
    And set "forkWithoutEntryConversationId" to "00000000-0000-4000-8000-000000000001"
    When I call POST "/v1/conversations/${forkWithoutEntryConversationId}/entries" with body:
    """
    {
      "forkedAtConversationId": "${conversationId}",
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "Fork message without explicit fork point"}]
    }
    """
    Then the response status should be 201
    When I list forks for the conversation
    Then the response status should be 200
    And the response body should contain "${forkWithoutEntryConversationId}"
    And the response body field "forkPoints" should not be null
    When I call GET "/v1/conversations/${forkWithoutEntryConversationId}/entries"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Fork message without explicit fork point"

  Scenario: Fork non-existent conversation
    When I fork conversation "00000000-0000-0000-0000-000000000000" at entry "00000000-0000-0000-0000-000000000001" with request:
    """
    {}
    """
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Fork at non-existent entry
    When I fork the conversation at entry "00000000-0000-0000-0000-000000000000" with request:
    """
    {}
    """
    Then the response status should be 404
    And the response should contain error code "not_found"

  Scenario: Fork conversation without access
    Given there is a conversation owned by "bob"
    When I fork that conversation at entry "00000000-0000-0000-0000-000000000000" with request:
    """
    {}
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
    {}
    """
    And I authenticate as user "bob"
    When I get conversation "${forkedConversationId}"
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
    And the conversation has an entry "B" in channel "CONTEXT"
    And the conversation has an entry "C" in channel "CONTEXT"
    And the conversation has an entry "D" in channel "HISTORY"

    # Capture entry D as the fork point (need agent auth to see CONTEXT entries)
    When I am authenticated as agent with API key "test-agent-key"
    And I list entries for the conversation
    And set "entryD_Id" to the json response field "data[3].id"
    # Switch back to user for forking
    And I am authenticated as user "alice"

    # Fork the conversation at entry D
    When I fork the conversation at entry "${entryD_Id}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "forkConversationId" to "${forkedConversationId}"

    # Switch to agent to add entries (users can't add CONTEXT entries)
    When I am authenticated as agent with API key "test-agent-key"

    # Add entries to the fork
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "E", "role": "USER"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "CONTEXT", "contentType": "test.v1", "content": [{"type": "text", "text": "F"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "CONTEXT", "contentType": "test.v1", "content": [{"type": "text", "text": "G"}]}
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
    {"channel": "CONTEXT", "contentType": "test.v1", "content": [{"type": "text", "text": "J"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${rootConversationId}/entries" with body:
    """
    {"channel": "CONTEXT", "contentType": "test.v1", "content": [{"type": "text", "text": "K"}]}
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
    # Expected: Parent entries BEFORE fork point (A, B, C) + fork step entry + added entries (E, F, G, H)
    # Note: D is NOT included because fork at D means "branch before D"
    When I call GET "/v1/conversations/${forkConversationId}/entries"
    Then the response status should be 200
    And the response should contain 8 entries
    And entry at index 0 should have content "A"
    And entry at index 1 should have content "B"
    And entry at index 2 should have content "C"
    And entry at index 3 should have content "Fork message"
    And entry at index 4 should have content "E"
    And entry at index 5 should have content "F"
    And entry at index 6 should have content "G"
    And entry at index 7 should have content "H"

    # Query root with forks=all - should return ALL entries from both conversations
    When I call GET "/v1/conversations/${rootConversationId}/entries?forks=all"
    Then the response status should be 200
    And the response should contain 13 entries

  # Channel filtering must happen after ancestry traversal so the parent/fork boundary
  # is computed before channel-specific entries are selected.
  Scenario: Channel filter on forked conversation preserves ancestry semantics
    Given I am authenticated as user "alice"
    And I have a conversation with title "Channel Fork Test"
    And set "rootConversationId" to "${conversationId}"

    # Create entries with mixed channels: HISTORY, CONTEXT, HISTORY
    And the conversation has an entry "H1" in channel "HISTORY"
    And the conversation has an entry "M1" in channel "CONTEXT"
    And the conversation has an entry "H2" in channel "HISTORY"

    # Get entry IDs (agent auth can see all channels)
    When I am authenticated as agent with API key "test-agent-key"
    And I list entries for the conversation
    Then the response should contain 3 entries
    And entry at index 0 should have content "H1"
    And entry at index 1 should have content "M1"
    And entry at index 2 should have content "H2"
    And set "entryH2_Id" to the json response field "data[2].id"

    # Fork at H2 → forkedAtEntryId = H2 (the first excluded parent entry)
    When I am authenticated as user "alice"
    And I fork the conversation at entry "${entryH2_Id}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "forkConversationId" to "${forkedConversationId}"

    # Add a HISTORY entry to the fork
    When I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "H3", "role": "USER"}]}
    """
    Then the response status should be 201

    # Sanity check: all-channel query returns correct fork entries
    # Includes: H1 (inherited), M1 (inherited), Fork message (from fork step), H3 (added)
    When I call GET "/v1/conversations/${forkConversationId}/entries"
    Then the response status should be 200
    And the response should contain 4 entries
    And entry at index 0 should have content "H1"
    And entry at index 1 should have content "M1"
    And entry at index 2 should have content "Fork message"
    And entry at index 3 should have content "H3"

    # KEY TEST: Query fork with channel=HISTORY filter
    When I call GET "/v1/conversations/${forkConversationId}/entries?channel=HISTORY"
    Then the response status should be 200
    And the response should contain 3 entries
    And entry at index 0 should have content "H1"
    And entry at index 1 should have content "Fork message"
    And entry at index 2 should have content "H3"

  # Regression test: Memory sync on a forked conversation should append deltas like the parent,
  # not bundle all inherited+new messages into a single entry.
  # This reproduces the bug where syncAgentEntry() only checks the fork's own entries when
  # comparing content (ignoring inherited parent entries), causing it to treat ALL incoming
  # messages as new content and bundle them into one entry.
  Scenario: Sync context entries on forked conversation appends delta like parent
    Given I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "Root Conversation"
    And set "rootConversationId" to "${conversationId}"

    # === Build parent conversation memory via sync (simulates langchain4j agent flow) ===

    # User asks a question
    When I call POST "/v1/conversations/${rootConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "Pick a number", "role": "USER"}]}
    """
    Then the response status should be 201

    # Agent syncs memory: [USER1]
    When I call POST "/v1/conversations/${rootConversationId}/entries/sync" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "LC4J",
      "content": [{"type": "USER", "contents": [{"text": "Pick a number", "type": "TEXT"}]}]
    }
    """
    Then the response status should be 200
    And the response body field "epochIncremented" should be "false"

    # Agent syncs memory: [USER1, AI1] — extends existing with AI response
    When I call POST "/v1/conversations/${rootConversationId}/entries/sync" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "LC4J",
      "content": [
        {"type": "USER", "contents": [{"text": "Pick a number", "type": "TEXT"}]},
        {"type": "AI", "text": "The number is 42."}
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epochIncremented" should be "false"

    # Agent posts AI history response
    When I call POST "/v1/conversations/${rootConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "The number is 42.", "role": "AI"}]}
    """
    Then the response status should be 201

    # Verify parent has 2 individual context entries at epoch 1
    When I call GET "/v1/conversations/${rootConversationId}/entries?channel=CONTEXT&epoch=latest"
    Then the response status should be 200
    And the response should contain 2 entries

    # User sends a second message (this will be the fork point)
    When I call POST "/v1/conversations/${rootConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "Next question", "role": "USER"}]}
    """
    Then the response status should be 201
    And set "forkPointEntryId" to the json response field "id"

    # === Fork before "Next question" — fork sees: H_USER1, M_USER1, M_AI1, H_AI1 ===
    When I am authenticated as user "alice"
    And set "conversationId" to "${rootConversationId}"
    And I fork the conversation at entry "${forkPointEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "forkConversationId" to "${forkedConversationId}"

    # === Simulate agent sync on forked conversation ===
    When I am authenticated as agent with API key "test-agent-key"

    # User sends message in fork
    When I call POST "/v1/conversations/${forkConversationId}/entries" with body:
    """
    {"channel": "HISTORY", "contentType": "history", "content": [{"text": "Pick a color", "role": "USER"}]}
    """
    Then the response status should be 201

    # Agent syncs memory for fork: [USER1, AI1, USER2]
    # The agent retrieved [USER1, AI1] from parent memory, adds USER2.
    # Expected: detect inherited [USER1, AI1] as prefix, append only delta [USER2]
    When I call POST "/v1/conversations/${forkConversationId}/entries/sync" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "LC4J",
      "content": [
        {"type": "USER", "contents": [{"text": "Pick a number", "type": "TEXT"}]},
        {"type": "AI", "text": "The number is 42."},
        {"type": "USER", "contents": [{"text": "Pick a color", "type": "TEXT"}]}
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epochIncremented" should be "false"

    # Verify: fork memory should have 3 entries (2 inherited + 1 new delta)
    When I call GET "/v1/conversations/${forkConversationId}/entries?channel=CONTEXT&epoch=latest"
    Then the response status should be 200
    And the response should contain 3 entries
    # The 3rd entry (index 2) is the fork's new entry — it should contain ONLY the delta
    # (1 content item: USER "Pick a color"), NOT all 3 messages bundled together.
    # With the bug: data[2].content has 3 items (all messages); content[0] is "Pick a number"
    # Correct:      data[2].content has 1 item (just the delta); content[0] is "Pick a color"
    And the response body field "data[2].content[0].contents[0].text" should be "Pick a color"
    And the response body field "data[2].content[1]" should be null

    # Agent syncs memory for fork after AI response: [USER1, AI1, USER2, AI2]
    When I call POST "/v1/conversations/${forkConversationId}/entries/sync" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "LC4J",
      "content": [
        {"type": "USER", "contents": [{"text": "Pick a number", "type": "TEXT"}]},
        {"type": "AI", "text": "The number is 42."},
        {"type": "USER", "contents": [{"text": "Pick a color", "type": "TEXT"}]},
        {"type": "AI", "text": "How about blue?"}
      ]
    }
    """
    Then the response status should be 200
    And the response body field "epochIncremented" should be "false"

    # Final: fork memory should have 4 entries (2 inherited + 2 new deltas), all at epoch 1
    When I call GET "/v1/conversations/${forkConversationId}/entries?channel=CONTEXT&epoch=latest"
    Then the response status should be 200
    And the response should contain 4 entries
    # Verify each entry has exactly 1 content item (individual messages, not bundled)
    And the response body field "data[0].content[1]" should be null
    And the response body field "data[1].content[1]" should be null
    And the response body field "data[2].content[1]" should be null
    And the response body field "data[3].content[1]" should be null

  Scenario: Same client can fork at a journal entry
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "llm_call", "model": "test-model"}]
    }
    """
    Then the response status should be 201
    And set "journalEntryId" to the json response field "id"
    When I fork the conversation at entry "${journalEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And the response body "forkedAtEntryId" should be "${journalEntryId}"
    And the response body "forkedAtConversationId" should be "${conversationId}"

  Scenario: Journal fork navigation is client scoped and supports journal continuations
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "checkpoint", "text": "Parent journal branch"}]
    }
    """
    Then the response status should be 201
    And set "journalEntryId" to the json response field "id"
    And set "journalForkConversationId" to "00000000-0000-4000-8000-000000000043"
    When I call POST "/v1/conversations/${journalForkConversationId}/entries" with body:
    """
    {
      "forkedAtConversationId": "${conversationId}",
      "forkedAtEntryId": "${journalEntryId}",
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "retry", "text": "Journal continuation"}]
    }
    """
    Then the response status should be 201
    And set "journalContinuationEntryId" to the json response field "id"

    When I list forks for the conversation
    Then the response status should be 200
    And the response body "forkPoints" should have 1 item
    And the response body field "forkPoints[0].entryId" should be "${journalEntryId}"

    When I list forks for conversation "${journalForkConversationId}"
    Then the response status should be 200
    And the response body "forkPoints" should have 1 item
    And the response body field "forkPoints[0].entryId" should be "${journalContinuationEntryId}"

    Given I am authenticated as user "alice"
    When I list forks for the conversation
    Then the response status should be 200
    And the response body "forkPoints" should have 0 items

    Given I am authenticated as agent with API key "test-agent-key-b"
    When I list forks for the conversation
    Then the response status should be 200
    And the response body "forkPoints" should have 0 items

    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${conversationId}/forks"
    Then the response status should be 200
    And the response body "forkPoints" should have 1 item
    And the response body field "forkPoints[0].entryId" should be "${journalEntryId}"

  Scenario: List sibling forks at the same journal entry
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "checkpoint", "text": "Shared journal branch point"}]
    }
    """
    Then the response status should be 201
    And set "journalEntryId" to the json response field "id"

    When I fork the conversation at entry "${journalEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "journalFork1Id" to "${forkedConversationId}"

    When I fork the conversation at entry "${journalEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "journalFork2Id" to "${forkedConversationId}"

    When I list forks for the conversation
    Then the response status should be 200
    And the response body "conversationIds" should have 3 items
    And the response body "forkPoints" should have 1 item
    And the response body field "forkPoints[0].entryId" should be "${journalEntryId}"
    And the response body "forkPoints[0].options" should have 3 items
    And the response body should contain "${conversationId}"
    And the response body should contain "${journalFork1Id}"
    And the response body should contain "${journalFork2Id}"

  Scenario: User without client identity cannot fork at a journal entry
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "tool_call"}]
    }
    """
    Then the response status should be 201
    And set "journalEntryId" to the json response field "id"
    Given I am authenticated as user "alice"
    When I fork the conversation at entry "${journalEntryId}" with request:
    """
    {}
    """
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Different client cannot fork at another client's journal entry
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "tool_call", "client": "a"}]
    }
    """
    Then the response status should be 201
    And set "journalEntryId" to the json response field "id"
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I fork the conversation at entry "${journalEntryId}" with request:
    """
    {}
    """
    Then the response status should be 403
    And the response should contain error code "forbidden"

  Scenario: Context entries cannot be fork anchors
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "CONTEXT",
      "contentType": "test.v1",
      "content": [{"type": "text", "text": "Context fork point"}]
    }
    """
    Then the response status should be 201
    And set "contextEntryId" to the json response field "id"
    When I fork the conversation at entry "${contextEntryId}" with request:
    """
    {}
    """
    Then the response status should be 400
    And the response should contain error code "validation_error"

  Scenario: History listing honors a fork anchored at a journal entry
    Given I am authenticated as user "alice"
    And I have a conversation with title "Journal Fork Ordering"
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "H1 before journal"}]
    }
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "checkpoint"}]
    }
    """
    Then the response status should be 201
    And set "journalEntryId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"role": "USER", "text": "H2 after journal"}]
    }
    """
    Then the response status should be 201
    When I fork the conversation at entry "${journalEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "journalForkConversationId" to "${forkedConversationId}"
    Given I am authenticated as user "alice"
    When I call GET "/v1/conversations/${journalForkConversationId}/entries?channel=HISTORY"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "H1 before journal"
    And entry at index 1 should have content "Fork message"

  Scenario: Journal listing honors an exclusive journal fork boundary
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "checkpoint", "text": "J1 before anchor"}]
    }
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "checkpoint", "text": "J2 anchor"}]
    }
    """
    Then the response status should be 201
    And set "journalEntryId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "checkpoint", "text": "J3 after anchor"}]
    }
    """
    Then the response status should be 201

    And set "journalForkConversationId" to "00000000-0000-4000-8000-000000000045"
    When I call POST "/v1/conversations/${journalForkConversationId}/entries" with body:
    """
    {
      "forkedAtConversationId": "${conversationId}",
      "forkedAtEntryId": "${journalEntryId}",
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "retry", "text": "JF fork continuation"}]
    }
    """
    Then the response status should be 201

    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "JOURNAL",
      "contentType": "agent/step",
      "content": [{"stepType": "checkpoint", "text": "J4 root continuation"}]
    }
    """
    Then the response status should be 201

    When I call GET "/v1/conversations/${conversationId}/entries?channel=JOURNAL"
    Then the response status should be 200
    And the response should contain 4 entries
    And entry at index 0 should have content "J1 before anchor"
    And entry at index 1 should have content "J2 anchor"
    And entry at index 2 should have content "J3 after anchor"
    And entry at index 3 should have content "J4 root continuation"

    When I call GET "/v1/conversations/${journalForkConversationId}/entries?channel=JOURNAL"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "J1 before anchor"
    And entry at index 1 should have content "JF fork continuation"
