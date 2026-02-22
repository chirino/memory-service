Feature: Encrypted file store
  As a system operator with encryption enabled
  I want attachments to be transparently encrypted at rest
  So that the storage backend never holds plaintext data

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Encrypted Attachment Test"

  @direct-stream-only
  Scenario: Upload and download with encryption enabled
    When I upload a file "encrypted.txt" with content type "text/plain" and content "hello encrypted world"
    Then the response status should be 201
    And the response body field "size" should be "21"
    And set "encAttId" to the json response field "id"
    When I call GET "/v1/attachments/${encAttId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "hello encrypted world"

  @direct-stream-only
  Scenario: SHA-256 field is populated and content decrypts correctly
    When I upload a file "sha-test.txt" with content type "text/plain" and content "sha256 test data"
    Then the response status should be 201
    And the response body field "sha256" should not be null
    And set "shaAttId" to the json response field "id"
    When I call GET "/v1/attachments/${shaAttId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "sha256 test data"

  @direct-stream-only
  Scenario: Multiple encrypted attachments are independently decryptable
    When I upload a file "first.txt" with content type "text/plain" and content "first attachment"
    Then the response status should be 201
    And set "firstId" to the json response field "id"
    When I upload a file "second.txt" with content type "text/plain" and content "second attachment"
    Then the response status should be 201
    And set "secondId" to the json response field "id"
    When I call GET "/v1/attachments/${firstId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "first attachment"
    When I call GET "/v1/attachments/${secondId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "second attachment"

  Scenario: Download URL proxies through server when encryption is enabled
    When I upload a file "proxy-test.txt" with content type "text/plain" and content "proxy download"
    Then the response status should be 201
    And set "proxyAttId" to the json response field "id"
    When I call GET "/v1/attachments/${proxyAttId}/download-url"
    Then the response status should be 200
    # With db store, the URL is always a server-proxy token URL (not a presigned S3 URL)
    And the response body field "url" should contain "/v1/attachments/download/"
