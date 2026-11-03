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
    And the response body field "kind" should be "default/v1"
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
    And the response body field "kind" should be "default/v1"
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

  Scenario: Put memory rejects negative TTL
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "prefs"],
      "key": "negative-ttl",
      "value": { "color": "dark" },
      "ttl_seconds": -1
    }
    """
    Then the response status should be 400
    And the response body should contain "ttl_seconds must be"

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
    And the response body field "items[0].kind" should be "default/v1"

  Scenario: Multi-query memory search validates request shape
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "notes"],
      "query": "release",
      "queries": [{ "text": "release" }],
      "limit": 10
    }
    """
    Then the response status should be 400
    And the response body should contain "query and queries are mutually exclusive"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "notes"],
      "queries": [{ "text": "  " }],
      "limit": 10
    }
    """
    Then the response status should be 400
    And the response body should contain "queries[0].text must not be empty"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "notes"],
      "queries": [{ "text": "release" }],
      "per_query_limit": 0,
      "limit": 10
    }
    """
    Then the response status should be 400
    And the response body should contain "per_query_limit must be between 1 and 100"

  Scenario: Memory search supports pushdownable attribute filter operators
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "filter-ops"],
      "key": "ops-alpha",
      "value": { "text": "alpha" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "filter-ops"],
      "key": "ops-beta",
      "value": { "text": "beta" }
    }
    """
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "sub": { "\u0024eq": "alice" } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-alpha"
    And the response body should contain "ops-beta"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "sub": { "\u0024eq": "bob" } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at most 0 items
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "sub": { "\u0024in": ["alice"] } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-alpha"
    And the response body should contain "ops-beta"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "filter-ops"],
      "filter": { "sub": { "\u0024exists": true } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ops-alpha"
    And the response body should contain "ops-beta"

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
    And I call GET "/admin/v1/memory-usage?ns=user&ns=alice&ns=usage&key=k2"
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
    And I call GET "/admin/v1/memory-usage/top?prefix=user&prefix=alice&prefix=usage-top&sort=fetch_count&limit=1"
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

  Scenario: User search cannot return another user's memories
    Given I am authenticated as user "bob"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "prefs"],
      "key": "private-note",
      "value": { "text": "Bob private note" }
    }
    """
    And I am authenticated as user "erin"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "bob", "prefs"],
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at most 0 items

  Scenario: Admin role does not broaden public memory search
    Given I am authenticated as user "bob"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "admin-public-search"],
      "key": "bob-private-note",
      "value": { "text": "Bob private note" }
    }
    """
    And the response status should be 200
    And I am authenticated as admin user "alice"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "bob", "admin-public-search"],
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at most 0 items

  Scenario: Admin index status endpoint returns pending count
    When I am authenticated as admin user "alice"
    And I call GET "/admin/v1/memory-index/status"
    Then the response status should be 200
    And the response body field "pending" should not be null

  Scenario: Admin can trigger one memory index cycle
    When I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-index/trigger" with body:
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

  Scenario: Auditor can list and get memories across users
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "admin-explore"],
      "key": "alice-theme",
      "value": { "color": "dark" }
    }
    """
    And the response status should be 200
    And set "adminMemoryId" to the json response field "id"
    And I am authenticated as user "bob"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "admin-explore"],
      "key": "bob-timezone",
      "value": { "zone": "UTC" }
    }
    """
    And the response status should be 200
    When I am authenticated as auditor user "charlie"
    And I call GET "/admin/v1/memories?namespacePrefix=user&namespacePrefix=alice&namespacePrefix=admin-explore&limit=10"
    Then the response status should be 200
    And the response body should contain "alice-theme"
    When I call GET "/admin/v1/memories/${adminMemoryId}"
    Then the response status should be 200
    And the response body field "value.color" should be "dark"
    When I call GET "/admin/v1/memories?namespacePrefix=user&namespacePrefix=bob&namespacePrefix=admin-explore&limit=10"
    Then the response status should be 200
    And the response body should contain "bob-timezone"

  Scenario: Auditor can search as a target user but cannot delete memories
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "admin-as-user"],
      "key": "alice-note",
      "value": { "kind": "note", "owner": "alice" }
    }
    """
    And the response status should be 200
    And set "auditorDeleteMemoryId" to the json response field "id"
    And I am authenticated as user "bob"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "admin-as-user"],
      "key": "bob-note",
      "value": { "kind": "note", "owner": "bob" }
    }
    """
    And the response status should be 200
    When I am authenticated as auditor user "charlie"
    And I call POST "/admin/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user"],
      "as_user_id": "bob",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "bob-note"
    And the response body should not contain "alice-note"
    When I call DELETE "/admin/v1/memories/${auditorDeleteMemoryId}"
    Then the response status should be 403

  Scenario: PUT with revision_id and revision: 1 creates the revision idempotently (create-only mode)
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-create"],
      "key": "idem-k1",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000001",
      "revision": 1
    }
    """
    Then the response status should be 201
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-000000000001"
    And set "firstCreatedAt" to the json response field "createdAt"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-create"],
      "key": "idem-k1",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000001",
      "revision": 1
    }
    """
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-000000000001"
    And the response body "createdAt" should be "${firstCreatedAt}"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'idem-k1'
    """
    Then the SQL result should match:
      | count |
      | 1     |

    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "idem-k1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Create-only mode on an existing active memory returns revision_already_exists
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-create-conflict"],
      "key": "existing-key",
      "value": {"initial": true}
    }
    """
    Then the response status should be 200
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-create-conflict"],
      "key": "existing-key",
      "value": {"attempt": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000002",
      "revision": 1
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "revision_already_exists"
    And the response body field "error" should be "an active memory already exists for the namespace and key"
    When I call GET "/v1/memories?ns=user&ns=alice&ns=idem-create-conflict&key=existing-key"
    Then the response status should be 200
    And the response body field "value.initial" should be "true"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'existing-key'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "existing-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-000000000002'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "6f1c1d0a-0000-4000-8000-000000000002"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |


  Scenario: Create-only mode rejects invalid revision_id before create-only check
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-invalid"],
      "key": "k-invalid-uuid",
      "value": {"v": 1},
      "revision_id": "not-a-valid-uuid",
      "revision": 1
    }
    """
    Then the response status should be 400

  Scenario: PutMemory rejects revision value other than 1
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-invalid"],
      "key": "k-invalid-rev",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000003",
      "revision": 2
    }
    """
    Then the response status should be 400

  Scenario: PUT with matching expected_revision_id updates memory idempotently
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-cas"],
      "key": "cas-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000010"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-cas"],
      "key": "cas-key",
      "value": {"v": 2},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000011",
      "expected_revision_id": "6f1c1d0a-0000-4000-8000-000000000010"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-000000000011"
    And set "updateCreatedAt" to the json response field "createdAt"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-cas"],
      "key": "cas-key",
      "value": {"v": 2},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000011",
      "expected_revision_id": "6f1c1d0a-0000-4000-8000-000000000010"
    }
    """
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-000000000011"
    And the response body "createdAt" should be "${updateCreatedAt}"

  Scenario: PUT with mismatching expected_revision_id returns expected_active_revision_id_mismatch
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-cas-mismatch"],
      "key": "mismatch-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000020"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-cas-mismatch"],
      "key": "mismatch-key",
      "value": {"v": 2},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000021",
      "expected_revision_id": "6f1c1d0a-0000-4000-8000-000000000099"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "expected_active_revision_id_mismatch"
    And the response body field "error" should be "active revision ID does not match expected_revision_id"

    When I call GET "/v1/memories?ns=user&ns=alice&ns=idem-cas-mismatch&key=mismatch-key"
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-000000000020"
    And the response body field "value.v" should be "1"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'mismatch-key'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "mismatch-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-000000000021'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "6f1c1d0a-0000-4000-8000-000000000021"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |


  Scenario: PUT with expected_revision_id when no active memory exists returns expected_active_revision_id_mismatch
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-cas-nonexistent"],
      "key": "no-prior-row",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000030",
      "expected_revision_id": "6f1c1d0a-0000-4000-8000-000000000031"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "expected_active_revision_id_mismatch"

  Scenario: PUT with expected_revision_id without revision_id returns 400
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-invalid"],
      "key": "no-rev-id",
      "value": {"v": 1},
      "expected_revision_id": "6f1c1d0a-0000-4000-8000-000000000040"
    }
    """
    Then the response status should be 400

  Scenario: PUT with expected_revision_id and expected_revision numeric mismatch returns numeric conflict
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-cas-both"],
      "key": "both-cas",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000050"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-cas-both"],
      "key": "both-cas",
      "value": {"v": 2},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000051",
      "expected_revision_id": "6f1c1d0a-0000-4000-8000-000000000050",
      "expected_revision": 999
    }
    """
    Then the response status should be 409
    And the response body field "error" should be "memory revision conflict"
    And the response body should not contain "expected_active_revision_id_mismatch"

  Scenario: PUT with caller-supplied revision_id returns 201 and retry returns 200
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-retry"],
      "key": "retry-key",
      "value": {"field": "hello"},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000060"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-000000000060"
    And set "retryFirstCreatedAt" to the json response field "createdAt"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-retry"],
      "key": "retry-key",
      "value": {"field": "hello"},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000060"
    }
    """
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-000000000060"
    And the response body "createdAt" should be "${retryFirstCreatedAt}"

  Scenario: Retry with same revision_id but different value returns memory_revision_already_exists
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-conflict"],
      "key": "conflict-val",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000070"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-conflict"],
      "key": "conflict-val",
      "value": {"v": 2},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000070"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "memory_revision_already_exists"
    And the response body field "details.revisionId" should be "6f1c1d0a-0000-4000-8000-000000000070"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'conflict-val'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "conflict-val"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: Retry with same revision_id and TTL succeeds; different TTL returns conflict
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-ttl"],
      "key": "ttl-key",
      "value": {"v": 1},
      "ttl_seconds": 3600,
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000080"
    }
    """
    Then the response status should be 201
    And set "firstExpiresAt" to the json response field "expiresAt"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-ttl"],
      "key": "ttl-key",
      "value": {"v": 1},
      "ttl_seconds": 3600,
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000080"
    }
    """
    Then the response status should be 200
    And the response body "expiresAt" should be "${firstExpiresAt}"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-ttl"],
      "key": "ttl-key",
      "value": {"v": 1},
      "ttl_seconds": 7200,
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000080"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "memory_revision_already_exists"

  Scenario: Cross-namespace revision_id collision returns generic conflict without disclosing data
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "secret-ns"],
      "key": "secret-key",
      "value": {"top_secret": "classified"},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000090"
    }
    """
    Then the response status should be 201

    When I am authenticated as user "bob"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "public-ns"],
      "key": "other-key",
      "value": {"data": 123},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000090"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "memory_revision_already_exists"
    And the response body field "details.revisionId" should be "6f1c1d0a-0000-4000-8000-000000000090"
    And the response body should not contain "secret-key"
    And the response body should not contain "classified"

    When I am authenticated as user "alice"
    And I call GET "/v1/memories?ns=user&ns=alice&ns=secret-ns&key=secret-key"
    Then the response status should be 200
    And the response body field "value.top_secret" should be "classified"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-000000000090'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "6f1c1d0a-0000-4000-8000-000000000090"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'other-key'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "other-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |


  Scenario: Different key within same namespace under same revision_id returns conflict
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "same-ns"],
      "key": "key-alpha",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000a0"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "same-ns"],
      "key": "key-beta",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000a0"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "memory_revision_already_exists"

  Scenario: Retry after another revision with the same key was written returns 200
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "superseded-idem"],
      "key": "same-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000b1"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "superseded-idem"],
      "key": "same-key",
      "value": {"v": 2},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000b2"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "superseded-idem"],
      "key": "same-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000b1"
    }
    """
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000b1"

  Scenario: Retry after memory was archived via PATCH returns 404
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "archive-idem"],
      "key": "arch-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000c0"
    }
    """
    Then the response status should be 201

    When I call PATCH "/v1/memories?ns=user&ns=alice&ns=archive-idem&key=arch-key" with body:
    """
    {
      "archived": true
    }
    """
    Then the response status should be 204

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "archive-idem"],
      "key": "arch-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000c0"
    }
    """
    Then the response status should be 404
    And the response body field "code" should be "not_found"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'arch-key' AND archived_at IS NOT NULL AND deleted_reason = 1
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "arch-key", "archived_at": {"$exists": true}, "deleted_reason": 1}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'arch-key'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "arch-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: Retry carrying expected_revision while row exists returns 200
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-exp-rev"],
      "key": "exp-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000d0"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-exp-rev"],
      "key": "exp-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000d0",
      "expected_revision": 999
    }
    """
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000d0"

  Scenario: Index nil and empty map match exactly on retry
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-index"],
      "key": "idx-key",
      "value": {"v": 1},
      "index": {},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000e0"
    }
    """
    Then the response status should be 201

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-index"],
      "key": "idx-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000e0"
    }
    """
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000e0"

  Scenario: Omitting revision_id generates a distinct ID each call
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-omit"],
      "key": "omit-key",
      "value": {"v": 1}
    }
    """
    Then the response status should be 200
    And the response body field "id" should not be null
    And set "firstGenId" to the json response field "id"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-omit"],
      "key": "omit-key",
      "value": {"v": 1}
    }
    """
    Then the response status should be 200
    And the response body field "id" should not be null

  Scenario: Side effects are suppressed on exact retry
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-side-effects"],
      "key": "side-eff-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000f0"
    }
    """
    Then the response status should be 201

    # Mark the row indexed so a re-triggered indexer would show up as indexed_at being cleared.
    # SQL backends set it directly: not every runner has a vector store, and without one the
    # indexer cannot converge (PostgreSQL's broad runner has no memory_vectors table). MongoDB
    # skips the SQL step and relies on the indexer cycle below.
    When I execute SQL query:
    """
    UPDATE memories SET indexed_at = CURRENT_TIMESTAMP WHERE id = '6f1c1d0a-0000-4000-8000-0000000000f0'
    """
    And I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-index/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-0000000000f0' AND indexed_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "6f1c1d0a-0000-4000-8000-0000000000f0", "indexed_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

    When I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-side-effects"],
      "key": "side-eff-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000f0"
    }
    """
    Then the response status should be 200

    # Exactly one add event and no update event: the retry emitted nothing.
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=idem-side-effects&kinds=add"
    Then the response status should be 200
    And the response body "events" should have 1 item
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=idem-side-effects&kinds=update"
    Then the response status should be 200
    And the response body "events" should have 0 items

    # No extra row and no archive.
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'side-eff-key'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'side-eff-key' AND archived_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 0     |
    # indexed_at survived the retry, so the indexer was not re-triggered.
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-0000000000f0' AND indexed_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "side-eff-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "side-eff-key", "archived_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "6f1c1d0a-0000-4000-8000-0000000000f0", "indexed_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Empty revision_id returns 400
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-invalid"],
      "key": "k-empty-rev-id",
      "value": {"v": 1},
      "revision_id": ""
    }
    """
    Then the response status should be 400
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'k-empty-rev-id'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "k-empty-rev-id"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Retry by a caller without write access to the namespace returns 403 before any lookup
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-authz"],
      "key": "authz-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000100"
    }
    """
    Then the response status should be 201

    When I am authenticated as user "bob"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-authz"],
      "key": "authz-key",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-000000000100"
    }
    """
    Then the response status should be 403
    And the response body field "reason" should be "access denied"
    And the response body should not contain "memory_revision_already_exists"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'authz-key'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "authz-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Non-UUID expected_revision_id returns 400 and writes nothing
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-invalid"],
      "key": "k-bad-expected-id",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000002b0",
      "expected_revision_id": "not-a-valid-uuid"
    }
    """
    Then the response status should be 400
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'k-bad-expected-id'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "k-bad-expected-id"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Evicted row retry with create-only mode returns revision_already_exists when active row exists
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "evict-idem"],
      "key": "evict-k1",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000e1",
      "revision": 1
    }
    """
    Then the response status should be 201
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000e1"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "evict-idem"],
      "key": "evict-k1",
      "value": {"v": 2},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000e2"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000e2"

    When I execute SQL query:
    """
    DELETE FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-0000000000e1'
    """
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "delete", "filter": {"_id": "6f1c1d0a-0000-4000-8000-0000000000e1"}}
    """

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "evict-idem"],
      "key": "evict-k1",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000e1",
      "revision": 1
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "revision_already_exists"
    And the response body field "error" should be "an active memory already exists for the namespace and key"

    When I call GET "/v1/memories?ns=user&ns=alice&ns=evict-idem&key=evict-k1"
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000e2"
    And the response body field "value.v" should be "2"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-0000000000e1'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "6f1c1d0a-0000-4000-8000-0000000000e1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: PUT with revision_id after HardDeleteEvictableUpdates is treated as a new write
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "hard-delete-idem"],
      "key": "hd-k1",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000f1"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000f1"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "hard-delete-idem"],
      "key": "hd-k1",
      "value": {"v": 2},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000f2"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000f2"

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "hard-delete-idem"],
      "key": "hd-k1",
      "value": {"v": 1},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000f1"
    }
    """
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000f1"

    When I execute SQL query:
    """
    DELETE FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-0000000000f1'
    """
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "delete", "filter": {"_id": "6f1c1d0a-0000-4000-8000-0000000000f1"}}
    """

    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "hard-delete-idem"],
      "key": "hd-k1",
      "value": {"v": 3},
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000000f1"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000f1"

    When I call GET "/v1/memories?ns=user&ns=alice&ns=hard-delete-idem&key=hd-k1"
    Then the response status should be 200
    And the response body "id" should be "6f1c1d0a-0000-4000-8000-0000000000f1"
    And the response body field "value.v" should be "3"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'hd-k1' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "hd-k1", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '6f1c1d0a-0000-4000-8000-0000000000f2' AND archived_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "6f1c1d0a-0000-4000-8000-0000000000f2", "archived_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
