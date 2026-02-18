Feature: Attachments REST API
  As a user
  I want to upload, retrieve, and link file attachments
  So that I can include server-stored files in conversation entries

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Attachment Test Conversation"

  Scenario: Upload a file attachment
    When I upload a file "test.txt" with content type "text/plain" and content "Hello World"
    Then the response status should be 201
    And the response body field "id" should not be null
    And the response body field "href" should not be null
    And the response body field "contentType" should be "text/plain"
    And the response body field "filename" should be "test.txt"
    And the response body field "size" should be "11"
    And the response body field "sha256" should not be null
    And the response body field "expiresAt" should not be null
    And the response body field "status" should be "ready"

  Scenario: Retrieve an uploaded attachment
    When I upload a file "hello.txt" with content type "text/plain" and content "Hello Retrieval"
    Then the response status should be 201
    Then set "attachmentId" to the json response field "id"
    When I call GET "/v1/attachments/${attachmentId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "Hello Retrieval"
    And the response header "Content-Type" should contain "text/plain"

  Scenario: Cannot retrieve unlinked attachment as different user
    When I upload a file "secret.txt" with content type "text/plain" and content "Secret Data"
    Then the response status should be 201
    Then set "attachmentId" to the json response field "id"
    Given I am authenticated as user "bob"
    When I call GET "/v1/attachments/${attachmentId}" expecting binary
    Then the response status should be 403

  Scenario: Link attachment to entry via attachmentId reference
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "photo.jpg" with content type "image/jpeg" and content "fake-image-data"
    Then the response status should be 201
    Then set "uploadedAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Check out this image",
        "attachments": [
          {
            "attachmentId": "${uploadedAttachmentId}",
            "contentType": "image/jpeg",
            "name": "photo.jpg"
          }
        ]
      }]
    }
    """
    Then the response status should be 201
    And the response body field "content[0].attachments[0].href" should contain "/v1/attachments/"

  Scenario: History channel rejects attachment missing both href and attachmentId
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Bad attachment",
        "attachments": [
          {
            "contentType": "image/jpeg"
          }
        ]
      }]
    }
    """
    Then the response status should be 400
    And the response body field "details.message" should be "History channel attachment at index 0 must have an 'href' or 'attachmentId' field"

  # --- Cache Headers ---
  # Scenarios tagged @direct-stream-only verify headers set by the app server on direct-stream
  # responses. When storage is S3, the retrieve endpoint returns a 307 redirect and RestAssured
  # follows it, so the response headers come from S3, not from the app server.

  @direct-stream-only
  Scenario: Attachment download includes ETag and Cache-Control headers
    When I upload a file "cached.txt" with content type "text/plain" and content "Cache Me"
    Then the response status should be 201
    And set "cachedAttId" to the json response field "id"
    And set "cachedSha256" to the json response field "sha256"
    When I call GET "/v1/attachments/${cachedAttId}" expecting binary
    Then the response status should be 200
    And the response header "ETag" should contain "${cachedSha256}"
    And the response header "Cache-Control" should contain "private"
    And the response header "Cache-Control" should contain "max-age="
    And the response header "Cache-Control" should contain "immutable"

  @direct-stream-only
  Scenario: Attachment download returns 304 Not Modified for matching ETag
    When I upload a file "etag-test.txt" with content type "text/plain" and content "ETag Content"
    Then the response status should be 201
    And set "etagAttId" to the json response field "id"
    And set "etagSha256" to the json response field "sha256"
    When I call GET "/v1/attachments/${etagAttId}" expecting binary with header "If-None-Match" = "\"${etagSha256}\""
    Then the response status should be 304
    And the response header "ETag" should contain "${etagSha256}"
    And the response header "Cache-Control" should contain "private"

  @direct-stream-only
  Scenario: Attachment download returns full response for non-matching ETag
    When I upload a file "etag-miss.txt" with content type "text/plain" and content "Full Response"
    Then the response status should be 201
    And set "etagMissAttId" to the json response field "id"
    When I call GET "/v1/attachments/${etagMissAttId}" expecting binary with header "If-None-Match" = "\"0000000000000000000000000000000000000000000000000000000000000000\""
    Then the response status should be 200
    And the binary response content should be "Full Response"

  @direct-stream-only
  Scenario: Cache-Control is private and never public on attachment downloads
    When I upload a file "private.txt" with content type "text/plain" and content "Private Content"
    Then the response status should be 201
    And set "privateAttId" to the json response field "id"
    When I call GET "/v1/attachments/${privateAttId}" expecting binary
    Then the response status should be 200
    And the response header "Cache-Control" should contain "private"

  @direct-stream-only
  Scenario: Signed token download includes ETag and Cache-Control headers
    When I upload a file "token-cached.txt" with content type "text/plain" and content "Token Cache Content"
    Then the response status should be 201
    And set "tokenCachedId" to the json response field "id"
    And set "tokenCachedSha" to the json response field "sha256"
    When I call GET "/v1/attachments/${tokenCachedId}/download-url"
    Then the response status should be 200
    And set "tokenUrl" to the json response field "url"
    When I call GET "${tokenUrl}" expecting binary without authentication
    Then the response status should be 200
    And the response header "ETag" should contain "${tokenCachedSha}"
    And the response header "Cache-Control" should contain "private"
    And the response header "Cache-Control" should contain "max-age="

  @direct-stream-only
  Scenario: Signed token download returns 304 for matching ETag
    When I upload a file "token-etag.txt" with content type "text/plain" and content "Token ETag Content"
    Then the response status should be 201
    And set "tokenEtagId" to the json response field "id"
    And set "tokenEtagSha" to the json response field "sha256"
    When I call GET "/v1/attachments/${tokenEtagId}/download-url"
    Then the response status should be 200
    And set "tokenEtagUrl" to the json response field "url"
    When I call GET "${tokenEtagUrl}" expecting binary without authentication with header "If-None-Match" = "\"${tokenEtagSha}\""
    Then the response status should be 304
    And the response header "ETag" should contain "${tokenEtagSha}"
