Feature: Background Task Queue

  Scenario: Task is created and processed successfully
    Given I create a task with type "vector_store_delete" and body:
      """
      {"conversationGroupId": "test-group-123"}
      """
    When the task processor runs
    Then the task should be deleted
    And the vector store should have received a delete call for "test-group-123"

  Scenario: Failed task is scheduled for retry
    Given I create a task with type "vector_store_delete" and body:
      """
      {"conversationGroupId": "failing-group"}
      """
    And the vector store will fail for "failing-group"
    When the task processor runs
    Then the task should still exist
    And the task retry_at should be in the future
    And the task last_error should contain the failure message
    And the task retry_count should be 1

  Scenario: Task is retried after retry delay
    Given I have a failed task with retry_at in the past
    When the task processor runs
    Then the task should be processed again

  Scenario: Multiple replicas process tasks concurrently without duplicates
    Given I have 100 pending tasks
    When 3 task processors run concurrently
    Then each task should be processed exactly once
