Feature: Forked Attachments REST API
  As a user
  I want to reference attachments from existing entries when creating new entries
  So that I can reuse files across forked conversations without re-uploading

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Parent Conversation"

  Scenario: Reference an already-linked attachment in the same conversation group
    # Upload and link an attachment to the first entry
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "image.png" with content type "image/png" and content "fake-png-data"
    Then the response status should be 201
    And set "originalAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Look at this image",
        "attachments": [{"attachmentId": "${originalAttachmentId}", "contentType": "image/png", "name": "image.png"}]
      }]
    }
    """
    Then the response status should be 201
    And the response body field "content[0].attachments[0].href" should contain "/v1/attachments/"
    # Fork the conversation
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    # Create a new entry in the fork referencing the same attachment
    When I call POST "/v1/conversations/${forkedConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Now crop the top-left corner",
        "attachments": [{"attachmentId": "${originalAttachmentId}", "contentType": "image/png", "name": "image.png"}]
      }]
    }
    """
    Then the response status should be 201
    And the response body field "content[0].attachments[0].href" should contain "/v1/attachments/"
    # The href should use a NEW attachment ID (shared reference, not the original)
    And set "newAttachmentHref" to the json response field "content[0].attachments[0].href"
    # The new attachment should be retrievable
    When I call GET "${newAttachmentHref}" expecting binary
    Then the response status should be 200
    And the binary response content should be "fake-png-data"

  Scenario: Reference attachment as a user (not agent) in a forked conversation
    # First, link an attachment as an agent
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "doc.pdf" with content type "application/pdf" and content "fake-pdf-data"
    Then the response status should be 201
    And set "originalAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Review this document",
        "attachments": [{"attachmentId": "${originalAttachmentId}", "contentType": "application/pdf", "name": "doc.pdf"}]
      }]
    }
    """
    Then the response status should be 201
    # Fork and reference as user
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    Given I am authenticated as user "alice"
    When I call POST "/v1/conversations/${forkedConversationId}/entries" with body:
    """
    {
      "content": [{
        "role": "USER",
        "text": "Summarize page 3",
        "attachments": [{"attachmentId": "${originalAttachmentId}", "contentType": "application/pdf", "name": "doc.pdf"}]
      }]
    }
    """
    Then the response status should be 201
    And the response body field "content[0].attachments[0].href" should contain "/v1/attachments/"

  Scenario: Blob survives when one referencing record is deleted
    # Upload and link to first entry
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "shared.txt" with content type "text/plain" and content "shared-content"
    Then the response status should be 201
    And set "originalAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Original message",
        "attachments": [{"attachmentId": "${originalAttachmentId}", "contentType": "text/plain", "name": "shared.txt"}]
      }]
    }
    """
    Then the response status should be 201
    And set "entry1Id" to the json response field "id"
    # Fork and create a second reference
    When I fork the conversation at entry "${entry1Id}" with request:
    """
    {}
    """
    Then the response status should be 200
    When I call POST "/v1/conversations/${forkedConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Reference the same file",
        "attachments": [{"attachmentId": "${originalAttachmentId}", "contentType": "text/plain", "name": "shared.txt"}]
      }]
    }
    """
    Then the response status should be 201
    And set "newAttachmentHref" to the json response field "content[0].attachments[0].href"
    # Delete the forked conversation (which deletes the second reference)
    When I call DELETE "/v1/conversations/${forkedConversationId}"
    Then the response status should be 204
    # The original attachment should still be accessible
    When I call GET "/v1/attachments/${originalAttachmentId}" expecting binary
    Then the response status should be 200
    And the binary response content should be "shared-content"

  Scenario: Cannot reference attachment from a different conversation group
    # Create a second conversation (different group)
    Given I am authenticated as user "alice"
    When I call POST "/v1/conversations" with body:
    """
    {"title": "Other Conversation"}
    """
    Then the response status should be 201
    And set "otherConversationId" to the json response field "id"
    # Upload and link an attachment to the other conversation
    Given I am authenticated as agent with API key "test-agent-key"
    When I upload a file "private.txt" with content type "text/plain" and content "private-data"
    Then the response status should be 201
    And set "otherAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${otherConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Private file",
        "attachments": [{"attachmentId": "${otherAttachmentId}", "contentType": "text/plain", "name": "private.txt"}]
      }]
    }
    """
    Then the response status should be 201
    # Try to reference it from the first conversation (different group)
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Steal the file",
        "attachments": [{"attachmentId": "${otherAttachmentId}", "contentType": "text/plain", "name": "private.txt"}]
      }]
    }
    """
    Then the response status should be 403

  Scenario: Cannot reference attachment user has no access to
    # Alice uploads and links an attachment
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "secret.txt" with content type "text/plain" and content "secret-data"
    Then the response status should be 201
    And set "aliceAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Alice's secret",
        "attachments": [{"attachmentId": "${aliceAttachmentId}", "contentType": "text/plain", "name": "secret.txt"}]
      }]
    }
    """
    Then the response status should be 201
    # Bob creates his own conversation and tries to reference Alice's attachment
    Given I am authenticated as user "bob"
    And I have a conversation with title "Bob's Conversation"
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Try to use Alice's file",
        "attachments": [{"attachmentId": "${aliceAttachmentId}", "contentType": "text/plain", "name": "secret.txt"}]
      }]
    }
    """
    Then the response status should be 403

  Scenario: Mix a referenced attachment with a fresh upload in the same entry
    # Upload and link an attachment to the first entry
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "original.png" with content type "image/png" and content "original-png-data"
    Then the response status should be 201
    And set "originalAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Here is an image",
        "attachments": [{"attachmentId": "${originalAttachmentId}", "contentType": "image/png", "name": "original.png"}]
      }]
    }
    """
    Then the response status should be 201
    # Fork the conversation
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    # Upload a fresh attachment for the forked entry
    When I upload a file "new-doc.pdf" with content type "application/pdf" and content "fresh-pdf-data"
    Then the response status should be 201
    And set "freshAttachmentId" to the json response field "id"
    # Create an entry in the fork that references the original AND includes the new upload
    When I call POST "/v1/conversations/${forkedConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Compare the image with this new document",
        "attachments": [
          {"attachmentId": "${originalAttachmentId}", "contentType": "image/png", "name": "original.png"},
          {"attachmentId": "${freshAttachmentId}", "contentType": "application/pdf", "name": "new-doc.pdf"}
        ]
      }]
    }
    """
    Then the response status should be 201
    # Both attachments should have href links
    And the response body field "content[0].attachments[0].href" should contain "/v1/attachments/"
    And the response body field "content[0].attachments[0].contentType" should be "image/png"
    And the response body field "content[0].attachments[0].name" should be "original.png"
    And the response body field "content[0].attachments[1].href" should contain "/v1/attachments/"
    And the response body field "content[0].attachments[1].contentType" should be "application/pdf"
    And the response body field "content[0].attachments[1].name" should be "new-doc.pdf"
    # Extract hrefs before doing binary GETs (binary responses clear the JSON context)
    And set "referencedHref" to the json response field "content[0].attachments[0].href"
    And set "freshHref" to the json response field "content[0].attachments[1].href"
    # Verify the referenced attachment content is accessible (blob shared from original)
    When I call GET "${referencedHref}" expecting binary
    Then the response status should be 200
    And the binary response content should be "original-png-data"
    # Verify the fresh attachment content is accessible
    When I call GET "${freshHref}" expecting binary
    Then the response status should be 200
    And the binary response content should be "fresh-pdf-data"

  Scenario: Fork with multiple referenced attachments and a new upload
    # Upload two attachments and link them to an entry
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "photo1.jpg" with content type "image/jpeg" and content "jpeg-data-1"
    Then the response status should be 201
    And set "attachment1Id" to the json response field "id"
    When I upload a file "photo2.jpg" with content type "image/jpeg" and content "jpeg-data-2"
    Then the response status should be 201
    And set "attachment2Id" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Compare these two photos",
        "attachments": [
          {"attachmentId": "${attachment1Id}", "contentType": "image/jpeg", "name": "photo1.jpg"},
          {"attachmentId": "${attachment2Id}", "contentType": "image/jpeg", "name": "photo2.jpg"}
        ]
      }]
    }
    """
    Then the response status should be 201
    # Fork the conversation
    When I list entries for the conversation
    And set "firstEntryId" to the json response field "data[0].id"
    When I fork the conversation at entry "${firstEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200
    # Upload a new attachment
    When I upload a file "notes.txt" with content type "text/plain" and content "my-notes"
    Then the response status should be 201
    And set "newAttachmentId" to the json response field "id"
    # Create entry referencing both originals plus the new upload
    When I call POST "/v1/conversations/${forkedConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Here are both photos and my notes",
        "attachments": [
          {"attachmentId": "${attachment1Id}", "contentType": "image/jpeg", "name": "photo1.jpg"},
          {"attachmentId": "${attachment2Id}", "contentType": "image/jpeg", "name": "photo2.jpg"},
          {"attachmentId": "${newAttachmentId}", "contentType": "text/plain", "name": "notes.txt"}
        ]
      }]
    }
    """
    Then the response status should be 201
    # All three should have hrefs
    And the response body field "content[0].attachments[0].href" should contain "/v1/attachments/"
    And the response body field "content[0].attachments[1].href" should contain "/v1/attachments/"
    And the response body field "content[0].attachments[2].href" should contain "/v1/attachments/"
    # Verify each blob is accessible with correct content
    And set "ref1Href" to the json response field "content[0].attachments[0].href"
    And set "ref2Href" to the json response field "content[0].attachments[1].href"
    And set "ref3Href" to the json response field "content[0].attachments[2].href"
    When I call GET "${ref1Href}" expecting binary
    Then the response status should be 200
    And the binary response content should be "jpeg-data-1"
    When I call GET "${ref2Href}" expecting binary
    Then the response status should be 200
    And the binary response content should be "jpeg-data-2"
    When I call GET "${ref3Href}" expecting binary
    Then the response status should be 200
    And the binary response content should be "my-notes"
