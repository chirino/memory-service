Feature: Descendant eviction lifecycle events

  # Serial required: eviction sweeps archived groups across the datastore.
  Scenario Outline: Eviction reports every descendant and cleans up its group
    Given I am authenticated as admin user "alice"
    And I have a conversation with title "Eviction parent"
    And set "parentId" to "${conversationId}"
    And set "parentGroup" to "${conversationGroupId}"
    And set "childId" to "${parentId}-child"
    When I call POST "/v1/conversations/${childId}/entries" with body:
      """
      {"startedByConversationId":"${parentId}","channel":"HISTORY","contentType":"history","content":[{"role":"USER","text":"child"}]}
      """
    Then the response status should be 201
    And set "childEntryId" to the json response field "id"
    When I call GET "/v1/conversations/${childId}"
    Then the response status should be 200
    And I resolve the conversation group ID for conversation "${childId}" into "childGroup"
    When I call POST "/v1/conversations/${childId}/memberships" with body:
      """
      {"userId":"bob","accessLevel":"reader"}
      """
    Then the response status should be 201
    Given set "forkId" to "${parentId}-child-fork"
    When I call POST "/v1/conversations/${forkId}/entries" with body:
      """
      {"forkedAtConversationId":"${childId}","forkedAtEntryId":"${childEntryId}","channel":"HISTORY","contentType":"history","content":[{"role":"USER","text":"fork"}]}
      """
    Then the response status should be 201
    Given set "grandchildId" to "${parentId}-grandchild"
    When I call POST "/v1/conversations/${grandchildId}/entries" with body:
      """
      {"startedByConversationId":"${forkId}","channel":"HISTORY","contentType":"history","content":[{"role":"USER","text":"grandchild"}]}
      """
    Then the response status should be 201
    When I call GET "/v1/conversations/${grandchildId}"
    Then the response status should be 200
    And I resolve the conversation group ID for conversation "${grandchildId}" into "grandchildGroup"
    Given I have a conversation with title "Unrelated survivor"
    And set "unrelatedId" to "${conversationId}"
    And set "conversationId" to "${parentId}"
    And set "conversationGroupId" to "${parentGroup}"
    And the conversation was archived 100 days ago
    And "alice" is connected to the SSE event stream
    And "bob" is connected to the SSE event stream
    When I call POST "/v1/admin/evict<query>" with Accept "<accept>" and body:
      """
      {"retentionPeriod":"P90D","resourceTypes":["conversations"],"justification":"Verify descendant deletion events"}
      """
    Then the response status should be <status>
    And "alice" should receive conversation deletion events for:
      | conversationId    |
      | ${parentId}       |
      | ${childId}        |
      | ${forkId}         |
      | ${grandchildId}   |
    And "bob" should receive conversation deletion events for:
      | conversationId    |
      | ${childId}        |
      | ${forkId}         |
      | ${grandchildId}   |
    And "alice" should not receive an SSE event with kind "conversation" and event "deleted" within 1 seconds
    And "bob" should not receive an SSE event with kind "conversation" and event "deleted" within 1 seconds
    When I call GET "/v1/conversations/${childId}"
    Then the response status should be 404
    When I call GET "/v1/conversations/${grandchildId}"
    Then the response status should be 404
    When I call GET "/v1/conversations/${unrelatedId}"
    Then the response status should be 200
    When I execute SQL query:
      """
      SELECT COUNT(*) AS count FROM conversation_groups WHERE id IN ('${parentGroup}', '${childGroup}', '${grandchildGroup}')
      """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
      """
      {"collection":"conversation_groups","operation":"count","filter":{"_id":{"$in":["${parentGroup}","${childGroup}","${grandchildGroup}"]}}}
      """
    Then the MongoDB result should match:
      | count |
      | 0     |

    Examples:
      | query       | accept            | status |
      |             | application/json  | 204    |
      |             | text/event-stream | 200    |
      | ?async=true | application/json  | 200    |
