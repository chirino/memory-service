Feature: Concurrent attachment creation with one caller-supplied ID
  N identical concurrent requests with one ID create exactly one attachment. This feature
  runs serially: the datastore is cleared before each scenario, so table counts are exact.
  The gRPC scenario lives here too, because only features/ is run by the serial runners.

  # Blob accounting: finalizing an attachment registers its blob as "referenced" in an
  # attachment_blob_delete task. A losing request never stores a blob, so after the race
  # there is exactly one such "referenced" task and no task queued or claimed for deletion.

  Background:
    Given I am authenticated as user "alice"

  Scenario: Concurrent multipart uploads with one ID create exactly one attachment and store one blob
    Given set "attId" to a new UUID
    When I upload a file "race.txt" with content type "text/plain" and content "Race Content" with id "${attId}" concurrently 8 times
    Then exactly one concurrent upload should have status 201 and the rest should have status 409
    And every concurrent upload with status 201 should have response body field "id" equal to "${attId}"
    And every concurrent upload with status 201 should have response body field "status" equal to "ready"
    When I call GET "/v1/attachments/${attId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Race Content"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM attachments
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM tasks WHERE task_type = 'attachment_blob_delete' AND task_body->>'state' = 'referenced'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM tasks WHERE task_type = 'attachment_blob_delete' AND task_body->>'state' <> 'referenced'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "attachments", "operation": "count", "filter": {}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "tasks", "operation": "count", "filter": {"task_type": "attachment_blob_delete", "task_body.state": "referenced"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "tasks", "operation": "count", "filter": {"task_type": "attachment_blob_delete", "task_body.state": {"$ne": "referenced"}}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Concurrent URL creations with one ID create one attachment and start one download
    Given a gated source server serving "Race Download" with content type "text/plain" as "sourceUrl"
    And set "attId" to a new UUID
    When I call POST "/v1/attachments" concurrently 8 times with body:
    """
    {"sourceUrl": "${sourceUrl}", "contentType": "text/plain", "id": "${attId}"}
    """
    Then exactly one response should have status 201 and the rest should have status 409
    And I wait for the source server "sourceUrl" to receive 1 request
    # Every duplicate has been answered by now, so a second download would already be visible.
    And the source server "sourceUrl" should have received exactly 1 request
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM attachments
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "attachments", "operation": "count", "filter": {}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I release the source server "sourceUrl"
    And I wait for attachment "${attId}" to have status "ready"
    Then the source server "sourceUrl" should have received exactly 1 request
    When I call GET "/v1/attachments/${attId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Race Download"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM tasks WHERE task_type = 'attachment_blob_delete' AND task_body->>'state' = 'referenced'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM tasks WHERE task_type = 'attachment_blob_delete' AND task_body->>'state' <> 'referenced'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "tasks", "operation": "count", "filter": {"task_type": "attachment_blob_delete", "task_body.state": "referenced"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "tasks", "operation": "count", "filter": {"task_type": "attachment_blob_delete", "task_body.state": {"$ne": "referenced"}}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Concurrent gRPC uploads with one ID create exactly one attachment
    Given set "attId" to a new UUID
    When I upload a file via gRPC with filename "race.txt" content type "text/plain" and content "gRPC Race" with id "${attId}" concurrently 8 times
    Then exactly one concurrent gRPC upload should succeed and the rest should have status "ABORTED"
    And the gRPC response field "id" should be "${attId}"
    And the gRPC response field "status" should be "ready"
    When I download attachment "${attId}" via gRPC
    Then the gRPC response should not have an error
    And the gRPC download content should be "gRPC Race"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM attachments
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM tasks WHERE task_type = 'attachment_blob_delete' AND task_body->>'state' = 'referenced'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM tasks WHERE task_type = 'attachment_blob_delete' AND task_body->>'state' <> 'referenced'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "attachments", "operation": "count", "filter": {}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "tasks", "operation": "count", "filter": {"task_type": "attachment_blob_delete", "task_body.state": "referenced"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "tasks", "operation": "count", "filter": {"task_type": "attachment_blob_delete", "task_body.state": {"$ne": "referenced"}}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |
