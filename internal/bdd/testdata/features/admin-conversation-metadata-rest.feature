Feature: Admin conversation metadata — summaries, PATCH, and filter (REST)
  As an administrator
  I want admin conversation summaries to include metadata
  And I want to update metadata and title via admin PATCH
  And I want to filter admin conversation lists by metadata key-value
  So that I can manage and audit conversations based on custom state

  Background:
    Given I am authenticated as admin user "alice"

  # ── A: metadata appears in admin conversation summaries ──────────────────

  Scenario: Admin list returns metadata in conversation summaries
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Summary Meta Test",
      "metadata": {
        "status": "pending",
        "priority": "high"
      }
    }
    """
    And the response status should be 201
    And set "metaConvId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?userId=bob&mode=all"
    Then the response status should be 200
    And the response body should contain "pending"
    And the response body should contain "priority"

  Scenario: Admin get conversation returns metadata in detail response
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Detail Meta",
      "metadata": {
        "env": "test",
        "owner": "ci"
      }
    }
    """
    And the response status should be 201
    And set "detailMetaConvId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations/${detailMetaConvId}"
    Then the response status should be 200
    And the response body should contain "env"
    And the response body should contain "test"
    And the response body should contain "owner"

  # ── B: admin PATCH updates metadata and title ─────────────────────────────

  Scenario: Admin PATCH updates metadata on a conversation
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin Meta PATCH Target"
    And set "patchTargetId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${patchTargetId}" with body:
    """
    {
      "metadata": {"status": "reviewed"},
      "justification": "admin metadata update test"
    }
    """
    Then the response status should be 200
    And the response body should contain "reviewed"
    When I call GET "/v1/admin/conversations/${patchTargetId}"
    Then the response status should be 200
    And the response body should contain "reviewed"

  Scenario: Admin PATCH updates title on a conversation
    Given I am authenticated as user "bob"
    And I have a conversation with title "Original Admin Title"
    And set "titlePatchId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${titlePatchId}" with body:
    """
    {
      "title": "Admin Updated Title",
      "justification": "admin title update test"
    }
    """
    Then the response status should be 200
    And the response body field "title" should be "Admin Updated Title"
    When I call GET "/v1/admin/conversations/${titlePatchId}"
    Then the response status should be 200
    And the response body field "title" should be "Admin Updated Title"

  Scenario: Admin PATCH title null clears the title
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin Title To Clear"
    And set "titleClearId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${titleClearId}" with body:
    """
    {
      "title": null,
      "justification": "clear admin title"
    }
    """
    Then the response status should be 200
    And the response body field "title" should be ""
    When I call GET "/v1/admin/conversations/${titleClearId}"
    Then the response status should be 200
    And the response body field "title" should be ""

  Scenario: Admin PATCH merge-patches metadata — absent keys are unchanged
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Merge Patch Test",
      "metadata": {"a": "1", "b": "2"}
    }
    """
    And the response status should be 201
    And set "mergePatchId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${mergePatchId}" with body:
    """
    {
      "metadata": {"b": "updated"},
      "justification": "admin merge patch test"
    }
    """
    Then the response status should be 200
    And the response body should contain "a"
    And the response body should contain "updated"
    And the response body should contain "1"

  Scenario: Admin PATCH with only archived field still works
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin Archive Only"
    And set "archiveOnlyId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${archiveOnlyId}" with body:
    """
    {
      "archived": true,
      "justification": "archive-only test"
    }
    """
    Then the response status should be 200
    And the response body field "archived" should be "true"

  Scenario: Admin PATCH with no fields returns 400
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin PATCH No Fields"
    And set "noFieldsId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${noFieldsId}" with body:
    """
    {
      "justification": "no-op test"
    }
    """
    Then the response status should be 400

  Scenario Outline: Admin PATCH metadata rejects non-object types with 400
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin Invalid Metadata Type"
    And set "invalidTypeId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${invalidTypeId}" with body:
    """
    {
      "metadata": <value>,
      "justification": "invalid metadata type"
    }
    """
    Then the response status should be 400

    Examples:
      | value |
      | []    |
      | "bad" |
      | 1     |
      | true  |

  Scenario: Admin PATCH with malformed JSON involving metadata returns 400
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin Malformed Metadata JSON"
    And set "malformedMetadataId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${malformedMetadataId}" with body:
    """
    {"metadata": {"key": "value", "justification": "test"
    """
    Then the response status should be 400

  Scenario: Admin PATCH metadata null is a valid no-op
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Null Metadata No-Op",
      "metadata": {"keep": "yes"}
    }
    """
    And the response status should be 201
    And set "nullNoOpId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${nullNoOpId}" with body:
    """
    {
      "metadata": null,
      "justification": "null metadata no-op"
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "metadata": {"keep": "yes"}
    }
    """

  Scenario: Admin PATCH metadata empty object is a valid no-op
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Empty Metadata No-Op",
      "metadata": {"keep": "yes"}
    }
    """
    And the response status should be 201
    And set "emptyNoOpId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${emptyNoOpId}" with body:
    """
    {
      "metadata": {},
      "justification": "empty metadata no-op"
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "metadata": {"keep": "yes"}
    }
    """

  # ── C: admin list filter by metadata key-value ────────────────────────────

  Scenario: Admin list filters by metadata key-value
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Meta Filter Waiting",
      "metadata": {"state": "waiting"}
    }
    """
    And the response status should be 201
    And set "adminWaitingId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "Admin Meta Filter Running",
      "metadata": {"state": "running"}
    }
    """
    And the response status should be 201
    And set "adminRunningId" to the json response field "id"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?userId=bob&mode=all&metadata[state]=waiting"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${adminWaitingId}"

  Scenario: Admin list metadata filter with no match returns empty list
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Filter No Match",
      "metadata": {"state": "running"}
    }
    """
    And the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?userId=bob&mode=all&metadata[state]=nonexistent"
    Then the response status should be 200
    And the response should contain 0 conversations

  Scenario: Admin list metadata filter with invalid key returns 400
    When I call GET "/v1/admin/conversations?metadata[bad key!]=val"
    Then the response status should be 400

  Scenario: Admin list filter with multiple metadata keys returns 400
    When I call GET "/v1/admin/conversations?metadata[status]=waiting&metadata[priority]=high"
    Then the response status should be 400

  Scenario: Admin list filter with repeated metadata value returns 400
    When I call GET "/v1/admin/conversations?metadata[status]=waiting&metadata[status]=running"
    Then the response status should be 400

  Scenario: Admin list filter with repeated metadata expressions uses AND semantics
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Waiting Worker 1",
      "metadata": {
        "status": "waiting",
        "agent-id": "worker-1"
      }
    }
    """
    And the response status should be 201
    And set "matchAdminId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "Admin Waiting Worker 2",
      "metadata": {
        "status": "waiting",
        "agent-id": "worker-2"
      }
    }
    """
    And the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?userId=bob&mode=all&metadata=status=waiting&metadata=agent-id!=worker-2"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${matchAdminId}"

  Scenario: Admin list filter with six metadata expressions returns 400
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?metadata=a=1&metadata=b=2&metadata=c=3&metadata=d=4&metadata=e=5&metadata=f=6"
    Then the response status should be 400

  Scenario: Admin list mixing repeated metadata filter and legacy metadata[key] returns 400
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?metadata=status=waiting&metadata[agent]=worker"
    Then the response status should be 400

  Scenario: Admin list filter with empty metadata key returns 400
    When I call GET "/v1/admin/conversations?metadata[]=value"
    Then the response status should be 400

  Scenario: Admin list filter with metadata key but no supplied value returns 400
    When I call GET "/v1/admin/conversations?metadata[status]"
    Then the response status should be 400

  Scenario: Admin list filter with malformed metadata prefix returns 400
    When I call GET "/v1/admin/conversations?metadata.foo=value"
    Then the response status should be 400

  Scenario: Admin list filter with nested metadata key returns 400
    When I call GET "/v1/admin/conversations?metadata[key][nested]=value"
    Then the response status should be 400

  Scenario: Admin list filter with dotted metadata key returns 400
    When I call GET "/v1/admin/conversations?metadata[key.nested]=value"
    Then the response status should be 400

  Scenario: Admin metadata filter matches string "1" but not numeric 1
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin String One",
      "metadata": {"count": "1"}
    }
    """
    And the response status should be 201
    And set "adminStringOneId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "Admin Numeric One",
      "metadata": {"count": 1}
    }
    """
    And the response status should be 201
    And I create a conversation with request:
    """
    {
      "title": "Admin Boolean True",
      "metadata": {"count": true}
    }
    """
    And the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?userId=bob&mode=all&metadata[count]=1"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${adminStringOneId}"

  Scenario: Admin metadata filter matches empty string value
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Empty Status",
      "metadata": {"status": ""}
    }
    """
    And the response status should be 201
    And set "adminEmptyStatusId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "Admin Null Status",
      "metadata": {"other": "value"}
    }
    """
    And the response status should be 201
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?userId=bob&mode=all&metadata[status]="
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${adminEmptyStatusId}"

  Scenario: Admin latest-fork mode returns latest matching conversation when older fork matches
    Given I am authenticated as user "bob"
    And I create a conversation with request:
    """
    {
      "title": "Admin Root Conversation",
      "metadata": {"status": "waiting"}
    }
    """
    And the response status should be 201
    And set "adminRootId" to the json response field "id"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "hello"}]
    }
    """
    And the response status should be 201
    And set "adminForkPointId" to "${response.body.id}"
    When I fork the conversation at entry "${adminForkPointId}"
    And set "adminForkId" to "${forkedConversationId}"
    When I call PATCH "/v1/conversations/${adminForkId}" with body:
    """
    {"metadata": {"status": "running"}}
    """
    Then the response status should be 200
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/conversations?userId=bob&mode=latest-fork&metadata[status]=waiting"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${adminRootId}"


  # ── D: admin PATCH validation order prevents partial mutations ──────────

  Scenario: Admin PATCH with archived=true and invalid title type returns 400 and leaves conversation unarchived
    Given I am authenticated as user "bob"
    And I have a conversation with title "Archive Invalid Title Type Test"
    And set "archiveInvalidTitleTypeId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${archiveInvalidTitleTypeId}" with body:
    """
    {
      "archived": true,
      "title": 12345,
      "justification": "archive with invalid title type test"
    }
    """
    Then the response status should be 400
    And the response body should contain "invalid title"
    When I call GET "/v1/admin/conversations/${archiveInvalidTitleTypeId}"
    Then the response status should be 200
    And the response body field "archived" should be "false"

  Scenario: Admin PATCH with archived=true and invalid metadata type returns 400 and leaves conversation unarchived
    Given I am authenticated as user "bob"
    And I have a conversation with title "Archive Invalid Metadata Type Test"
    And set "archiveInvalidMetaTypeId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${archiveInvalidMetaTypeId}" with body:
    """
    {
      "archived": true,
      "metadata": "not-an-object",
      "justification": "archive with invalid metadata type test"
    }
    """
    Then the response status should be 400
    When I call GET "/v1/admin/conversations/${archiveInvalidMetaTypeId}"
    Then the response status should be 200
    And the response body field "archived" should be "false"

  Scenario: Admin PATCH with archived=true and title exceeding max length returns 400 and leaves conversation unarchived
    Given I am authenticated as user "bob"
    And I have a conversation with title "Archive Long Title Test"
    And set "archiveLongTitleId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${archiveLongTitleId}" with body:
    """
    {
      "archived": true,
      "title": "This title is way too long and exceeds the maximum allowed length of 500 characters. Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum. Sed ut perspiciatis unde omnis iste natus error.",
      "justification": "archive with long title test"
    }
    """
    Then the response status should be 400
    And the response body should contain "title exceeds maximum length"
    When I call GET "/v1/admin/conversations/${archiveLongTitleId}"
    Then the response status should be 200
    And the response body field "archived" should be "false"
