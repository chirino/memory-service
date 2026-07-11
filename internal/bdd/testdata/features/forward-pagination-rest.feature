Feature: Forward pagination for conversation entries (REST)
  As a chat client
  I want to page forward through conversation entries
  So that I can scroll through a conversation without loading everything at once

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Forward Pagination Test"

  Scenario: First page returns afterCursor when more entries exist
    Given the conversation has 5 entries
    When I list entries with limit 2
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 1"
    And entry at index 1 should have content "Entry 2"
    And the response should have an afterCursor
    And the response should not have a beforeCursor

  Scenario: Subsequent page using afterCursor returns the next entries
    Given the conversation has 5 entries
    When I list entries with limit 2
    Then the response should contain 2 entries
    And entry at index 0 should have content "Entry 1"
    And entry at index 1 should have content "Entry 2"
    When I list entries with afterCursor from previous response and limit 2
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 3"
    And entry at index 1 should have content "Entry 4"
    And the response should have an afterCursor
    And the response should have a beforeCursor
    When I list entries with afterCursor from previous response and limit 2
    Then the response status should be 200
    And the response should contain 1 entries
    And entry at index 0 should have content "Entry 5"
    And the response should not have an afterCursor
    And the response should have a beforeCursor

  Scenario: Last page returns no afterCursor
    Given the conversation has 3 entries
    When I list entries with limit 3
    Then the response status should be 200
    And the response should contain 3 entries
    And the response should not have an afterCursor
    And the response should not have a beforeCursor

  Scenario: Forward pagination respects fork ancestry boundaries
    Given the conversation has an entry "Parent 1"
    And the conversation has an entry "Parent 2"
    And the conversation has an entry "Parent 3"
    When I list entries for the conversation
    And set "forkPointId" to the json response field "data[1].id"
    And I fork the conversation at entry "${forkPointId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "forkConversationId" to "${forkedConversationId}"
    And set "conversationId" to "${forkConversationId}"
    And the conversation has an entry "Child 1"
    And the conversation has an entry "Child 2"
    And the conversation has an entry "Child 3"
    When I list entries with limit 2
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Parent 1"
    And entry at index 1 should have content "Fork message"
    And the response should have an afterCursor
    And the response should not have a beforeCursor
    When I list entries with afterCursor from previous response and limit 2
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Child 1"
    And entry at index 1 should have content "Child 2"
    And the response should have an afterCursor
    And the response should have a beforeCursor
    When I list entries with afterCursor from previous response and limit 2
    Then the response status should be 200
    And the response should contain 1 entries
    And entry at index 0 should have content "Child 3"
    And the response should not have an afterCursor
    And the response should have a beforeCursor

  Scenario: Explicit forks none excludes later parent entries and sibling branches
    Given the conversation has an entry "Before fork"
    And the conversation has an entry "Fork point"
    And the conversation has an entry "Later parent entry"
    And set "rootConversationId" to "${conversationId}"
    When I list entries for the conversation
    And set "explicitNoneForkPointId" to the json response field "data[1].id"
    And set "explicitNoneSiblingId" to "00000000-0000-4000-8000-000000000301"
    When I call POST "/v1/conversations/${explicitNoneSiblingId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "forkedAtConversationId": "${rootConversationId}",
      "forkedAtEntryId": "${explicitNoneForkPointId}",
      "content": [{"role": "USER", "text": "Sibling branch entry"}]
    }
    """
    Then the response status should be 201
    And set "explicitNoneSelectedId" to "00000000-0000-4000-8000-000000000302"
    When I call POST "/v1/conversations/${explicitNoneSelectedId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "forkedAtConversationId": "${rootConversationId}",
      "forkedAtEntryId": "${explicitNoneForkPointId}",
      "content": [{"role": "USER", "text": "Selected branch entry"}]
    }
    """
    Then the response status should be 201
    When I call GET "/v1/conversations/${explicitNoneSelectedId}/entries?channel=history&forks=none&tail=true&limit=50"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Before fork"
    And entry at index 1 should have content "Selected branch entry"

  Scenario: Forward pagination cursor crossing ancestor segment boundary
    Given the conversation has an entry "Root 1"
    And the conversation has an entry "Root 2"
    When I list entries for the conversation
    And set "forkPointId" to the json response field "data[1].id"
    And I fork the conversation at entry "${forkPointId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "conversationId" to "${forkedConversationId}"
    And the conversation has an entry "Child 1"
    And the conversation has an entry "Child 2"
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history&limit=10"
    Then the response status should be 200
    And the response should contain 4 entries
    And entry at index 0 should have content "Root 1"
    And entry at index 1 should have content "Fork message"
    And entry at index 2 should have content "Child 1"
    And entry at index 3 should have content "Child 2"
    When I list entries with limit 1
    Then the response should contain 1 entries
    And entry at index 0 should have content "Root 1"
    When I list entries with afterCursor from previous response and limit 1
    Then the response should contain 1 entries
    And entry at index 0 should have content "Fork message"
    When I list entries with afterCursor from previous response and limit 10
    Then the response should contain 2 entries
    And entry at index 0 should have content "Child 1"
    And entry at index 1 should have content "Child 2"

  Scenario: Forward pagination with forks=all pages across the whole group
    Given the conversation has an entry "Group Parent 1"
    And the conversation has an entry "Group Parent 2"
    And set "rootConversationId" to "${conversationId}"
    When I list entries for the conversation
    And set "forkPointId" to the json response field "data[0].id"
    And I fork the conversation at entry "${forkPointId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "conversationId" to "${forkedConversationId}"
    And the conversation has an entry "Group Child 1"
    When I call GET "/v1/conversations/${rootConversationId}/entries?channel=history&forks=all&limit=2"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Group Parent 1"
    And entry at index 1 should have content "Group Parent 2"
    And set "groupAfterCursor" to the json response field "afterCursor"
    When I call GET "/v1/conversations/${rootConversationId}/entries?channel=history&forks=all&afterCursor=${groupAfterCursor}&limit=2"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Fork message"
    And entry at index 1 should have content "Group Child 1"
    When I call GET "/v1/conversations/${rootConversationId}/entries?channel=history&forks=all&tail=true&limit=2&upToEntryId=${groupAfterCursor}"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Group Parent 1"
    And entry at index 1 should have content "Group Parent 2"

  Scenario: Forward pagination with forks=all pages context entries across the whole group
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a context entry "Group Context Parent" with epoch 1 and contentType "test.v1"
    And the conversation has an entry "Group Context Fork Point"
    And set "rootConversationId" to "${conversationId}"
    When I list entries for the conversation with channel "HISTORY"
    Then the response status should be 200
    And set "forkPointId" to the json response field "data[0].id"
    And I fork the conversation at entry "${forkPointId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "conversationId" to "${forkedConversationId}"
    And the conversation has a context entry "Group Context Child" with epoch 1 and contentType "test.v1"
    When I call GET "/v1/conversations/${rootConversationId}/entries?channel=context&forks=all&epoch=1&limit=1"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Group Context Parent"
    And set "contextAfterCursor" to the json response field "afterCursor"
    When I call GET "/v1/conversations/${rootConversationId}/entries?channel=context&forks=all&epoch=1&afterCursor=${contextAfterCursor}&limit=1"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Group Context Child"

  Scenario: fromSeq pagination respects fork ancestry boundaries
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","seq":1,"content":[{"role":"USER","text":"Root seq one"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","seq":2,"content":[{"role":"USER","text":"Root seq two"}]}
    """
    Then the response status should be 201
    When I list entries for the conversation
    And set "forkSeqPointId" to the json response field "data[1].id"
    And I fork the conversation at entry "${forkSeqPointId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "conversationId" to "${forkedConversationId}"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","seq":3,"content":[{"role":"USER","text":"Child seq three"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","seq":4,"content":[{"role":"USER","text":"Child seq four"}]}
    """
    Then the response status should be 201
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history&fromSeq=3&limit=10"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Child seq three"
    And entry at index 1 should have content "Child seq four"

  Scenario: Forward pagination rejects afterCursor from wrong ancestry branch
    Given the conversation has an entry "Parent entry"
    When I list entries for the conversation
    And set "rootConvId" to "${conversationId}"
    And set "parentEntryId" to the json response field "data[0].id"
    And set "blankForkId" to "00000000-0000-0000-0000-000000000209"
    When I call POST "/v1/conversations/${blankForkId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "forkedAtConversationId": "${rootConvId}",
      "content": [{"role": "USER", "text": "Blank-slate child entry"}]
    }
    """
    Then the response status should be 201
    When I call GET "/v1/conversations/${blankForkId}/entries?channel=history&afterCursor=${parentEntryId}"
    Then the response status should be 400

  Scenario: Forward pagination rejects an unknown afterCursor
    Given the conversation has 2 entries
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history&afterCursor=00000000-0000-0000-0000-000000000404&limit=1"
    Then the response status should be 400
    And the response body field "error" should contain "afterCursor entry not found"

  Scenario: Forward pagination rejects a malformed afterCursor
    Given the conversation has 1 entries
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history&afterCursor=not-a-uuid&limit=1"
    Then the response status should be 400
    And the response body field "error" should contain "afterCursor"

  Scenario: Entry listing rejects a malformed upToEntryId
    Given the conversation has 1 entries
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history&upToEntryId=not-a-uuid&limit=1"
    Then the response status should be 400
    And the response body field "error" should contain "upToEntryId"
