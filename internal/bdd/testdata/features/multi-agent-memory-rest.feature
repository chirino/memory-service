Feature: Multi-agent memory scoping via REST
  As multiple agents
  I want memory entries scoped to my client id
  So that agents do not read each other's memory

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Multi-agent memory"
    And the conversation has an entry "User visible history"

  Scenario: Agents see only their own memory entries
    Given I am authenticated as agent with API key "test-agent-key"
    When I append an entry with content "Agent A memory" and channel "MEMORY" and contentType "test.v1"
    Then the response status should be 201
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I append an entry with content "Agent B memory" and channel "MEMORY" and contentType "test.v1"
    Then the response status should be 201
    Given I am authenticated as agent with API key "test-agent-key"
    When I list entries for the conversation with channel "MEMORY"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Agent A memory"
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I list entries for the conversation with channel "MEMORY"
    Then the response status should be 200
    And the response should contain 1 entry
    And entry at index 0 should have content "Agent B memory"
