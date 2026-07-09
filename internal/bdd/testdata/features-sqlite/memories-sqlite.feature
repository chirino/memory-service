Feature: Episodic memories with SQLite vector backend
  As a user in sqlite+sqlite mode
  I want episodic semantic search to apply namespace-prefix filtering correctly
  So that vector search does not leak adjacent namespaces

  Background:
    Given I am authenticated as user "alice"

  Scenario: Semantic memory search enforces namespace prefix by ancestor match
    Given I am authenticated as admin user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "facts"],
      "key": "k-alice",
      "value": { "text": "alpha-token-only" },
      "index": { "text": "alpha-token-only" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "aliced", "facts"],
      "key": "k-aliced",
      "value": { "text": "beta-token-only" },
      "index": { "text": "beta-token-only" }
    }
    """
    And I call POST "/admin/v1/memory-index/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    And the response body field "triggered" should be "true"
    Given I am authenticated as user "alice"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice"],
      "query": "alpha-token-only",
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 1 items
    And the response body field "items.0.key" should be "k-alice"
    And the response body should not contain "k-aliced"

  Scenario: Multi-query semantic memory search returns attributed merged results
    Given I am authenticated as admin user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "multi-query"],
      "key": "release-note",
      "value": { "text": "release context" },
      "index": { "text": "releasealpha" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "multi-query"],
      "key": "docker-note",
      "value": { "text": "docker context" },
      "index": { "text": "dockerbeta" }
    }
    """
    And I call POST "/admin/v1/memory-index/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    Given I am authenticated as user "alice"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "multi-query"],
      "queries": [
        { "text": "releasealpha", "purpose": "release-context" },
        { "text": "dockerbeta", "purpose": "docker-context" }
      ],
      "per_query_limit": 1,
      "limit": 2
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 2 items
    And the response body should contain "release-note"
    And the response body should contain "docker-note"
    And the response body should contain "release-context"
    And the response body should contain "docker-context"

  Scenario: Admin multi-query semantic memory search returns query attribution
    Given I am authenticated as admin user "alice"
    And I call PUT "/admin/v1/memories?justification=multi-query-admin-setup" with body:
    """
    {
      "namespace": ["user", "alice", "admin-multi-query"],
      "key": "admin-release-note",
      "value": { "text": "admin release context" },
      "index": { "text": "adminreleasealpha" }
    }
    """
    And I call POST "/admin/v1/memory-index/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    When I call POST "/admin/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "admin-multi-query"],
      "queries": [
        { "text": "adminreleasealpha", "purpose": "admin-release" }
      ],
      "limit": 1
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 1 items
    And the response body field "items.0.key" should be "admin-release-note"
    And the response body field "items.0.matchedQueries.0" should be "admin-release"

  Scenario: Semantic memory search pushes attribute filters into vector search
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "vector-filter"],
      "key": "vector-alice",
      "value": { "text": "shared-vector-token" },
      "index": { "text": "shared-vector-token" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "vector-filter"],
      "key": "vector-beta",
      "value": { "text": "shared-vector-token" },
      "index": { "text": "shared-vector-token" }
    }
    """
    And I call POST "/admin/v1/memory-index/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "vector-filter"],
      "query": "shared-vector-token",
      "filter": { "sub": { "\u0024eq": "alice" } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 1 items
    And the response body should contain "vector-alice"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "vector-filter"],
      "query": "shared-vector-token",
      "filter": { "sub": { "\u0024eq": "bob" } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at most 0 items
