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
