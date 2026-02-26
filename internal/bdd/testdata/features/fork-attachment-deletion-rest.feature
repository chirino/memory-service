Feature: Fork with attachment deletion edge cases REST API
  As a user
  I want to create forked conversations with attachments
  So that attachment references are validated and not silently dropped

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Parent Conversation"

  Scenario: Creating entry referencing a deleted attachment returns error
    # Upload and link an attachment to the parent entry
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "image.png" with content type "image/png" and content "original-data"
    Then the response status should be 201
    And set "linkedAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Here is an image",
        "attachments": [{"attachmentId": "${linkedAttachmentId}", "contentType": "image/png", "name": "image.png"}]
      }]
    }
    """
    Then the response status should be 201
    And set "parentEntryId" to the json response field "id"

    # Upload a NEW attachment (not linked to any entry yet), then DELETE it
    When I upload a file "deleted.png" with content type "image/png" and content "deleted-data"
    Then the response status should be 201
    And set "deletedAttachmentId" to the json response field "id"
    When I call DELETE "/v1/attachments/${deletedAttachmentId}"
    Then the response status should be 204

    # Try to create an entry referencing the deleted attachment — should fail with 404
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Reference deleted file",
        "attachments": [{"attachmentId": "${deletedAttachmentId}", "contentType": "image/png", "name": "deleted.png"}]
      }]
    }
    """
    Then the response status should be 404

  Scenario: Fork with fresh attachment succeeds when not prematurely deleted
    # Upload and link an attachment to the parent entry
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I upload a file "doc.pdf" with content type "application/pdf" and content "parent-pdf-data"
    Then the response status should be 201
    And set "parentAttachmentId" to the json response field "id"
    When I call POST "/v1/conversations/${conversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Review this document",
        "attachments": [{"attachmentId": "${parentAttachmentId}", "contentType": "application/pdf", "name": "doc.pdf"}]
      }]
    }
    """
    Then the response status should be 201
    And set "parentEntryId" to the json response field "id"

    # Upload a NEW attachment for the fork
    When I upload a file "fork-image.png" with content type "image/png" and content "fork-image-data"
    Then the response status should be 201
    And set "forkAttachmentId" to the json response field "id"

    # Create forked entry with the new attachment — uses the fork step which
    # generates a new conversation UUID and auto-creates the fork
    When I fork the conversation at entry "${parentEntryId}" with request:
    """
    {}
    """
    Then the response status should be 200

    # Now add an entry to the fork with the fresh attachment
    When I call POST "/v1/conversations/${forkedConversationId}/entries" with body:
    """
    {
      "channel": "HISTORY",
      "contentType": "history",
      "content": [{
        "role": "USER",
        "text": "Here is a new image for the fork",
        "attachments": [{"attachmentId": "${forkAttachmentId}", "contentType": "image/png", "name": "fork-image.png"}]
      }]
    }
    """
    Then the response status should be 201
    And the response body field "content[0].attachments[0].href" should contain "/v1/attachments/"
    And set "forkAttachmentHref" to the json response field "content[0].attachments[0].href"

    # Verify the attachment is accessible
    When I call GET "${forkAttachmentHref}" expecting binary
    Then the response status should be 200
    And the binary response content should be "fork-image-data"
