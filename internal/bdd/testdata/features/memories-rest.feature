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

  Scenario: Archive a memory keeps it readable with archive filters
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "tmp"],
      "key": "to-delete",
      "value": { "x": 1 }
    }
    """
    When I call PATCH "/v1/memories?ns=user&ns=alice&ns=tmp&key=to-delete" with body:
    """
    {
      "archived": true
    }
    """
    Then the response status should be 204
    When I call GET "/v1/memories?ns=user&ns=alice&ns=tmp&key=to-delete"
    Then the response status should be 404
    When I call GET "/v1/memories?ns=user&ns=alice&ns=tmp&key=to-delete&archived=include"
    Then the response status should be 200
    And the response body field "archived" should be "true"
    When I call GET "/v1/memories?ns=user&ns=alice&ns=tmp&key=to-delete&archived=only"
    Then the response status should be 200
    And the response body field "archived" should be "true"

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

  Scenario: Memory search supports pushdownable attribute filter operators
    Given I am authenticated as admin user "alice"
    And I call PUT "/admin/v1/memories/policies" with body:
    """
    {
      "authz": "package memories.authz\nimport future.keywords.if\ndefault decision = {\"allow\": false, \"reason\": \"access denied\"}\ndecision = {\"allow\": true} if { input.namespace[0] == \"user\"; input.namespace[1] == input.context.user_id }",
      "attributes": "package memories.attributes\nimport future.keywords.if\ndefault attributes = {}\nbase := {\"namespace\": input.namespace[0], \"sub\": input.namespace[1]}\nextra := {k: v | k := [\"topic\", \"confidence\", \"createdAt\", \"sourceHash\", \"tags\"][_]; v := input.value[k]; v != null}\nattributes = object.union(base, extra) if { count(input.namespace) >= 2 }",
      "filter": "package memories.filter\nimport future.keywords.if\nimport future.keywords.in\nuser_prefix := [\"user\", input.context.user_id]\nis_admin if { \"admin\" in input.context.jwt_claims.roles }\nnamespace_prefix := input.namespace_prefix if { is_admin }\nnamespace_prefix := user_prefix if { not is_admin }\nattribute_filter := {} if { is_admin }\nattribute_filter := {\"namespace\": \"user\", \"sub\": input.context.user_id} if { not is_admin }"
    }
    """
    Then the response status should be 204
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "filter-ops"],
      "key": "ops-alpha",
      "value": {
        "text": "alpha",
        "topic": "python",
        "confidence": 0.9,
        "createdAt": "2026-01-02T03:04:05Z",
        "sourceHash": "sha-alpha",
        "tags": ["language", "backend"]
      }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "filter-ops"],
      "key": "ops-beta",
      "value": {
        "text": "beta",
        "topic": "go",
        "confidence": 0.4,
        "createdAt": "2025-01-02T03:04:05Z",
        "sourceHash": "sha-beta",
        "tags": ["systems"]
      }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "filter-ops"],
      "key": "ops-gamma",
      "value": {
        "text": "gamma",
        "topic": "python",
        "confidence": 0.2,
        "createdAt": "2024-01-02T03:04:05Z",
        "tags": ["language"]
      }
    }
    """
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "topic": { "\u0024eq": "go" } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-beta"
    And the response body should not contain "ops-alpha"
    And the response body should not contain "ops-gamma"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "topic": { "\u0024in": ["go", "rust"] } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-beta"
    And the response body should not contain "ops-alpha"
    And the response body should not contain "ops-gamma"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "sourceHash": { "\u0024exists": true } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-alpha"
    And the response body should contain "ops-beta"
    And the response body should not contain "ops-gamma"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "confidence": { "\u0024gte": 0.8 } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-alpha"
    And the response body should not contain "ops-beta"
    And the response body should not contain "ops-gamma"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "createdAt": { "\u0024lte": "2024-06-01T00:00:00Z" } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-gamma"
    And the response body should not contain "ops-alpha"
    And the response body should not contain "ops-beta"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "tags": ["backend"] },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-alpha"
    And the response body should not contain "ops-beta"
    And the response body should not contain "ops-gamma"

  Scenario: Memory search rejects obsolete paging and ordering fields
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "offset": 1
    }
    """
    Then the response status should be 400
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "after_cursor": "cursor-1"
    }
    """
    Then the response status should be 400
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "order": "createdAtAsc"
    }
    """
    Then the response status should be 400

  Scenario: Memory search rejects unsupported filter operators
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "filter": {
        "topic": {"\u0024unknown": "python"}
      }
    }
    """
    Then the response status should be 400

  Scenario: Memory search and namespace listing honor archive filters
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "archive-search"],
      "key": "active-note",
      "value": { "text": "still active" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "archive-search"],
      "key": "archived-note",
      "value": { "text": "only archived" }
    }
    """
    And I call PATCH "/v1/memories?ns=user&ns=alice&ns=archive-search&key=archived-note" with body:
    """
    {
      "archived": true
    }
    """
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "archive-search"],
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 1 items
    And the response body should contain "active-note"
    And the response body should not contain "archived-note"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "archive-search"],
      "archived": "include",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "active-note"
    And the response body should contain "archived-note"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "archive-search"],
      "archived": "only",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "archived-note"
    And the response body should not contain "active-note"
    When I call GET "/v1/memories/namespaces?prefix=user&prefix=alice&prefix=archive-search"
    Then the response status should be 200
    And the response body "namespaces" should have at least 1 items
    When I call GET "/v1/memories/namespaces?prefix=user&prefix=alice&prefix=archive-search&archived=only"
    Then the response status should be 200
    And the response body "namespaces" should have at least 1 items

  Scenario: Get memory with include_usage returns usage counters
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "usage"],
      "key": "k1",
      "value": { "text": "tracked" }
    }
    """
    When I call GET "/v1/memories?ns=user&ns=alice&ns=usage&key=k1&include_usage=true"
    Then the response status should be 200
    And the response body field "usage.fetchCount" should be "1"
    And the response body field "usage.lastFetchedAt" should not be null

  Scenario: Search include_usage does not increment fetch counters
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "usage"],
      "key": "k2",
      "value": { "text": "search-test" }
    }
    """
    And I call GET "/v1/memories?ns=user&ns=alice&ns=usage&key=k2&include_usage=true"
    And the response status should be 200
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "usage"],
      "include_usage": true,
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body field "items.0.usage.fetchCount" should be "1"
    When I am authenticated as admin user "alice"
    And I call GET "/admin/v1/memories/usage?ns=user&ns=alice&ns=usage&key=k2"
    Then the response status should be 200
    And the response body field "fetchCount" should be "1"

  Scenario: Admin usage top endpoint ranks by fetch_count
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "usage-top"],
      "key": "k-top",
      "value": { "text": "top" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "usage-top"],
      "key": "k-low",
      "value": { "text": "low" }
    }
    """
    And I call GET "/v1/memories?ns=user&ns=alice&ns=usage-top&key=k-top"
    And the response status should be 200
    And I call GET "/v1/memories?ns=user&ns=alice&ns=usage-top&key=k-top"
    And the response status should be 200
    And I call GET "/v1/memories?ns=user&ns=alice&ns=usage-top&key=k-low"
    And the response status should be 200
    When I am authenticated as admin user "alice"
    And I call GET "/admin/v1/memories/usage/top?prefix=user&prefix=alice&prefix=usage-top&sort=fetch_count&limit=1"
    Then the response status should be 200
    And the response body field "items.0.key" should be "k-top"

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
    And the response body field "reason" should be "access denied"

  Scenario: User cannot read another user's memory
    When I call GET "/v1/memories?ns=user&ns=bob&key=secret"
    Then the response status should be 403
    And the response body field "reason" should be "access denied"

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

  Scenario: Event timeline records archive as update
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "events-del"],
      "key": "del-key",
      "value": { "x": 1 }
    }
    """
    And the response status should be 200
    When I call PATCH "/v1/memories?ns=user&ns=alice&ns=events-del&key=del-key" with body:
    """
    {
      "archived": true
    }
    """
    Then the response status should be 204
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=events-del&kinds=add&kinds=update"
    Then the response status should be 200
    And the response body "events" should have at least 2 items
    And the response body should contain json:
    """
    {
      "events": [
        { "key": "del-key", "kind": "add"    },
        { "key": "del-key", "kind": "update" }
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
      "authz": "package memories.authz\nimport future.keywords.if\ndefault decision = {\"allow\": false, \"reason\": \"access denied\"}\ndecision = {\"allow\": true} if { input.namespace[0] == \"user\"; input.namespace[1] == input.context.user_id }",
      "attributes": "package memories.attributes\nimport future.keywords.if\ndefault attributes = {}\nattributes = {\"namespace\": input.namespace[0], \"sub\": input.namespace[1]} if { count(input.namespace) >= 2 }",
      "filter": "package memories.filter\nimport future.keywords.if\nimport future.keywords.in\nuser_prefix := [\"user\", input.context.user_id]\nis_admin if { \"admin\" in input.context.jwt_claims.roles }\nnamespace_prefix := input.namespace_prefix if { is_admin }\nnamespace_prefix := user_prefix if { not is_admin }\nattribute_filter := {} if { is_admin }\nattribute_filter := {\"namespace\": \"user\", \"sub\": input.context.user_id} if { not is_admin }"
    }
    """
    Then the response status should be 204
