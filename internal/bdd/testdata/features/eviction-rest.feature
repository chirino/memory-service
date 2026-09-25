Feature: Data Eviction

  Background:
    Given I am authenticated as admin user "alice"

  # Serial required: this scenario runs a datastore-wide eviction sweep that can hard-delete records and queue tasks for data created by other scenarios.
  Scenario: Evict conversation groups past retention period (default response)
    Given I have a conversation with title "Old Conversation"
    And set "oldConversationId" to "${conversationId}"
    And set "oldGroupId" to "${conversationGroupId}"
    And the conversation was archived 100 days ago
    And I have a conversation with title "Recent Conversation"
    And set "recentConversationId" to "${conversationId}"
    And set "recentGroupId" to "${conversationGroupId}"
    And the conversation was archived 10 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"],
        "justification": "Test cleanup"
      }
      """
    Then the response status should be 204
    # Verify old conversation is gone
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversations WHERE id = '${oldConversationId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
      """
      {
        "collection": "conversations",
        "operation": "count",
        "filter": {
          "_id": "${oldConversationId}"
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 0     |
    # Verify recent conversation still exists
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversations WHERE id = '${recentConversationId}'
      """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
      """
      {
        "collection": "conversation_groups",
        "operation": "count",
        "filter": {
          "_id": "${recentGroupId}"
        }
      }
      """
    Then the MongoDB result should match:
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
    When I execute MongoDB query:
      """
      {
        "collection": "tasks",
        "operation": "count",
        "filter": {
          "task_type": "vector_store_delete"
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 1     |

  # Serial required: this scenario runs a datastore-wide eviction sweep and verifies descendant deletion across logical child trees.
  Scenario: Evicting a parent group deletes child groups and their forks
    Given I have a conversation with title "Eviction lineage parent"
    And set "parentConversationId" to "${conversationId}"
    And set "parentGroupId" to "${conversationGroupId}"
    And I am authenticated as agent with API key "test-agent-key"
    And I append an entry to the conversation:
      """
      {
        "channel": "HISTORY",
        "contentType": "history",
        "content": [{"role": "USER", "text": "Delegate child work"}]
      }
      """
    And set "parentEntryId" to the json response field "id"
    When I call POST "/v1/conversations/00000000-0000-4000-8000-000000000801/entries" with body:
      """
      {
        "channel": "HISTORY",
        "contentType": "history",
        "startedByConversationId": "${parentConversationId}",
        "startedByEntryId": "${parentEntryId}",
        "content": [{"role": "USER", "text": "Direct child work"}]
      }
      """
    Then the response status should be 201
    And set "childConversationId" to "00000000-0000-4000-8000-000000000801"
    And set "childEntryId" to the json response field "id"
    And I resolve the conversation group ID for conversation "${childConversationId}" into "childGroupId"
    When I fork conversation "${childConversationId}" at entry "${childEntryId}" with request:
      """
      {}
      """
    And set "childForkId" to "${forkedConversationId}"
    And set "nestedConversationId" to "00000000-0000-4000-8000-000000000802"
    When I call POST "/v1/conversations/${nestedConversationId}/entries" with body:
      """
      {
        "channel": "HISTORY",
        "contentType": "history",
        "startedByConversationId": "${childForkId}",
        "content": [{"role": "USER", "text": "Nested child work"}]
      }
      """
    Then the response status should be 201
    And set "nestedEntryId" to the json response field "id"
    And I resolve the conversation group ID for conversation "${nestedConversationId}" into "nestedGroupId"
    When I fork conversation "${nestedConversationId}" at entry "${nestedEntryId}" with request:
      """
      {}
      """
    And set "nestedForkId" to "${forkedConversationId}"

    When I archive conversation "${parentConversationId}"
    Then the response status should be 200
    And the response body field "archived" should be "true"
    When I call GET "/v1/conversations/${childConversationId}"
    Then the response status should be 200
    And the response body field "archived" should be "false"
    When I call GET "/v1/conversations/${childForkId}"
    Then the response status should be 200
    And the response body field "archived" should be "false"
    When I call GET "/v1/conversations/${nestedConversationId}"
    Then the response status should be 200
    And the response body field "archived" should be "false"
    When I call GET "/v1/conversations/${nestedForkId}"
    Then the response status should be 200
    And the response body field "archived" should be "false"

    And set "conversationId" to "${parentConversationId}"
    And the conversation was archived 100 days ago
    And "alice" is connected to the SSE event stream
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
      }
      """
    Then the response status should be 204
    And "alice" should receive conversation deletion events for:
      | conversationId          |
      | ${parentConversationId} |
      | ${childConversationId}  |
      | ${childForkId}          |
      | ${nestedConversationId} |
      | ${nestedForkId}         |
    And "alice" should not receive an SSE event with kind "conversation" and event "deleted" within 2 seconds
    When I execute SQL query:
      """
      SELECT COUNT(*) AS count FROM conversation_groups
      WHERE id IN ('${parentGroupId}', '${childGroupId}', '${nestedGroupId}')
      """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
      """
      {
        "collection": "conversation_groups",
        "operation": "count",
        "filter": {
          "_id": {
            "$in": ["${parentGroupId}", "${childGroupId}", "${nestedGroupId}"]
          }
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 0     |
    When I execute SQL query:
      """
      SELECT COUNT(*) AS count FROM tasks
      WHERE task_type = 'vector_store_delete'
        AND task_body->>'conversationGroupId' IN ('${parentGroupId}', '${childGroupId}', '${nestedGroupId}')
      """
    Then the SQL result should match:
      | count |
      | 3     |
    When I execute MongoDB query:
      """
      {
        "collection": "tasks",
        "operation": "count",
        "filter": {
          "task_type": "vector_store_delete",
          "task_body.conversationGroupId": {
            "$in": ["${parentGroupId}", "${childGroupId}", "${nestedGroupId}"]
          }
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 3     |

    When I call GET "/v1/conversations/${parentConversationId}"
    Then the response status should be 404
    When I call GET "/v1/conversations/${childConversationId}"
    Then the response status should be 404
    When I call GET "/v1/conversations/${childForkId}"
    Then the response status should be 404
    When I call GET "/v1/conversations/${nestedConversationId}"
    Then the response status should be 404
    When I call GET "/v1/conversations/${nestedForkId}"
    Then the response status should be 404

    When I call GET "/v1/conversations?ancestry=roots&mode=all"
    Then the response status should be 200
    And the response should contain 0 conversations
    When I call GET "/v1/conversations?ancestry=children&mode=all"
    Then the response status should be 200
    And the response should contain 0 conversations

  # Serial required: this scenario runs a datastore-wide eviction sweep that can hard-delete records created by other scenarios.
  Scenario: Evict with SSE progress stream via Accept header
    Given I have a conversation with title "To Evict"
    And the conversation was archived 100 days ago
    When I call POST "/v1/admin/evict" with Accept "text/event-stream" and body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
      }
      """
    Then the response status should be 200
    And the response content type should be "text/event-stream"
    And the SSE stream should contain progress events
    And the final progress should be 100

  # Serial required: this scenario runs a datastore-wide eviction sweep that can hard-delete records created by other scenarios.
  Scenario: Evict with SSE progress stream via async=true
    Given I have a conversation with title "To Evict Async"
    And the conversation was archived 100 days ago
    When I call POST "/v1/admin/evict?async=true" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
      }
      """
    Then the response status should be 200
    And the response content type should be "text/event-stream"
    And the SSE stream should contain progress events
    And the final progress should be 100

  # Serial required: this scenario intentionally runs multiple eviction requests against the shared datastore and would interfere with any other scenario's archived records.
  Scenario: Concurrent eviction is safe
    Given I have 100 conversations archived 100 days ago
    When I call POST "/v1/admin/evict" concurrently 3 times with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
      }
      """
    Then all responses should have status 204
    # Verify all conversations were deleted exactly once
    # Note: With concurrent eviction, all 100 conversations should be hard-deleted
    # We check that there are no archived conversations remaining
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversations WHERE archived_at IS NOT NULL
      """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
      """
      {
        "collection": "conversations",
        "operation": "count",
        "filter": {
          "archived_at": {
            "$ne": null
          }
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 0     |

  # Serial today only because this feature shares the serial eviction runner; this scenario is a pure authorization check and appears parallel-safe.
  Scenario: Non-admin user cannot evict
    Given I am authenticated as auditor user "charlie"
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
      }
      """
    Then the response status should be 403

  # Serial today only because this feature shares the serial eviction runner; this scenario is a pure validation check and appears parallel-safe.
  Scenario: Invalid retention period format rejected
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "90 days",
        "resourceTypes": ["conversations"]
      }
      """
    Then the response status should be 400

  # Serial today only because this feature shares the serial eviction runner; this scenario is a pure validation check and appears parallel-safe.
  Scenario: Unknown resource type rejected
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["entries"]
      }
      """
    Then the response status should be 400

  # Serial required: this scenario runs a datastore-wide eviction sweep and asserts hard-deletion side effects in shared tables.
  Scenario: Cascade deletes child records
    Given I have a conversation with title "Parent Conversation"
    And set "groupId" to "${conversationGroupId}"
    And the conversation has entries
    And the conversation was archived 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
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
    When I execute MongoDB query:
      """
      {
        "collection": "entries",
        "operation": "count",
        "filter": {
          "conversation_group_id": "${groupId}"
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 0     |

  # Note: Membership eviction tests removed - memberships are now hard-deleted immediately
  # (see enhancement 028-membership-hard-delete.md)

  # Serial required: this scenario runs a datastore-wide eviction sweep that can hard-delete records created by other scenarios.
  Scenario: Evict multiple conversations in single request
    Given I have a conversation with title "Group To Evict"
    And set "conversationAId" to "${conversationId}"
    And set "groupAId" to "${conversationGroupId}"
    And the conversation was archived 100 days ago
    And I have a conversation with title "Another Group"
    And set "conversationBId" to "${conversationId}"
    And set "groupBId" to "${conversationGroupId}"
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
      }
      """
    Then the response status should be 204
    # Group A should be hard-deleted
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversations WHERE id = '${conversationAId}'
      """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
      """
      {
        "collection": "conversation_groups",
        "operation": "count",
        "filter": {
          "_id": "${groupAId}"
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 0     |
    # Group B should still exist
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversations WHERE id = '${conversationBId}'
      """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
      """
      {
        "collection": "conversation_groups",
        "operation": "count",
        "filter": {
          "_id": "${groupBId}"
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 1     |

  # Serial required: this scenario still executes the global eviction path, so concurrent scenarios could make the datastore non-empty and change the outcome.
  Scenario: Empty eviction returns 204
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
      }
      """
    Then the response status should be 204

  # Serial required: this scenario runs a datastore-wide eviction sweep over many records and would interfere with other scenarios' archived data.
  Scenario: Batching evicts all records
    Given I have 25 conversations archived 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
      }
      """
    Then the response status should be 204
    # All 25 should be gone (batch-size=10 exercises 3 batches)
    When I execute SQL query:
      """
      SELECT COUNT(*) as count FROM conversations WHERE archived_at IS NOT NULL
      """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
      """
      {
        "collection": "conversations",
        "operation": "count",
        "filter": {
          "archived_at": {
            "$ne": null
          }
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 0     |

  # Serial required: this scenario runs a datastore-wide eviction sweep and asserts hard-deletion side effects in shared tables.
  Scenario: Cascade deletes memberships and ownership transfers
    Given I have a conversation with title "Full Cascade"
    And set "groupId" to "${conversationGroupId}"
    And the conversation is shared with user "bob"
    And the conversation has a pending ownership transfer to user "bob"
    And the conversation was archived 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
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
    When I execute MongoDB query:
      """
      {
        "collection": "conversation_memberships",
        "operation": "count",
        "filter": {
          "conversation_group_id": "${groupId}"
        }
      }
      """
    Then the MongoDB result should match:
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
    When I execute MongoDB query:
      """
      {
        "collection": "conversation_ownership_transfers",
        "operation": "count",
        "filter": {
          "conversation_group_id": "${groupId}"
        }
      }
      """
    Then the MongoDB result should match:
      | count |
      | 0     |

  # Serial required: this scenario runs the global eviction path and then inspects the shared task queue contents.
  Scenario: Vector store task contains correct group ID
    Given I have a conversation with title "Vector Cleanup"
    And set "groupId" to "${conversationGroupId}"
    And the conversation was archived 100 days ago
    When I call POST "/v1/admin/evict" with body:
      """
      {
        "retentionPeriod": "P90D",
        "resourceTypes": ["conversations"]
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
    When I execute MongoDB query:
      """
      {
        "collection": "tasks",
        "operation": "aggregate",
        "pipeline": [
          {
            "$match": {
              "task_type": "vector_store_delete"
            }
          },
          {
            "$project": {
              "_id": 0,
              "group_id": "$task_body.conversationGroupId"
            }
          }
        ]
      }
      """
    Then the MongoDB result should match:
      | group_id   |
      | ${groupId} |
