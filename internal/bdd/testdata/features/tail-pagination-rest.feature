Feature: Tail and backward pagination for conversation entries (REST)
  As a chat client
  I want to load the newest entries first and page backward
  So that I can open at the bottom of a conversation without scanning from the beginning

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Pagination Test"

  Scenario: Tail page returns the newest entries in chronological order
    Given the conversation has 5 entries
    When I list entries with tail enabled and limit 3
    Then the response status should be 200
    And the response should contain 3 entries
    And entry at index 0 should have content "Entry 3"
    And entry at index 1 should have content "Entry 4"
    And entry at index 2 should have content "Entry 5"
    And the response should have a beforeCursor
    And the response should not have an afterCursor

  Scenario: Tail on a conversation with fewer entries than the limit
    Given the conversation has 3 entries
    When I list entries with tail enabled and limit 10
    Then the response status should be 200
    And the response should contain 3 entries
    And the response should not have a beforeCursor
    And the response should not have an afterCursor

  Scenario: Tail on an empty conversation
    Given the conversation has no entries
    When I list entries with tail enabled and limit 5
    Then the response status should be 200
    And the response should contain 0 entries
    And the response should not have a beforeCursor
    And the response should not have an afterCursor

  Scenario: Backward pagination using beforeCursor from tail page
    Given the conversation has 5 entries
    When I list entries with tail enabled and limit 2
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 4"
    And entry at index 1 should have content "Entry 5"
    And the response should have a beforeCursor
    When I list entries with beforeCursor and limit 2
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 2"
    And entry at index 1 should have content "Entry 3"
    And the response should have a beforeCursor
    And the response should have an afterCursor

  Scenario: Backward pagination reaches the beginning
    Given the conversation has 4 entries
    When I list entries with tail enabled and limit 3
    Then the response status should be 200
    And the response should contain 3 entries
    And entry at index 0 should have content "Entry 2"
    And entry at index 1 should have content "Entry 3"
    And entry at index 2 should have content "Entry 4"
    And the response should have a beforeCursor
    When I list entries with beforeCursor and limit 3
    Then the response status should be 200
    And the response should contain 1 entries
    And entry at index 0 should have content "Entry 1"
    And the response should not have a beforeCursor
    And the response should have an afterCursor

  Scenario: Forward pagination after backward page returns correct entries
    Given the conversation has 5 entries
    When I list entries with tail enabled and limit 2
    Then the response should contain 2 entries
    And entry at index 0 should have content "Entry 4"
    And entry at index 1 should have content "Entry 5"
    When I list entries with beforeCursor and limit 2
    Then the response should contain 2 entries
    And entry at index 0 should have content "Entry 2"
    And entry at index 1 should have content "Entry 3"
    When I list entries with afterCursor from previous response and limit 2
    Then the response should contain 2 entries
    And entry at index 0 should have content "Entry 4"
    And entry at index 1 should have content "Entry 5"

  Scenario: Standard forward pagination also returns beforeCursor when there are older entries
    Given the conversation has 5 entries
    When I list entries with limit 2
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 1"
    And entry at index 1 should have content "Entry 2"
    And the response should have an afterCursor
    And the response should not have a beforeCursor

  Scenario: Conflicting pagination controls are rejected
    Given the conversation has 1 entries
    When I list entries with both afterCursor and tail enabled
    Then the response status should be 400

  Scenario Outline: Every conflicting pagination pair is rejected
    Given the conversation has 1 entry
    When I call GET "/v1/conversations/${conversationId}/entries?<query>"
    Then the response status should be 400

    Examples:
      | query |
      | afterCursor=00000000-0000-0000-0000-000000000001&beforeCursor=00000000-0000-0000-0000-000000000002 |
      | beforeCursor=00000000-0000-0000-0000-000000000001&tail=true |

  Scenario: Invalid tail value returns 400
    Given the conversation has 1 entries
    When I list entries with tail "invalid" and limit 5
    Then the response status should be 400

  Scenario: Tail=tru (close misspelling) returns 400
    Given the conversation has 1 entries
    When I list entries with tail "tru" and limit 5
    Then the response status should be 400

  Scenario: Malformed beforeCursor returns 400
    Given the conversation has 1 entry
    When I call GET "/v1/conversations/${conversationId}/entries?beforeCursor=not-a-uuid"
    Then the response status should be 400

  Scenario: Exact limit at tail returns no beforeCursor
    Given the conversation has 3 entries
    When I list entries with tail enabled and limit 3
    Then the response status should be 200
    And the response should contain 3 entries
    And the response should not have a beforeCursor
    And the response should not have an afterCursor

  Scenario: Tail and backward pagination preserve fork ancestry boundaries
    Given the conversation has an entry "Parent 1"
    And the conversation has an entry "Parent 2"
    And the conversation has an entry "Excluded fork point"
    When I list entries for the conversation
    And set "forkPointId" to the json response field "data[2].id"
    And I fork the conversation at entry "${forkPointId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "forkConversationId" to "${forkedConversationId}"
    And set "conversationId" to "${forkConversationId}"
    And the conversation has an entry "Child 1"
    And the conversation has an entry "Child 2"
    When I list entries with tail enabled and limit 10
    Then the response status should be 200
    And the response should contain 5 entries
    And entry at index 0 should have content "Parent 1"
    And entry at index 1 should have content "Parent 2"
    And entry at index 2 should have content "Fork message"
    And entry at index 3 should have content "Child 1"
    And entry at index 4 should have content "Child 2"
    When I list entries with tail enabled and limit 2
    Then the response should contain 2 entries
    And entry at index 0 should have content "Child 1"
    And entry at index 1 should have content "Child 2"
    When I list entries with beforeCursor and limit 2
    Then the response should contain 2 entries
    And entry at index 0 should have content "Parent 2"
    And entry at index 1 should have content "Fork message"
    When I list entries with beforeCursor and limit 2
    Then the response should contain 1 entry
    And entry at index 0 should have content "Parent 1"
    When I list entries for the conversation
    And set "secondForkPointId" to the json response field "data[4].id"
    And I fork the conversation at entry "${secondForkPointId}" with request:
    """
    {}
    """
    Then the response status should be 200
    And set "conversationId" to "${forkedConversationId}"
    And the conversation has an entry "Grandchild 1"
    When I list entries with tail enabled and limit 10
    Then the response should contain 6 entries
    And entry at index 0 should have content "Parent 1"
    And entry at index 1 should have content "Parent 2"
    And entry at index 2 should have content "Fork message"
    And entry at index 3 should have content "Child 1"
    And entry at index 4 should have content "Fork message"
    And entry at index 5 should have content "Grandchild 1"
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history&beforeCursor=${forkPointId}"
    Then the response status should be 400

  Scenario: History backward pagination rejects a context cursor
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "History before context"
    And the conversation has an entry "Context cursor" in channel "CONTEXT" with contentType "test.v1"
    When I list entries for the conversation
    And set "contextCursor" to the json response field "data[1].id"
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history&beforeCursor=${contextCursor}"
    Then the response status should be 400

  Scenario: Backward pagination rejects an entry from a blank-slate ancestor
    Given set "rootConversationId" to "${conversationId}"
    And the conversation has an entry "Invisible parent entry"
    When I list entries for the conversation
    And set "invisibleParentEntryId" to the json response field "data[0].id"
    And set "blankForkId" to "00000000-0000-0000-0000-000000000109"
    When I call POST "/v1/conversations/${blankForkId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "forkedAtConversationId": "${rootConversationId}",
      "content": [{"role": "USER", "text": "Blank-slate child entry"}]
    }
    """
    Then the response status should be 201
    When I call GET "/v1/conversations/${blankForkId}/entries?channel=history&beforeCursor=${invisibleParentEntryId}"
    Then the response status should be 400

  Scenario: Backward pagination preserves null seq before seq zero
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","content":[{"role":"USER","text":"Null seq"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","seq":0,"content":[{"role":"USER","text":"Seq zero"}]}
    """
    Then the response status should be 201
    And the conversation entries share the same createdAt timestamp
    When I list entries with tail enabled and limit 1
    Then the response should contain 1 entry
    And entry at index 0 should have content "Seq zero"
    When I list entries with beforeCursor and limit 1
    Then the response should contain 1 entry
    And entry at index 0 should have content "Null seq"

  Scenario: Tail pagination applies the upToEntryId filter first
    Given the conversation has 5 entries
    When I list entries for the conversation
    And set "upToEntryId" to the json response field "data[3].id"
    When I call GET "/v1/conversations/${conversationId}/entries?tail=true&limit=2&upToEntryId=${upToEntryId}"
    Then the response status should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 3"
    And entry at index 1 should have content "Entry 4"

  Scenario: Tail pagination applies context epoch filtering first
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a context entry "Epoch one" with epoch 1 and contentType "test.v1"
    And the conversation has a context entry "Epoch two first" with epoch 2 and contentType "test.v1"
    And the conversation has a context entry "Epoch two second" with epoch 2 and contentType "test.v1"
    When I call GET "/v1/conversations/${conversationId}/entries?channel=context&epoch=2&tail=true&limit=1"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Epoch two second"

  Scenario: Tail pagination applies fromSeq filtering first
    Given I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","seq":1,"content":[{"role":"USER","text":"Seq one"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","seq":2,"content":[{"role":"USER","text":"Seq two"}]}
    """
    Then the response status should be 201
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","seq":3,"content":[{"role":"USER","text":"Seq three"}]}
    """
    Then the response status should be 201
    When I call GET "/v1/conversations/${conversationId}/entries?channel=history&fromSeq=2&tail=true&limit=1"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Seq three"
