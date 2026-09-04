Feature: Entries CreatedAt Filtering gRPC API
  As a client of the memory service
  I want to filter conversation entries by createdAt timestamp via gRPC
  So that I can retrieve entries within specific time windows

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "CreatedAt Test Conversation gRPC"
    And the conversation has an entry "Entry 1"
    And set "entry1Id" to the json response field "id"
    And the conversation has an entry "Entry 2"
    And set "entry2Id" to the json response field "id"
    And the conversation has an entry "Entry 3"
    And set "entry3Id" to the json response field "id"
    And entry "entry1Id" has createdAt "2026-01-01T10:00:00Z"
    And entry "entry2Id" has createdAt "2026-01-02T10:00:00Z"
    And entry "entry3Id" has createdAt "2026-01-03T10:00:00Z"

  Scenario: Filter entries with exact createdAt match via gRPC
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    created_at_eq {
      seconds: 1767348000
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entries

  Scenario: Filter entries with createdAtAfter and createdAtBefore range via gRPC
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    created_at_after {
      seconds: 1767261600
    }
    created_at_before {
      seconds: 1767348000
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries

  Scenario: Reject mutually exclusive createdAt and createdAtAfter via gRPC
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    created_at_eq {
      seconds: 1767348000
    }
    created_at_after {
      seconds: 1767261600
    }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    And the gRPC error message should contain "mutually exclusive"
