Feature: Admin Memory REST API
  As an administrator
  I want to write memories across namespaces via admin REST endpoints
  So that I can store namespace-scoped cognition memories through admin authorization

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Admin can write memory in a user namespace via REST
    When I call PUT "/admin/v1/memories?justification=BDD%20admin%20memory%20write" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "fact-rest-001",
      "value": {
        "content": "User prefers light theme",
        "confidence": 0.90
      }
    }
    """
    Then the response status should be 200
    And the response body field "namespace[0]" should be "user"
    And the response body field "namespace[1]" should be "alice"
    And the response body field "namespace[2]" should be "cognition.v1"
    And the response body field "key" should be "fact-rest-001"
    And the response body field "revision" should not be null

  Scenario: Admin can update (archive) memory via REST
    Given I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "cognition.v1", "facts"],
      "key": "fact-to-archive",
      "value": {
        "content": "temporary"
      }
    }
    """
    When I call PATCH "/admin/v1/memories?ns=user&ns=bob&ns=cognition.v1&ns=facts&key=fact-to-archive&justification=BDD%20admin%20memory%20archive" with body:
    """
    {
      "archived": true
    }
    """
    Then the response status should be 204

  Scenario: Admin REST put rejects negative TTL
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "negative-ttl",
      "value": {
        "content": "invalid ttl"
      },
      "ttl_seconds": -1
    }
    """
    Then the response status should be 400
    And the response body should contain "ttl_seconds must be"

  Scenario: Admin REST put detects stale expected revision
    Given I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "cas-rest",
      "value": {
        "content": "first value"
      }
    }
    """
    And the response status should be 200
    And set "adminRevision" to the json response field "revision"
    And I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "cas-rest",
      "value": {
        "content": "second value"
      },
      "expected_revision": ${adminRevision}
    }
    """
    And the response status should be 200
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "cas-rest",
      "value": {
        "content": "stale value"
      },
      "expected_revision": ${adminRevision}
    }
    """
    Then the response status should be 409
    And the response body should contain "memory revision conflict"

  Scenario: Admin write with TTL via REST
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "charlie", "cognition.v1", "facts"],
      "key": "temp-fact",
      "value": {
        "content": "expires in an hour"
      },
      "ttl_seconds": 3600
    }
    """
    Then the response status should be 200
    And the response body field "expiresAt" should not be null

  Scenario: Admin write with index fields via REST
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "dave", "cognition.v1", "preferences"],
      "key": "indexed-pref",
      "value": {
        "theme": "dark",
        "language": "en"
      },
      "index": {
        "category": "ui",
        "priority": "high"
      }
    }
    """
    Then the response status should be 200

  Scenario: Admin REST put runs attribute extraction with neutral admin context
    Given I call PUT "/admin/v1/memory-policies" with body:
    """
    {
      "authz": "package memories.authz\nimport future.keywords.if\ndefault decision = {\"allow\": true}",
      "attributes": "package memories.attributes\nimport future.keywords.if\ndefault attributes = {}\nattributes = {\"admin_user_id\": input.context.user_id, \"admin_role\": input.context.jwt_claims.roles[0]} if { count(input.context.jwt_claims.roles) > 0 }",
      "filter": "package memories.filter\nimport future.keywords.if\nnamespace_prefix := input.namespace_prefix\nattribute_filter := {}"
    }
    """
    And the response status should be 204
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "erin", "cognition.v1", "facts"],
      "key": "neutral-context-rest",
      "value": {
        "content": "uses neutral admin context"
      }
    }
    """
    Then the response status should be 200
    When I call POST "/admin/v1/memories/search" with body:
    """
    {
      "namespace_prefix": ["user", "erin", "cognition.v1", "facts"],
      "filter": {
        "admin_user_id": { "\u0024eq": "" },
        "admin_role": { "\u0024eq": "admin" }
      },
      "limit": 10
    }
    """
    Then the response status should be 200
    And the response body field "items[0].key" should be "neutral-context-rest"
    And the response body field "items[0].attributes.admin_user_id" should be ""
    And the response body field "items[0].attributes.admin_role" should be "admin"

  Scenario: Non-admin receives 403 on admin memory endpoints
    Given I am authenticated as user "eve"
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "eve", "test"],
      "key": "unauthorized",
      "value": {"test": "value"}
    }
    """
    Then the response status should be 403
