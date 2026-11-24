Feature: Attachment upload and URL creation with a caller-supplied ID
  A caller-supplied attachment ID identifies the create operation. Repeating the request
  never creates a second attachment, and the caller recovers the outcome by looking the
  attachment up under the same ID.

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Attachment Idempotency Conversation"

  # --- Multipart upload ---

  Scenario: Multipart upload with a caller-supplied ID stores the attachment under that ID
    Given set "attId" to a new UUID
    When I upload a file "mine.txt" with content type "text/plain" and content "Hello Caller ID" with id "${attId}"
    Then the response status should be 201
    And the response body field "id" should be "${attId}"
    And the response body field "status" should be "ready"
    And the response body field "filename" should be "mine.txt"
    And the response body field "size" should be "15"
    And the response body field "sha256" should not be null
    When I call GET "/v1/attachments/${attId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Hello Caller ID"
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

  Scenario: Multipart upload without an ID keeps the legacy behavior
    When I upload a file "legacy.txt" with content type "text/plain" and content "No Caller ID"
    Then the response status should be 201
    And the response body field "id" should not be null
    And the response body field "status" should be "ready"
    And the response body field "size" should be "12"
    And set "firstId" to the json response field "id"
    When I upload a file "legacy.txt" with content type "text/plain" and content "No Caller ID"
    Then the response status should be 201
    And the response body field "status" should be "ready"
    And set "secondId" to the json response field "id"
    # Without an ID every request creates its own attachment: no deduplication.
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM attachments WHERE id IN ('${firstId}', '${secondId}')
    """
    Then the SQL result should match:
      | count |
      | 2     |

  Scenario: A repeated upload with the same ID is a conflict and never replaces the stored content
    Given set "attId" to a new UUID
    When I upload a file "first.txt" with content type "text/plain" and content "Original Content" with id "${attId}"
    Then the response status should be 201
    When I upload a file "second.txt" with content type "text/plain" and content "Different Content" with id "${attId}"
    Then the response status should be 409
    When I call GET "/v1/attachments/${attId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Original Content"
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

  Scenario: The outcome of an upload whose response was lost is recovered by metadata lookup
    Given set "attId" to a new UUID
    # The first response is treated as lost: the client only knows its request may have succeeded.
    When I upload a file "lost.txt" with content type "text/plain" and content "Lost Ack" with id "${attId}"
    Then the response status should be 201
    And set "originalSha" to the json response field "sha256"
    When I upload a file "lost.txt" with content type "text/plain" and content "Lost Ack" with id "${attId}"
    Then the response status should be 409
    When I call GET "/v1/attachments/${attId}/metadata"
    Then the response status should be 200
    And the response body field "id" should be "${attId}"
    And the response body field "status" should be "ready"
    And the response body field "size" should be "8"
    And the response body field "sha256" should be "${originalSha}"

  Scenario: An ID that belongs to another user is not found and is not disturbed
    Given set "attId" to a new UUID
    When I upload a file "alice.txt" with content type "text/plain" and content "Alice Data" with id "${attId}"
    Then the response status should be 201
    Given I am authenticated as user "bob"
    When I upload a file "bob.txt" with content type "text/plain" and content "Bob Data" with id "${attId}"
    Then the response status should be 404
    Given I am authenticated as user "alice"
    When I call GET "/v1/attachments/${attId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Alice Data"
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

  Scenario: A malformed ID is rejected before anything is stored
    When I upload a file "bad.txt" with content type "text/plain" and content "Bad Id" with id "not-a-uuid"
    Then the response status should be 400
    When I upload a file "blank.txt" with content type "text/plain" and content "Blank Id" with id ""
    Then the response status should be 400
    When I upload a file "nil.txt" with content type "text/plain" and content "Nil Id" with id "00000000-0000-0000-0000-000000000000"
    Then the response status should be 400
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

  Scenario: An ID can be reused after its attachment is deleted
    Given set "attId" to a new UUID
    When I upload a file "v1.txt" with content type "text/plain" and content "Version One" with id "${attId}"
    Then the response status should be 201
    When I call DELETE "/v1/attachments/${attId}"
    Then the response status should be 204
    When I call GET "/v1/attachments/${attId}/metadata"
    Then the response status should be 404
    When I upload a file "v2.txt" with content type "text/plain" and content "Version Two" with id "${attId}"
    Then the response status should be 201
    And the response body field "status" should be "ready"
    When I call GET "/v1/attachments/${attId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Version Two"

  # --- Recovery while the request body is still being transferred ---

  Scenario: A 404 lookup during a multipart transfer is inconclusive and the original request still completes
    Given set "attId" to a new UUID
    When I start a stalled upload of file "slow.txt" with content type "text/plain" and content "Slow Body" with id "${attId}"
    # The record is created only after the whole body has been buffered, so this 404 proves nothing.
    And I call GET "/v1/attachments/${attId}/metadata"
    Then the response status should be 404
    When I finish the stalled upload
    Then the response status should be 201
    And the response body field "id" should be "${attId}"
    And the response body field "status" should be "ready"
    When I call GET "/v1/attachments/${attId}/metadata"
    Then the response status should be 200
    And the response body field "status" should be "ready"

  Scenario: Retrying with the same ID after a transfer that never completed creates the attachment once
    Given set "attId" to a new UUID
    When I start a stalled upload of file "dropped.txt" with content type "text/plain" and content "Dropped" with id "${attId}"
    And I call GET "/v1/attachments/${attId}/metadata"
    Then the response status should be 404
    When I abandon the stalled upload
    And I upload a file "dropped.txt" with content type "text/plain" and content "Dropped" with id "${attId}"
    Then the response status should be 201
    And the response body field "status" should be "ready"
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

  # --- URL creation ---

  Scenario: URL creation with a caller-supplied ID returns downloading immediately and becomes ready
    Given a gated source server serving "Downloaded Body" with content type "text/plain" as "sourceUrl"
    And set "attId" to a new UUID
    When I call POST "/v1/attachments" with body:
    """
    {
      "sourceUrl": "${sourceUrl}",
      "contentType": "text/plain",
      "name": "remote.txt",
      "id": "${attId}"
    }
    """
    Then the response status should be 201
    And the response body field "id" should be "${attId}"
    And the response body field "status" should be "downloading"
    And I wait for the source server "sourceUrl" to receive 1 request
    When I call GET "/v1/attachments/${attId}/metadata"
    Then the response status should be 200
    And the response body field "status" should be "downloading"
    When I release the source server "sourceUrl"
    And I wait for attachment "${attId}" to have status "ready"
    Then the response body field "size" should be "15"
    When I call GET "/v1/attachments/${attId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Downloaded Body"

  Scenario: A duplicate URL creation starts no second download
    Given a gated source server serving "Only Once" with content type "text/plain" as "sourceUrl"
    And set "attId" to a new UUID
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${sourceUrl}", "contentType": "text/plain", "id": "${attId}"}
    """
    Then the response status should be 201
    And I wait for the source server "sourceUrl" to receive 1 request
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${sourceUrl}", "contentType": "text/plain", "id": "${attId}"}
    """
    Then the response status should be 409
    When I release the source server "sourceUrl"
    And I wait for attachment "${attId}" to have status "ready"
    Then the source server "sourceUrl" should have received exactly 1 request
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

  Scenario: URL creation with an ID that belongs to another user is not found and starts no download
    Given a source server serving "Alice Remote" with content type "text/plain" as "aliceSource"
    And a source server serving "Bob Remote" with content type "text/plain" as "bobSource"
    And set "attId" to a new UUID
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${aliceSource}", "contentType": "text/plain", "id": "${attId}"}
    """
    Then the response status should be 201
    And I wait for attachment "${attId}" to have status "ready"
    Given I am authenticated as user "bob"
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${bobSource}", "contentType": "text/plain", "id": "${attId}"}
    """
    Then the response status should be 404
    Then the source server "bobSource" should have received exactly 0 requests

  Scenario: A malformed ID on URL creation is rejected and starts no download
    Given a source server serving "Never Fetched" with content type "text/plain" as "sourceUrl"
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${sourceUrl}", "contentType": "text/plain", "id": "not-a-uuid"}
    """
    Then the response status should be 400
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${sourceUrl}", "contentType": "text/plain", "id": ""}
    """
    Then the response status should be 400
    Then the source server "sourceUrl" should have received exactly 0 requests

  Scenario: URL creation without an ID keeps the legacy behavior
    Given a source server serving "Legacy Remote" with content type "text/plain" as "sourceUrl"
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${sourceUrl}", "contentType": "text/plain"}
    """
    Then the response status should be 201
    And the response body field "id" should not be null
    And the response body field "status" should be "downloading"
    And set "attId" to the json response field "id"
    When I wait for attachment "${attId}" to have status "ready"
    Then the response body field "size" should be "13"

  Scenario: A failed download keeps blocking its ID until the attachment is deleted
    Given a source server failing with status 500 as "badSource"
    And a source server serving "Recovered Body" with content type "text/plain" as "goodSource"
    And set "attId" to a new UUID
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${badSource}", "contentType": "text/plain", "id": "${attId}"}
    """
    Then the response status should be 201
    When I wait for attachment "${attId}" to have status "failed"
    And I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${goodSource}", "contentType": "text/plain", "id": "${attId}"}
    """
    Then the response status should be 409
    And the source server "goodSource" should have received exactly 0 requests
    When I call DELETE "/v1/attachments/${attId}"
    Then the response status should be 204
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${goodSource}", "contentType": "text/plain", "id": "${attId}"}
    """
    Then the response status should be 201
    When I wait for attachment "${attId}" to have status "ready"
    And I call GET "/v1/attachments/${attId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Recovered Body"

  # --- Linking requires a ready attachment ---

  Scenario: An unfinished attachment cannot be linked to an entry and a ready one can
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    And a gated source server serving "Link Me" with content type "text/plain" as "sourceUrl"
    And set "attId" to a new UUID
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${sourceUrl}", "contentType": "text/plain", "name": "link.txt", "id": "${attId}"}
    """
    Then the response status should be 201
    And the response body field "status" should be "downloading"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Too early",
        "attachments": [{"attachmentId": "${attId}", "contentType": "text/plain", "name": "link.txt"}]
      }]
    }
    """
    Then the response status should be 409
    And the response body field "error" should contain "until it is ready"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM attachments WHERE id = '${attId}' AND entry_id IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "attachments", "operation": "count", "filter": {"_id": "${attId}", "entry_id": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I release the source server "sourceUrl"
    And I wait for attachment "${attId}" to have status "ready"
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Now ready",
        "attachments": [{"attachmentId": "${attId}", "contentType": "text/plain", "name": "link.txt"}]
      }]
    }
    """
    Then the response status should be 201
    And the response body field "content[0].attachments[0].attachmentId" should be "${attId}"

  Scenario: A failed attachment cannot be linked to an entry
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    And a source server failing with status 500 as "badSource"
    And set "attId" to a new UUID
    When I call POST "/v1/attachments" with body:
    """
    {"sourceUrl": "${badSource}", "contentType": "text/plain", "name": "bad.txt", "id": "${attId}"}
    """
    Then the response status should be 201
    When I wait for attachment "${attId}" to have status "failed"
    And I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Failed attachment",
        "attachments": [{"attachmentId": "${attId}", "contentType": "text/plain", "name": "bad.txt"}]
      }]
    }
    """
    Then the response status should be 409
    And the response body field "error" should contain "failed"
