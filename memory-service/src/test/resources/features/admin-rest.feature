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

  Scenario: Admin can list all conversations across users
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
    And the response should contain at least 2 conversations

  Scenario: Admin can filter conversations by userId
    When I call GET "/v1/admin/conversations?userId=bob"
    Then the response status should be 200
    And the response should contain at least 1 conversation
    And all conversations should have ownerUserId "bob"

  Scenario: Admin can view soft-deleted conversations with includeDeleted=true
    Given the conversation owned by "bob" is deleted
    When I call GET "/v1/admin/conversations?includeDeleted=true"
    Then the response status should be 200
    And the response should contain at least 1 conversation with deletedAt set

  Scenario: Admin can filter by onlyDeleted
    Given the conversation owned by "bob" is deleted
    When I call GET "/v1/admin/conversations?onlyDeleted=true"
    Then the response status should be 200
    And all conversations should have deletedAt set

  Scenario: Admin can get any conversation including soft-deleted
    Given the conversation owned by "bob" is deleted
    When I call GET "/v1/admin/conversations/${bobConversationId}?includeDeleted=true"
    Then the response status should be 200
    And the response body should have field "deletedAt" that is not null

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

  Scenario: Restoring an already-active conversation returns conflict
    When I call POST "/v1/admin/conversations/${aliceConversationId}/restore" with body:
    """
    {
      "justification": "Test restoration"
    }
    """
    Then the response status should be 409

  Scenario: Auditor can list conversations across users
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
    And the response should contain at least 1 conversation

  Scenario: Auditor can view any conversation
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/conversations/${bobConversationId}"
    Then the response status should be 200

  Scenario: Auditor receives 403 Forbidden on delete operation
    Given I am authenticated as auditor user "charlie"
    When I call DELETE "/v1/admin/conversations/${bobConversationId}" with body:
    """
    {
      "justification": "Test deletion"
    }
    """
    Then the response status should be 403

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

  Scenario: Non-admin user receives 403 Forbidden on all admin endpoints
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 403

  Scenario: Justification is logged when provided
    When I call GET "/v1/admin/conversations?justification=Support+ticket+1234"
    Then the response status should be 200
    And the admin audit log should contain "listConversations"
    And the admin audit log should contain "Support ticket 1234"

  Scenario: Admin can get entries from any conversation
    Given the conversation owned by "bob" has an entry "Test entry"
    When I call GET "/v1/admin/conversations/${bobConversationId}/entries"
    Then the response status should be 200
    And the response should contain at least 1 items

  Scenario: Admin can get memberships for any conversation
    When I call GET "/v1/admin/conversations/${bobConversationId}/memberships"
    Then the response status should be 200
    And the response should contain at least 1 memberships

  Scenario: Admin can perform system-wide semantic search
    Given the conversation owned by "bob" has an entry "Searchable content"
    When I call POST "/v1/admin/search/entries" with body:
    """
    {
      "query": "Searchable"
    }
    """
    Then the response status should be 200
    And the response should contain at least 1 items

  Scenario: Admin search can filter by userId
    Given the conversation owned by "bob" has an entry "Bob's entry"
    Given the conversation owned by "alice" has an entry "Alice's entry"
    When I call POST "/v1/admin/search/entries" with body:
    """
    {
      "query": "entry",
      "userId": "bob"
    }
    """
    Then the response status should be 200
    And all search results should have conversation owned by "bob"

  Scenario: Admin conversation response does not contain conversationGroupId
    When I call GET "/v1/admin/conversations/${bobConversationId}"
    Then the response status should be 200
    And the response body should not contain "conversationGroupId"

  Scenario: Admin membership response contains conversationId
    When I call GET "/v1/admin/conversations/${bobConversationId}/memberships"
    Then the response status should be 200
    And the response should contain at least 1 memberships
    And the response body "data[0].conversationId" should be "${bobConversationId}"
    And the response body should not contain "conversationGroupId"
