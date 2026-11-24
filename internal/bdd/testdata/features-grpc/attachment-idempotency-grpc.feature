Feature: Attachment upload and URL creation with a caller-supplied ID over gRPC
  A caller-supplied attachment ID identifies the create operation. A repeated request
  returns ABORTED for the owner and NOT_FOUND for anyone else, and the caller recovers the
  outcome with GetAttachment.

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "gRPC Attachment Idempotency"

  Scenario: Upload with a caller-supplied ID stores the attachment under that ID
    Given set "attId" to a new UUID
    When I upload a file via gRPC with filename "mine.txt" content type "text/plain" and content "Hello gRPC ID" with id "${attId}"
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${attId}"
    And the gRPC response field "status" should be "ready"
    And the gRPC response field "size" should be "13"
    When I download attachment "${attId}" via gRPC
    Then the gRPC response should not have an error
    And the gRPC download content should be "Hello gRPC ID"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM attachments WHERE id = '${attId}'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "attachments", "operation": "count", "filter": {"_id": "${attId}"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Upload without an ID keeps the legacy behavior
    When I upload a file via gRPC with filename "legacy.txt" content type "text/plain" and content "No ID"
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "status" should be "ready"

  Scenario: A malformed ID is rejected with INVALID_ARGUMENT before anything is stored
    When I upload a file via gRPC with filename "bad.txt" content type "text/plain" and content "Bad" with id "not-a-uuid"
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I upload a file via gRPC with filename "blank.txt" content type "text/plain" and content "Blank" with id ""
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I upload a file via gRPC with filename "nil.txt" content type "text/plain" and content "Nil" with id "00000000-0000-0000-0000-000000000000"
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM attachments WHERE user_id = 'alice'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "attachments", "operation": "count", "filter": {"user_id": "alice"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: A same-owner duplicate is ABORTED and the stored content is untouched
    Given set "attId" to a new UUID
    When I upload a file via gRPC with filename "first.txt" content type "text/plain" and content "Original" with id "${attId}"
    Then the gRPC response should not have an error
    When I upload a file via gRPC with filename "second.txt" content type "text/plain" and content "Different" with id "${attId}"
    Then the gRPC response should have status "ABORTED"
    When I download attachment "${attId}" via gRPC
    Then the gRPC response should not have an error
    And the gRPC download content should be "Original"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM attachments WHERE id = '${attId}'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "attachments", "operation": "count", "filter": {"_id": "${attId}"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Status lookup through GetAttachment recovers the outcome of a repeated upload
    Given set "attId" to a new UUID
    When I upload a file via gRPC with filename "lost.txt" content type "text/plain" and content "Lost Ack" with id "${attId}"
    Then the gRPC response should not have an error
    And set "originalSha" to the gRPC response field "sha256"
    When I upload a file via gRPC with filename "lost.txt" content type "text/plain" and content "Lost Ack" with id "${attId}"
    Then the gRPC response should have status "ABORTED"
    When I get attachment "${attId}" metadata via gRPC
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${attId}"
    And the gRPC response field "status" should be "ready"
    And the gRPC response field "sha256" should be "${originalSha}"

  Scenario: An ID that belongs to another user is NOT_FOUND and the owner's attachment is untouched
    Given set "attId" to a new UUID
    When I upload a file via gRPC with filename "alice.txt" content type "text/plain" and content "Alice Data" with id "${attId}"
    Then the gRPC response should not have an error
    Given I am authenticated as user "bob"
    When I upload a file via gRPC with filename "bob.txt" content type "text/plain" and content "Bob Data" with id "${attId}"
    Then the gRPC response should have status "NOT_FOUND"
    Given I am authenticated as user "alice"
    When I download attachment "${attId}" via gRPC
    Then the gRPC response should not have an error
    And the gRPC download content should be "Alice Data"

  Scenario: A stream cancelled after the ID was claimed leaves a failed attachment that blocks the ID until deleted
    Given set "attId" to a new UUID
    When I start a gRPC upload with id "${attId}" and cancel the stream after sending content "partial"
    And I wait for attachment "${attId}" to have status "failed" via gRPC
    And I upload a file via gRPC with filename "retry.txt" content type "text/plain" and content "Retry" with id "${attId}"
    Then the gRPC response should have status "ABORTED"
    When I send gRPC request "AttachmentsService/DeleteAttachment" with body:
    """
    id: "${attId}"
    """
    Then the gRPC response should not have an error
    When I upload a file via gRPC with filename "retry.txt" content type "text/plain" and content "Retry" with id "${attId}"
    Then the gRPC response should not have an error
    And the gRPC response field "status" should be "ready"
    When I download attachment "${attId}" via gRPC
    Then the gRPC response should not have an error
    And the gRPC download content should be "Retry"

  Scenario: URL creation with a caller-supplied ID returns downloading and a duplicate starts no second download
    Given a gated source server serving "gRPC Downloaded" with content type "text/plain" as "sourceUrl"
    And set "attId" to a new UUID
    When I send gRPC request "AttachmentsService/CreateAttachmentFromUrl" with body:
    """
    source_url: "${sourceUrl}"
    content_type: "text/plain"
    filename: "remote.txt"
    id: "${attId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${attId}"
    And the gRPC response field "status" should be "downloading"
    And I wait for the source server "sourceUrl" to receive 1 request
    When I send gRPC request "AttachmentsService/CreateAttachmentFromUrl" with body:
    """
    source_url: "${sourceUrl}"
    content_type: "text/plain"
    filename: "remote.txt"
    id: "${attId}"
    """
    Then the gRPC response should have status "ABORTED"
    When I get attachment "${attId}" metadata via gRPC
    Then the gRPC response should not have an error
    And the gRPC response field "status" should be "downloading"
    When I release the source server "sourceUrl"
    And I wait for attachment "${attId}" to have status "ready" via gRPC
    Then the source server "sourceUrl" should have received exactly 1 request
    When I download attachment "${attId}" via gRPC
    Then the gRPC response should not have an error
    And the gRPC download content should be "gRPC Downloaded"

  Scenario: URL creation with an ID that belongs to another user is NOT_FOUND and starts no download
    Given a source server serving "Alice Remote" with content type "text/plain" as "aliceSource"
    And a source server serving "Bob Remote" with content type "text/plain" as "bobSource"
    And set "attId" to a new UUID
    When I send gRPC request "AttachmentsService/CreateAttachmentFromUrl" with body:
    """
    source_url: "${aliceSource}"
    content_type: "text/plain"
    id: "${attId}"
    """
    Then the gRPC response should not have an error
    When I wait for attachment "${attId}" to have status "ready" via gRPC
    Given I am authenticated as user "bob"
    When I send gRPC request "AttachmentsService/CreateAttachmentFromUrl" with body:
    """
    source_url: "${bobSource}"
    content_type: "text/plain"
    id: "${attId}"
    """
    Then the gRPC response should have status "NOT_FOUND"
    And the source server "bobSource" should have received exactly 0 requests

  Scenario: A malformed ID on URL creation is INVALID_ARGUMENT and starts no download
    Given a source server serving "Never Fetched" with content type "text/plain" as "sourceUrl"
    When I send gRPC request "AttachmentsService/CreateAttachmentFromUrl" with body:
    """
    source_url: "${sourceUrl}"
    content_type: "text/plain"
    id: "not-a-uuid"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I send gRPC request "AttachmentsService/CreateAttachmentFromUrl" with body:
    """
    source_url: "${sourceUrl}"
    content_type: "text/plain"
    id: ""
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    And the source server "sourceUrl" should have received exactly 0 requests

  Scenario: An unfinished attachment cannot be linked to an entry and a ready one can
    Given a gated source server serving "Link Me" with content type "text/plain" as "sourceUrl"
    And set "attId" to a new UUID
    When I send gRPC request "AttachmentsService/CreateAttachmentFromUrl" with body:
    """
    source_url: "${sourceUrl}"
    content_type: "text/plain"
    filename: "link.txt"
    id: "${attId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "status" should be "downloading"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields { key: "role" value { string_value: "USER" } }
          fields { key: "text" value { string_value: "Too early" } }
          fields {
            key: "attachments"
            value { list_value { values { struct_value {
              fields { key: "attachmentId" value { string_value: "${attId}" } }
              fields { key: "contentType" value { string_value: "text/plain" } }
              fields { key: "name" value { string_value: "link.txt" } }
            } } } }
          }
        }
      }
    }
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "until it is ready"
    When I release the source server "sourceUrl"
    And I wait for attachment "${attId}" to have status "ready" via gRPC
    And I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields { key: "role" value { string_value: "USER" } }
          fields { key: "text" value { string_value: "Now ready" } }
          fields {
            key: "attachments"
            value { list_value { values { struct_value {
              fields { key: "attachmentId" value { string_value: "${attId}" } }
              fields { key: "contentType" value { string_value: "text/plain" } }
              fields { key: "name" value { string_value: "link.txt" } }
            } } } }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
