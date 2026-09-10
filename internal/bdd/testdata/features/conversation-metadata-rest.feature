Feature: Conversation metadata write, read, and filter (REST)
  As an agent app
  I want to write and read arbitrary metadata on conversations
  And filter conversations by metadata key-value pairs
  So that I can efficiently track conversation state without per-conversation fetches

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"

  # ── A: metadata in PATCH /v1/conversations/{id} ──────────────────────────

  Scenario: PATCH sets metadata on an existing conversation
    Given I have a conversation with title "Metadata PATCH Test"
    When I update the conversation with request:
    """
    {
      "metadata": {
        "status": "waiting",
        "customKey": "hello"
      }
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "id": "${conversationId}",
      "title": "Metadata PATCH Test",
      "ownerUserId": "alice",
      "createdAt": "${response.body.createdAt}",
      "updatedAt": "${response.body.updatedAt}",
      "accessLevel": "owner",
      "metadata": {
        "status": "waiting",
        "customKey": "hello"
      }
    }
    """

  Scenario: PATCH metadata merge-patch — absent keys are left unchanged
    Given I create a conversation with request:
    """
    {
      "title": "Merge Patch Test",
      "metadata": {
        "a": "1",
        "b": "2"
      }
    }
    """
    And the response status should be 201
    When I update the conversation with request:
    """
    {
      "metadata": {
        "b": "updated"
      }
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "metadata": {
        "a": "1",
        "b": "updated"
      }
    }
    """

  Scenario: PATCH metadata merge-patch — keys set to null are removed
    Given I create a conversation with request:
    """
    {
      "title": "Null Remove Test",
      "metadata": {
        "keep": "yes",
        "remove": "gone"
      }
    }
    """
    And the response status should be 201
    When I update the conversation with request:
    """
    {
      "metadata": {
        "remove": null
      }
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "metadata": {
        "keep": "yes"
      }
    }
    """
    And the response body should not contain "remove"

  Scenario: PATCH metadata and title in same request
    Given I have a conversation with title "Old Title"
    When I update the conversation with request:
    """
    {
      "title": "New Title",
      "metadata": {
        "status": "active"
      }
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "title": "New Title",
      "metadata": {
        "status": "active"
      }
    }
    """

  Scenario: PATCH archived, title, and metadata in same request applies all three
    Given I have a conversation with title "Triple Patch Test"
    When I update the conversation with request:
    """
    {
      "archived": true,
      "title": "Archived And Renamed",
      "metadata": { "status": "done" }
    }
    """
    Then the response status should be 200
    When I call GET "/v1/conversations?archived=only&mode=all"
    Then the response status should be 200
    And the response body should contain "${conversationId}"
    When I get the conversation
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "title": "Archived And Renamed",
      "metadata": { "status": "done" }
    }
    """

  Scenario Outline: PATCH metadata rejects non-object types with 400
    Given I have a conversation with title "Invalid Metadata Type"
    When I update the conversation with request:
    """
    {
      "metadata": <value>
    }
    """
    Then the response status should be 400

    Examples:
      | value |
      | []    |
      | "bad" |
      | 1     |
      | true  |

  Scenario: PATCH with malformed JSON involving metadata returns 400
    Given I have a conversation with title "Malformed Metadata JSON"
    When I call PATCH "/v1/conversations/${conversationId}" with body:
    """
    {"metadata": {"key": "value"
    """
    Then the response status should be 400

  Scenario: PATCH metadata null is a valid no-op
    Given I create a conversation with request:
    """
    {
      "title": "Null Metadata No-Op",
      "metadata": {"keep": "yes"}
    }
    """
    And the response status should be 201
    When I update the conversation with request:
    """
    {
      "metadata": null
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "metadata": {"keep": "yes"}
    }
    """

  Scenario: PATCH metadata empty object is a valid no-op
    Given I create a conversation with request:
    """
    {
      "title": "Empty Metadata No-Op",
      "metadata": {"keep": "yes"}
    }
    """
    And the response status should be 201
    When I update the conversation with request:
    """
    {
      "metadata": {}
    }
    """
    Then the response status should be 200
    And the response body should be json:
    """
    {
      "metadata": {"keep": "yes"}
    }
    """

  # ── B: metadata in ConversationSummary (GET /v1/conversations) ───────────

  Scenario: List conversations returns metadata in each summary
    Given I create a conversation with request:
    """
    {
      "title": "Summary Metadata Test",
      "metadata": {
        "status": "pending",
        "priority": "high"
      }
    }
    """
    And the response status should be 201
    When I list conversations
    Then the response status should be 200
    And the response body should contain "status"
    And the response body should contain "pending"

  Scenario: GET /v1/conversations summary includes metadata written by PATCH
    Given I have a conversation with title "Metadata List Check"
    And I update the conversation with request:
    """
    {
      "metadata": {
        "status": "done"
      }
    }
    """
    And the response status should be 200
    When I list conversations
    Then the response status should be 200
    And the response body should contain "done"

  # ── C: metadata[key]=value filter on GET /v1/conversations ───────────────

  Scenario: Filter conversations by metadata key-value — only matching returned
    Given I create a conversation with request:
    """
    {
      "title": "Waiting Conversation",
      "metadata": {"status": "waiting"}
    }
    """
    And the response status should be 201
    And set "waitingId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "Running Conversation",
      "metadata": {"status": "running"}
    }
    """
    And the response status should be 201
    And set "runningId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "No Status Conversation"
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations?mode=all&metadata[status]=waiting"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${waitingId}"

  Scenario: Filter conversations by metadata — no match returns empty list
    Given I create a conversation with request:
    """
    {
      "title": "Filter No Match",
      "metadata": {"status": "running"}
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations?mode=all&metadata[status]=nonexistent-value"
    Then the response status should be 200
    And the response should contain 0 conversations

  Scenario: Filter conversations by metadata — updated status is visible
    Given I have a conversation with title "Status Update Visible"
    And set "filteredId" to "${conversationId}"
    And I update the conversation with request:
    """
    {
      "metadata": {"status": "waiting"}
    }
    """
    And the response status should be 200
    When I call GET "/v1/conversations?mode=all&metadata[status]=waiting"
    Then the response status should be 200
    And the response body should contain "${filteredId}"

  Scenario: Filter with invalid metadata key characters returns 400
    When I call GET "/v1/conversations?metadata[bad key!]=val"
    Then the response status should be 400

  Scenario: Filter with multiple metadata keys returns 400
    When I call GET "/v1/conversations?metadata[status]=waiting&metadata[priority]=high"
    Then the response status should be 400

  Scenario: Filter with repeated metadata value returns 400
    When I call GET "/v1/conversations?metadata[status]=waiting&metadata[status]=running"
    Then the response status should be 400

  Scenario: Filter with repeated metadata expressions uses AND semantics
    Given I create a conversation with request:
    """
    {
      "title": "Waiting Worker 1",
      "metadata": {
        "status": "waiting",
        "agent-id": "worker-1"
      }
    }
    """
    And the response status should be 201
    And set "matchId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "Waiting Worker 2",
      "metadata": {
        "status": "waiting",
        "agent-id": "worker-2"
      }
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations?mode=all&metadata=status=waiting&metadata=agent-id!=worker-2"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${matchId}"

  Scenario: Filter with duplicate keys on metadata expressions uses AND
    Given I create a conversation with request:
    """
    {
      "title": "Status Running",
      "metadata": {
        "status": "running"
      }
    }
    """
    And the response status should be 201
    And set "runningId" to the json response field "id"
    When I call GET "/v1/conversations?mode=all&metadata=status=running&metadata=status!=waiting"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${runningId}"

  Scenario: Filter with six metadata expressions returns 400
    When I call GET "/v1/conversations?metadata=a=1&metadata=b=2&metadata=c=3&metadata=d=4&metadata=e=5&metadata=f=6"
    Then the response status should be 400

  Scenario: Mixing repeated metadata filter and legacy metadata[key] returns 400
    When I call GET "/v1/conversations?metadata=status=waiting&metadata[agent]=worker"
    Then the response status should be 400

  Scenario: Filter with bare metadata parameter without equals or operator returns 400
    When I call GET "/v1/conversations?metadata=status"
    Then the response status should be 400

  Scenario: Filter with empty metadata key returns 400
    When I call GET "/v1/conversations?metadata[]=value"
    Then the response status should be 400

  Scenario: Filter with metadata key but no supplied value returns 400
    When I call GET "/v1/conversations?metadata[status]"
    Then the response status should be 400

  Scenario: Filter with malformed metadata prefix returns 400
    When I call GET "/v1/conversations?metadata.foo=value"
    Then the response status should be 400

  Scenario: Filter with nested metadata key returns 400
    When I call GET "/v1/conversations?metadata[key][nested]=value"
    Then the response status should be 400

  Scenario: Filter with dotted metadata key returns 400
    When I call GET "/v1/conversations?metadata[key.nested]=value"
    Then the response status should be 400

  Scenario: Metadata filter matches string "1" but not numeric 1
    Given I create a conversation with request:
    """
    {
      "title": "String One",
      "metadata": {"count": "1"}
    }
    """
    And the response status should be 201
    And set "stringOneId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "Numeric One",
      "metadata": {"count": 1}
    }
    """
    And the response status should be 201
    And I create a conversation with request:
    """
    {
      "title": "Boolean True",
      "metadata": {"count": true}
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations?mode=all&metadata[count]=1"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${stringOneId}"

  Scenario: Metadata filter matches empty string value
    Given I create a conversation with request:
    """
    {
      "title": "Empty Status",
      "metadata": {"status": ""}
    }
    """
    And the response status should be 201
    And set "emptyStatusId" to the json response field "id"
    And I create a conversation with request:
    """
    {
      "title": "Null Status",
      "metadata": {"other": "value"}
    }
    """
    And the response status should be 201
    When I call GET "/v1/conversations?mode=all&metadata[status]="
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${emptyStatusId}"

  Scenario: Latest-fork mode returns latest matching conversation when older fork matches
    Given I create a conversation with request:
    """
    {
      "title": "Root Conversation",
      "metadata": {"status": "waiting"}
    }
    """
    And the response status should be 201
    And set "rootId" to the json response field "id"
    And I append an entry to the conversation:
    """
    {
      "contentType": "message",
      "content": [{"type": "text", "text": "hello"}]
    }
    """
    And the response status should be 201
    And set "forkPointId" to "${response.body.id}"
    When I fork the conversation at entry "${forkPointId}"
    And set "forkId" to "${forkedConversationId}"
    When I call PATCH "/v1/conversations/${forkId}" with body:
    """
    {"metadata": {"status": "running"}}
    """
    Then the response status should be 200
    When I call GET "/v1/conversations?mode=latest-fork&metadata[status]=waiting"
    Then the response status should be 200
    And the response should contain 1 conversation
    And the response body "data[0].id" should be "${rootId}"
