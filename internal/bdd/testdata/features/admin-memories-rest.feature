Feature: Admin Memory REST API
  As a service principal (cognition processor)
  I want to write memories on behalf of users via admin REST endpoints
  So that I can store derived cognition memories without user JWT authentication

  Background:
    Given I am authenticated with API key as admin client "cognition_processor"

  Scenario: Admin can write memory on behalf of a user via REST
    When I send PUT to "/admin/v1/memories" with JSON body:
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
    And the JSON response field "namespace[0]" should be "user"
    And the JSON response field "namespace[1]" should be "alice"
    And the JSON response field "namespace[2]" should be "cognition.v1"
    And the JSON response field "key" should be "fact-rest-001"

  Scenario: Admin can update (archive) memory via REST
    Given I send PUT to "/admin/v1/memories" with JSON body:
    """
    {
      "namespace": ["user", "bob", "cognition.v1", "facts"],
      "key": "fact-to-archive",
      "value": {
        "content": "temporary"
      }
    }
    """
    When I send PATCH to "/admin/v1/memories?ns=user&ns=bob&ns=cognition.v1&ns=facts&key=fact-to-archive" with JSON body:
    """
    {
      "archived": true
    }
    """
    Then the response status should be 204

  Scenario: Admin write with TTL via REST
    When I send PUT to "/admin/v1/memories" with JSON body:
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
    And the JSON response field "expires_at" should not be null

  Scenario: Admin write with index fields via REST
    When I send PUT to "/admin/v1/memories" with JSON body:
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

  Scenario: Non-admin receives 403 on admin memory endpoints
    Given I am authenticated as user "eve"
    When I send PUT to "/admin/v1/memories" with JSON body:
    """
    {
      "namespace": ["user", "eve", "test"],
      "key": "unauthorized",
      "value": {"test": "value"}
    }
    """
    Then the response status should be 403
