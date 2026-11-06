Feature: Memory Kind Versions and Migrations
  As an administrator
  I want to create immutable kind versions and migrate memories
  So that attribute projections are versioned and replayable

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Create and retrieve an immutable kind version
    When I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "profile/v1",
      "attributes": {
        "kind": "string"
      },
      "projectionRego": "package memories.attributes\nattributes := {\"kind\": \"profile\"}"
    }
    """
    Then the response status should be 200
    And the response body field "name" should be "profile/v1"
    And the response body field "writable" should not be null
    When I call GET "/admin/v1/memory-kinds/profile/v1"
    Then the response status should be 200
    And the response body field "name" should be "profile/v1"

  Scenario: Duplicate kind version creation is idempotent
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "idempotent/v1",
      "attributes": {"tag": "string"},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    When I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "idempotent/v1",
      "attributes": {"tag": "string"},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    Then the response status should be 200
    And the response body field "name" should be "idempotent/v1"

  Scenario: Conflicting kind version returns 409
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "conflict/v1",
      "attributes": {"tag": "string"},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I add the "X-Request-ID" header value "kind-conflict-request"
    When I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "conflict/v1",
      "attributes": {"score": "number"},
      "projectionRego": "package memories.attributes\nattributes := {\"score\": 1}"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "conflict"
    And the response body field "error" should not be null
    And the response body field "requestId" should be "kind-conflict-request"
    And the response header "X-Request-ID" should contain "kind-conflict-request"

  Scenario: Kind lifecycle validation errors use the structured error envelope
    Given I add the "X-Request-ID" header value "kind-validation-request"
    When I call POST "/admin/v1/memory-kinds" with body:
    """
    {"name":"INVALID","attributes":{}}
    """
    Then the response status should be 400
    And the response body field "code" should be "invalid_request"
    And the response body field "error" should not be null
    And the response body field "requestId" should be "kind-validation-request"

  Scenario: Missing kind lifecycle resources use the structured error envelope
    Given I add the "X-Request-ID" header value "kind-not-found-request"
    When I call GET "/admin/v1/memory-kinds/missing/v1"
    Then the response status should be 404
    And the response body field "code" should be "not_found"
    And the response body field "error" should not be null
    And the response body field "requestId" should be "kind-not-found-request"

  @memory-kind-regression
  Scenario: Changing only writable conflicts with an immutable kind version
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "writableconflict/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}",
      "writable": false
    }
    """
    And the response status should be 200
    When I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "writableconflict/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}",
      "writable": true
    }
    """
    Then the response status should be 409

  @memory-kind-regression
  Scenario: List kind versions omits Rego while get includes it
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "listing/v1",
      "attributes": {"label": "string"},
      "projectionRego": "package memories.attributes\n# listing-secret-marker\nattributes := {}"
    }
    """
    When I call GET "/admin/v1/memory-kinds?family=listing"
    Then the response status should be 200
    And the response body field "items[0].name" should be "listing/v1"
    And the response body should not contain "listing-secret-marker"
    When I call GET "/admin/v1/memory-kinds/listing/v1"
    Then the response status should be 200
    And the response body should contain "listing-secret-marker"

  @memory-kind-regression
  Scenario: Family-only write kind is rejected
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "myfamily/v1",
      "attributes": {"family": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"family\": \"myfamily-v1\"}"
    }
    """
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "family-default"],
      "key": "resolved",
      "value": {"note": "hello"},
      "kind": "myfamily"
    }
    """
    Then the response status should be 400

  Scenario: Write memory with explicit schema stores schema reference
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "tagged/v1",
      "attributes": {"tag": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"tag\": \"memory\"}"
    }
    """
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "schema-test"],
      "key": "tagged-mem",
      "value": {"note": "hello"},
      "kind": "tagged/v1"
    }
    """
    Then the response status should be 200
    And the response body field "kind" should be "tagged/v1"

  @memory-kind-regression
  Scenario: Complete migration between kind versions
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "migtest/v1",
      "attributes": {"old": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"old\": \"source\"}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "migtest/v2",
      "attributes": {"new": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"new\": \"target\"}"
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "migration-e2e"],
      "key": "migrated-memory",
      "value": {"payload": "still-readable"},
      "kind": "migtest/v1"
    }
    """
    And the response status should be 200
    And set "sourceMemoryId" to the json response field "id"
    And the response body field "kind" should be "migtest/v1"
    And the response body field "attributes.old" should be "source"
    When I call POST "/admin/v1/memory-kind-migrations" with body:
    """
    {
      "source": "migtest/v1",
      "target": "migtest/v2",
      "namespace_prefix": ["user", "alice", "migration-e2e"]
    }
    """
    Then the response status should be 200
    And the response body field "source" should be "migtest/v1"
    And the response body field "target" should be "migtest/v2"
    And the response body field "state" should be "queued"
    And set "migrationId" to the json response field "id"
    When the memory kind migration processor runs
    And I call GET "/admin/v1/memory-kind-migrations/${migrationId}"
    Then the response status should be 200
    And the response body field "source" should be "migtest/v1"
    And the response body field "state" should be "succeeded"
    And the response body field "migrated_count" should be "1"
    And the response body field "skipped_tombstone_count" should be "0"
    When I call GET "/v1/memories?ns=user&ns=alice&ns=migration-e2e&key=migrated-memory"
    Then the response status should be 200
    And the response body field "kind" should be "migtest/v2"
    And the response body field "attributes.new" should be "target"
    And the response body field "attributes.old" should be null
    And the response body field "value.payload" should be "still-readable"
    When I call GET "/admin/v1/memories/${sourceMemoryId}"
    Then the response status should be 200
    And the response body field "revision" should be "2"

  Scenario: Duplicate active migration for same source returns 409
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "dupemig/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "dupemig/v2",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "dupemig/v3",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kind-migrations" with body:
    """
    {"source": "dupemig/v1", "target": "dupemig/v2"}
    """
    When I call POST "/admin/v1/memory-kind-migrations" with body:
    """
    {"source": "dupemig/v1", "target": "dupemig/v3"}
    """
    Then the response status should be 409

  Scenario: Cancel a migration
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "canceltest/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "canceltest/v2",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kind-migrations" with body:
    """
    {"source": "canceltest/v1", "target": "canceltest/v2"}
    """
    And set "cancelMigId" to the json response field "id"
    When I call DELETE "/admin/v1/memory-kind-migrations/${cancelMigId}"
    Then the response status should be 204

  Scenario: Cancel a migration that does not exist returns 404
    When I call DELETE "/admin/v1/memory-kind-migrations/00000000-0000-0000-0000-000000000000"
    Then the response status should be 404

  Scenario: List migrations
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "listmig/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "listmig/v2",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kind-migrations" with body:
    """
    {"source": "listmig/v1", "target": "listmig/v2"}
    """
    When I call GET "/admin/v1/memory-kind-migrations"
    Then the response status should be 200
    And the response body field "items" should not be null

  Scenario: Attribute sort orders memories correctly and missing values sort last
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "sorttest/v1",
      "attributes": {"label": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"label\": input.value.label}"
    }
    """
    And I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "sort-test"],
      "key": "beta",
      "value": {"label": "beta"},
      "kind": "sorttest/v1"
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "sort-test"],
      "key": "alpha",
      "value": {"label": "alpha"},
      "kind": "sorttest/v1"
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "sort-test"],
      "key": "no-label",
      "value": {},
      "kind": "sorttest/v1"
    }
    """
    When I am authenticated as user "alice"
    And I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "sort-test"],
      "kind": "sorttest/v1",
      "sort": {"field": "label", "direction": "asc"},
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body field "items[0].key" should be "alpha"
    And the response body field "items[1].key" should be "beta"
    And the response body field "items[2].key" should be "no-label"

  Scenario: Sort is rejected when combined with semantic query
    When I am authenticated as user "alice"
    And I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "query": "some question",
      "sort": {"field": "label", "direction": "asc"},
      "limit": 5
    }
    """
    Then the response status should be 400

  Scenario: Attribute sort descending reverses order
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "sortdesc/v1",
      "attributes": {"label": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"label\": input.value.label}"
    }
    """
    And I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "sort-desc"],
      "key": "aaa",
      "value": {"label": "aaa"},
      "kind": "sortdesc/v1"
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "sort-desc"],
      "key": "zzz",
      "value": {"label": "zzz"},
      "kind": "sortdesc/v1"
    }
    """
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "sort-desc"],
      "kind": "sortdesc/v1",
      "sort": {"field": "label", "direction": "desc"},
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body field "items[0].key" should be "zzz"
    And the response body field "items[1].key" should be "aaa"

  Scenario: Sort with invalid field name is rejected
    When I am authenticated as user "alice"
    And I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "sort": {"field": "bad field!", "direction": "asc"},
      "limit": 5
    }
    """
    Then the response status should be 400

  Scenario: kind exact-name selector returns only matching schema
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "selector/v1",
      "attributes": {"kind": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"kind\": \"v1\"}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "selector/v2",
      "attributes": {"kind": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"kind\": \"v2\"}"
    }
    """
    And I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "sel-test"],
      "key": "mem-v1",
      "value": {"x": 1},
      "kind": "selector/v1"
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "sel-test"],
      "key": "mem-v2",
      "value": {"x": 2},
      "kind": "selector/v2"
    }
    """
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "sel-test"],
      "kind": "selector/v1",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "mem-v1"
    And the response body should not contain "mem-v2"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "sel-test"],
      "kind": "selector/v2",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should not contain "mem-v1"
    And the response body should contain "mem-v2"

  Scenario: kind family-name selector returns all versions in the family
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "events/v1",
      "attributes": {"channel": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"channel\": input.value.channel}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "events/v2",
      "attributes": {"channel": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"channel\": input.value.channel}"
    }
    """
    And I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "family-test"],
      "key": "ev1",
      "value": {"channel": "web"},
      "kind": "events/v1"
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "family-test"],
      "key": "ev2",
      "value": {"channel": "mobile"},
      "kind": "events/v2"
    }
    """
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "family-test"],
      "kind": "events",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "ev1"
    And the response body should contain "ev2"

  Scenario: memory written without explicit kind uses fixed default/v1 and returns that kind
    Given I am authenticated as user "alice"
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "default-kind-test"],
      "key": "no-kind-mem",
      "value": {"note": "written without explicit kind"}
    }
    """
    Then the response status should be 200
    And the response body field "kind" should be "default/v1"
    When I call GET "/v1/memories?ns=user&ns=alice&ns=default-kind-test&key=no-kind-mem"
    Then the response status should be 200
    And the response body field "kind" should be "default/v1"

  Scenario: kind is present in search results
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "resp/v1",
      "attributes": {"tag": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"tag\": \"resp\"}"
    }
    """
    And I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "resp-test"],
      "key": "resp-mem",
      "value": {"x": 1},
      "kind": "resp/v1"
    }
    """
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "resp-test"],
      "kind": "resp/v1",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body field "items[0].kind" should be "resp/v1"

  Scenario: AdminList kind exact filter returns only matching memories
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "alist/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "alist/v2",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call PUT "/admin/v1/memories?justification=kind-list-setup" with body:
    """
    {
      "namespace": ["user", "alice", "alist-test"],
      "key": "a-v1",
      "value": {"x": 1},
      "kind": "alist/v1"
    }
    """
    And the response status should be 200
    And I call PUT "/admin/v1/memories?justification=kind-list-setup" with body:
    """
    {
      "namespace": ["user", "alice", "alist-test"],
      "key": "a-v2",
      "value": {"x": 2},
      "kind": "alist/v2"
    }
    """
    And the response status should be 200
    When I call GET "/admin/v1/memories?namespacePrefix=user&namespacePrefix=alice&namespacePrefix=alist-test&kind=alist%2Fv1&limit=10"
    Then the response status should be 200
    And the response body field "items[0].kind" should be "alist/v1"
    And the response body should contain "a-v1"
    And the response body should not contain "a-v2"

  Scenario: AdminList kind family filter returns all versions in family
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "afam/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "afam/v2",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call PUT "/admin/v1/memories?justification=kind-family-list" with body:
    """
    {
      "namespace": ["user", "alice", "afam-test"],
      "key": "fam-v1",
      "value": {"n": 1},
      "kind": "afam/v1"
    }
    """
    And the response status should be 200
    And I call PUT "/admin/v1/memories?justification=kind-family-list" with body:
    """
    {
      "namespace": ["user", "alice", "afam-test"],
      "key": "fam-v2",
      "value": {"n": 2},
      "kind": "afam/v2"
    }
    """
    And the response status should be 200
    When I call GET "/admin/v1/memories?namespacePrefix=user&namespacePrefix=alice&namespacePrefix=afam-test&kind=afam&limit=10"
    Then the response status should be 200
    And the response body should contain "fam-v1"
    And the response body should contain "fam-v2"

  Scenario: AdminSearch kind filter exact mirrors agent search kind semantics
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "asearch/v1",
      "attributes": {"label": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"label\": input.value.label}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "asearch/v2",
      "attributes": {"label": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"label\": input.value.label}"
    }
    """
    And I call PUT "/admin/v1/memories?justification=admin-search-kind" with body:
    """
    {
      "namespace": ["user", "alice", "asearch-test"],
      "key": "srch-v1",
      "value": {"label": "first"},
      "kind": "asearch/v1"
    }
    """
    And the response status should be 200
    And I call PUT "/admin/v1/memories?justification=admin-search-kind" with body:
    """
    {
      "namespace": ["user", "alice", "asearch-test"],
      "key": "srch-v2",
      "value": {"label": "second"},
      "kind": "asearch/v2"
    }
    """
    And the response status should be 200
    When I call POST "/admin/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "asearch-test"],
      "kind": "asearch/v1",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body field "items[0].kind" should be "asearch/v1"
    And the response body should contain "srch-v1"
    And the response body should not contain "srch-v2"
    When I call POST "/admin/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "asearch-test"],
      "kind": "asearch",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "srch-v1"
    And the response body should contain "srch-v2"

  Scenario: migration source equal to target is rejected with 400
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "samesrc/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    When I call POST "/admin/v1/memory-kind-migrations" with body:
    """
    {
      "source": "samesrc/v1",
      "target": "samesrc/v1"
    }
    """
    Then the response status should be 400

  Scenario: fresh writes always carry a non-empty kind in response
    Given I am authenticated as user "alice"
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "fresh-kind-test"],
      "key": "fresh-mem",
      "value": {"data": "present"}
    }
    """
    Then the response status should be 200
    And the response body field "kind" should not be null

  @memory-kind-regression
  Scenario: archived-only and include select the same row for kind authorization and content
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {"name":"archive-select/v1","attributes":{},"projectionRego":"package memories.attributes\nattributes := {}"}
    """
    And the response status should be 200
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {"name":"archive-select/v2","attributes":{},"projectionRego":"package memories.attributes\nattributes := {}"}
    """
    And the response status should be 200
    And I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {"namespace":["user","alice","archive-select"],"key":"same-key","value":{"version":"a"},"kind":"archive-select/v1"}
    """
    And the response status should be 200
    And I call PATCH "/v1/memories?ns=user&ns=alice&ns=archive-select&key=same-key" with body:
    """
    {"archived":true}
    """
    And the response status should be 204
    And I call PUT "/v1/memories" with body:
    """
    {"namespace":["user","alice","archive-select"],"key":"same-key","value":{"version":"b"},"kind":"archive-select/v2"}
    """
    And the response status should be 200
    When I call GET "/v1/memories?ns=user&ns=alice&ns=archive-select&key=same-key&archived=only"
    Then the response status should be 200
    And the response body field "kind" should be "archive-select/v1"
    And the response body field "value.version" should be "a"
    When I call GET "/v1/memories?ns=user&ns=alice&ns=archive-select&key=same-key&archived=include"
    Then the response status should be 200
    And the response body field "kind" should be "archive-select/v2"
    And the response body field "value.version" should be "b"
    When I call POST "/v1/memories/search" with body:
    """
    {"namespace_prefix":["user","alice","archive-select"],"archived":"only","limit":10}
    """
    Then the response status should be 200
    And the response body field "items[0].kind" should be "archive-select/v1"
    And the response body field "items[0].value.version" should be "a"
    When I call POST "/v1/memories/search" with body:
    """
    {"namespace_prefix":["user","alice","archive-select"],"archived":"include","limit":10}
    """
    Then the response status should be 200
    And the response body field "items[0].kind" should be "archive-select/v2"
    And the response body field "items[0].value.version" should be "b"
    When I call GET "/v1/memories/namespaces?prefix=user&prefix=alice&prefix=archive-select&archived=only"
    Then the response status should be 200
    And the response body "namespaces" should have at least 1 items

  Scenario: AdminSearch with archived flag mirrors agent search archived semantics
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "archsearch/v1",
      "attributes": {},
      "projectionRego": "package memories.attributes\nattributes := {}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {"name":"archsearch/v2","attributes":{},"projectionRego":"package memories.attributes\nattributes := {}"}
    """
    And the response status should be 200
    And I call PUT "/admin/v1/memories?justification=archive-search" with body:
    """
    {
      "namespace": ["user", "alice", "archsearch-test"],
      "key": "arch-mem",
      "value": {"status": "archived"},
      "kind": "archsearch/v1"
    }
    """
    And the response status should be 200
    And I call PATCH "/admin/v1/memories?ns=user&ns=alice&ns=archsearch-test&key=arch-mem" with body:
    """
    {"archived": true}
    """
    And the response status should be 204
    And I call PUT "/admin/v1/memories?justification=archive-search" with body:
    """
    {"namespace":["user","alice","archsearch-test"],"key":"arch-mem","value":{"status":"active-replacement"},"kind":"archsearch/v2"}
    """
    And the response status should be 200
    When I call POST "/admin/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "archsearch-test"],
      "kind": "archsearch/v1",
      "archived": "exclude",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should not contain "arch-mem"
    When I call POST "/admin/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "archsearch-test"],
      "kind": "archsearch/v1",
      "archived": "only",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body should contain "arch-mem"
    And the response body should contain "archived"
    And the response body should not contain "active-replacement"
    When I call GET "/admin/v1/memories?namespacePrefix=user&namespacePrefix=alice&namespacePrefix=archsearch-test&archived=only&limit=10"
    Then the response status should be 200
    And the response body should contain "archived"
    And the response body should not contain "active-replacement"
    When I call GET "/admin/v1/memories?namespacePrefix=user&namespacePrefix=alice&namespacePrefix=archsearch-test&archived=include&limit=10"
    Then the response status should be 200
    And the response body should contain "active-replacement"
    And the response body should not contain "\"status\":\"archived\""

  Scenario: non-admin can search a custom kind without reserved projection attributes
    Given I call POST "/admin/v1/memory-kinds?justification=custom-kind-search" with body:
    """
    {
      "name":"tagonly/v1",
      "attributes":{"tag":"string"},
      "projectionRego":"package memories.attributes\nattributes := {\"tag\": input.value.tag}"
    }
    """
    And the response status should be 200
    And I am authenticated as user "bob"
    And I call PUT "/v1/memories" with body:
    """
    {"namespace":["user","bob","tag-only"],"key":"custom-visible","value":{"tag":"find-me"},"kind":"tagonly/v1"}
    """
    And the response status should be 200
    When I call POST "/v1/memories/search" with body:
    """
    {"namespace_prefix":["user","bob","tag-only"],"kind":"tagonly/v1","filter":{"tag":"find-me"},"limit":10}
    """
    Then the response status should be 200
    And the response body should contain "custom-visible"

  Scenario: Retry with the same revision_id and a different kind returns memory_revision_already_exists
    Given I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "idemkind/v1",
      "attributes": {"tag": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"tag\": \"one\"}"
    }
    """
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {
      "name": "idemkind/v2",
      "attributes": {"tag": "string"},
      "projectionRego": "package memories.attributes\nattributes := {\"tag\": \"two\"}"
    }
    """
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-kind-retry"],
      "key": "kind-retry-key",
      "value": {"v": 1},
      "kind": "idemkind/v1",
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000002a0"
    }
    """
    Then the response status should be 201
    And the response body field "kind" should be "idemkind/v1"
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-kind-retry"],
      "key": "kind-retry-key",
      "value": {"v": 1},
      "kind": "idemkind/v1",
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000002a0"
    }
    """
    Then the response status should be 200
    And the response body field "kind" should be "idemkind/v1"
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "idem-kind-retry"],
      "key": "kind-retry-key",
      "value": {"v": 1},
      "kind": "idemkind/v2",
      "revision_id": "6f1c1d0a-0000-4000-8000-0000000002a0"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "memory_revision_already_exists"
    And the response body field "details.revisionId" should be "6f1c1d0a-0000-4000-8000-0000000002a0"
    And the response body should not contain "idemkind"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'kind-retry-key' AND memory_kind = 'idemkind/v1' AND deleted_reason IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'kind-retry-key'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "kind-retry-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
