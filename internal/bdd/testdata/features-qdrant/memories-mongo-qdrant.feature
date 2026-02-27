Feature: Episodic memories with Qdrant vector backend
  As a user in mongo+qdrant mode
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
      "value": { "text": "alpha-token-only" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "aliced", "facts"],
      "key": "k-aliced",
      "value": { "text": "beta-token-only" }
    }
    """
    And I call POST "/admin/v1/memories/index/trigger" with body:
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
