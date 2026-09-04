Feature: Entries CreatedAt Filtering REST API
  As an agent or user
  I want to filter conversation entries by createdAt timestamp
  So that I can retrieve history within specific time windows

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "CreatedAt Test Conversation"
    And the conversation has an entry "Entry 1"
    And set "entry1Id" to the json response field "id"
    And the conversation has an entry "Entry 2"
    And set "entry2Id" to the json response field "id"
    And the conversation has an entry "Entry 3"
    And set "entry3Id" to the json response field "id"
    And entry "entry1Id" has createdAt "2026-01-01T10:00:00Z"
    And entry "entry2Id" has createdAt "2026-01-02T10:00:00Z"
    And entry "entry3Id" has createdAt "2026-01-03T10:00:00Z"

  Scenario: Filter entries with exact createdAt match
    When I call GET "/v1/conversations/${conversationId}/entries?createdAt=2026-01-02T10:00:00Z"
    Then the response code should be 200
    And the response should contain 1 entries
    And entry at index 0 should have content "Entry 2"

  Scenario: Filter entries with createdAtAfter
    When I call GET "/v1/conversations/${conversationId}/entries?createdAtAfter=2026-01-02T10:00:00Z"
    Then the response code should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 2"
    And entry at index 1 should have content "Entry 3"

  Scenario: Filter entries with createdAtBefore
    When I call GET "/v1/conversations/${conversationId}/entries?createdAtBefore=2026-01-02T10:00:00Z"
    Then the response code should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 1"
    And entry at index 1 should have content "Entry 2"

  Scenario: Filter entries with createdAt date range
    When I call GET "/v1/conversations/${conversationId}/entries?createdAtAfter=2026-01-01T10:00:00Z&createdAtBefore=2026-01-02T10:00:00Z"
    Then the response code should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 1"
    And entry at index 1 should have content "Entry 2"

  Scenario: Reject mutually exclusive createdAt and createdAtAfter
    When I call GET "/v1/conversations/${conversationId}/entries?createdAt=2026-01-02T10:00:00Z&createdAtAfter=2026-01-01T10:00:00Z"
    Then the response code should be 400
    And the response should contain "mutually exclusive"

  Scenario: Reject mutually exclusive createdAt and createdAtBefore
    When I call GET "/v1/conversations/${conversationId}/entries?createdAt=2026-01-02T10:00:00Z&createdAtBefore=2026-01-03T10:00:00Z"
    Then the response code should be 400
    And the response should contain "mutually exclusive"

  Scenario: Admin filter entries by createdAt range
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${conversationId}/entries?createdAtAfter=2026-01-02T00:00:00Z&createdAtBefore=2026-01-03T23:59:59Z"
    Then the response code should be 200
    And the response should contain 2 entries
    And entry at index 0 should have content "Entry 2"
    And entry at index 1 should have content "Entry 3"

  Scenario: Admin reject mutually exclusive createdAt and createdAtAfter
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${conversationId}/entries?createdAt=2026-01-02T10:00:00Z&createdAtAfter=2026-01-01T10:00:00Z"
    Then the response code should be 400
    And the response should contain "mutually exclusive"
