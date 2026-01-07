Feature: Summaries API
  As an agent
  I want to create summaries for conversations
  So that they can be searched by users

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Test Conversation"
    And the conversation has a message "Order status question"

  Scenario: Agent can create a summary and user can search it
    Given I am authenticated as agent with API key "test-agent-key"
    When I create a summary with title "Order summary" and summary "Customer asked about refund policy." and untilMessageId "00000000-0000-0000-0000-000000000101" and summarizedAt "2024-01-01T00:00:00Z"
    Then the response status should be 201
    And the message should have channel "SUMMARY"
    And the message should have content "Customer asked about refund policy."
    Given I am authenticated as user "alice"
    When I search messages for query "refund policy"
    Then the response status should be 200
    And the search response should contain 1 results
    And search result at index 0 should have message content "Customer asked about refund policy."

  Scenario: Summary creation requires agent API key
    Given I am authenticated as user "alice"
    When I create a summary with title "Blocked summary" and summary "Should not be created." and untilMessageId "00000000-0000-0000-0000-000000000102" and summarizedAt "2024-01-01T00:00:00Z"
    Then the response status should be 403
    And the response should contain error code "forbidden"
