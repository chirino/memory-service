Feature: Admin checkpoint REST API
  As an administrator
  I want to manage remote client checkpoints through the admin API
  So that standalone processors can resume safely with typed checkpoint payloads

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Admin can create a checkpoint with typed generic JSON data
    When I call PUT "/v1/admin/checkpoints/generic-client-a" with body:
    """
    {
      "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
      "value": {
        "cursor": "cursor-123",
        "processed": 3,
        "recentConversationIds": ["conv-a", "conv-b"],
        "state": {
          "mode": "live"
        }
      }
    }
    """
    Then the response status should be 200
    And the response body field "clientId" should be "generic-client-a"
    And the response body field "contentType" should be "application/vnd.memory-service.checkpoint+json;v=1"
    And the response body field "value.cursor" should be "cursor-123"
    And the response body field "value.processed" should be "3"
    And the response body field "value.recentConversationIds[0]" should be "conv-a"
    And the response body field "value.recentConversationIds[1]" should be "conv-b"
    And the response body field "value.state.mode" should be "live"
    And the response body field "updatedAt" should not be null

  Scenario: Admin can read a previously stored checkpoint
    Given I call PUT "/v1/admin/checkpoints/generic-client-b" with body:
    """
    {
      "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
      "value": {
        "cursor": "cursor-456",
        "batch": {
          "size": 10
        }
      }
    }
    """
    And the response status should be 200
    When I call GET "/v1/admin/checkpoints/generic-client-b"
    Then the response status should be 200
    And the response body should contain json:
    """
    {
      "clientId": "generic-client-b",
      "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
      "value": {
        "cursor": "cursor-456",
        "batch": {
          "size": 10
        }
      }
    }
    """
    And the response body field "updatedAt" should not be null

  Scenario: Admin can replace a checkpoint with a different typed JSON payload
    Given I call PUT "/v1/admin/checkpoints/generic-client-c" with body:
    """
    {
      "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
      "value": {
        "cursor": "cursor-old"
      }
    }
    """
    And the response status should be 200
    When I call PUT "/v1/admin/checkpoints/generic-client-c" with body:
    """
    {
      "contentType": "application/example+json",
      "value": {
        "positions": [1, 2],
        "mode": "full"
      }
    }
    """
    Then the response status should be 200
    And the response body field "contentType" should be "application/example+json"
    And the response body field "value.positions[0]" should be "1"
    And the response body field "value.positions[1]" should be "2"
    And the response body field "value.mode" should be "full"
    When I call GET "/v1/admin/checkpoints/generic-client-c"
    Then the response status should be 200
    And the response body field "contentType" should be "application/example+json"
    And the response body field "value.mode" should be "full"
    And the response body field "value.cursor" should be null

  Scenario: Admin clients can only access checkpoints for their own client ID
    Given I am authenticated as admin client with API key "test-agent-key"
    And set "ownerClientId" to the current client ID
    When I call PUT "/v1/admin/checkpoints/${ownerClientId}" with body:
    """
    {
      "contentType": "application/example+json",
      "value": {
        "cursor": "owner-client"
      }
    }
    """
    Then the response status should be 200
    Given I am authenticated as admin user "alice"
    And I am authenticated as admin client with API key "test-agent-key-b"
    When I call GET "/v1/admin/checkpoints/${ownerClientId}"
    Then the response status should be 404
    When I call PUT "/v1/admin/checkpoints/${ownerClientId}" with body:
    """
    {
      "contentType": "application/example+json",
      "value": {
        "cursor": "other-client"
      }
    }
    """
    Then the response status should be 404
    Given I am authenticated as admin user "alice"
    And I am authenticated as admin client with API key "test-agent-key"
    When I call GET "/v1/admin/checkpoints/${ownerClientId}"
    Then the response status should be 200
    And the response body field "clientId" should be "${ownerClientId}"
    And the response body field "value.cursor" should be "owner-client"

  Scenario: Admin gets not found for an unknown checkpoint
    When I call GET "/v1/admin/checkpoints/missing-client"
    Then the response status should be 404
    And the response body field "error" should be "checkpoint not found"

  Scenario: Admin cannot write a checkpoint without contentType
    When I call PUT "/v1/admin/checkpoints/generic-client-invalid" with body:
    """
    {
      "value": {
        "cursor": "cursor-789"
      }
    }
    """
    Then the response status should be 400
    And the response body field "error" should contain "contentType"

  Scenario: Auditor cannot read or write checkpoints
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/checkpoints/generic-client-a"
    Then the response status should be 403
    When I call PUT "/v1/admin/checkpoints/generic-client-a" with body:
    """
    {
      "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
      "value": {
        "cursor": "cursor-000"
      }
    }
    """
    Then the response status should be 403

  Scenario: Regular users cannot read or write checkpoints
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/checkpoints/generic-client-a"
    Then the response status should be 403
    When I call PUT "/v1/admin/checkpoints/generic-client-a" with body:
    """
    {
      "contentType": "application/vnd.memory-service.checkpoint+json;v=1",
      "value": {
        "cursor": "cursor-000"
      }
    }
    """
    Then the response status should be 403

  Scenario: Unauthenticated callers cannot read checkpoints
    Given I am not authenticated
    When I call GET "/v1/admin/checkpoints/generic-client-a"
    Then the response status should be 401
