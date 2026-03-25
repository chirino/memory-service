Feature: Background Task Queue

  Background:
    Given all tasks are deleted

  # Serial required: this scenario clears, claims, and deletes rows from the shared task queue tables.
  Scenario: Task is created and processed successfully
    Given I create a task with type "vector_store_delete" and body:
      """
      {"conversationGroupId": "test-group-123"}
      """
    When the task processor runs
    Then the task should be deleted
    And the vector store should have received a delete call for "test-group-123"

  # Serial required: this scenario clears, claims, and updates rows in the shared task queue tables.
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

  # Serial required: this scenario creates and reprocesses shared task queue rows.
  Scenario: Task is retried after retry delay
    Given I have a failed task with retry_at in the past
    When the task processor runs
    Then the task should be processed again

  # Serial required: this scenario intentionally races multiple processors against the shared task queue tables.
  Scenario: Multiple replicas process tasks concurrently without duplicates
    Given I have 100 pending tasks
    When 3 task processors run concurrently
    Then each task should be processed exactly once
