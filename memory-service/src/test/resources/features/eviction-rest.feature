Feature: Data Eviction

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Evict conversation groups past retention period (default response)
    Given I have a conversation with title "Old Conversation"
    And set "oldGroupId" to "${conversationGroupId}"
    And the conversation was soft-deleted 100 days ago
    And I have a conversation with title "Recent Conversation"
    And set "recentGroupId" to "${conversationGroupId}"
    And the conversation was soft-deleted 10 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"],
        "justification": "Test cleanup"
      }
      """
    Then the response status should be 204
    # Verify old conversation is gone
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE id = '${oldGroupId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |
    # Verify recent conversation still exists
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE id = '${recentGroupId}'
      """
    Then the SQL result should match:
      | count |
      | 1     |
    # Verify vector store cleanup task was created
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM tasks WHERE task_type = 'vector_store_delete'
      """
    Then the SQL result should match:
      | count |
      | 1     |

  Scenario: Evict with SSE progress stream via Accept header
    Given I have a conversation with title "To Evict"
    And the conversation was soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" with Accept "text/event-stream" and body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 200
    And the response content type should be "text/event-stream"
    And the SSE stream should contain progress events
    And the final progress should be 100

  Scenario: Evict with SSE progress stream via async=true
    Given I have a conversation with title "To Evict Async"
    And the conversation was soft-deleted 100 days ago
    When I call POST "/v1/admin/evict?async=true" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 200
    And the response content type should be "text/event-stream"
    And the SSE stream should contain progress events
    And the final progress should be 100

  Scenario: Concurrent eviction is safe
    Given I have 100 conversations soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" concurrently 3 times with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then all responses should have status 204
    # Verify all conversations were deleted exactly once
    # Note: With concurrent eviction, all 100 conversations should be hard-deleted
    # We check that there are no soft-deleted conversations remaining
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE deleted_at IS NOT NULL
      """
    Then the SQL result should match:
      | count |
      | 0     |

  Scenario: Non-admin user cannot evict
    Given I am authenticated as auditor user "charlie"
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 403

  Scenario: Invalid retention period format rejected
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "90 days",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 400

  Scenario: Unknown resource type rejected
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["entries"]
      }
      """
    Then the response status should be 400

  Scenario: Cascade deletes child records
    Given I have a conversation with title "Parent Conversation"
    And set "groupId" to "${conversationGroupId}"
    And the conversation has entries
    And the conversation was soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 204
    # Verify entries were cascade deleted
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM entries WHERE conversation_group_id = '${groupId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |

  Scenario: Evict soft-deleted memberships
    Given I have a conversation with title "Membership Test"
    And set "groupId" to "${conversationGroupId}"
    And the conversation is shared with user "bob"
    And the conversation is shared with user "charlie"
    And the membership for user "bob" was soft-deleted 100 days ago
    And the membership for user "charlie" was soft-deleted 10 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_memberships"]
      }
      """
    Then the response status should be 204
    # Bob's membership should be hard-deleted (past retention)
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_memberships WHERE conversation_group_id = '${groupId}' AND user_id = 'bob'
      """
    Then the SQL result should match:
      | count |
      | 0     |
    # Charlie's membership should still exist (within retention)
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_memberships WHERE conversation_group_id = '${groupId}' AND user_id = 'charlie'
      """
    Then the SQL result should match:
      | count |
      | 1     |
    # The conversation group itself should NOT be deleted
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE id = '${groupId}'
      """
    Then the SQL result should match:
      | count |
      | 1     |

  Scenario: Evict multiple resource types in single request
    Given I have a conversation with title "Group To Evict"
    And set "groupAId" to "${conversationGroupId}"
    And the conversation was soft-deleted 100 days ago
    And I have a conversation with title "Membership Host"
    And set "groupBId" to "${conversationGroupId}"
    And the conversation is shared with user "bob"
    And the membership for user "bob" was soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups", "conversation_memberships"]
      }
      """
    Then the response status should be 204
    # Group A should be hard-deleted
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE id = '${groupAId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |
    # Bob's membership from Group B should be hard-deleted
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_memberships WHERE conversation_group_id = '${groupBId}' AND user_id = 'bob'
      """
    Then the SQL result should match:
      | count |
      | 0     |
    # Group B itself should still exist
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE id = '${groupBId}'
      """
    Then the SQL result should match:
      | count |
      | 1     |

  Scenario: Empty eviction returns 204
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 204

  Scenario: Batching evicts all records
    Given I have 25 conversations soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 204
    # All 25 should be gone (batch-size=10 exercises 3 batches)
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_groups WHERE deleted_at IS NOT NULL
      """
    Then the SQL result should match:
      | count |
      | 0     |

  Scenario: Cascade deletes memberships and ownership transfers
    Given I have a conversation with title "Full Cascade"
    And set "groupId" to "${conversationGroupId}"
    And the conversation is shared with user "bob"
    And the conversation has a pending ownership transfer to user "bob"
    And the conversation was soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 204
    # Memberships should be cascade deleted
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_memberships WHERE conversation_group_id = '${groupId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |
    # Ownership transfers should be cascade deleted
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversation_ownership_transfers WHERE conversation_group_id = '${groupId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |

  Scenario: Vector store task contains correct group ID
    Given I have a conversation with title "Vector Cleanup"
    And set "groupId" to "${conversationGroupId}"
    And the conversation was soft-deleted 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversation_groups"]
      }
      """
    Then the response status should be 204
    When I execute SQL query:
      """
      SELECT task_body->>'conversationGroupId' as group_id FROM tasks WHERE task_type = 'vector_store_delete'
      """
    Then the SQL result should match:
      | group_id     |
      | ${groupId}   |
