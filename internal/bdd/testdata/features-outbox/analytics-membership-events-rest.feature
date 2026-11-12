@analytics_memberships
Feature: Complete membership analytics over admin REST events
  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Membership analytics"

  Scenario: The active durable source advertises membership support
    When I call GET "/v1/capabilities"
    Then the response status should be 200
    And the response body should be json:
    """
    {"features":{"analytics_membership_snapshots":true}}
    """

  Scenario: Current baseline includes owners and grants, then delivers role changes and revokes
    When I call POST "/v1/conversations/${conversationId}/memberships" with body:
    """
    {"userId":"bob","accessLevel":"reader"}
    """
    Then the response status should be 201
    Given "alice" is connected to the admin SSE event stream with query "kinds=membership&detail=full&initial_state=current"
    Then "alice" should receive an SSE event with kind "membership" and event "snapshot" where data "userId" is "bob"
    And the SSE event data "accessLevel" should be "reader"
    And the SSE event data should contain "id"
    And the SSE event data should contain "conversationGroupId"
    And the SSE event data should contain "createdAt"
    When I call PATCH "/v1/conversations/${conversationId}/memberships/bob" with body:
    """
    {"accessLevel":"writer"}
    """
    Then the response status should be 200
    And "alice" should receive an SSE event with kind "membership" and event "updated" where data "accessLevel" is "writer"
    And the SSE event cursor should be saved as "beforeRevoke"
    When I call DELETE "/v1/conversations/${conversationId}/memberships/bob"
    Then the response status should be 204
    And "alice" should receive an SSE event with kind "membership" and event "deleted" where data "user" is "bob"
    And the SSE event data should contain "conversation_group"
    Given "alice" is connected to the admin SSE event stream with query "kinds=membership&detail=full&after=${beforeRevoke}"
    Then "alice" should receive an SSE event with kind "membership" and event "deleted" where data "user" is "bob"
    When I call POST "/v1/conversations/${conversationId}/memberships" with body:
    """
    {"userId":"bob","accessLevel":"manager"}
    """
    Then the response status should be 201
    And "alice" should receive an SSE event with kind "membership" and event "created" where data "accessLevel" is "manager"

  Scenario: Implicit owner grant and both ownership-transfer roles are durable
    Given "alice" is connected to the admin SSE event stream with query "kinds=membership,conversation&detail=full&after=start"
    Then "alice" should receive an SSE event with kind "membership" and event "created" where data "userId" is "alice"
    And the SSE event data "accessLevel" should be "owner"
    Given I share the conversation with user "bob" and access level "reader"
    When I call POST "/v1/ownership-transfers" with body:
    """
    {"conversationId":"${conversationId}","newOwnerUserId":"bob"}
    """
    Then the response status should be 201
    And set "transferId" to the json response field "id"
    Given I am authenticated as user "bob"
    When I call POST "/v1/ownership-transfers/${transferId}/accept"
    Then the response status should be 204
    And "alice" should receive an SSE event with kind "membership" and event "updated" where data "userId" is "alice"
    And the SSE event data "accessLevel" should be "manager"
    And "alice" should receive an SSE event with kind "membership" and event "updated" where data "userId" is "bob"
    And the SSE event data "accessLevel" should be "owner"

    And "alice" should receive an SSE event with kind "conversation" and event "updated" where data "ownerUserId" is "bob"

  Scenario: Started conversation groups publish copied memberships
    Given "alice" is connected to the admin SSE event stream with query "kinds=membership&detail=full"
    And I share the conversation with user "bob" and access level "reader"
    Then "alice" should receive an SSE event with kind "membership" and event "created" where data "userId" is "bob"
    And set "parentId" to "${conversationId}"
    And I am authenticated as agent with API key "test-agent-key"
    When I call POST "/v1/conversations/00000000-0000-4000-8000-000000118118/entries" with body:
    """
    {"channel":"HISTORY","contentType":"history","startedByConversationId":"${parentId}","content":[{"role":"USER","text":"Child workflow"}]}
    """
    Then the response status should be 201
    When I call GET "/v1/admin/conversations/00000000-0000-4000-8000-000000118118"
    Then the response status should be 200
    And set "childGroupId" to the json response field "conversationGroupId"
    And "alice" should receive an SSE event with kind "membership" and event "created" where data "userId" is "bob"
    And the SSE event data "conversationGroupId" should be "${childGroupId}"
    And the SSE event data "accessLevel" should be "reader"

  Scenario: A hard-deleted group is reported on the admin REST stream
    Given I share the conversation with user "bob" and access level "reader"
    And "alice" is connected to the admin SSE event stream with query "kinds=conversation,membership&detail=full"
    And I am authenticated as admin user "alice"
    And the conversation was archived 100 days ago
    When I call POST "/v1/admin/evict" with body:
    """
    {"retentionPeriod":"P90D","resourceTypes":["conversations"],"justification":"Verify membership group deletion"}
    """
    Then the response status should be 204
    And "alice" should receive an SSE event with kind "conversation" and event "deleted" where data "conversation" is "${conversationId}"
    And the SSE event data should contain "conversation_group"
    And the SSE event data should contain "members"
