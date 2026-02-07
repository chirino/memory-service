Feature: Attachments gRPC API
  As a client of the memory service
  I want to upload and download file attachments via gRPC
  So that I can handle large files using streaming without hitting message size limits

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "gRPC Attachment Test"

  Scenario: Upload a file via gRPC
    When I upload a file via gRPC with filename "test.txt" content type "text/plain" and content "Hello gRPC World"
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "href" should not be null
    And the gRPC response field "contentType" should be "text/plain"
    And the gRPC response field "filename" should be "test.txt"
    And the gRPC response field "size" should be "16"
    And the gRPC response field "sha256" should not be null
    And the gRPC response field "expiresAt" should not be null

  Scenario: Download an uploaded attachment via gRPC
    When I upload a file via gRPC with filename "download-test.txt" content type "text/plain" and content "Download me via gRPC"
    Then the gRPC response should not have an error
    And set "attachmentId" to the gRPC response field "id"
    When I download attachment "${attachmentId}" via gRPC
    Then the gRPC response should not have an error
    And the gRPC download content should be "Download me via gRPC"
    And the gRPC download metadata field "contentType" should be "text/plain"
    And the gRPC download metadata field "filename" should be "download-test.txt"

  Scenario: Get attachment metadata via gRPC
    When I upload a file via gRPC with filename "metadata-test.txt" content type "text/plain" and content "Metadata check"
    Then the gRPC response should not have an error
    And set "attachmentId" to the gRPC response field "id"
    When I get attachment "${attachmentId}" metadata via gRPC
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "contentType" should be "text/plain"
    And the gRPC response field "filename" should be "metadata-test.txt"
    And the gRPC response field "size" should be "14"

  Scenario: Cannot download unlinked attachment as different user
    When I upload a file via gRPC with filename "secret.txt" content type "text/plain" and content "Secret Data"
    Then the gRPC response should not have an error
    And set "attachmentId" to the gRPC response field "id"
    Given I am authenticated as user "bob"
    When I download attachment "${attachmentId}" via gRPC
    Then the gRPC response should have status "PERMISSION_DENIED"
