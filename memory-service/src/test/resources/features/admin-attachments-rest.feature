Feature: Admin Attachments REST API
  As an administrator or auditor
  I want to manage attachments across all users via REST API
  So that I can perform administrative tasks, compliance reviews, and audits

  Background:
    Given I am authenticated as admin user "alice"

  # --- Listing ---

  Scenario: Admin can list all attachments across users
    Given I am authenticated as user "alice"
    And I have a conversation with title "Alice Attachment Conv"
    When I upload a file "alice-photo.jpg" with content type "image/jpeg" and content "alice-image-data"
    Then the response status should be 201
    And set "aliceAttachmentId" to the json response field "id"
    Given I am authenticated as user "bob"
    And I have a conversation with title "Bob Attachment Conv"
    When I upload a file "bob-doc.pdf" with content type "application/pdf" and content "bob-pdf-data"
    Then the response status should be 201
    And set "bobAttachmentId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments"
    Then the response status should be 200
    And the response body "data" should have at least 2 item

  Scenario: Admin can filter attachments by userId
    Given I am authenticated as user "bob"
    And I have a conversation with title "Bob Filter Conv"
    When I upload a file "bob-file.txt" with content type "text/plain" and content "bob-content"
    Then the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments?userId=bob"
    Then the response status should be 200
    And the response body "data" should have at least 1 item

  Scenario: Admin can filter attachments by status=linked
    Given I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "Linked Attachment Conv"
    And the conversation exists
    When I upload a file "linked.txt" with content type "text/plain" and content "linked-data"
    Then the response status should be 201
    And set "linkedAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"text": "See attachment", "role": "USER", "attachments": [{"attachmentId": "${linkedAttachmentId}", "contentType": "text/plain", "name": "linked.txt"}]}]
    }
    """
    Then the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments?status=linked"
    Then the response status should be 200
    And the response body "data" should have at least 1 item

  Scenario: Admin can paginate attachment listing
    Given I am authenticated as user "alice"
    And I have a conversation with title "Pagination Conv"
    When I upload a file "page1.txt" with content type "text/plain" and content "data1"
    Then the response status should be 201
    When I upload a file "page2.txt" with content type "text/plain" and content "data2"
    Then the response status should be 201
    When I upload a file "page3.txt" with content type "text/plain" and content "data3"
    Then the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments?limit=2"
    Then the response status should be 200
    And the response body field "afterCursor" should not be null

  # --- Get Metadata ---

  Scenario: Admin can get metadata for any attachment
    Given I am authenticated as user "bob"
    And I have a conversation with title "Bob Metadata Conv"
    When I upload a file "secret.jpg" with content type "image/jpeg" and content "secret-data"
    Then the response status should be 201
    And set "secretAttachmentId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/${secretAttachmentId}"
    Then the response status should be 200
    And the response body field "filename" should be "secret.jpg"
    And the response body field "userId" should be "bob"
    And the response body field "refCount" should not be null

  Scenario: Admin get returns 404 for non-existent attachment
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/00000000-0000-0000-0000-000000000099"
    Then the response status should be 404

  # --- Download ---

  Scenario: Admin can download content for any attachment
    Given I am authenticated as user "bob"
    And I have a conversation with title "Bob Download Conv"
    When I upload a file "download.txt" with content type "text/plain" and content "downloadable content"
    Then the response status should be 201
    And set "downloadAttachmentId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/${downloadAttachmentId}/content" expecting binary
    Then the response status should be 200
    And the binary response content should be "downloadable content"

  Scenario: Admin can get download URL for any attachment
    Given I am authenticated as user "bob"
    And I have a conversation with title "Bob DL URL Conv"
    When I upload a file "urlfile.txt" with content type "text/plain" and content "url-content"
    Then the response status should be 201
    And set "urlAttachmentId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/${urlAttachmentId}/download-url"
    Then the response status should be 200
    And the response body field "url" should not be null
    And the response body field "expiresIn" should not be null

  Scenario: Download content returns 404 for non-existent attachment
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/00000000-0000-0000-0000-000000000099/content" expecting binary
    Then the response status should be 404

  # --- Delete ---

  Scenario: Admin can delete an unlinked attachment
    Given I am authenticated as user "bob"
    And I have a conversation with title "Bob Delete Conv"
    When I upload a file "delete-me.txt" with content type "text/plain" and content "delete-data"
    Then the response status should be 201
    And set "deleteAttachmentId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call DELETE "/v1/admin/attachments/${deleteAttachmentId}" with body:
    """
    {
      "justification": "Storage cleanup"
    }
    """
    Then the response status should be 204

  Scenario: Admin can delete a linked attachment
    Given I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "Linked Delete Conv"
    And the conversation exists
    When I upload a file "linked-delete.txt" with content type "text/plain" and content "linked-del-data"
    Then the response status should be 201
    And set "linkedDelAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{"text": "With attachment", "role": "USER", "attachments": [{"attachmentId": "${linkedDelAttachmentId}", "contentType": "text/plain", "name": "linked-delete.txt"}]}]
    }
    """
    Then the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call DELETE "/v1/admin/attachments/${linkedDelAttachmentId}" with body:
    """
    {
      "justification": "Policy violation"
    }
    """
    Then the response status should be 204

  Scenario: Admin delete returns 404 for non-existent attachment
    Given I am authenticated as admin user "alice"
    When I call DELETE "/v1/admin/attachments/00000000-0000-0000-0000-000000000099" with body:
    """
    {
      "justification": "Test"
    }
    """
    Then the response status should be 404

  # --- Authorization ---

  Scenario: Auditor can list attachments
    Given I am authenticated as user "bob"
    And I have a conversation with title "Auditor List Conv"
    When I upload a file "auditor-list.txt" with content type "text/plain" and content "auditor-data"
    Then the response status should be 201
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/attachments"
    Then the response status should be 200

  Scenario: Auditor can view attachment metadata
    Given I am authenticated as user "bob"
    And I have a conversation with title "Auditor View Conv"
    When I upload a file "auditor-view.txt" with content type "text/plain" and content "auditor-view-data"
    Then the response status should be 201
    And set "auditorViewAttachmentId" to the json response field "id"
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/attachments/${auditorViewAttachmentId}"
    Then the response status should be 200

  Scenario: Auditor cannot delete attachments
    Given I am authenticated as user "bob"
    And I have a conversation with title "Auditor Delete Conv"
    When I upload a file "auditor-del.txt" with content type "text/plain" and content "auditor-del-data"
    Then the response status should be 201
    And set "auditorDelAttachmentId" to the json response field "id"
    Given I am authenticated as auditor user "charlie"
    When I call DELETE "/v1/admin/attachments/${auditorDelAttachmentId}" with body:
    """
    {
      "justification": "Test deletion"
    }
    """
    Then the response status should be 403

  Scenario: Non-admin user cannot access admin attachment endpoints
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/attachments"
    Then the response status should be 403

  # --- Audit logging ---

  Scenario: Justification is logged when listing attachments
    When I call GET "/v1/admin/attachments?justification=Compliance+audit+2024"
    Then the response status should be 200
    And the admin audit log should contain "listAttachments"
    And the admin audit log should contain "Compliance audit 2024"

  # --- Cache Headers ---

  Scenario: Admin attachment content download includes ETag and Cache-Control
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin Cache Conv"
    When I upload a file "admin-cached.txt" with content type "text/plain" and content "Admin Cache Content"
    Then the response status should be 201
    And set "adminCachedId" to the json response field "id"
    And set "adminCachedSha" to the json response field "sha256"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/${adminCachedId}/content" expecting binary
    Then the response status should be 200
    And the response header "ETag" should contain "${adminCachedSha}"
    And the response header "Cache-Control" should contain "private"
    And the response header "Cache-Control" should contain "max-age="
    And the response header "Cache-Control" should contain "immutable"

  Scenario: Admin attachment content returns 304 for matching ETag
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin ETag Conv"
    When I upload a file "admin-etag.txt" with content type "text/plain" and content "Admin ETag Content"
    Then the response status should be 201
    And set "adminEtagId" to the json response field "id"
    And set "adminEtagSha" to the json response field "sha256"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/${adminEtagId}/content" expecting binary with header "If-None-Match" = "\"${adminEtagSha}\""
    Then the response status should be 304
    And the response header "ETag" should contain "${adminEtagSha}"
