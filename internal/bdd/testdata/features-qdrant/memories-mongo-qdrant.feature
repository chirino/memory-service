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

  Scenario: Semantic memory search pushes attribute filters into Qdrant
    Given I am authenticated as admin user "alice"
    And I call PUT "/admin/v1/memories/policies" with body:
    """
    {
      "authz": "package memories.authz\nimport future.keywords.if\ndefault decision = {\"allow\": false, \"reason\": \"access denied\"}\ndecision = {\"allow\": true} if { input.namespace[0] == \"user\"; input.namespace[1] == input.context.user_id }",
      "attributes": "package memories.attributes\nimport future.keywords.if\ndefault attributes = {}\nbase := {\"namespace\": input.namespace[0], \"sub\": input.namespace[1]}\nextra := {k: v | k := [\"topic\"][_]; v := input.value[k]; v != null}\nattributes = object.union(base, extra) if { count(input.namespace) >= 2 }",
      "filter": "package memories.filter\nimport future.keywords.if\nimport future.keywords.in\nuser_prefix := [\"user\", input.context.user_id]\nis_admin if { \"admin\" in input.context.jwt_claims.roles }\nnamespace_prefix := input.namespace_prefix if { is_admin }\nnamespace_prefix := user_prefix if { not is_admin }\nattribute_filter := {} if { is_admin }\nattribute_filter := {\"namespace\": \"user\", \"sub\": input.context.user_id} if { not is_admin }"
    }
    """
    Then the response status should be 204
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "vector-filter"],
      "key": "vector-python",
      "value": { "text": "shared-vector-token", "topic": "python" },
      "index": { "text": "shared-vector-token" }
    }
    """
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "vector-filter"],
      "key": "vector-go",
      "value": { "text": "shared-vector-token", "topic": "go" },
      "index": { "text": "shared-vector-token" }
    }
    """
    And I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memories/index/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    Given I am authenticated as user "alice"
    When I call POST "/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "alice", "vector-filter"],
      "query": "shared-vector-token",
      "filter": { "topic": { "\u0024eq": "python" } },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body "items" should have at least 1 items
    And the response body field "items.0.key" should be "vector-python"
    And the response body should not contain "vector-go"
