Feature: Episodic Memory REST API
  As an agent or user
  I want to store and retrieve named memories via the REST API
  So that agents can persist state across sessions

  Background:
    Given I am authenticated as user "alice"

  Scenario: Store and retrieve a memory
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "prefs"],
      "key": "theme",
      "value": { "color": "dark" }
    }
    """
    Then the response status should be 200
    And the response body should contain json:
    """
    {
      "namespace": ["user", "alice", "prefs"],
      "key": "theme"
    }
    """
    And set "memoryId" to the json response field "id"
    When I call GET "/v1/memories?ns=user&ns=alice&ns=prefs&key=theme"
    Then the response status should be 200
    And the response body should contain json:
    """
    {
      "namespace": ["user", "alice", "prefs"],
      "key": "theme",
      "value": { "color": "dark" }
    }
    """

  Scenario: Overwrite a memory replaces the value
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "prefs"],
      "key": "lang",
      "value": { "locale": "en" }
    }
    """
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "prefs"],
      "key": "lang",
      "value": { "locale": "fr" }
    }
    """
    Then the response status should be 200
    When I call GET "/v1/memories?ns=user&ns=alice&ns=prefs&key=lang"
    Then the response status should be 200
    And the response body should contain json:
    """
    {
      "value": { "locale": "fr" }
    }
    """

  Scenario: Get a non-existent memory returns 404
    When I call GET "/v1/memories?ns=user&ns=alice&key=does-not-exist"
    Then the response status should be 404

  Scenario: Delete a memory makes it unavailable
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "tmp"],
      "key": "to-delete",
      "value": { "x": 1 }
    }
    """
    When I call DELETE "/v1/memories?ns=user&ns=alice&ns=tmp&key=to-delete"
    Then the response status should be 204
    When I call GET "/v1/memories?ns=user&ns=alice&ns=tmp&key=to-delete"
    Then the response status should be 404

  Scenario: Search memories within namespace prefix
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "notes"],
      "key": "note-1",
      "value": { "text": "first note" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "notes"],
      "key": "note-2",
      "value": { "text": "second note" }
    }
    """
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "notes"],
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 2 items

  Scenario: List namespaces under a prefix
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "ns-test", "a"],
      "key": "k1",
      "value": { "v": 1 }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "ns-test", "b"],
      "key": "k2",
      "value": { "v": 2 }
    }
    """
    When I call GET "/v1/memories/namespaces?prefix=user&prefix=alice&prefix=ns-test"
    Then the response status should be 200
    And the response body "namespaces" should have at least 2 items

  Scenario: User cannot access another user's namespace
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "prefs"],
      "key": "theme",
      "value": { "color": "light" }
    }
    """
    Then the response status should be 403

  Scenario: User cannot read another user's memory
    When I call GET "/v1/memories?ns=user&ns=bob&key=secret"
    Then the response status should be 403

  Scenario: Admin index status endpoint returns pending count
    When I am authenticated as admin user "alice"
    And I call GET "/admin/v1/memories/index/status"
    Then the response status should be 200
    And the response body field "pending" should not be null

  Scenario: Admin can trigger one memory index cycle
    When I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memories/index/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    And the response body field "triggered" should be "true"
    And the response body field "stats.pending" should not be null

  Scenario: Event timeline records add and update events
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "events-test"],
      "key": "evt-key",
      "value": { "v": 1 }
    }
    """
    Then the response status should be 200
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "events-test"],
      "key": "evt-key",
      "value": { "v": 2 }
    }
    """
    Then the response status should be 200
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=events-test"
    Then the response status should be 200
    And the response body "events" should have at least 2 items
    And the response body should contain json:
    """
    {
      "events": [
        { "key": "evt-key", "kind": "add",    "value": { "v": 1 } },
        { "key": "evt-key", "kind": "update",  "value": { "v": 2 } }
      ]
    }
    """

  Scenario: Event timeline records delete event
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "events-del"],
      "key": "del-key",
      "value": { "x": 1 }
    }
    """
    And the response status should be 200
    When I call DELETE "/v1/memories?ns=user&ns=alice&ns=events-del&key=del-key"
    Then the response status should be 204
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=events-del&kinds=add&kinds=delete"
    Then the response status should be 200
    And the response body "events" should have at least 2 items
    And the response body should contain json:
    """
    {
      "events": [
        { "key": "del-key", "kind": "add"    },
        { "key": "del-key", "kind": "delete" }
      ]
    }
    """

  Scenario: Event timeline kind filter returns only requested kinds
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "events-filter"],
      "key": "filter-key",
      "value": { "z": 1 }
    }
    """
    And the response status should be 200
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=events-filter&kinds=update"
    Then the response status should be 200
    And the response body should contain json:
    """
    { "events": [] }
    """
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=events-filter&kinds=add"
    Then the response status should be 200
    And the response body "events" should have at least 1 items

  Scenario: Event timeline returns 200 with empty list for unknown namespace
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=does-not-exist"
    Then the response status should be 200
    And the response body should contain json:
    """
    { "events": [] }
    """

  Scenario: Admin can download and upload episodic policy bundle
    When I am authenticated as admin user "alice"
    And I call GET "/admin/v1/memories/policies"
    Then the response status should be 200
    And the response body field "authz" should not be null
    And the response body field "attributes" should not be null
    And the response body field "filter" should not be null
    When I call PUT "/admin/v1/memories/policies" with body:
    """
    {
      "authz": "package memories.authz\nimport future.keywords.if\ndefault allow = false\nallow if { input.namespace[0] == \"user\"; input.namespace[1] == input.context.user_id }",
      "attributes": "package memories.attributes\nimport future.keywords.if\ndefault attributes = {}\nattributes = {\"namespace\": input.namespace[0], \"sub\": input.namespace[1]} if { count(input.namespace) >= 2 }",
      "filter": "package memories.filter\nimport future.keywords.if\nimport future.keywords.in\nuser_prefix := [\"user\", input.context.user_id]\nis_admin if { \"admin\" in input.context.jwt_claims.roles }\nnamespace_prefix := input.namespace_prefix if { is_admin }\nnamespace_prefix := user_prefix if { not is_admin }\nattribute_filter := {} if { is_admin }\nattribute_filter := {\"namespace\": \"user\", \"sub\": input.context.user_id} if { not is_admin }"
    }
    """
    Then the response status should be 204
